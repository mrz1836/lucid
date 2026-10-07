package observations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strptr(s string) *string { return &s }

func rangeEvent(id, start, endRFC string) Event {
	return Event{
		ID: id, Schema: Schema, Kind: KindSleep,
		RecordedAt: endRFC, OccurredAt: start + "T23:00:00-04:00",
		OccurredAtPrecision: PrecisionRange, OccurredAtEnd: strptr(endRFC),
		LogicalDate: start, Source: SourceMicrolog,
		Payload: map[string]any{"quality": 3},
	}
}

func TestIsRangeSpanning(t *testing.T) {
	night := rangeEvent("obs_2026_07_01_004", "2026-07-01", "2026-07-02T07:10:00-04:00")

	assert.True(t, IsRangeSpanning(night, "2026-07-02", loc), "spans into the morning-after day")
	assert.False(t, IsRangeSpanning(night, "2026-07-01", loc), "own start day is not 'spanning'")
	assert.False(t, IsRangeSpanning(night, "2026-07-03", loc), "past the end")

	// A non-range event never spans.
	point := Event{OccurredAtPrecision: PrecisionExact, LogicalDate: "2026-07-01"}
	assert.False(t, IsRangeSpanning(point, "2026-07-02", loc))

	// A range with no end never spans.
	noEnd := night
	noEnd.OccurredAtEnd = nil
	assert.False(t, IsRangeSpanning(noEnd, "2026-07-02", loc))
}

func TestAssembleDayView_SortsAndFiltersSpanning(t *testing.T) {
	dayEvents := []Event{
		{ID: "obs_2026_07_02_003", Kind: KindPain, Payload: map[string]any{"intensity": 6}},
		{ID: "obs_2026_07_02_001", Kind: KindMood, Payload: map[string]any{"level": 3}},
		{ID: "obs_2026_07_02_002", Kind: KindElimination, Payload: map[string]any{"class": "bm"}},
	}
	candidates := []Event{
		rangeEvent("obs_2026_07_01_004", "2026-07-01", "2026-07-02T07:10:00-04:00"), // spans in
		rangeEvent("obs_2026_06_30_001", "2026-06-30", "2026-06-30T07:00:00-04:00"), // does not span
	}
	dv := AssembleDayView("2026-07-02", dayEvents, candidates, loc)

	require.Len(t, dv.Events, 3)
	assert.Equal(t, "obs_2026_07_02_001", dv.Events[0].ID, "events sorted by id")
	assert.Equal(t, "obs_2026_07_02_003", dv.Events[2].ID)

	require.Len(t, dv.RangeEvents, 1, "only the spanning range event surfaces")
	assert.Equal(t, "obs_2026_07_01_004", dv.RangeEvents[0].ID)
	assert.False(t, dv.Empty())
}

func TestDayView_Empty(t *testing.T) {
	assert.True(t, AssembleDayView("2026-07-02", nil, nil, loc).Empty())
}

// TestAssembleDayView_FoldsMemoryAmendments proves `/day` folds a memory and its
// in-day amendment into a single line carrying the amended values, not two
// separate lines (AC-11, fold-on-read everywhere). The amendment reuses the
// target's logical_date, so both land in the same day slice and fold correctly.
func TestAssembleDayView_FoldsMemoryAmendments(t *testing.T) {
	base := Event{
		ID: "obs_2010_07_15_001", Schema: Schema, Kind: KindMemory,
		OccurredAt: "2010-07-15T00:00:00Z", OccurredAtPrecision: PrecisionExact,
		LogicalDate: "2010-07-15", Source: SourceExcavation,
		Payload: map[string]any{MemoryFieldText: "the drive", MemoryFieldCertainty: "hazy"},
	}
	amend := Event{
		ID: "obs_2010_07_15_002", Schema: Schema, Kind: KindMemory,
		OccurredAt: "2010-07-15T00:00:00Z", OccurredAtPrecision: PrecisionExact,
		LogicalDate: "2010-07-15", Source: SourceExcavation,
		Payload: map[string]any{MemoryFieldCertainty: "vivid"},
		Refs:    map[string]any{RefCorrects: "obs_2010_07_15_001"},
	}

	dv := AssembleDayView("2010-07-15", []Event{base, amend}, nil, loc)

	require.Len(t, dv.Events, 1, "the amendment folds onto the base — one memory line, not two")
	assert.Equal(t, "obs_2010_07_15_001", dv.Events[0].ID)
	assert.Equal(t, "vivid", dv.Events[0].Payload[MemoryFieldCertainty], "the day view reflects the amended certainty")
	assert.Equal(t, "the drive", dv.Events[0].Payload[MemoryFieldText], "an unamended field is intact")

	line := strings.Join(dv.Lines(), "\n")
	assert.Contains(t, line, "certainty=vivid")
	assert.NotContains(t, line, "certainty=hazy", "the pre-amend value never renders after the fold")

	// The input slice is never mutated — the base event still carries its original
	// certainty (the fold works on a copy).
	assert.Equal(t, "hazy", base.Payload[MemoryFieldCertainty], "AssembleDayView folds on a copy, leaving the input untouched")
}

// TestAssembleDayView_FoldsWorkoutAmendments proves `/day` folds a logged
// session and its same-day correction into one line carrying the corrected
// values, alongside a memory amendment on the same day; and that a re-date —
// filed under the new day — leaves the original day's view on the base dates
// while the new day drops the correction whose base it cannot see (the
// documented one-day-read limit).
func TestAssembleDayView_FoldsWorkoutAmendments(t *testing.T) {
	session := Event{
		ID: "obs_2026_01_15_001", Schema: Schema, Kind: KindWorkout,
		RecordedAt: "2026-01-15T19:05:00-05:00", OccurredAt: "2026-01-15T19:05:00-05:00",
		OccurredAtPrecision: PrecisionExact, LogicalDate: "2026-01-15", Source: SourceMicrolog,
		Payload: map[string]any{"type": "climbing", "duration_min": 60},
	}
	correction := Event{
		ID: "obs_2026_01_15_002", Schema: Schema, Kind: KindWorkout,
		RecordedAt: "2026-01-15T21:30:00.123456789-05:00", OccurredAt: "2026-01-15T19:05:00-05:00",
		OccurredAtPrecision: PrecisionExact, LogicalDate: "2026-01-15", Source: SourceMicrolog,
		Payload: map[string]any{"rpe": 4},
		Refs:    map[string]any{RefCorrects: "obs_2026_01_15_001"},
	}
	memory := memoryEvent("obs_2026_01_15_003", "2026-01-15", map[string]any{MemoryFieldText: "old"}, nil)
	memAmend := Event{
		ID: "obs_2026_01_15_004", Schema: Schema, Kind: KindMemory,
		RecordedAt: "2026-01-15T22:00:00-05:00", OccurredAt: memory.OccurredAt,
		OccurredAtPrecision: PrecisionExact, LogicalDate: "2026-01-15", Source: SourceExcavation,
		Payload: map[string]any{MemoryFieldText: "new"},
		Refs:    map[string]any{RefCorrects: memory.ID},
	}

	dv := AssembleDayView("2026-01-15", []Event{session, correction, memory, memAmend}, nil, loc)

	require.Len(t, dv.Events, 2, "one session line and one memory line — both corrections folded")
	got := dv.Events[0]
	assert.Equal(t, "obs_2026_01_15_001", got.ID)
	assert.Equal(t, 4, got.Payload["rpe"], "the day view reflects the corrected rpe")
	assert.Equal(t, 60, got.Payload["duration_min"], "an uncorrected field is intact")
	assert.Equal(t, "new", dv.Events[1].Payload[MemoryFieldText], "memory amendments still fold")

	line := strings.Join(dv.Lines(), "\n")
	assert.Contains(t, line, "rpe=4")
	assert.NotContains(t, line, "obs_2026_01_15_002", "the correction never renders as its own line")
	_, mutated := session.Payload["rpe"]
	assert.False(t, mutated, "AssembleDayView folds on a copy, leaving the input untouched")

	t.Run("a re-date is a one-day-read limit", func(t *testing.T) {
		redate := Event{
			ID: "obs_2026_01_14_001", Schema: Schema, Kind: KindWorkout,
			RecordedAt: "2026-01-15T21:31:00-05:00", OccurredAt: "2026-01-14T00:00:00-05:00",
			OccurredAtPrecision: PrecisionApproximate, LogicalDate: "2026-01-14", Source: SourceMicrolog,
			Payload: map[string]any{},
			Refs:    map[string]any{RefCorrects: "obs_2026_01_15_001", RefRedate: true},
		}
		original := AssembleDayView("2026-01-15", []Event{session}, nil, loc)
		require.Len(t, original.Events, 1)
		assert.Equal(t, "2026-01-15", original.Events[0].LogicalDate, "the original day keeps the session on its base dates")

		moved := AssembleDayView("2026-01-14", []Event{redate}, nil, loc)
		assert.True(t, moved.Empty(), "the new day drops a re-date whose base it cannot see")
	})
}

// TestDayView_Lines_ByteStableAndInventoryOnly: the render is deterministic
// (byte-stable across reruns) and free of evaluative language (§0).
func TestDayView_Lines_ByteStableAndInventoryOnly(t *testing.T) {
	dayEvents := []Event{
		{ID: "obs_2026_07_02_001", Kind: KindPain, Payload: map[string]any{"site": "knee", "intensity": 6}},
		{ID: "obs_2026_07_02_002", Kind: KindMood, Payload: map[string]any{"level": 3, "word": "steady"}},
	}
	candidates := []Event{rangeEvent("obs_2026_07_01_004", "2026-07-01", "2026-07-02T07:10:00-04:00")}
	dv := AssembleDayView("2026-07-02", dayEvents, candidates, loc)

	first := strings.Join(dv.Lines(), "\n")
	second := strings.Join(AssembleDayView("2026-07-02", dayEvents, candidates, loc).Lines(), "\n")
	assert.Equal(t, first, second, "the render is byte-stable across reruns")

	// Payload keys render in sorted order (intensity before site).
	assert.Contains(t, first, "pain intensity=6, site=knee (obs_2026_07_02_001)")
	assert.Contains(t, first, "(spanning)")

	for _, banned := range []string{"streak", "good", "keep it up", "great", "score"} {
		assert.NotContainsf(t, strings.ToLower(first), banned,
			"the day view is inventory, never obligation — %q must not appear", banned)
	}
}
