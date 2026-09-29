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

// gratitudePath returns registries/gratitude/<key>.json. The key is routed
// through [safeRecordPath] so a separator-bearing or empty key can never escape
// the gratitude tree — the same guard the person/insight/registry paths apply
// (closing the `lucid gratitude add --into ../../..` read-oracle before any read
// touches disk).
func (a *Adapter) gratitudePath(key string) (string, error) {
	return safeRecordPath(a.gratitudeDir(), key, ".json", "gratitude key")
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

// ResolveGratitudeKey resolves the canonical entry key for a phrase — tier 1 of
// the gratitude match, the canonical-key seam (gratitude.md §7.1). It reuses the
// shared salted registry-key derivation (the same low-signal key
// people/injuries/places use) under the gratitude kind, with the collision-suffix
// rule so two genuinely different things that hash alike get distinct keys. Two
// phrasings that normalize equal resolve to the same key; two that normalize
// differently resolve to different keys. Matching beyond that — the tier-2 token
// match over live wordings, and the optional by-meaning tier 3 — is deliberately
// NOT attempted here: the router's match pipeline runs those steps only after
// this key finds no record, reading the live set through [Adapter.ReadGratitudeAll].
func (a *Adapter) ResolveGratitudeKey(phrase string) (string, error) {
	return a.ResolveRegistryKey(observations.RegistryGratitude, phrase)
}

// ReadGratitude reads one gratitude entry by key, returning (entry, found,
// error). A missing entry is not an error.
func (a *Adapter) ReadGratitude(key string) (observations.GratitudeEntry, bool, error) {
	path, err := a.gratitudePath(key)
	if err != nil {
		return observations.GratitudeEntry{}, false, err
	}
	return readJSONOptional[observations.GratitudeEntry](path, fmt.Sprintf("gratitude %q", key))
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
// into aka (an `add --into` bump). An event naming a Person (an `add --person`)
// also links that person onto the entry's grow-only people[] in the same write
// (gratitude.md §8). A merged-away (tombstoned) entry is refused — its
// destination must be targeted instead (gratitude.md §4).
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
	entry = entry.LinkPerson(ev.Person)

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

// AppendGratitudeExpressed records that you told a person about a gratitude
// (gratitude.md §8, the write behind `lucid gratitude thank`): it links the
// person key onto the live entry at key (grow-only people[], a no-op when
// already linked) and appends one tally-neutral `expressed` event naming them at
// logicalDate, minting its receipt under the single-writer discipline exactly as
// [Adapter.AppendGratitudeEvent] does. Unlike an add it never creates an entry
// and never touches the wordings: a missing entry or a merge tombstone is a
// clean error that writes nothing. It returns the stored entry and the appended
// event with its receipt id filled.
func (a *Adapter) AppendGratitudeExpressed(
	key, personKey, logicalDate string, now time.Time,
) (observations.GratitudeEntry, observations.GratitudeEvent, error) {
	key = strings.TrimSpace(key)
	personKey = strings.TrimSpace(personKey)
	if personKey == "" {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
			"storage: an expressed gratitude needs a person key; nothing was saved",
		)
	}
	entry, found, err := a.ReadGratitude(key)
	if err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	if !found {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
			"storage: no gratitude entry %q; nothing was saved", key,
		)
	}
	if entry.IsTombstone() {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
			"storage: gratitude entry %q was merged into %q; target the destination instead", key, entry.RedirectTo,
		)
	}

	nowStr := now.Format(time.RFC3339)
	entry = entry.LinkPerson(personKey)
	ev := observations.GratitudeEvent{
		ID:     observations.GratitudeReceiptID(logicalDate, observations.NextGratitudeSeq(entry.History)),
		At:     nowStr,
		Type:   observations.GratitudeEventExpressed,
		Date:   logicalDate,
		Person: personKey,
	}
	entry.History = append(slices.Clone(entry.History), ev)
	entry.UpdatedAt = nowStr

	if err = entry.Validate(); err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	if err = a.writeGratitude(entry); err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	return entry, ev, nil
}

// MergeGratitude folds the source gratitude entry into the target (gratitude.md
// §4; the same append-and-redirect identity model [Adapter.MergePersons] uses).
// The whole of the source's tally — its derived count and its first/last span —
// is folded into the target via a single `merge` event appended to the target
// that names the absorbed source key (and carries the source's last-expressed
// date per person, so the target's fold stays local), and the target's aka[]
// and people[] absorb the source's wordings and links. The source is then
// rewritten as a redirect tombstone
// forwarding to the target: omitted from the active tally (gratitude.md §6) but
// auditably kept, never deleted. Every tombstone that already pointed at the
// source is re-pointed at the target so the redirect graph stays single-hop.
//
// Both keys must name live entries: a self-merge, a missing source or target,
// and a source or target that is itself a tombstone are each a clean error that
// changes nothing. It returns the merged target entry and the appended merge
// event (with its receipt id) so the caller can ack the receipt like every other
// mutation. This is the only writer of the two files it touches (architecture P3).
func (a *Adapter) MergeGratitude(
	sourceKey, targetKey string, now time.Time,
) (observations.GratitudeEntry, observations.GratitudeEvent, error) {
	if err := a.ScaffoldGratitude(); err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	sourceKey = strings.TrimSpace(sourceKey)
	targetKey = strings.TrimSpace(targetKey)
	if sourceKey == "" || targetKey == "" {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
			"storage: merge needs a source and a target gratitude id; nothing was changed",
		)
	}
	if sourceKey == targetKey {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
			"storage: cannot merge gratitude entry %q into itself; nothing was changed", sourceKey,
		)
	}

	source, found, err := a.ReadGratitude(sourceKey)
	if err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	if !found {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
			"storage: no gratitude entry %q to merge; nothing was changed", sourceKey,
		)
	}
	if source.IsTombstone() {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
			"storage: gratitude entry %q was already merged into %q; nothing was changed", sourceKey, source.RedirectTo,
		)
	}
	target, found, err := a.ReadGratitude(targetKey)
	if err != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, err
	}
	if !found {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
			"storage: no gratitude entry %q to merge into; nothing was changed", targetKey,
		)
	}
	if target.IsTombstone() {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, fmt.Errorf(
			"storage: gratitude entry %q was merged into %q; merge into that entry instead", targetKey, target.RedirectTo,
		)
	}

	nowStr := now.Format(time.RFC3339)
	srcTally := source.Tally()

	// The target absorbs the source's wordings for readability; resolution stays
	// by canonical key + tombstone, never by scanning aka[] (the people precedent).
	target.Aka = addUnique(target.Aka, source.DisplayName)
	for _, aka := range source.Aka {
		target.Aka = addUnique(target.Aka, aka)
	}
	// The source's person links carry across, grow-only, like its wordings
	// (gratitude.md §4, §8).
	for _, person := range source.People {
		target = target.LinkPerson(person)
	}

	// The whole source tally folds into the target as one auditable merge event,
	// minted under the single-writer discipline. Its receipt encodes today's
	// logical date — the day the fold happened.
	seq := observations.NextGratitudeSeq(target.History)
	receiptDate := observations.DateString(observations.DateOf(now))
	ev := observations.GratitudeEvent{
		ID:          observations.GratitudeReceiptID(receiptDate, seq),
		At:          nowStr,
		Type:        observations.GratitudeEventMerge,
		SourceKey:   sourceKey,
		SourceCount: srcTally.Count,
		SourceFirst: srcTally.First,
		SourceLast:  srcTally.Last,
	}
	if expressed := source.LastExpressed(); len(expressed) > 0 {
		ev.SourceExpressed = expressed
	}
	target.History = append(slices.Clone(target.History), ev)
	target.UpdatedAt = nowStr
	if verr := target.Validate(); verr != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, verr
	}
	if werr := a.writeGratitude(target); werr != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, werr
	}

	// Rewrite the source as a redirect tombstone; its history is frozen and kept
	// for audit, but it is now omitted from the active tally.
	source.RedirectTo = targetKey
	source.UpdatedAt = nowStr
	if werr := a.writeGratitude(source); werr != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, werr
	}

	if ferr := a.flattenGratitudeRedirects(sourceKey, targetKey); ferr != nil {
		return observations.GratitudeEntry{}, observations.GratitudeEvent{}, ferr
	}
	return target, ev, nil
}

// flattenGratitudeRedirects re-points every tombstone forwarding to oldTarget so
// it forwards to newTarget instead, keeping the redirect graph single-hop after a
// merge would otherwise chain two records (gratitude.md §4). It mirrors the
// people-merge flatten precedent.
func (a *Adapter) flattenGratitudeRedirects(oldTarget, newTarget string) error {
	all, err := a.ReadGratitudeAll()
	if err != nil {
		return err
	}
	for _, e := range all {
		if !e.IsTombstone() || e.RedirectTo != oldTarget || e.Key == newTarget {
			continue
		}
		e.RedirectTo = newTarget
		if werr := a.writeGratitude(e); werr != nil {
			return werr
		}
	}
	return nil
}

// writeGratitude persists one gratitude entry as indented JSON, creating the
// subtree if needed. It is the only writer of a gratitude file (architecture P3).
// An entry read at an older schema is written back at the current one
// (gratitude.md §2 Versioning): every schema-2 addition is optional, so the
// upgrade is the version stamp alone — there is no migration pass.
func (a *Adapter) writeGratitude(entry observations.GratitudeEntry) error {
	if entry.Schema < observations.GratitudeSchema {
		entry.Schema = observations.GratitudeSchema
	}
	path, err := a.gratitudePath(entry.Key)
	if err != nil {
		return err
	}
	if err = ensureDir(a.gratitudeDir(), "gratitude"); err != nil {
		return err
	}
	content, err := marshalJSON(entry.Normalized())
	if err != nil {
		return err
	}
	// Atomic: a gratitude entry holds an append-only multi-year tally, so a
	// torn write must leave the previous entry intact rather than abort the tally.
	return writeFileAtomic(path, content, fmt.Sprintf("gratitude %q", entry.Key))
}
