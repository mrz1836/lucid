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
