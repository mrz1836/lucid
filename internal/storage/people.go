package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/data"
)

// peopleDirName is the flat Mirror subtree of low-signal person references
// (data-model.md §"People references"). Only the adapter touches it; the
// deterministic People routine (update_person) is the only writer.
const (
	peopleDirName = "people"
	personExt     = ".json"
)

// PersonMention is one extracted people[] entry the People routine resolves:
// the display name as written, the raw entry it appeared in, and when it
// occurred. It is what the router hands [Adapter.UpdatePerson] for each
// mention Structuring returned (agent-contracts.md §"How contracts compose"),
// after Structuring returns and before write_processed.
type PersonMention struct {
	// DisplayName is the name exactly as the user wrote it ("M.", "M", ...).
	DisplayName string
	// RawEntryID is the raw entry this mention came from; it is deduplicated
	// into entry_refs and drives the first_mention determination.
	RawEntryID string
	// At is when the mention occurred (the raw entry's occurred_at); it
	// widens the first_seen_at / last_seen_at window.
	At time.Time
}

// PersonResult reports what [Adapter.UpdatePerson] resolved for a mention:
// the stable slug and whether this raw entry is the person's first
// appearance. The router back-fills both onto the processed artifact's
// people[] entry so no person_key: null ever reaches disk and first_mention
// is authoritative (data-model.md §"Processed artifacts").
type PersonResult struct {
	// PersonKey is the resolved low-signal slug (person_<initial>-<word>,
	// with a -2/-3 collision suffix when a different name already holds it).
	PersonKey string
	// FirstMention is true when RawEntryID is the earliest entry in which
	// this person was recorded — a determination that is order-independent
	// and stable across reruns (idempotent), so a repeated Structuring pass
	// never flips it (acceptance-criteria.md test cases 4.1–4.3).
	FirstMention bool
}

// PersonRecord is a decoded people/<key>.json record (data-model.md
// §"People references"). It is extractive only — no relationships, no
// affect, no dynamics — a place future relational features can grow into.
type PersonRecord struct {
	PersonKey   string
	DisplayName string
	Aka         []string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	EntryRefs   []string
	Notes       *string
}

// personRecordJSON is the on-disk JSON shape; field order matches
// data-model.md §"People references" so a written record reads like the
// documented schema.
type personRecordJSON struct {
	PersonKey   string   `json:"person_key"`
	DisplayName string   `json:"display_name"`
	Aka         []string `json:"aka"`
	FirstSeenAt string   `json:"first_seen_at"`
	LastSeenAt  string   `json:"last_seen_at"`
	EntryRefs   []string `json:"entry_refs"`
	Notes       *string  `json:"notes"`
}

// peopleDir returns ~/.lucid/people/.
func (a *Adapter) peopleDir() string { return filepath.Join(a.home, peopleDirName) }

// personPath returns the on-disk path for a person key. It rejects a key
// carrying a path separator so a malformed slug can never escape the tree.
func (a *Adapter) personPath(key string) (string, error) {
	if key == "" || strings.ContainsAny(key, `/\`) {
		return "", fmt.Errorf("storage: invalid person key %q", key)
	}
	return filepath.Join(a.peopleDir(), key+personExt), nil
}

// ReadPerson loads the person record at key, returning (record, found,
// error). A missing record is not an error — the People routine reads a
// candidate key to decide create-vs-merge and to apply the collision rule.
func (a *Adapter) ReadPerson(key string) (PersonRecord, bool, error) {
	path, err := a.personPath(key)
	if err != nil {
		return PersonRecord{}, false, err
	}
	rec, found, err := readJSONOptional[personRecordJSON](path, fmt.Sprintf("person %q", key))
	if err != nil || !found {
		return PersonRecord{}, false, err
	}
	return rec.decode()
}

// ListPeopleKeys returns the person_key of every record on disk, sorted so the
// result is deterministic regardless of directory-read order. It is the read
// primitive the deterministic /person join builds on: the router loads each
// record to match a queried name against display_name / aka[] (error-states.md
// §P-1/§P-2). A missing people/ tree is not an error — it means no one has been
// mentioned yet.
func (a *Adapter) ListPeopleKeys() ([]string, error) {
	entries, err := os.ReadDir(a.peopleDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: scan people dir: %w", err)
	}
	var keys []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != personExt {
			continue
		}
		keys = append(keys, strings.TrimSuffix(e.Name(), personExt))
	}
	slices.Sort(keys)
	return keys, nil
}

// UpdatePerson is the deterministic People routine (agent-contracts.md
// §"People (extractive)"; treated as part of the storage adapter for the
// MVP). It resolves the mention to a stable slug via the wordlist and the
// -2/-3 collision rule, then creates or merges the people/<key>.json record
// and returns the resolved key plus the authoritative first_mention.
//
// It is idempotent: re-running it for the same (person, raw entry) neither
// duplicates an entry_ref nor flips first_mention, so a repeated Structuring
// pass leaves the record unchanged (acceptance-criteria.md test case 4.3).
// A missing/empty wordlist or an empty display name is an error the router
// degrades on without writing a processed artifact (error-states.md §S-5).
func (a *Adapter) UpdatePerson(m PersonMention) (PersonResult, error) {
	if strings.TrimSpace(m.DisplayName) == "" {
		return PersonResult{}, errors.New("storage: update_person: empty display_name")
	}
	if m.RawEntryID == "" {
		return PersonResult{}, errors.New("storage: update_person: empty raw_entry_id")
	}

	key, err := ResolvePersonKey(m.DisplayName, data.Wordlist(), a.personKeyOwner)
	if err != nil {
		return PersonResult{}, fmt.Errorf("storage: update_person: resolve key: %w", err)
	}

	existing, found, err := a.ReadPerson(key)
	if err != nil {
		return PersonResult{}, err
	}

	rec := mergePerson(existing, found, key, m)

	path, err := a.personPath(key)
	if err != nil {
		return PersonResult{}, err
	}
	if err = ensureDir(a.peopleDir(), "people"); err != nil {
		return PersonResult{}, err
	}
	content, err := marshalJSON(rec.encode())
	if err != nil {
		return PersonResult{}, err
	}
	if err = os.WriteFile(path, content, filePerm); err != nil {
		return PersonResult{}, fmt.Errorf("storage: write person %q: %w", key, err)
	}

	// first_mention is order-independent: this entry introduces the person
	// exactly when it is the earliest ref on the merged record. entry_refs is
	// kept sorted, so the earliest is the first element.
	firstMention := len(rec.EntryRefs) > 0 && rec.EntryRefs[0] == m.RawEntryID
	return PersonResult{PersonKey: key, FirstMention: firstMention}, nil
}

// personKeyOwner reports the normalized name currently stored at a candidate
// key, so [ResolvePersonKey] can tell a same-person reuse from a genuine
// collision (data-model.md §"person_key derivation"). Every spelling on a
// record normalizes to the same string by construction — a different
// normalized name derives a different base key — so the record's display
// name is a sufficient witness.
func (a *Adapter) personKeyOwner(candidate string) (normalized string, exists bool) {
	rec, found, err := a.ReadPerson(candidate)
	if err != nil || !found {
		return "", false
	}
	return NormalizeName(rec.DisplayName), true
}

// mergePerson folds a mention into a person record: a fresh referent is
// created with the mention as its only ref and its seen-window collapsed to
// the mention instant; an existing one adopts the latest spelling, records a
// new aka variant, widens first/last seen, and appends the ref (deduplicated
// and re-sorted). The receiver is never mutated in place.
func mergePerson(existing PersonRecord, found bool, key string, m PersonMention) PersonRecord {
	if !found {
		return PersonRecord{
			PersonKey:   key,
			DisplayName: m.DisplayName,
			Aka:         []string{m.DisplayName},
			FirstSeenAt: m.At,
			LastSeenAt:  m.At,
			EntryRefs:   []string{m.RawEntryID},
		}
	}

	rec := existing
	rec.PersonKey = key
	rec.DisplayName = m.DisplayName // latest spelling wins
	rec.Aka = addUnique(rec.Aka, m.DisplayName)
	slices.Sort(rec.Aka)
	if !m.At.IsZero() && (rec.FirstSeenAt.IsZero() || m.At.Before(rec.FirstSeenAt)) {
		rec.FirstSeenAt = m.At
	}
	if m.At.After(rec.LastSeenAt) {
		rec.LastSeenAt = m.At
	}
	rec.EntryRefs = addUnique(rec.EntryRefs, m.RawEntryID)
	slices.Sort(rec.EntryRefs)
	return rec
}

// addUnique appends v to xs only if it is not already present, returning the
// (possibly unchanged) slice. It keeps aka[] and entry_refs[] free of
// duplicates so a repeated mention is a no-op.
func addUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// encode renders a PersonRecord as its on-disk JSON shape, normalizing nil
// slices to empty arrays so aka[] and entry_refs[] never serialize as null,
// and formatting timestamps with the host's local TZ offset (data-model.md
// §"Time zone rule").
func (r PersonRecord) encode() personRecordJSON {
	return personRecordJSON{
		PersonKey:   r.PersonKey,
		DisplayName: r.DisplayName,
		Aka:         orEmpty(r.Aka),
		FirstSeenAt: r.FirstSeenAt.Format(time.RFC3339),
		LastSeenAt:  r.LastSeenAt.Format(time.RFC3339),
		EntryRefs:   orEmpty(r.EntryRefs),
		Notes:       r.Notes,
	}
}

// decode parses the on-disk JSON shape back into a PersonRecord, returning
// the (found=true) tuple shape ReadPerson uses.
func (j personRecordJSON) decode() (PersonRecord, bool, error) {
	first, err := parseOptionalTime(j.FirstSeenAt)
	if err != nil {
		return PersonRecord{}, false, fmt.Errorf("storage: person first_seen_at: %w", err)
	}
	last, err := parseOptionalTime(j.LastSeenAt)
	if err != nil {
		return PersonRecord{}, false, fmt.Errorf("storage: person last_seen_at: %w", err)
	}
	return PersonRecord{
		PersonKey:   j.PersonKey,
		DisplayName: j.DisplayName,
		Aka:         j.Aka,
		FirstSeenAt: first,
		LastSeenAt:  last,
		EntryRefs:   j.EntryRefs,
		Notes:       j.Notes,
	}, true, nil
}

// parseOptionalTime parses an RFC3339 timestamp, treating the empty string as
// the zero time so a record written before a field was set round-trips.
func parseOptionalTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}
