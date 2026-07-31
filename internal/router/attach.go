package router

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// commandAttach is the verb stamped on the raw entry an attach emits.
const commandAttach = "/attach"

// shaAckPrefixLen bounds the sha256 shown in the human ack. The full hash
// rides the sidecar and the --json result; the ack shows a short prefix so the
// confirmation stays legible (data-model.md renders sha256 the same way).
const shaAckPrefixLen = 12

// AttachRequest carries the inputs for one `lucid attach` turn. Path is the
// file being attached; Caption is the optional verbatim description (stored
// as-is and used to derive the filename slug). DayArg is the optional `--day`
// token (`@yesterday` / `@YYYY-MM-DD`, or bare forms); an empty value attaches
// to the current logical day under the 04:00 rollover. Now, Source, Harness,
// and ChannelID are harness provenance supplied by the surface that received
// the command — a zero Now defaults to the wall clock and an empty
// Source/Harness defaults to the local CLI surface, so a bare CLI call still
// produces a well-formed attachment.
type AttachRequest struct {
	Path      string
	Caption   string
	DayArg    string
	Now       time.Time
	Source    string
	Harness   string
	ChannelID string
}

// AttachResult reports what an attach wrote and the acknowledgement to show
// the user. StoredPath is the absolute path to the stored binary; SHA256 is
// its integrity hash; Day is the logical day it was attributed to; RawID is
// the linked immutable raw entry; Caption echoes what was captured.
type AttachResult struct {
	StoredPath string
	SHA256     string
	Day        string
	RawID      string
	Caption    string
	Ack        string
}

// Attach executes the `lucid attach` command: it copies req.Path into the
// ~/.lucid/media/ store (hashed, sidecar-recorded), then emits one immutable
// raw entry (plus its session record, mirroring [Router.Log]) whose body
// references the stored media so the day view and the Mirror can find it. It
// is deterministic and agent-free — no model runs in the write path
// (architecture P3): the copy, the sha256, and the metadata are mechanical.
//
// The media is written first so the raw entry can reference the real stored
// path; the raw id the sidecar links to is derived from req.Now at minute
// precision (the documented raw-id form), which the single-writer local
// runtime — the same assumption storage's exclusive-write discipline makes —
// resolves to exactly the id [storage.Adapter.WriteRaw] then assigns. A
// malformed source/harness/day, or an unreadable file, is rejected before
// anything is written (error-states.md §St-1), so a bad turn leaves nothing on
// disk.
func (r *Router) Attach(req AttachRequest) (AttachResult, error) {
	now := whenOr(req.Now)

	path := strings.TrimSpace(req.Path)
	if path == "" {
		return AttachResult{}, fmt.Errorf("attach needs a file path; nothing was saved")
	}

	// Resolve the provenance tokens and the target day before writing anything:
	// a malformed value must leave nothing on disk (error-states.md §St-1), so
	// each is validated up front, ahead of the media copy that lands first.
	source, err := resolveSource(req.Source)
	if err != nil {
		return AttachResult{}, fmt.Errorf("invalid source; nothing was saved: %w", err)
	}
	harness, err := resolveSource(req.Harness)
	if err != nil {
		return AttachResult{}, fmt.Errorf("invalid harness; nothing was saved: %w", err)
	}
	occ, precision, day, err := resolveCaptureDay(req.DayArg, now)
	if err != nil {
		return AttachResult{}, err
	}

	// The sidecar links to the raw entry emitted alongside it; the raw id is
	// derived from now (minute precision), so it is known before the media is
	// stored. WriteMedia reads the source file, so a missing/unreadable path
	// surfaces here with nothing written (error-states.md §St-1).
	rawID := predictRawID(now)
	rec, err := r.store.WriteMedia(storage.MediaAttachment{
		SourcePath:       path,
		OriginalFilename: filepath.Base(path),
		CapturedAt:       now,
		LogicalDay:       day,
		Caption:          req.Caption,
		Source:           source,
		RawEntryID:       rawID,
	})
	if err != nil {
		return AttachResult{}, fmt.Errorf("could not store the media; nothing was saved: %w", err)
	}

	relPath := r.mediaRelPath(rec)
	res, err := r.store.WriteRaw(storage.RawEntry{
		RecordedAt:          now,
		OccurredAt:          occ,
		OccurredAtPrecision: precision,
		Source:              source,
		Command:             commandAttach,
		Bootstrap:           false,
		Body:                attachBody(relPath, req.Caption),
	})
	if err != nil {
		return AttachResult{}, fmt.Errorf(
			"could not write the raw entry (out of disk space or permission denied?): %w", err,
		)
	}

	if _, err := r.store.WriteSession(storage.Session{
		ID:            res.SessionID,
		StartedAt:     now,
		EndedAt:       now,
		Harness:       harness,
		ChannelID:     req.ChannelID,
		Command:       commandAttach,
		RawEntryIDs:   []string{res.RawID},
		AgentVersions: r.cfg.AgentVersions,
	}); err != nil {
		return AttachResult{}, fmt.Errorf("could not write the session record for %s: %w", res.RawID, err)
	}

	return AttachResult{
		StoredPath: rec.StoredPath,
		SHA256:     rec.SHA256,
		Day:        day,
		RawID:      res.RawID,
		Caption:    req.Caption,
		Ack:        attachAck(relPath, day, rec.SHA256, res.RawID),
	}, nil
}

// resolveCaptureDay resolves the capture instant, its precision, and the
// logical day for one capture turn from the optional `--day` token. It is the
// single backdating grammar the capture verbs share — `lucid log`,
// `lucid attach`, and `lucid memory --attach` all resolve `--day` here, so
// there is one rule to learn and one rollover boundary, never a second clock.
// A leading `@` is optional (the `--day` flag reads naturally either way):
//
//   - "" (empty)  → now at exact precision; the logical day applies the 04:00
//     rollover, so a pre-dawn capture files under the day just lived.
//   - "yesterday" → the logical day before the current logical day, at
//     approximate precision. It steps back from the rollover-adjusted base
//     date the same way [resolveDayArg] does for `/day`, so before 04:00 it
//     names the day before the one a bare capture files under rather than
//     colliding with it — a 02:00 catch-up means the day before the one just
//     lived.
//   - "YYYY-MM-DD" → that civil day at approximate precision.
//
// A resolved day later than the current civil date is refused: a capture
// cannot be attributed to a day that has not happened, and the realistic typo
// (a wrong year, transposed digits) would otherwise file an entry silently far
// in the future. The ceiling is the civil date rather than the logical day so
// that before 04:00 the date on the caller's own clock still resolves.
//
// An unparseable token or a future day is a clean error and nothing is written
// (error-states.md §St-1).
func resolveCaptureDay(dayArg string, now time.Time) (occ time.Time, precision, day string, err error) {
	arg := strings.TrimPrefix(strings.TrimSpace(dayArg), "@")
	switch {
	case arg == "":
		occ, precision = now, storage.PrecisionExact
		day = observations.DeriveLogicalDate(now, observations.PrecisionExact, observations.DefaultRolloverMin)
	case strings.EqualFold(arg, "yesterday"):
		occ = observations.LogicalBaseDate(now, observations.DefaultRolloverMin).AddDate(0, 0, -1)
		precision, day = storage.PrecisionApproximate, observations.DateString(occ)
	default:
		d, perr := observations.ParseDate(arg, now.Location())
		if perr != nil {
			return time.Time{}, "", "", fmt.Errorf(
				"could not read the day %q (want @yesterday or @YYYY-MM-DD); nothing was saved: %w", dayArg, perr,
			)
		}
		occ, precision, day = d, storage.PrecisionApproximate, observations.DateString(d)
	}

	// Every branch falls through to one ceiling, so the rule is stated once.
	// The empty and yesterday branches can never trip it; routing them through
	// it anyway keeps a single comparison site instead of three.
	if observations.DateOf(occ).After(observations.DateOf(now)) {
		return time.Time{}, "", "", fmt.Errorf(
			"cannot capture against %s — that day has not happened yet; nothing was saved", day,
		)
	}

	return occ, precision, day, nil
}

// predictRawID renders the minute-precision raw id for a capture instant
// (raw_YYYY_MM_DD_HH_MM, data-model.md §"Raw entries"). Attach derives it up
// front so the media sidecar can link to the raw entry it is emitted alongside
// before that entry is written. Under the single-writer local runtime — the
// same assumption the storage adapter's exclusive-write discipline relies on —
// this equals the id WriteRaw then assigns; a separate capture already
// occupying the same minute would shift WriteRaw's id, and the day view still
// joins media by logical day rather than by chasing this link.
func predictRawID(now time.Time) string {
	return fmt.Sprintf("raw_%04d_%02d_%02d_%02d_%02d",
		now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute())
}

// mediaRelPath renders the media store-relative path of a stored record
// (media/YYYY/MM/<id>) for the raw body and the ack. It falls back to the
// stored id if the record is somehow outside the Ledger home.
func (r *Router) mediaRelPath(rec storage.MediaRecord) string {
	rel, err := filepath.Rel(r.store.Home(), rec.StoredPath)
	if err != nil {
		return rec.ID
	}
	return rel
}

// attachBody builds the raw entry body: a reference to the stored media path
// plus the verbatim caption, so the entry stands in for the binary in the
// Mirror/Retro. It is inventory, not evaluation — no score, no judgment.
func attachBody(relPath, caption string) string {
	if strings.TrimSpace(caption) == "" {
		return "Media attachment " + relPath
	}
	return "Media attachment " + relPath + " — " + caption
}

// attachAck builds the user-facing acknowledgement, emitted only after the
// writes land (provenance over magic): it names the stored path, a short
// sha256, the logical day, and the linked raw id.
func attachAck(relPath, day, sha, rawID string) string {
	return fmt.Sprintf("Attached %s for %s (sha256 %s) — logged as `%s`.",
		relPath, day, shortSHA(sha), rawID)
}

// shortSHA returns a legible prefix of a sha256 hex digest for the ack.
func shortSHA(sha string) string {
	if len(sha) <= shaAckPrefixLen {
		return sha
	}
	return sha[:shaAckPrefixLen] + "…"
}
