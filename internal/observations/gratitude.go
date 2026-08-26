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
// carry, it keeps a typed occurrence/seed/merge history that the derived
// Count/First/Last are folded from. This package owns the schema, the id
// grammar, the fold, and the pure value helpers; the storage adapter owns the
// single-writer append/read discipline (architecture P3), and no path here
// touches the filesystem or an LLM (P9).

// GratitudeSchema is the gratitude entry schema version (gratitude.md §2). It is
// versioned per record family exactly as the observation envelope and the other
// registries are: new needs go in a new optional field or a new event type under
// a bumped schema — readers tolerate an unknown field and a higher version.
const GratitudeSchema = 1

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
// (gratitude.md §2). Each event carries its own unique receipt id, a real write
// timestamp At, a Type, and the type-specific fields the fold reads. Collection
// and per-type fields are omitempty so an occurrence line stays lean and a seed
// line carries only its count/span. The receipt id encodes the event's logical
// date, so a backdated occurrence's receipt reflects the logical day, not the
// recording time.
type GratitudeEvent struct {
	ID     string `json:"id"`
	At     string `json:"at"`
	Type   string `json:"type"`
	Date   string `json:"date,omitempty"`   // occurrence: the logical date it counts at
	Source string `json:"source,omitempty"` // occurrence/seed provenance
	Count  int    `json:"count,omitempty"`  // seed: the explicit count (+N)
	First  string `json:"first,omitempty"`  // seed: the explicit first date
	Last   string `json:"last,omitempty"`   // seed: the explicit last date
	// merge fields: the absorbed source key and its derived count + span, so the
	// fold reads the merge locally without re-reading the tombstoned source.
	SourceKey   string `json:"source_key,omitempty"`
	SourceCount int    `json:"source_count,omitempty"`
	SourceFirst string `json:"source_first,omitempty"`
	SourceLast  string `json:"source_last,omitempty"`
}

// GratitudeEntry is one gratitude referent (gratitude.md §2): one JSON object,
// one file per entry, under registries/gratitude/, keyed by the salted
// kind-prefixed registry key. The count and the first/last span are DERIVED from
// History and never stored ([GratitudeEntry.Tally]); RedirectTo is set only on a
// merge tombstone, naming the canonical entry a merged-away duplicate resolves
// to. Field order matches the documented schema so a marshaled entry reads like
// the spec.
type GratitudeEntry struct {
	Key         string           `json:"key"`
	Kind        string           `json:"kind"`
	Schema      int              `json:"schema"`
	DisplayName string           `json:"display_name"`
	Aka         []string         `json:"aka"`
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
// absorbed source's count and span. Because the tally is a pure fold, it is
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
		}
	}
	return t
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

// Validate reports the first structural problem before an entry is written.
func (e GratitudeEntry) Validate() error {
	if e.Schema != GratitudeSchema {
		return fmt.Errorf("observations: unsupported gratitude schema %d (want %d)", e.Schema, GratitudeSchema)
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
// three digits, wider values legal). It never collides with a stable entry key,
// which is a word-slug (gratitude_<slug>).
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

// NextGratitudeSeq returns max-seq+1 over the entry's history (gratitude.md §2:
// never a count, single-writer). A fresh entry starts at seq 1; an event whose
// id is not a well-formed receipt is ignored, so a hand-edited line never
// perturbs id assignment.
func NextGratitudeSeq(history []GratitudeEvent) int {
	maxSeq := 0
	for _, ev := range history {
		if s, ok := ParseGratitudeReceiptSeq(ev.ID); ok && s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq + 1
}
