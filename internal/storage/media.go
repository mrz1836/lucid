package storage

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/mrz1836/lucid/internal/observations"
)

// Media-tree names under ~/.lucid/media/ (data-model.md §"Media attachments").
// Only this adapter touches them; the attach verb copies bytes, hashes, and
// writes a sidecar exclusively through these ops. media/ mirrors raw/: an
// immutable, YYYY/MM-sharded store the state-folder backup covers and no git
// tree ever holds.
const (
	mediaDirName    = "media"
	mediaSidecarExt = ".json"
)

// maxMediaSlugLen bounds the derived slug so a long caption cannot produce an
// unwieldy filename. It trims on a rune boundary of the already-normalized
// (ASCII alnum + hyphen) slug.
const maxMediaSlugLen = 60

// maxSameDaySlugIDs bounds the `_N` counter appended when a same-day slug
// filename is already taken (data-model.md §"Naming conventions": append `_N`
// on a same-day slug collision). It is far above any plausible same-day
// same-slug capture rate and exists only so a pathological caller cannot spin
// forever — mirroring [maxSameSecondIDs] for raw ids.
const maxSameDaySlugIDs = 1000

// defaultMediaSlug is the fallback label when neither the caption nor the
// original basename yields any slug characters (e.g. an all-punctuation name).
const defaultMediaSlug = "attachment"

// MediaAttachment is the caller's intent for one immutable media attachment
// (data-model.md §"Media attachments"). The router resolves the logical day and
// provenance upstream (reusing the observations rollover/backdate rules — no
// second clock here) and hands this to storage; storage derives the stored
// filename, hashes the bytes, and writes the metadata sidecar. The content is
// stored opaquely — any binary, no type gate. The caller never sets the stored
// id or the sha256; storage assigns those.
type MediaAttachment struct {
	// SourcePath is the path to the file being attached; its bytes are copied
	// verbatim. Set this or [MediaAttachment.Bytes] (SourcePath wins when both
	// are present).
	SourcePath string
	// Bytes is the attachment content when the caller already holds it in
	// memory — an alternative to SourcePath (used by tests and any in-memory
	// capture).
	Bytes []byte
	// OriginalFilename is the basename as dropped. It is preserved for
	// provenance, supplies the stored extension, and — absent a caption — seeds
	// the slug. Required.
	OriginalFilename string
	// CapturedAt is when the attachment is written (seeds captured_at). Required.
	CapturedAt time.Time
	// LogicalDay is the YYYY-MM-DD day the attachment is attributed to — the
	// filename prefix and shard. The router resolves it via the shared
	// rollover/`@yesterday` rules; storage only validates its shape. Required.
	LogicalDay string
	// Caption is the optional free-text caption, stored verbatim; when present
	// it also seeds the slug. Empty on the frictionless "drop it" path.
	Caption string
	// Alt is optional alt-text for a future accessible surface; it renders as a
	// JSON null in the sidecar when unset.
	Alt string
	// Source is the provenance token (cli, or a harness token), stored as-is —
	// the same field the raw entry / session record carry.
	Source string
	// RawEntryID links the immutable raw entry the router emits alongside this
	// attachment (data-model.md §"Sidecar schema"). The router supplies it;
	// storage stays agnostic about the raw/media write order and never derives
	// a raw id itself.
	RawEntryID string
}

// MediaRecord is the metadata sidecar written for a stored attachment and the
// value the media reads return (data-model.md §"Sidecar schema"). Field order
// matches the documented schema so a written sidecar reads like the spec.
type MediaRecord struct {
	// ID is the stored filename (YYYY-MM-DD-<slug>.<ext>) — the on-disk name of
	// the binary.
	ID string `json:"id"`
	// SHA256 is the integrity hash of the stored bytes, computed at write.
	SHA256 string `json:"sha256"`
	// OriginalFilename is the basename as dropped, preserved for provenance.
	OriginalFilename string `json:"original_filename"`
	// CapturedAt is when the attachment was written (RFC3339, local offset).
	CapturedAt string `json:"captured_at"`
	// LogicalDay is the YYYY-MM-DD day the attachment is attributed to.
	LogicalDay string `json:"logical_day"`
	// Caption is the optional caption; omitted on disk when empty.
	Caption string `json:"caption,omitempty"`
	// Alt is optional alt-text; a nil pointer renders as `alt: null`.
	Alt *string `json:"alt"`
	// RawEntryID is the id of the raw entry that references this attachment.
	RawEntryID string `json:"raw_entry_id"`
	// Source is the provenance token, normalized on write.
	Source string `json:"source"`
	// StoredPath is the absolute path to the stored binary. It is derived (from
	// the id and the Ledger home) and is never part of the on-disk sidecar.
	StoredPath string `json:"-"`
}

// ScaffoldMedia creates the media/ tree if missing (data-model.md §"Media
// attachments"). It is lazy and idempotent — a re-run makes no changes —
// mirroring [Adapter.ScaffoldObservations] / [Adapter.ScaffoldEngine].
// [Adapter.WriteMedia] also creates its day shard on demand, so scaffolding is
// only about having the store present from init.
func (a *Adapter) ScaffoldMedia() error {
	if err := os.MkdirAll(a.mediaDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create media dir: %w", err)
	}
	return nil
}

// WriteMedia copies the attachment bytes into
// media/YYYY/MM/YYYY-MM-DD-<slug>.<ext>, computes their sha256, and writes the
// paired <stored-filename>.json metadata sidecar, returning the resulting
// record with its StoredPath set. The stored file is the original binary,
// byte-for-byte — no type gate, no transcode. A same-day slug collision
// advances to `<slug>_N` per data-model.md §"Naming conventions"; an existing
// stored file is never overwritten. On any read/copy/sidecar failure nothing is
// left on disk (error-states.md §St-1): a partial copy has its binary removed
// before the error is returned, so a reader never sees a binary without its
// sidecar.
func (a *Adapter) WriteMedia(att MediaAttachment) (MediaRecord, error) {
	if err := att.validate(); err != nil {
		return MediaRecord{}, err
	}
	content, err := att.readContent()
	if err != nil {
		return MediaRecord{}, err
	}
	shard, err := a.mediaShardDir(att.LogicalDay)
	if err != nil {
		return MediaRecord{}, err
	}
	if err = os.MkdirAll(shard, dirPerm); err != nil {
		return MediaRecord{}, fmt.Errorf("storage: prepare media dir %q: %w", shard, err)
	}

	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	slug := mediaSlug(att.Caption, att.OriginalFilename)
	ext := filepath.Ext(att.OriginalFilename)

	for _, name := range mediaFilenameCandidates(att.LogicalDay, slug, ext) {
		binPath := filepath.Join(shard, name)
		wrote, werr := writeExcl(binPath, content)
		if werr != nil {
			return MediaRecord{}, werr
		}
		if !wrote {
			continue // filename taken: advance to <slug>_N
		}
		rec := MediaRecord{
			ID:               name,
			SHA256:           hash,
			OriginalFilename: filepath.Base(att.OriginalFilename),
			CapturedAt:       att.CapturedAt.Format(time.RFC3339),
			LogicalDay:       att.LogicalDay,
			Caption:          att.Caption,
			Alt:              optionalString(att.Alt),
			RawEntryID:       att.RawEntryID,
			Source:           att.Source,
		}
		sidecar, merr := marshalJSON(rec)
		if merr != nil {
			_ = os.Remove(binPath) // St-1: leave nothing on disk
			return MediaRecord{}, merr
		}
		swrote, serr := writeExcl(binPath+mediaSidecarExt, sidecar)
		if serr != nil || !swrote {
			_ = os.Remove(binPath) // St-1: the binary and its sidecar land together or not at all
			if serr != nil {
				return MediaRecord{}, serr
			}
			return MediaRecord{}, fmt.Errorf("storage: media sidecar %q already exists", name+mediaSidecarExt)
		}
		rec.StoredPath = binPath
		return rec, nil
	}
	return MediaRecord{}, fmt.Errorf("storage: could not allocate a unique media filename for %s-%s", att.LogicalDay, slug)
}

// ReadMediaForDay returns the attachment records attributed to one logical day
// (YYYY-MM-DD), sorted by stored id — the retrieval primitive the day view
// joins on. A missing shard yields no records and no error. A sidecar that
// fails to parse is skipped (mirroring the observations bad-line skip), and a
// `.json` original binary is never mistaken for a sidecar because its own
// stored file must exist for it to count.
func (a *Adapter) ReadMediaForDay(date string) ([]MediaRecord, error) {
	shard, err := a.mediaShardDir(date)
	if err != nil {
		return nil, err
	}
	entries, rerr := os.ReadDir(shard)
	if errors.Is(rerr, fs.ErrNotExist) {
		return nil, nil
	}
	if rerr != nil {
		return nil, fmt.Errorf("storage: read media shard %q: %w", shard, rerr)
	}
	prefix := date + "-"
	var out []MediaRecord
	for _, e := range entries {
		rec, ok := a.decodeSidecarEntry(shard, e, prefix)
		if ok {
			out = append(out, rec)
		}
	}
	slices.SortFunc(out, func(a, b MediaRecord) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// ListMedia returns every attachment record in the store, sorted by stored id —
// a whole-tree retrieval primitive. It walks the media/ tree and decodes each
// valid sidecar; a missing store yields no records and no error.
func (a *Adapter) ListMedia() ([]MediaRecord, error) {
	var out []MediaRecord
	err := filepath.WalkDir(a.mediaDir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if rec, ok := a.decodeSidecarEntry(filepath.Dir(path), d, ""); ok {
			out = append(out, rec)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	slices.SortFunc(out, func(a, b MediaRecord) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// decodeSidecarEntry reports whether dir entry e is a valid media sidecar and,
// if so, returns its decoded record. A sidecar is a `.json` file whose
// stored-name prefix matches (when one is given), whose stored binary exists,
// which parses cleanly, and whose id equals the stored filename — the four
// guards that keep a `.json` original (its own binary) from being read as a
// sidecar and skip a corrupt sidecar.
func (a *Adapter) decodeSidecarEntry(dir string, e fs.DirEntry, prefix string) (MediaRecord, bool) {
	if e.IsDir() || !strings.HasSuffix(e.Name(), mediaSidecarExt) {
		return MediaRecord{}, false
	}
	storedName := strings.TrimSuffix(e.Name(), mediaSidecarExt)
	if prefix != "" && !strings.HasPrefix(storedName, prefix) {
		return MediaRecord{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, storedName)); err != nil {
		return MediaRecord{}, false // no stored binary: this is a `.json` original, not a sidecar
	}
	rec, err := readMediaSidecar(dir, e.Name())
	if err != nil || rec.ID != storedName {
		return MediaRecord{}, false // unparseable or mismatched: skip, do not fail the read
	}
	return rec, true
}

// readMediaSidecar reads and decodes one sidecar, filling StoredPath from the
// sidecar's own path (the stored binary is the sidecar path minus `.json`).
func readMediaSidecar(dir, sidecarName string) (MediaRecord, error) {
	path := filepath.Join(dir, sidecarName)
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path under the Ledger media tree
	if err != nil {
		return MediaRecord{}, fmt.Errorf("storage: read media sidecar %q: %w", sidecarName, err)
	}
	var rec MediaRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return MediaRecord{}, fmt.Errorf("storage: parse media sidecar %q: %w", sidecarName, err)
	}
	rec.StoredPath = strings.TrimSuffix(path, mediaSidecarExt)
	return rec, nil
}

// validate reports the first structural problem with an attachment before any
// byte is written. RawEntryID is deliberately not required here: storage stays
// agnostic about the raw/media write order and the router supplies the link.
func (att MediaAttachment) validate() error {
	if att.SourcePath == "" && len(att.Bytes) == 0 {
		return errors.New("storage: media attachment needs a source path or bytes")
	}
	if att.OriginalFilename == "" {
		return errors.New("storage: media attachment original_filename is required")
	}
	if att.CapturedAt.IsZero() {
		return errors.New("storage: media attachment captured_at is required")
	}
	if att.Source == "" {
		return errors.New("storage: media attachment source is required")
	}
	if _, err := observations.ParseDate(att.LogicalDay, time.UTC); err != nil {
		return fmt.Errorf("storage: media attachment logical_day %q invalid: %w", att.LogicalDay, err)
	}
	return nil
}

// readContent returns the bytes to store, reading the source file when
// SourcePath is set (it wins over Bytes) or returning the in-memory Bytes.
func (att MediaAttachment) readContent() ([]byte, error) {
	if att.SourcePath != "" {
		b, err := os.ReadFile(att.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("storage: read media source %q: %w", att.SourcePath, err)
		}
		return b, nil
	}
	return att.Bytes, nil
}

// mediaDir returns the ~/.lucid/media/ store root.
func (a *Adapter) mediaDir() string { return filepath.Join(a.home, mediaDirName) }

// mediaShardDir returns media/YYYY/MM for a logical day (YYYY-MM-DD), mirroring
// rawShardDir; it also validates the day's shape.
func (a *Adapter) mediaShardDir(logicalDay string) (string, error) {
	d, err := observations.ParseDate(logicalDay, time.UTC)
	if err != nil {
		return "", fmt.Errorf("storage: bad media logical day %q: %w", logicalDay, err)
	}
	return filepath.Join(a.mediaDir(),
		fmt.Sprintf("%04d", d.Year()), fmt.Sprintf("%02d", int(d.Month()))), nil
}

// mediaFilenameCandidates yields the ordered stored-filename candidates for a
// day+slug+ext: the bare `YYYY-MM-DD-<slug><ext>` first, then `<slug>_2`,
// `<slug>_3`, ... on a same-day collision (data-model.md §"Naming
// conventions"). The `_N` suffix sits on the slug, before the extension, so the
// original extension is always preserved.
func mediaFilenameCandidates(day, slug, ext string) []string {
	base := day + "-" + slug
	out := make([]string, 0, maxSameDaySlugIDs)
	out = append(out, base+ext)
	for n := 2; n <= maxSameDaySlugIDs; n++ {
		out = append(out, fmt.Sprintf("%s_%d%s", base, n, ext))
	}
	return out
}

// mediaSlug derives a low-signal filename slug from the caption when present,
// else the original basename (extension dropped): lowercased, with runs of
// non-alphanumerics collapsed to a single hyphen, trimmed, and truncated to
// [maxMediaSlugLen]. An empty result falls back to [defaultMediaSlug].
func mediaSlug(caption, originalFilename string) string {
	src := strings.TrimSpace(caption)
	if src == "" {
		base := filepath.Base(originalFilename)
		src = strings.TrimSuffix(base, filepath.Ext(base))
	}
	var b strings.Builder
	pendingHyphen := false
	for _, ru := range strings.ToLower(src) {
		if unicode.IsLetter(ru) || unicode.IsDigit(ru) {
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(ru)
			continue
		}
		pendingHyphen = true
	}
	slug := b.String()
	if len(slug) > maxMediaSlugLen {
		slug = strings.Trim(slug[:maxMediaSlugLen], "-")
	}
	if slug == "" {
		return defaultMediaSlug
	}
	return slug
}

// optionalString returns a pointer to s, or nil when s is empty — so an unset
// optional field renders as a JSON null (alt) rather than an empty string.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
