package storage

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// gratitude.go is the storage adapter for the gratitude record family
// (gratitude.md §2): the only code that touches registries/gratitude/
// (architecture P3). A gratitude entry is one JSON file per referent, keyed by
// the salted registry key, carrying an append-only typed history. Every mutation
// is a whole-file read-modify-write under the single-writer discipline — read
// the entry, mint the next receipt id from its history, append one event, write
// it back — so the count/first/last stay a pure fold over events that are never
// rewritten.

const gratitudeDirName = "gratitude"

// gratitudeDir returns ~/.lucid/registries/gratitude/.
func (a *Adapter) gratitudeDir() string {
	return filepath.Join(a.registriesDir(), gratitudeDirName)
}

// gratitudePath returns registries/gratitude/<key>.json.
func (a *Adapter) gratitudePath(key string) string {
	return filepath.Join(a.gratitudeDir(), key+".json")
}

// ScaffoldGratitude ensures the gratitude subtree exists, plus the shared
// observations config that carries the key_salt gratitude keys derive from
// (gratitude keys use the same salted low-signal derivation as the other
// registries). It is idempotent and the append path self-creates its parents, so
// scaffolding is a convenience the router runs at boot, not a precondition for a
// write.
func (a *Adapter) ScaffoldGratitude() error {
	if err := a.ScaffoldObservations(); err != nil {
		return err
	}
	if err := os.MkdirAll(a.gratitudeDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create gratitude dir: %w", err)
	}
	return nil
}

// ResolveGratitudeKey resolves the canonical entry key for a phrase — the v1
// canonical-key match seam (gratitude.md §7). It reuses the shared salted
// registry-key derivation (the same low-signal key people/injuries/places use)
// under the gratitude kind, with the collision-suffix rule so two genuinely
// different things that hash alike get distinct keys. This IS the whole of v1
// matching: two phrasings that normalize equal resolve to the same key (a
// bump); two that normalize differently resolve to different keys (a new entry).
// By-meaning matching — connecting "my house" to a stored "a roof over my head"
// — is deliberately NOT attempted here; that is the future work item R-011,
// which slots in behind this same seam. Until then the human agent supplies the
// by-meaning judgment through `add --into` and `merge`.
func (a *Adapter) ResolveGratitudeKey(phrase string) (string, error) {
	return a.ResolveRegistryKey(observations.RegistryGratitude, phrase)
}

// ReadGratitude reads one gratitude entry by key, returning (entry, found,
// error). A missing entry is not an error.
func (a *Adapter) ReadGratitude(key string) (observations.GratitudeEntry, bool, error) {
	return readJSONOptional[observations.GratitudeEntry](a.gratitudePath(key), fmt.Sprintf("gratitude %q", key))
}

// ReadGratitudeAll reads every gratitude entry — live and tombstoned — sorted by
// key. It returns the raw set (including merge redirects) so a caller that needs
// the audit trail sees it; the router drops tombstones from the active tally
// (gratitude.md §4, §6). A missing tree is (nil, nil).
func (a *Adapter) ReadGratitudeAll() ([]observations.GratitudeEntry, error) {
	dirEntries, err := os.ReadDir(a.gratitudeDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read gratitude dir: %w", err)
	}
	var out []observations.GratitudeEntry
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		key := strings.TrimSuffix(de.Name(), ".json")
		rec, found, rerr := a.ReadGratitude(key)
		if rerr != nil {
			return nil, rerr
		}
		if found {
			out = append(out, rec)
		}
	}
	slices.SortFunc(out, func(x, y observations.GratitudeEntry) int { return cmp.Compare(x.Key, y.Key) })
	return out, nil
}

// AppendGratitudeEvent appends ev to the entry at key — creating a fresh entry
// from displayName when none exists — mints ev's receipt id under the
// single-writer discipline (seq = max over the entry's history + 1, never a
// count), stamps ev.At with the real write time, and writes the file back. It
// returns the stored entry and the appended event with its receipt id filled.
//
// receiptDate is the logical date the receipt id encodes (an occurrence's own
// date), so a backdated write's receipt encodes the logical day, not the
// recording time. makePrimary refreshes display_name to displayName (a plain
// `add`, whose canonical key already matched) versus only recording the wording
// into aka (an `add --into` bump). A merged-away (tombstoned) entry is refused —
// its destination must be targeted instead (gratitude.md §4).
func (a *Adapter) AppendGratitudeEvent(
	key, displayName, receiptDate string, makePrimary bool, ev observations.GratitudeEvent, now time.Time,
) (observations.GratitudeEntry, observations.GratitudeEvent, error) {
	if err := a.ScaffoldGratitude(); err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	nowStr := now.Format(time.RFC3339)

	existing, found, err := a.ReadGratitude(key)
	if err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	var entry observations.GratitudeEntry
	if found {
		if existing.IsTombstone() {
			return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
				"storage: gratitude entry %q was merged into %q; target the destination instead", key, existing.RedirectTo,
			)
		}
		entry = existing.RecordWording(displayName, makePrimary)
	} else {
		entry = observations.NewGratitudeEntry(key, displayName, nowStr)
	}

	seq := observations.NextGratitudeSeq(entry.History)
	ev.ID = observations.GratitudeReceiptID(receiptDate, seq)
	ev.At = nowStr
	entry.History = append(slices.Clone(entry.History), ev)
	entry.UpdatedAt = nowStr

	if err := entry.Validate(); err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	if err := a.writeGratitude(entry); err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	return entry, ev, nil
}

// writeGratitude persists one gratitude entry as indented JSON, creating the
// subtree if needed. It is the only writer of a gratitude file (architecture P3).
func (a *Adapter) writeGratitude(entry observations.GratitudeEntry) error {
	if err := ensureDir(a.gratitudeDir(), "gratitude"); err != nil {
		return err
	}
	content, err := marshalJSON(entry.Normalized())
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.gratitudePath(entry.Key), content, filePerm); err != nil {
		return fmt.Errorf("storage: write gratitude %q: %w", entry.Key, err)
	}
	return nil
}
