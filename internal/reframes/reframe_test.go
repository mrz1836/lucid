package reframes

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// synthReframe returns a synthetic, valid entry. Every fixture in this package
// is invented — real reframes live only in the private Ledger (reframes.md §6).
func synthReframe(id, catch, flip, logicalDate string) Reframe {
	return Reframe{
		ID: id, Schema: Schema, Catch: catch, Flip: flip,
		RecordedAt: "2026-08-23T21:45:10-04:00", LogicalDate: logicalDate,
		Source: SourceReframe,
	}
}

func TestReframeID(t *testing.T) {
	assert.Equal(t, "reframe_2026_08_23_001", ReframeID("2026-08-23", 1))
	assert.Equal(t, "reframe_2026_08_23_042", ReframeID("2026-08-23", 42))
	// Wider values are legal (parsed numerically, not fixed width).
	assert.Equal(t, "reframe_2026_08_23_1000", ReframeID("2026-08-23", 1000))
}

func TestParseSeq(t *testing.T) {
	seq, ok := ParseSeq("reframe_2026_08_23_007")
	require.True(t, ok)
	assert.Equal(t, 7, seq)

	seq, ok = ParseSeq("reframe_2026_08_23_1000")
	require.True(t, ok)
	assert.Equal(t, 1000, seq)

	// Not a reframe id.
	_, ok = ParseSeq("obs_2026_08_23_001")
	assert.False(t, ok)
	_, ok = ParseSeq("reframe_2026_08_23_")
	assert.False(t, ok)
	_, ok = ParseSeq("reframe_2026_08_23_abc")
	assert.False(t, ok)
}

func TestReframeDate(t *testing.T) {
	d, ok := ReframeDate("reframe_2026_08_23_001")
	require.True(t, ok)
	assert.Equal(t, "2026-08-23", d)

	// A bare date with no seq field is not a valid id.
	_, ok = ReframeDate("reframe_2026_08_23")
	assert.False(t, ok)
	// A non-date component is rejected.
	_, ok = ReframeDate("reframe_2026_13_40_001")
	assert.False(t, ok)
	// Wrong prefix.
	_, ok = ReframeDate("obs_2026_08_23_001")
	assert.False(t, ok)

	// Round-trips with ReframeID for a range of seqs.
	id := ReframeID("2026-01-05", 3)
	d, ok = ReframeDate(id)
	require.True(t, ok)
	assert.Equal(t, "2026-01-05", d)
}

func TestValidate(t *testing.T) {
	valid := synthReframe("reframe_2026_08_23_001", "I can't do this", "I can learn this", "2026-08-23")
	require.NoError(t, valid.Validate())

	bad := valid
	bad.Schema = 2
	require.Error(t, bad.Validate(), "unsupported schema rejected")

	bad = valid
	bad.Catch = "   "
	require.Error(t, bad.Validate(), "blank catch rejected")

	bad = valid
	bad.Flip = ""
	require.Error(t, bad.Validate(), "empty flip rejected")

	bad = valid
	bad.RecordedAt = ""
	require.Error(t, bad.Validate(), "missing recorded_at rejected")

	bad = valid
	bad.LogicalDate = ""
	require.Error(t, bad.Validate(), "missing logical_date rejected")

	// A logical_date must be a real YYYY-MM-DD civil day, not a separator- or
	// dot-bearing value that a day-file path could be built from, and not a
	// partial period (reframes.md §2; defense-in-depth for the storage day path).
	for _, shape := range []string{"2026-08-23/../../etc", "2026-08", "2026-13-01", "not-a-date"} {
		bad = valid
		bad.LogicalDate = shape
		require.Error(t, bad.Validate(), "malformed logical_date %q rejected", shape)
	}

	bad = valid
	bad.Source = ""
	require.Error(t, bad.Validate(), "missing source rejected")
}

func TestMarshalLine_RoundTripAndStableShape(t *testing.T) {
	r := synthReframe("reframe_2026_08_23_001", "I always mess up", "I'm still practicing", "2026-08-23")

	line, err := r.MarshalLine()
	require.NoError(t, err)
	// One line, no trailing newline (the storage adapter adds the terminator).
	assert.NotContains(t, string(line), "\n")

	// Empty collections marshal as [] / {}, never null — a stable on-disk shape.
	assert.Contains(t, string(line), `"tags":[]`)
	assert.Contains(t, string(line), `"refs":{}`)

	// Byte-stable: the same entry always marshals to the same bytes.
	line2, err := r.MarshalLine()
	require.NoError(t, err)
	assert.Equal(t, line, line2)

	// Round-trips through Unmarshal, preserving every field.
	got, err := UnmarshalReframeLine(line)
	require.NoError(t, err)
	assert.Equal(t, r.normalized(), got)

	// A malformed line is a decode error, not a panic.
	_, err = UnmarshalReframeLine([]byte(`{"id":"reframe_2026_08_23_001","sch`))
	assert.Error(t, err)
}

func TestMarshalLine_FieldOrderMatchesSchema(t *testing.T) {
	r := synthReframe("reframe_2026_08_23_001", "This is too hard", "This is worth the effort", "2026-08-23")
	r.Tags = []string{"growth"}

	line, err := r.MarshalLine()
	require.NoError(t, err)

	// The marshaled shape is the documented schema; a decode into an ordered
	// generic confirms the keys are present and typed as specified.
	var generic map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(line, &generic))
	for _, key := range []string{"id", "schema", "catch", "flip", "recorded_at", "logical_date", "source", "tags", "refs"} {
		assert.Contains(t, generic, key, "schema field %q present", key)
	}
}

func TestCorrectsAndFoldCorrections(t *testing.T) {
	orig := synthReframe("reframe_2026_08_23_001", "I can't do this", "I can learn this", "2026-08-23")
	_, ok := orig.Corrects()
	assert.False(t, ok, "a plain entry corrects nothing")

	correction := synthReframe("reframe_2026_08_23_002", "I can't do this", "I choose to learn this", "2026-08-23")
	correction.Refs = map[string]any{"corrects": orig.ID}
	target, ok := correction.Corrects()
	require.True(t, ok)
	assert.Equal(t, orig.ID, target)

	// A non-string / empty corrects value is ignored.
	junk := synthReframe("reframe_2026_08_23_003", "a", "b", "2026-08-23")
	junk.Refs = map[string]any{"corrects": 7}
	_, ok = junk.Corrects()
	assert.False(t, ok)

	folded := FoldCorrections([]Reframe{orig, correction, junk})
	ids := idsOf(folded)
	assert.NotContains(t, ids, orig.ID, "the superseded original drops out")
	assert.Contains(t, ids, correction.ID, "the correction survives")
	assert.Contains(t, ids, junk.ID, "an unrelated entry survives")

	// A chain of corrections folds transitively: c3 → c2 → orig.
	c2 := synthReframe("reframe_2026_08_24_001", "I can't do this", "I am learning this", "2026-08-24")
	c2.Refs = map[string]any{"corrects": correction.ID}
	c3 := synthReframe("reframe_2026_08_25_001", "I can't do this", "I keep learning this", "2026-08-25")
	c3.Refs = map[string]any{"corrects": c2.ID}
	chain := FoldCorrections([]Reframe{orig, correction, c2, c3})
	assert.Equal(t, []string{c3.ID}, idsOf(chain), "only the newest link survives the chain")
}

func TestLogicalDay_AppliesRollover(t *testing.T) {
	loc := time.FixedZone("EDT", -4*3600)
	// 03:00 local is before the 04:00 rollover → belongs to the previous day.
	assert.Equal(t, "2026-08-22", LogicalDay(time.Date(2026, 8, 23, 3, 0, 0, 0, loc)))
	// 04:00 and after belong to the current civil day.
	assert.Equal(t, "2026-08-23", LogicalDay(time.Date(2026, 8, 23, 4, 0, 0, 0, loc)))
	assert.Equal(t, "2026-08-23", LogicalDay(time.Date(2026, 8, 23, 21, 45, 0, 0, loc)))
}

func idsOf(rfs []Reframe) []string {
	out := make([]string, len(rfs))
	for i, r := range rfs {
		out[i] = r.ID
	}
	return out
}
