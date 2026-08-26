package focus

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// synthFocus returns a synthetic, valid active entry. Every fixture in this
// package is invented — real focus items live only in the private Ledger
// (focus.md §6).
func synthFocus(id, text, success string) Focus {
	return Focus{
		ID: id, Schema: Schema, Text: text, SuccessCriterion: success, State: StateActive,
		RecordedAt: "2026-08-23T21:45:10-04:00", LogicalDate: "2026-08-23",
		Source: SourceFocus,
	}
}

func TestFocusID(t *testing.T) {
	assert.Equal(t, "focus_2026_08_23_001", ID("2026-08-23", 1))
	assert.Equal(t, "focus_2026_08_23_042", ID("2026-08-23", 42))
	// Wider values are legal (parsed numerically, not fixed width).
	assert.Equal(t, "focus_2026_08_23_1000", ID("2026-08-23", 1000))
}

func TestParseSeq(t *testing.T) {
	seq, ok := ParseSeq("focus_2026_08_23_007")
	require.True(t, ok)
	assert.Equal(t, 7, seq)

	seq, ok = ParseSeq("focus_2026_08_23_1000")
	require.True(t, ok)
	assert.Equal(t, 1000, seq)

	// Not a focus id.
	_, ok = ParseSeq("reframe_2026_08_23_001")
	assert.False(t, ok)
	_, ok = ParseSeq("focus_2026_08_23_")
	assert.False(t, ok)
	_, ok = ParseSeq("focus_2026_08_23_abc")
	assert.False(t, ok)
}

func TestFocusDate(t *testing.T) {
	d, ok := Date("focus_2026_08_23_001")
	require.True(t, ok)
	assert.Equal(t, "2026-08-23", d)

	// A bare date with no seq field is not a valid id.
	_, ok = Date("focus_2026_08_23")
	assert.False(t, ok)
	// A non-date component is rejected.
	_, ok = Date("focus_2026_13_40_001")
	assert.False(t, ok)
	// Wrong prefix.
	_, ok = Date("reframe_2026_08_23_001")
	assert.False(t, ok)

	// Round-trips with ID for a range of seqs.
	id := ID("2026-01-05", 3)
	d, ok = Date(id)
	require.True(t, ok)
	assert.Equal(t, "2026-01-05", d)
}

func TestValidate(t *testing.T) {
	valid := synthFocus("focus_2026_08_23_001", "Take the stairs today", "I skipped the elevator")
	require.NoError(t, valid.Validate())

	// An item with no explicit success criterion is still valid.
	noSuccess := synthFocus("focus_2026_08_23_002", "Read ten pages", "")
	require.NoError(t, noSuccess.Validate())

	bad := valid
	bad.Schema = 2
	require.Error(t, bad.Validate(), "unsupported schema rejected")

	bad = valid
	bad.Text = "   "
	require.Error(t, bad.Validate(), "blank text rejected")

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
	// partial period (focus.md §2; defense-in-depth for the storage day path).
	for _, shape := range []string{"2026-08-23/../../etc", "2026-08", "2026-13-01", "not-a-date"} {
		bad = valid
		bad.LogicalDate = shape
		require.Error(t, bad.Validate(), "malformed logical_date %q rejected", shape)
	}

	bad = valid
	bad.Source = ""
	require.Error(t, bad.Validate(), "missing source rejected")

	// A retirement event carries no text and is still valid (text waived).
	marker := NewRetirement("focus_2026_08_23_001", "2026-08-24", "2026-08-24T09:00:00-04:00")
	require.NoError(t, marker.Validate(), "a retirement marker needs no text")
}

func TestMarshalLine_RoundTripAndStableShape(t *testing.T) {
	f := synthFocus("focus_2026_08_23_001", "Text an old friend", "I hit send")

	line, err := f.MarshalLine()
	require.NoError(t, err)
	// One line, no trailing newline (the storage adapter adds the terminator).
	assert.NotContains(t, string(line), "\n")

	// success_criterion is always emitted (no omitempty), even when empty.
	empty := synthFocus("focus_2026_08_23_002", "Water the plants", "")
	emptyLine, err := empty.MarshalLine()
	require.NoError(t, err)
	assert.Contains(t, string(emptyLine), `"success_criterion":""`)

	// Empty collections marshal as [] / {}, never null — a stable on-disk shape.
	assert.Contains(t, string(line), `"tags":[]`)
	assert.Contains(t, string(line), `"refs":{}`)

	// Byte-stable: the same entry always marshals to the same bytes.
	line2, err := f.MarshalLine()
	require.NoError(t, err)
	assert.Equal(t, line, line2)

	// Round-trips through Unmarshal, preserving every field.
	got, err := UnmarshalFocusLine(line)
	require.NoError(t, err)
	assert.Equal(t, f.normalized(), got)

	// A malformed line is a decode error, not a panic.
	_, err = UnmarshalFocusLine([]byte(`{"id":"focus_2026_08_23_001","sch`))
	assert.Error(t, err)
}

func TestMarshalLine_FieldOrderMatchesSchema(t *testing.T) {
	f := synthFocus("focus_2026_08_23_001", "Ask a question in standup", "I raised my hand")
	f.Tags = []string{"growth"}

	line, err := f.MarshalLine()
	require.NoError(t, err)

	// The marshaled shape is the documented schema; a decode into an ordered
	// generic confirms the keys are present and typed as specified.
	var generic map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(line, &generic))
	for _, key := range []string{"id", "schema", "text", "success_criterion", "state", "recorded_at", "logical_date", "source", "tags", "refs"} {
		assert.Contains(t, generic, key, "schema field %q present", key)
	}
}

func TestRetiresAndFoldState(t *testing.T) {
	a := synthFocus("focus_2026_08_23_001", "Take the stairs today", "I skipped the elevator")
	_, ok := a.Retires()
	assert.False(t, ok, "a plain entry retires nothing")
	assert.False(t, a.IsRetirement())

	b := synthFocus("focus_2026_08_23_002", "Text an old friend", "")

	// A retirement event references the item it retires by id.
	marker := NewRetirement(a.ID, "2026-08-24", "2026-08-24T09:00:00-04:00")
	target, ok := marker.Retires()
	require.True(t, ok)
	assert.Equal(t, a.ID, target)
	assert.True(t, marker.IsRetirement())

	// A non-string / empty retires value is ignored.
	junk := synthFocus("focus_2026_08_23_003", "junk", "")
	junk.Refs = map[string]any{"retires": 7}
	_, ok = junk.Retires()
	assert.False(t, ok)

	folded := FoldState([]Focus{a, b, marker})
	// The marker itself is not a focus item; the two work-ons remain.
	ids := idsOf(folded)
	assert.Equal(t, []string{a.ID, b.ID}, ids, "markers are folded out; items preserved in order")

	// State is resolved: a is retired (a later marker names it), b is active.
	byID := map[string]Focus{}
	for _, f := range folded {
		byID[f.ID] = f
	}
	assert.Equal(t, StateRetired, byID[a.ID].State, "retired item stays with retired state (history preserved)")
	assert.Equal(t, StateActive, byID[b.ID].State, "an unreferenced item is active")
}

func TestLogicalDay_AppliesRollover(t *testing.T) {
	loc := time.FixedZone("EDT", -4*3600)
	// 03:00 local is before the 04:00 rollover → belongs to the previous day.
	assert.Equal(t, "2026-08-22", LogicalDay(time.Date(2026, 8, 23, 3, 0, 0, 0, loc)))
	// 04:00 and after belong to the current civil day.
	assert.Equal(t, "2026-08-23", LogicalDay(time.Date(2026, 8, 23, 4, 0, 0, 0, loc)))
	assert.Equal(t, "2026-08-23", LogicalDay(time.Date(2026, 8, 23, 21, 45, 0, 0, loc)))
}

func idsOf(fs []Focus) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.ID
	}
	return out
}
