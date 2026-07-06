package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// completed builds a minimal completed day record for a logical date.
func completed(date string) DayRecord {
	return DayRecord{
		DayID:       "day_" + date[:4] + "_" + date[5:7] + "_" + date[8:10],
		LogicalDate: date,
		Links:       map[string]string{"journal": StatusDone},
		Completed:   true,
	}
}

func TestFolded_AppliesCorrectionsInOrder(t *testing.T) {
	rec := DayRecord{
		LogicalDate: "2026-07-05",
		Links:       map[string]string{"journal": StatusFloor},
		Completed:   false,
		Missed:      true,
		Partial:     true,
		Capacity:    2,
		Corrections: []Correction{
			{Fields: map[string]any{"completed": true, "missed": false, "partial": false, "capacity": 4}},
			{Fields: map[string]any{"capacity": 5, "limiter_tag": "wrist"}},
		},
	}
	f := rec.Folded()
	assert.True(t, f.Completed)
	assert.False(t, f.Missed)
	assert.False(t, f.Partial)
	assert.Equal(t, 5, f.Capacity) // last write wins
	assert.Equal(t, "wrist", f.LimiterTag)

	// Original body is untouched.
	assert.False(t, rec.Completed)
	assert.Equal(t, 2, rec.Capacity)
}

func TestFolded_LinksAndRawEntryID(t *testing.T) {
	rec := DayRecord{
		Links:      map[string]string{"journal": StatusSkipped},
		RawEntryID: "raw_a",
		Corrections: []Correction{
			{Fields: map[string]any{
				"links":        map[string]any{"journal": StatusDone, "dock": StatusFloor},
				"floor_day":    true,
				"raw_entry_id": "raw_b",
			}},
		},
	}
	f := rec.Folded()
	assert.Equal(t, map[string]string{"journal": StatusDone, "dock": StatusFloor}, f.Links)
	assert.True(t, f.FloorDay)
	assert.Equal(t, "raw_b", f.RawEntryID)
	// The original links map is not mutated by the fold.
	assert.Equal(t, map[string]string{"journal": StatusSkipped}, rec.Links)
}

func TestFolded_NativeAndJSONTypes(t *testing.T) {
	// capacity arrives as a JSON float64 (read from disk) or a Go int
	// (built in-process); both fold.
	rec := DayRecord{Corrections: []Correction{
		{Fields: map[string]any{"capacity": float64(3)}},
	}}
	assert.Equal(t, 3, rec.Folded().Capacity)

	rec = DayRecord{Corrections: []Correction{
		{Fields: map[string]any{"capacity": int64(4)}},
	}}
	assert.Equal(t, 4, rec.Folded().Capacity)
}

func TestFolded_IgnoresIllTypedField(t *testing.T) {
	// A hand-edited file with a wrong-typed value is ignored, not a panic.
	rec := DayRecord{
		Completed:   true,
		Corrections: []Correction{{Fields: map[string]any{"completed": "yes", "links": 7}}},
	}
	f := rec.Folded()
	assert.True(t, f.Completed) // string "yes" ignored, original kept
}

func TestFoldableAndImmutableFields(t *testing.T) {
	for _, ok := range []string{"links", "floor_day", "completed", "missed", "partial", "capacity", "limiter_tag", "raw_entry_id"} {
		assert.Truef(t, FoldableField(ok), "%s should be foldable", ok)
	}
	for _, no := range []string{"mode", "mode_declared_at", "logical_date", "day_id", "recorded_at", "storm", "profile"} {
		assert.Falsef(t, FoldableField(no), "%s should be immutable", no)
	}

	legal := Correction{Fields: map[string]any{"completed": true, "capacity": 3}}
	assert.Empty(t, ImmutableCorrectionFields(legal))

	illegal := Correction{Fields: map[string]any{"mode": "red", "storm": true, "completed": true}}
	assert.Equal(t, []string{"mode", "storm"}, ImmutableCorrectionFields(illegal))
}

func TestComputeStreaks(t *testing.T) {
	loc := time.UTC
	// Empty.
	assert.Equal(t, Streaks{}, ComputeStreaks(nil, loc))

	// A 3-day run.
	recs := []DayRecord{completed("2026-07-03"), completed("2026-07-04"), completed("2026-07-05")}
	assert.Equal(t, Streaks{Current: 3, Longest: 3}, ComputeStreaks(recs, loc))

	// A gap breaks the current streak but the longest run is preserved.
	recs = []DayRecord{
		completed("2026-07-01"), completed("2026-07-02"), completed("2026-07-03"), // run of 3
		completed("2026-07-06"), completed("2026-07-07"), // run of 2, most recent
	}
	assert.Equal(t, Streaks{Current: 2, Longest: 3}, ComputeStreaks(recs, loc))

	// A non-completed day in the set does not count.
	recs = []DayRecord{completed("2026-07-04"), {LogicalDate: "2026-07-05", Missed: true}, completed("2026-07-06")}
	assert.Equal(t, Streaks{Current: 1, Longest: 1}, ComputeStreaks(recs, loc))
}

func TestComputeStreaks_DedupAndNilLoc(t *testing.T) {
	recs := []DayRecord{completed("2026-07-04"), completed("2026-07-04"), completed("2026-07-05")}
	assert.Equal(t, Streaks{Current: 2, Longest: 2}, ComputeStreaks(recs, nil))
}

func TestComputeStreaks_SkipsMalformedDate(t *testing.T) {
	recs := []DayRecord{{LogicalDate: "not-a-date", Completed: true}, completed("2026-07-05")}
	assert.Equal(t, Streaks{Current: 1, Longest: 1}, ComputeStreaks(recs, time.UTC))
}

func TestEarliestCompletedDate(t *testing.T) {
	assert.Empty(t, EarliestCompletedDate(nil, time.UTC))
	recs := []DayRecord{completed("2026-07-05"), completed("2026-07-02"), {LogicalDate: "2026-07-01", Missed: true}}
	assert.Equal(t, "2026-07-02", EarliestCompletedDate(recs, time.UTC))
}
