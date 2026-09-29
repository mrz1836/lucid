package observations

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gratitude_test.go covers the pure gratitude value helpers (gratitude.go): the
// schema constructor, the count/first-last fold, the id grammar, and the small
// record transforms. Every fixture here is synthetic — invented gratitudes and
// dates — never a real entry.

// TestNewGratitudeEntry_Shape: a fresh entry carries the kind, the current
// schema, an empty typed history, and seeds Aka with the display name so the
// first wording is always searchable.
func TestNewGratitudeEntry_Shape(t *testing.T) {
	e := NewGratitudeEntry("gratitude_a-river", "morning coffee", "2026-01-02T03:04:05Z")

	assert.Equal(t, "gratitude_a-river", e.Key)
	assert.Equal(t, RegistryGratitude, e.Kind)
	assert.Equal(t, GratitudeSchema, e.Schema)
	assert.Equal(t, "morning coffee", e.DisplayName)
	assert.Equal(t, []string{"morning coffee"}, e.Aka)
	assert.Empty(t, e.History)
	assert.NotNil(t, e.History, "history is [] not nil so the on-disk shape is stable")
	assert.Equal(t, "2026-01-02T03:04:05Z", e.CreatedAt)
	assert.Equal(t, "2026-01-02T03:04:05Z", e.UpdatedAt)
	assert.Empty(t, e.RedirectTo)
	assert.False(t, e.IsTombstone())
}

// TestGratitudeTally_FoldsOccurrenceSeedMerge: the tally is a pure fold — an
// occurrence contributes +1 at its date, a seed contributes its explicit count
// and first/last span, and a merge contributes the absorbed source's count and
// span. The first/last widen across all three event types.
func TestGratitudeTally_FoldsOccurrenceSeedMerge(t *testing.T) {
	e := GratitudeEntry{
		History: []GratitudeEvent{
			{Type: GratitudeEventOccurrence, Date: "2026-03-10"},
			{Type: GratitudeEventOccurrence, Date: "2026-03-12"},
			{Type: GratitudeEventSeed, Count: 4, First: "2026-01-01", Last: "2026-02-01"},
			{Type: GratitudeEventMerge, SourceKey: "gratitude_b-dupe", SourceCount: 2, SourceFirst: "2025-12-20", SourceLast: "2026-04-01"},
		},
	}

	tally := e.Tally()
	assert.Equal(t, 1+1+4+2, tally.Count, "occurrence +1 each, seed +count, merge +source_count")
	assert.Equal(t, "2025-12-20", tally.First, "first widens to the earliest across all event types")
	assert.Equal(t, "2026-04-01", tally.Last, "last widens to the latest across all event types")
}

// TestGratitudeTally_EmptyAndUnknown: an empty history folds to zero with no
// span, and an unknown event type contributes nothing (forward-compatible
// readers tolerate a newer event type without miscounting).
func TestGratitudeTally_EmptyAndUnknown(t *testing.T) {
	assert.Equal(t, GratitudeTally{}, GratitudeEntry{}.Tally())

	e := GratitudeEntry{History: []GratitudeEvent{
		{Type: "some-future-event", Date: "2026-05-05"},
		{Type: GratitudeEventOccurrence, Date: ""}, // an empty date counts but never widens the span
	}}
	tally := e.Tally()
	assert.Equal(t, 1, tally.Count, "the occurrence still counts")
	assert.Empty(t, tally.First, "an empty date contributes nothing to the span")
	assert.Empty(t, tally.Last)
}

// TestGratitudeEntry_IsTombstone: an entry is a tombstone exactly when its
// RedirectTo names a canonical entry; whitespace is not a redirect.
func TestGratitudeEntry_IsTombstone(t *testing.T) {
	assert.False(t, GratitudeEntry{}.IsTombstone())
	assert.False(t, GratitudeEntry{RedirectTo: "   "}.IsTombstone())
	assert.True(t, GratitudeEntry{RedirectTo: "gratitude_a-river"}.IsTombstone())
}

// TestGratitudeEntry_RecordWording: a plain add (makePrimary) refreshes the
// display and appends the wording; an --into bump records the wording in Aka
// without changing the canonical display. A blank phrase is a no-op, duplicates
// are not re-added, and the receiver is never mutated.
func TestGratitudeEntry_RecordWording(t *testing.T) {
	base := NewGratitudeEntry("gratitude_a-river", "my house", "2026-01-01T00:00:00Z")

	primary := base.RecordWording("  my home  ", true)
	assert.Equal(t, "my home", primary.DisplayName, "a plain add refreshes the display to the latest wording")
	assert.Equal(t, []string{"my house", "my home"}, primary.Aka)
	assert.Equal(t, "my house", base.DisplayName, "the receiver is not mutated")
	assert.Equal(t, []string{"my house"}, base.Aka)

	into := base.RecordWording("roof over my head", false)
	assert.Equal(t, "my house", into.DisplayName, "an --into bump keeps the canonical display")
	assert.Equal(t, []string{"my house", "roof over my head"}, into.Aka)

	dup := primary.RecordWording("my house", true)
	assert.Equal(t, []string{"my house", "my home"}, dup.Aka, "an existing wording is not duplicated")

	blank := base.RecordWording("   ", true)
	assert.Equal(t, base, blank, "a blank phrase is a no-op")
}

// TestGratitudeEntry_Normalized: nil slice fields become empty slices so a
// written record always carries [] rather than null.
func TestGratitudeEntry_Normalized(t *testing.T) {
	n := GratitudeEntry{}.Normalized()
	assert.NotNil(t, n.Aka)
	assert.NotNil(t, n.History)
	assert.Empty(t, n.Aka)
	assert.Empty(t, n.History)
}

// TestGratitudeEntry_Validate: Validate reports the first structural problem and
// accepts a well-formed entry.
func TestGratitudeEntry_Validate(t *testing.T) {
	good := NewGratitudeEntry("gratitude_a-river", "the ocean", "2026-01-01T00:00:00Z")
	require.NoError(t, good.Validate())

	badSchema := good
	badSchema.Schema = GratitudeSchema + 1
	require.ErrorContains(t, badSchema.Validate(), "unsupported gratitude schema")

	badKind := good
	badKind.Kind = "injury"
	require.ErrorContains(t, badKind.Validate(), "wrong kind")

	noKey := good
	noKey.Key = "  "
	require.ErrorContains(t, noKey.Validate(), "missing key")

	noName := good
	noName.DisplayName = ""
	require.ErrorContains(t, noName.Validate(), "missing display_name")
}

// TestGratitudeReceiptID_Format: the receipt id renders as grat_<date>_<seq>
// with the date in underscores and the seq zero-padded to three digits, and a
// wider seq stays legal.
func TestGratitudeReceiptID_Format(t *testing.T) {
	assert.Equal(t, "grat_2026_03_10_001", GratitudeReceiptID("2026-03-10", 1))
	assert.Equal(t, "grat_2026_12_31_042", GratitudeReceiptID("2026-12-31", 42))
	assert.Equal(t, "grat_2026_01_01_1000", GratitudeReceiptID("2026-01-01", 1000))
}

// TestParseGratitudeReceiptSeq: a well-formed receipt yields its numeric seq
// (padded or wider), and anything else reports ok=false so a hand-edited line
// never perturbs seq derivation.
func TestParseGratitudeReceiptSeq(t *testing.T) {
	seq, ok := ParseGratitudeReceiptSeq("grat_2026_03_10_001")
	assert.True(t, ok)
	assert.Equal(t, 1, seq)

	seq, ok = ParseGratitudeReceiptSeq("grat_2026_01_01_1000")
	assert.True(t, ok)
	assert.Equal(t, 1000, seq)

	for _, bad := range []string{
		"",                    // empty
		"injury_a-cedar",      // a stable entry key, not a receipt
		"grat_",               // no trailing seq segment
		"grat_2026_03_10_",    // trailing underscore, empty seq
		"grat_2026_03_10_abc", // non-numeric seq
		"grat_2026_03_10_-1",  // negative seq
	} {
		_, ok := ParseGratitudeReceiptSeq(bad)
		assert.Falsef(t, ok, "%q is not a well-formed receipt id", bad)
	}
}

// TestNextGratitudeSeq: a fresh entry starts at 1, otherwise it is max-seq+1,
// and a malformed id is ignored rather than counted.
func TestNextGratitudeSeq(t *testing.T) {
	assert.Equal(t, 1, NextGratitudeSeq(nil))
	assert.Equal(t, 1, NextGratitudeSeq([]GratitudeEvent{}))

	history := []GratitudeEvent{
		{ID: "grat_2026_03_10_001"},
		{ID: "grat_2026_03_12_005"},
		{ID: "hand-edited-line"}, // ignored, not counted
	}
	assert.Equal(t, 6, NextGratitudeSeq(history), "max seq 5 + 1")
}

// TestGratitudeMatchTier_EventAttribution: an automatic landing's match_tier and
// match_score ride on the occurrence (gratitude.md §2, §7.7) and round-trip; an
// occurrence with neither marshals exactly as the v1 shape (no match keys at
// all); and the attribution never changes what the event folds to — an
// auto-matched occurrence is +1 at its date like any other.
func TestGratitudeMatchTier_EventAttribution(t *testing.T) {
	auto := GratitudeEvent{
		ID: "grat_2026_03_12_002", At: "2026-03-12T21:00:00Z", Type: GratitudeEventOccurrence,
		Date: "2026-03-12", Source: GratitudeSourceGratitude, MatchTier: 3, MatchScore: 0.94,
	}
	b, err := json.Marshal(auto)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"grat_2026_03_12_002","at":"2026-03-12T21:00:00Z","type":"occurrence",
		"date":"2026-03-12","source":"gratitude","match_tier":3,"match_score":0.94}`, string(b))
	var back GratitudeEvent
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, auto, back, "the attribution round-trips")

	plain := auto
	plain.MatchTier, plain.MatchScore = 0, 0
	b, err = json.Marshal(plain)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "match_tier", "a tier-1, --into, or confirmed landing stays the v1 shape")
	assert.NotContains(t, string(b), "match_score")

	e := GratitudeEntry{History: []GratitudeEvent{
		{Type: GratitudeEventOccurrence, Date: "2026-03-10"},
		auto,
	}}
	assert.Equal(t, GratitudeTally{Count: 2, First: "2026-03-10", Last: "2026-03-12"}, e.Tally(),
		"an auto-matched occurrence folds exactly like any other")
}

// TestGratitudeMatchTier_ReadsSchemaOneAndUnknownFields: a schema-1 entry written
// before match attribution existed still decodes and folds unchanged, and a
// reader tolerates fields and event types it does not know (gratitude.md §2
// Versioning — read what you understand, skip what you don't).
func TestGratitudeMatchTier_ReadsSchemaOneAndUnknownFields(t *testing.T) {
	const v1 = `{
	  "key": "gratitude_a-river", "kind": "gratitude", "schema": 1,
	  "display_name": "my morning coffee", "aka": ["my morning coffee"],
	  "history": [
	    {"id": "grat_2026_03_10_001", "at": "2026-03-10T21:00:00Z", "type": "occurrence", "date": "2026-03-10", "source": "gratitude"},
	    {"id": "grat_2026_03_11_002", "at": "2026-03-11T21:00:00Z", "type": "seed", "count": 3, "first": "2026-01-01", "last": "2026-02-01", "source": "migration"}
	  ],
	  "redirect_to": "", "created_at": "2026-03-10T21:00:00Z", "updated_at": "2026-03-11T21:00:00Z"
	}`
	var e GratitudeEntry
	require.NoError(t, json.Unmarshal([]byte(v1), &e))
	require.NoError(t, e.Validate(), "a schema-1 entry is a valid entry")
	assert.Zero(t, e.History[0].MatchTier, "a v1 occurrence carries no attribution")
	assert.Equal(t, GratitudeTally{Count: 4, First: "2026-01-01", Last: "2026-03-10"}, e.Tally())

	const future = `{
	  "key": "gratitude_a-river", "kind": "gratitude", "schema": 1,
	  "display_name": "my morning coffee", "aka": ["my morning coffee"],
	  "a_future_field": ["anything"],
	  "history": [
	    {"id": "grat_2026_03_10_001", "at": "2026-03-10T21:00:00Z", "type": "occurrence", "date": "2026-03-10", "match_tier": 2, "match_score": 1, "a_future_key": true},
	    {"id": "grat_2026_03_12_002", "at": "2026-03-12T21:00:00Z", "type": "a_future_type", "date": "2026-03-12"}
	  ],
	  "redirect_to": "", "created_at": "2026-03-10T21:00:00Z", "updated_at": "2026-03-12T21:00:00Z"
	}`
	var f GratitudeEntry
	require.NoError(t, json.Unmarshal([]byte(future), &f), "unknown fields are skipped, not fatal")
	assert.Equal(t, 2, f.History[0].MatchTier)
	assert.Equal(t, GratitudeTally{Count: 1, First: "2026-03-10", Last: "2026-03-10"}, f.Tally(),
		"an unknown event type contributes nothing")
}
