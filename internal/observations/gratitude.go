package observations

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// gratitude.go is the gratitude record family (gratitude.md §2): the
// accumulating nightly-gratitude tally. A gratitude entry is a registry-shaped
// referent — a salted low-signal key, alternate wordings, an append-and-redirect
// tombstone — but instead of the plain status_history the other registries
// carry, it keeps a typed occurrence/seed/merge/expressed history that the
// derived Count/First/Last (and each linked person's last-expressed date) are
// folded from. This package owns the schema, the id
// grammar, the fold, and the pure value helpers; the storage adapter owns the
// single-writer append/read discipline (architecture P3), and no path here
// touches the filesystem or an LLM (P9).

// GratitudeSchema is the gratitude entry schema version (gratitude.md §2). It is
// versioned per record family exactly as the observation envelope and the other
// registries are: new needs go in a new optional field or a new event type under
// a bumped schema — readers tolerate an unknown field and a higher version.
// Schema 2 adds the optional people[] links, the tally-neutral `expressed` event,
// and the optional match_tier / match_score / person / source_expressed event
// fields; every addition is optional, so a schema-1 entry reads unchanged and is
// written back as schema 2 on its next write, with no migration pass.
const GratitudeSchema = 2

// GratitudeSchemaV1 is the oldest gratitude schema still read and accepted
// (gratitude.md §2 Versioning): readers and Validate take 1 through
// [GratitudeSchema].
const GratitudeSchemaV1 = 1

// Gratitude event types (gratitude.md §2 "The typed history"). Every mutation
// appends exactly one event; no event is ever rewritten.
const (
	// GratitudeEventOccurrence is a nightly `add` (or `add --into`): +1 at its
	// logical date, widening the first/last span.
	GratitudeEventOccurrence = "occurrence"
	// GratitudeEventSeed is a one-time `import` (or `add --count …`): an explicit
	// Count with explicit First/Last, fabricating no per-occurrence dates.
	GratitudeEventSeed = "seed"
	// GratitudeEventMerge folds a source entry's whole history into the
	// destination (its count and its first/last span) and names the source key it
	// absorbed — an auditable record of the fold.
	GratitudeEventMerge = "merge"
	// GratitudeEventExpressed is a `thank <id> --person <subject>`: a private
	// record that you told the named person, on its logical date. It is
	// tally-neutral — it contributes nothing to Count/First/Last and feeds only
	// that person's last-expressed date (gratitude.md §2, §8).
	GratitudeEventExpressed = "expressed"
)

// Gratitude event sources (gratitude.md §9): provenance so a hand-tallied
// occurrence is always distinguishable from a one-time migration seed.
const (
	// GratitudeSourceGratitude is a normal nightly `lucid gratitude add`.
	GratitudeSourceGratitude = "gratitude"
	// GratitudeSourceMigration is a one-time seed/import of a pre-counted row.
	GratitudeSourceMigration = "migration"
)

// GratitudeEvent is one append-only entry in a gratitude record's typed history
// (gratitude.md §2). Each event carries a receipt id unique across the whole
// Ledger (the seq is a per-logical-date high-water mark across the whole
// registry: one above the highest seq any entry already holds for that date —
// see [NextGratitudeSeqForDate]), a real write timestamp At, a Type, and the
// type-specific fields the fold reads. Two different referents tallied the same
// night therefore get two different receipts. Ids minted before this rule used a
// per-entry seq and may repeat across entries; they are never rewritten and stay
// valid. Collection and per-type fields are omitempty so an occurrence line stays lean
// and a seed line carries only its count/span. The receipt id encodes the
// event's logical date, so a backdated occurrence's receipt reflects the logical
// day, not the recording time.
//
// MatchTier and MatchScore attribute an occurrence that an automatic match
// landed (gratitude.md §2, §7.7): the tier (2 by wording, 3 by meaning) whose
// High band chose the entry, and its winning score. They are set on nothing
// else — a canonical-key (tier 1) bump, an `--into` bump, a human-confirmed
// suggestion, and a create carry neither, so those stay exactly the v1 shape —
// and they never change what the event contributes to the fold.
//
// Person names the person key an event concerns (gratitude.md §2, §8): on an
// `expressed` event, whom you told; on an occurrence, the person an `add
// --person` linked in the same write. SourceExpressed, on a merge, carries the
// absorbed source's last-expressed date per person so the destination's fold of
// last-expressed dates stays local, like the source's count and span.
type GratitudeEvent struct {
	ID         string  `json:"id"`
	At         string  `json:"at"`
	Type       string  `json:"type"`
	Date       string  `json:"date,omitempty"`        // occurrence/expressed: the logical date
	Source     string  `json:"source,omitempty"`      // occurrence/seed provenance
	MatchTier  int     `json:"match_tier,omitempty"`  // occurrence: the tier that matched it automatically
	MatchScore float64 `json:"match_score,omitempty"` // occurrence: that tier's winning score
	Person     string  `json:"person,omitempty"`      // expressed: whom you told; occurrence: the person `add --person` linked
	Count      int     `json:"count,omitempty"`       // seed: the explicit count (+N)
	First      string  `json:"first,omitempty"`       // seed: the explicit first date
	Last       string  `json:"last,omitempty"`        // seed: the explicit last date
	// merge fields: the absorbed source key and its derived count + span (and
	// last-expressed dates), so the fold reads the merge locally without
	// re-reading the tombstoned source.
	SourceKey       string            `json:"source_key,omitempty"`
	SourceCount     int               `json:"source_count,omitempty"`
	SourceFirst     string            `json:"source_first,omitempty"`
	SourceLast      string            `json:"source_last,omitempty"`
	SourceExpressed map[string]string `json:"source_expressed,omitempty"`
}

// GratitudeEntry is one gratitude referent (gratitude.md §2): one JSON object,
// one file per entry, under registries/gratitude/, keyed by the salted
// kind-prefixed registry key. The count and the first/last span are DERIVED from
// History and never stored ([GratitudeEntry.Tally]); RedirectTo is set only on a
// merge tombstone, naming the canonical entry a merged-away duplicate resolves
// to. People lists the person keys the entry is linked to (gratitude.md §8) —
// grow-only, omitted when unlinked. Field order matches the documented schema so
// a marshaled entry reads like the spec.
type GratitudeEntry struct {
	Key         string           `json:"key"`
	Kind        string           `json:"kind"`
	Schema      int              `json:"schema"`
	DisplayName string           `json:"display_name"`
	Aka         []string         `json:"aka"`
	People      []string         `json:"people,omitempty"`
	History     []GratitudeEvent `json:"history"`
	RedirectTo  string           `json:"redirect_to"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
}

// GratitudeTally is the derived count and first/last span folded from an entry's
// append-only history (gratitude.md §2 "Deriving the tally"). It is computed at
// read time and never written back onto the record.
type GratitudeTally struct {
	Count int
	First string
	Last  string
}

// NewGratitudeEntry builds a fresh gratitude record for a first tally — the
// entry itself, with no events yet; the caller appends the first occurrence.
func NewGratitudeEntry(key, displayName, at string) GratitudeEntry {
	return GratitudeEntry{
		Key:         key,
		Kind:        RegistryGratitude,
		Schema:      GratitudeSchema,
		DisplayName: displayName,
		Aka:         []string{displayName},
		History:     []GratitudeEvent{},
		CreatedAt:   at,
		UpdatedAt:   at,
	}
}

// Tally folds the count and the first/last span over the append-only history
// (gratitude.md §2): an occurrence contributes +1 at its date; a seed
// contributes its explicit count and first/last; a merge contributes the
// absorbed source's count and span; an `expressed` event contributes nothing —
// telling someone never moves the tally. Because the tally is a pure fold, it is
// rebuildable from the events alone and no derived number is stored.
func (e GratitudeEntry) Tally() GratitudeTally {
	var t GratitudeTally
	for _, ev := range e.History {
		switch ev.Type {
		case GratitudeEventOccurrence:
			t.Count++
			t.widen(ev.Date)
		case GratitudeEventSeed:
			t.Count += ev.Count
			t.widen(ev.First)
			t.widen(ev.Last)
		case GratitudeEventMerge:
			t.Count += ev.SourceCount
			t.widen(ev.SourceFirst)
			t.widen(ev.SourceLast)
		case GratitudeEventExpressed:
			// Tally-neutral by design (gratitude.md §0, §8): expression is a
			// private note, never a count, so it moves neither Count nor the span.
		}
	}
	return t
}

// LastExpressed folds, per person key, the latest date you recorded telling
// that person about this gratitude (gratitude.md §2 "Deriving the tally"): the
// latest `date` across the entry's `expressed` events naming them, and any
// merge event's absorbed source_expressed date for them. Keys are the person
// keys as stored — the router resolves them forward through person redirects
// at read time. A person never told is absent; the map is empty, never nil.
func (e GratitudeEntry) LastExpressed() map[string]string {
	out := map[string]string{}
	note := func(person, date string) {
		person = strings.TrimSpace(person)
		if person == "" || date == "" {
			return
		}
		if date > out[person] {
			out[person] = date
		}
	}
	for _, ev := range e.History {
		switch ev.Type {
		case GratitudeEventExpressed:
			note(ev.Person, ev.Date)
		case GratitudeEventMerge:
			for person, date := range ev.SourceExpressed {
				note(person, date)
			}
		}
	}
	return out
}

// LinkPerson adds a person key to the entry's people[] (gratitude.md §8):
// grow-only and deduplicated, a blank key a no-op. The receiver is not mutated.
func (e GratitudeEntry) LinkPerson(personKey string) GratitudeEntry {
	personKey = strings.TrimSpace(personKey)
	if personKey == "" || slices.Contains(e.People, personKey) {
		return e
	}
	out := e
	out.People = append(slices.Clone(e.People), personKey)
	return out
}

// widen extends the span to include date (YYYY-MM-DD compares lexically for a
// real span); an empty date contributes nothing.
func (t *GratitudeTally) widen(date string) {
	if date == "" {
		return
	}
	if t.First == "" || date < t.First {
		t.First = date
	}
	if t.Last == "" || date > t.Last {
		t.Last = date
	}
}

// IsTombstone reports whether this entry is a merge redirect (gratitude.md §4):
// a merged-away duplicate kept as an auditable redirect, omitted from the active
// tally rather than deleted.
func (e GratitudeEntry) IsTombstone() bool {
	return strings.TrimSpace(e.RedirectTo) != ""
}

// RecordWording notes a phrasing this entry was tallied under. It always adds
// the phrase to Aka (deduplicated) for readability; when makePrimary is set (a
// plain `add`, whose canonical key already matched) it also refreshes
// DisplayName to the latest wording. An `add --into` bump records the wording
// without changing the canonical display, since that wording may be a different
// phrase for the same thing (gratitude.md §4). The receiver is not mutated.
func (e GratitudeEntry) RecordWording(phrase string, makePrimary bool) GratitudeEntry {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return e
	}
	out := e
	out.Aka = slices.Clone(e.Aka)
	if makePrimary {
		out.DisplayName = phrase
	}
	if !slices.Contains(out.Aka, phrase) {
		out.Aka = append(out.Aka, phrase)
	}
	return out
}

// Normalized returns a copy whose slice fields are non-nil so the written record
// always carries [] rather than null — a stable on-disk shape.
func (e GratitudeEntry) Normalized() GratitudeEntry {
	if e.Aka == nil {
		e.Aka = []string{}
	}
	if e.History == nil {
		e.History = []GratitudeEvent{}
	}
	return e
}

// Validate reports the first structural problem before an entry is written. It
// accepts every schema this build reads — 1 through [GratitudeSchema]
// (gratitude.md §2 Versioning) — and refuses a newer one it cannot write back
// faithfully.
func (e GratitudeEntry) Validate() error {
	if e.Schema < GratitudeSchemaV1 || e.Schema > GratitudeSchema {
		return fmt.Errorf("observations: unsupported gratitude schema %d (want %d–%d)", e.Schema, GratitudeSchemaV1, GratitudeSchema)
	}
	if e.Kind != RegistryGratitude {
		return fmt.Errorf("observations: gratitude entry has wrong kind %q", e.Kind)
	}
	if strings.TrimSpace(e.Key) == "" {
		return fmt.Errorf("observations: gratitude entry is missing key")
	}
	if strings.TrimSpace(e.DisplayName) == "" {
		return fmt.Errorf("observations: gratitude entry is missing display_name")
	}
	return nil
}

// GratitudeReceiptID renders a receipt id for a gratitude event (gratitude.md §2
// Ids: grat_<logical_date>_<seq>, the date in underscores, seq zero-padded to
// three digits, wider values legal). The caller supplies seq from
// [NextGratitudeSeqForDate], which makes the id unique across the whole Ledger.
// It never collides with a stable entry key, which is a word-slug
// (gratitude_<slug>).
func GratitudeReceiptID(logicalDate string, seq int) string {
	return fmt.Sprintf("grat_%s_%03d", strings.ReplaceAll(logicalDate, "-", "_"), seq)
}

// ParseGratitudeReceiptSeq extracts the numeric sequence from a receipt id,
// parsed numerically so a wider value (grat_..._1000) is legal. It returns
// ok=false for anything that is not a well-formed gratitude receipt id, so the
// single-writer seq derivation ignores such an event rather than counting it.
func ParseGratitudeReceiptSeq(id string) (seq int, ok bool) {
	if !strings.HasPrefix(id, "grat_") {
		return 0, false
	}
	i := strings.LastIndex(id, "_")
	if i < 0 || i+1 >= len(id) {
		return 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// ParseGratitudeReceiptID splits a receipt id (grat_<YYYY>_<MM>_<DD>_<seq>) into
// the logical date it encodes, in the YYYY-MM-DD form [GratitudeReceiptID] takes,
// and its numeric seq (via [ParseGratitudeReceiptSeq], so a wider seq is legal).
// It returns ok=false for anything that is not a well-formed gratitude receipt —
// a stable entry key, a wrong segment count, a non-digit date part, or a
// non-numeric/negative seq — so the registry-wide seq derivation skips it.
func ParseGratitudeReceiptID(id string) (date string, seq int, ok bool) {
	parts := strings.Split(id, "_")
	if len(parts) != 5 || parts[0] != "grat" {
		return "", 0, false
	}
	if !allDigits(parts[1], 4) || !allDigits(parts[2], 2) || !allDigits(parts[3], 2) {
		return "", 0, false
	}
	seq, ok = ParseGratitudeReceiptSeq(id)
	if !ok {
		return "", 0, false
	}
	return parts[1] + "-" + parts[2] + "-" + parts[3], seq, true
}

// allDigits reports whether s is exactly n ASCII digits.
func allDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// NextGratitudeSeqForDate returns the next receipt seq for logicalDate: one above
// the highest seq any receipt already carries for that date across the whole
// gratitude registry (gratitude.md §2 Ids: a per-logical-date high-water mark,
// never a count, single-writer). entries is the full registry — live entries and
// tombstones alike, since a tombstone's frozen history still holds minted ids.
// Historical ids minted under the old per-entry rule count toward the mark, so a
// new receipt can never duplicate one. A date with no receipt starts at seq 1;
// an event whose id is not a well-formed receipt is ignored, so a hand-edited
// line never perturbs id assignment.
func NextGratitudeSeqForDate(entries []GratitudeEntry, logicalDate string) int {
	maxSeq := 0
	for _, entry := range entries {
		for _, ev := range entry.History {
			if d, s, ok := ParseGratitudeReceiptID(ev.ID); ok && d == logicalDate && s > maxSeq {
				maxSeq = s
			}
		}
	}
	return maxSeq + 1
}
