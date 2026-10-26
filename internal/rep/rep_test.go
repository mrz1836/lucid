package rep

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// synthRep returns a synthetic, valid active entry. Every fixture in this
// package is invented — real reps live only in the private Ledger (reps.md §7).
func synthRep(id, text, solo string, level int) Rep {
	return Rep{
		ID: id, Schema: Schema, Text: text, Solo: solo, Level: level, State: StateActive,
		RecordedAt: "2026-08-23T21:45:10-04:00", LogicalDate: "2026-08-23",
		Source: SourceRep,
	}
}

func TestRepID(t *testing.T) {
	assert.Equal(t, "rep_2026_08_23_001", ID("2026-08-23", 1))
	assert.Equal(t, "rep_2026_08_23_042", ID("2026-08-23", 42))
	// Wider values are legal (parsed numerically, not fixed width).
	assert.Equal(t, "rep_2026_08_23_1000", ID("2026-08-23", 1000))
}

func TestRepParseSeq(t *testing.T) {
	seq, ok := ParseSeq("rep_2026_08_23_007")
	require.True(t, ok)
	assert.Equal(t, 7, seq)

	seq, ok = ParseSeq("rep_2026_08_23_1000")
	require.True(t, ok)
	assert.Equal(t, 1000, seq)

	// Not a rep id.
	_, ok = ParseSeq("focus_2026_08_23_001")
	assert.False(t, ok)
	_, ok = ParseSeq("rep_2026_08_23_")
	assert.False(t, ok)
	_, ok = ParseSeq("rep_2026_08_23_abc")
	assert.False(t, ok)
	_, ok = ParseSeq("rep_2026_08_23_-1")
	assert.False(t, ok)
}

func TestRepDate(t *testing.T) {
	d, ok := Date("rep_2026_08_23_001")
	require.True(t, ok)
	assert.Equal(t, "2026-08-23", d)

	// A bare date with no seq field is not a valid id.
	_, ok = Date("rep_2026_08_23")
	assert.False(t, ok)
	// A non-date component is rejected.
	_, ok = Date("rep_2026_13_40_001")
	assert.False(t, ok)
	// Wrong prefix.
	_, ok = Date("focus_2026_08_23_001")
	assert.False(t, ok)

	// Round-trips with ID.
	id := ID("2026-01-05", 3)
	d, ok = Date(id)
	require.True(t, ok)
	assert.Equal(t, "2026-01-05", d)
}

func TestRepValidate(t *testing.T) {
	valid := synthRep("rep_2026_08_23_001", "Ask one question in a meeting", "Write the question down and send it afterward", 2)
	require.NoError(t, valid.Validate())

	// A rep with no solo variant and no level (unlevelled, 0) is still valid.
	bare := synthRep("rep_2026_08_23_002", "Try one new route home", "", 0)
	require.NoError(t, bare.Validate(), "level 0 is the valid unlevelled value")

	// A large level is legal — there is no maximum.
	big := synthRep("rep_2026_08_23_003", "Try one new route home", "", 40)
	require.NoError(t, big.Validate(), "no maximum level")

	bad := valid
	bad.Schema = 2
	require.Error(t, bad.Validate(), "unsupported schema rejected")

	bad = valid
	bad.Text = "   "
	require.Error(t, bad.Validate(), "blank text rejected")

	bad = valid
	bad.Text = ""
	require.Error(t, bad.Validate(), "empty text rejected")

	bad = valid
	bad.Level = -1
	require.Error(t, bad.Validate(), "negative level rejected")

	bad = valid
	bad.State = "paused"
	require.Error(t, bad.Validate(), "unknown state rejected")

	bad = valid
	bad.RecordedAt = ""
	require.Error(t, bad.Validate(), "missing recorded_at rejected")

	bad = valid
	bad.LogicalDate = ""
	require.Error(t, bad.Validate(), "missing logical_date rejected")

	// A logical_date must be a real YYYY-MM-DD civil day, not a separator- or
	// dot-bearing value that a day-file path could be built from, and not a
	// partial period (defense-in-depth for the storage day path).
	for _, shape := range []string{"2026-08-23/../../etc", "2026-08", "2026-13-01", "not-a-date"} {
		bad = valid
		bad.LogicalDate = shape
		require.Error(t, bad.Validate(), "malformed logical_date %q rejected", shape)
	}

	bad = valid
	bad.Source = ""
	require.Error(t, bad.Validate(), "missing source rejected")

	// A retirement event carries no text and is still valid (text waived).
	marker := NewRetirement("rep_2026_08_23_001", "2026-08-24", "2026-08-24T09:00:00-04:00")
	require.NoError(t, marker.Validate(), "a retirement marker needs no text")
}

// TestRepSchema_TextSoloLevelOnly locks the entry schema (reps.md §2): text plus
// an optional solo and an optional level, and nothing else rep-specific — no
// third variant field. Solo and level are always emitted, so an entry with only
// text set still carries "solo": "" and "level": 0.
func TestRepSchema_TextSoloLevelOnly(t *testing.T) {
	r := Rep{Text: "Ask one question in a meeting"}

	line, err := r.MarshalLine()
	require.NoError(t, err)

	var generic map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(line, &generic))

	keys := make([]string, 0, len(generic))
	for k := range generic {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"id", "level", "logical_date", "recorded_at", "refs", "schema", "solo", "source", "state", "tags", "text"}
	assert.Equal(t, want, keys, "the exact documented key set — no third variant field")

	assert.JSONEq(t, `""`, string(generic["solo"]), "solo is always emitted, empty when unset")
	assert.JSONEq(t, `0`, string(generic["level"]), "level is always emitted, 0 = unlevelled")
	assert.JSONEq(t, `[]`, string(generic["tags"]))
	assert.JSONEq(t, `{}`, string(generic["refs"]))
	assert.JSONEq(t, `"Ask one question in a meeting"`, string(generic["text"]))

	// A solo variant and a level round-trip verbatim.
	r.Solo = "Write the question down and send it afterward"
	r.Level = 6
	line, err = r.MarshalLine()
	require.NoError(t, err)
	got, err := UnmarshalRepLine(line)
	require.NoError(t, err)
	assert.Equal(t, "Write the question down and send it afterward", got.Solo)
	assert.Equal(t, 6, got.Level)
	assert.Equal(t, r.Text, got.Text)
}

func TestRepMarshalLine_RoundTripAndStableShape(t *testing.T) {
	r := synthRep("rep_2026_08_23_001", "Ask one question in a meeting", "Write the question down and send it afterward", 2)

	line, err := r.MarshalLine()
	require.NoError(t, err)
	// One line, no trailing newline (the storage adapter adds the terminator).
	assert.NotContains(t, string(line), "\n")

	// Empty collections marshal as [] / {}, never null — a stable on-disk shape.
	assert.Contains(t, string(line), `"tags":[]`)
	assert.Contains(t, string(line), `"refs":{}`)

	// The content and the field order both match the documented schema.
	assert.JSONEq(t,
		`{"id":"rep_2026_08_23_001","schema":1,"text":"Ask one question in a meeting",`+
			`"solo":"Write the question down and send it afterward","level":2,"state":"active",`+
			`"recorded_at":"2026-08-23T21:45:10-04:00","logical_date":"2026-08-23","source":"rep",`+
			`"tags":[],"refs":{}}`,
		string(line))
	assert.Equal(t,
		[]string{"id", "schema", "text", "solo", "level", "state", "recorded_at", "logical_date", "source", "tags", "refs"},
		topLevelKeyOrder(t, line), "struct field order is the documented schema order")

	// Byte-stable: the same entry always marshals to the same bytes.
	line2, err := r.MarshalLine()
	require.NoError(t, err)
	assert.Equal(t, line, line2)

	// Round-trips through Unmarshal, preserving every field.
	got, err := UnmarshalRepLine(line)
	require.NoError(t, err)
	assert.Equal(t, r.normalized(), got)

	// An unlevelled rep with no solo variant keeps the same shape.
	bare := synthRep("rep_2026_08_23_002", "Try one new route home", "", 0)
	bareLine, err := bare.MarshalLine()
	require.NoError(t, err)
	assert.Contains(t, string(bareLine), `"solo":""`)
	assert.Contains(t, string(bareLine), `"level":0`)

	// A malformed line is a decode error, not a panic.
	_, err = UnmarshalRepLine([]byte(`{"id":"rep_2026_08_23_001","sch`))
	assert.Error(t, err)
}

func TestRepRetiresAndFoldState(t *testing.T) {
	a := synthRep("rep_2026_08_23_001", "Ask one question in a meeting", "", 2)
	_, ok := a.Retires()
	assert.False(t, ok, "a plain entry retires nothing")
	assert.False(t, a.IsRetirement())

	b := synthRep("rep_2026_08_23_002", "Try one new route home", "", 0)

	// A retirement event references the rep it retires by id.
	marker := NewRetirement(a.ID, "2026-08-24", "2026-08-24T09:00:00-04:00")
	target, ok := marker.Retires()
	require.True(t, ok)
	assert.Equal(t, a.ID, target)
	assert.True(t, marker.IsRetirement())
	assert.Equal(t, SourceRep, marker.Source)
	assert.Empty(t, marker.Text, "a retirement marker carries no text")

	// A non-string / empty retires value is ignored.
	junk := synthRep("rep_2026_08_23_003", "junk", "", 0)
	junk.Refs = map[string]any{"retires": 7}
	_, ok = junk.Retires()
	assert.False(t, ok)
	junk.Refs = map[string]any{"retires": ""}
	_, ok = junk.Retires()
	assert.False(t, ok)

	folded := FoldState([]Rep{a, b, marker})
	// The marker itself is not a rep; the two reps remain, in order.
	assert.Equal(t, []string{a.ID, b.ID}, repIDs(folded), "markers are folded out; reps preserved in order")

	byID := map[string]Rep{}
	for _, r := range folded {
		byID[r.ID] = r
	}
	assert.Equal(t, StateRetired, byID[a.ID].State, "retired rep stays with retired state (history preserved)")
	assert.Equal(t, StateActive, byID[b.ID].State, "an unreferenced rep is active")
}

func TestRepLogicalDay_AppliesRollover(t *testing.T) {
	loc := time.FixedZone("EDT", -4*3600)
	// 03:00 local is before the 04:00 rollover → belongs to the previous day.
	assert.Equal(t, "2026-08-22", LogicalDay(time.Date(2026, 8, 23, 3, 0, 0, 0, loc)))
	// 04:00 and after belong to the current civil day.
	assert.Equal(t, "2026-08-23", LogicalDay(time.Date(2026, 8, 23, 4, 0, 0, 0, loc)))
	assert.Equal(t, "2026-08-23", LogicalDay(time.Date(2026, 8, 23, 21, 45, 0, 0, loc)))
}

// topLevelKeyOrder returns the top-level object keys of a JSON line in the
// order they appear on the wire.
func topLevelKeyOrder(t *testing.T, line []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(line))
	tok, err := dec.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), tok)
	var keys []string
	for dec.More() {
		k, kerr := dec.Token()
		require.NoError(t, kerr)
		key, ok := k.(string)
		require.True(t, ok)
		keys = append(keys, key)
		var skip json.RawMessage
		require.NoError(t, dec.Decode(&skip))
	}
	return keys
}

func repIDs(rs []Rep) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}
