package workout

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The synthetic ExampleProgram is anchored to 2026-01-05 (a Monday) and defines
// targets_by_week only for week 2, so its ramp exercises every branch of the week
// math: days 0–6 are week 1 (base targets), days 7–13 are week 2 (the override),
// and anything later holds at week 2's numbers.
const (
	anchorStartDay  = "2026-01-05T12:00:00Z" // day 0 — week 1
	anchorWeek1End  = "2026-01-11T12:00:00Z" // day 6 — still week 1
	anchorWeek2Day  = "2026-01-12T12:00:00Z" // day 7 — week 2 begins
	anchorWeek3Day  = "2026-01-19T12:00:00Z" // day 14 — week 3, past the last defined week
	anchorWeek5Day  = "2026-02-02T12:00:00Z" // day 28 — week 5
	anchorPreStart  = "2026-01-01T12:00:00Z" // four days before the program starts
	anchorFarBefore = "2025-12-01T12:00:00Z" // five weeks before the program starts
)

// --- Week derivation from start_date. ---

// TestProgramWeekDerivation pins the 1-indexed week math: the index counts from
// start_date at the Ledger's rollover boundary, never falls below 1, and treats an
// absent or unparseable start_date as week 1.
func TestProgramWeekDerivation(t *testing.T) {
	tests := []struct {
		name      string
		startDate string
		now       string
		want      int
	}{
		{name: "start day is week 1", startDate: "2026-01-05", now: anchorStartDay, want: 1},
		{name: "day 6 is still week 1", startDate: "2026-01-05", now: anchorWeek1End, want: 1},
		{name: "day 7 opens week 2", startDate: "2026-01-05", now: anchorWeek2Day, want: 2},
		{name: "day 13 is still week 2", startDate: "2026-01-05", now: "2026-01-18T12:00:00Z", want: 2},
		{name: "day 14 opens week 3", startDate: "2026-01-05", now: anchorWeek3Day, want: 3},
		{name: "day 28 is week 5", startDate: "2026-01-05", now: anchorWeek5Day, want: 5},
		{name: "before the start clamps to week 1", startDate: "2026-01-05", now: anchorPreStart, want: 1},
		{name: "long before the start still clamps to week 1", startDate: "2026-01-05", now: anchorFarBefore, want: 1},
		{name: "absent start_date is week 1", startDate: "", now: anchorWeek5Day, want: 1},
		{name: "blank start_date is week 1", startDate: "   ", now: anchorWeek5Day, want: 1},
		{name: "unparseable start_date is week 1", startDate: "week one", now: anchorWeek5Day, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := programWeek(tt.startDate, mustTime(t, tt.now), time.UTC)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestProgramWeekTurnsAtTheRolloverBoundary proves the week advances at the
// Ledger's 04:00 rollover rather than at midnight-local: an instant before the
// boundary belongs to the previous logical day, so the last night of week 1 still
// reads as week 1.
func TestProgramWeekTurnsAtTheRolloverBoundary(t *testing.T) {
	const start = "2026-01-05"

	assert.Equal(t, 1, programWeek(start, mustTime(t, "2026-01-12T02:00:00Z"), time.UTC),
		"02:00 on day 7 is still the day-6 logical day — week 1")
	assert.Equal(t, 2, programWeek(start, mustTime(t, "2026-01-12T04:00:00Z"), time.UTC),
		"at the rollover the logical day turns over and week 2 begins")
}

// --- The projection over the synthetic program. ---

// TestBuildAnchorTargetsByProgramWeek proves the projection emits one line per
// anchor item in the program's own order, carrying the target that applies to the
// derived week: the base targets in week 1, the week-2 override once the ramp
// starts, and that same override held once the program runs past its last defined
// week. The accumulate mode rides along untouched.
func TestBuildAnchorTargetsByProgramWeek(t *testing.T) {
	tests := []struct {
		name     string
		now      string
		wantWeek int
		want     []AnchorLine
	}{
		{
			name: "week 1 shows the base targets", now: anchorStartDay, wantWeek: 1,
			want: []AnchorLine{
				{Name: "squats", Target: 50},
				{Name: "core", Target: 40},
				{Name: "easy push-ups", Target: 20, Mode: "accumulate"},
			},
		},
		{
			name: "week 2 shows the ramped override", now: anchorWeek2Day, wantWeek: 2,
			want: []AnchorLine{
				{Name: "squats", Target: 55},
				{Name: "core", Target: 50},
				{Name: "easy push-ups", Target: 25, Mode: "accumulate"},
			},
		},
		{
			name: "past the last defined week the ramp holds", now: anchorWeek5Day, wantWeek: 5,
			want: []AnchorLine{
				{Name: "squats", Target: 55},
				{Name: "core", Target: 50},
				{Name: "easy push-ups", Target: 25, Mode: "accumulate"},
			},
		},
		{
			name: "before the start the floor reads as week 1", now: anchorPreStart, wantWeek: 1,
			want: []AnchorLine{
				{Name: "squats", Target: 50},
				{Name: "core", Target: 40},
				{Name: "easy push-ups", Target: 20, Mode: "accumulate"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildAnchor(ExampleProgram(), mustTime(t, tt.now), time.UTC)

			assert.Equal(t, tt.wantWeek, got.Week)
			require.Len(t, got.Items, len(tt.want), "one line per anchor item, in program order")
			assert.Equal(t, tt.want, got.Items)
		})
	}
}

// TestBuildAnchorWithoutAnchorItemsIsHonestEmpty proves a program that defines no
// daily anchor yields the derived week and no items — the empty projection the
// renderer drops rather than a hollow region.
func TestBuildAnchorWithoutAnchorItemsIsHonestEmpty(t *testing.T) {
	prog := ExampleProgram()
	prog.DailyAnchor = DailyAnchor{}

	got := BuildAnchor(prog, mustTime(t, anchorWeek2Day), time.UTC)

	assert.Equal(t, 2, got.Week, "the week is still derived without any items")
	assert.Nil(t, got.Items, "no anchor items means no lines at all")
}

// TestBuildAnchorNilLocationFallsBackToUTC proves a caller that passes no location
// gets the UTC read rather than a panic, matching BuildTrend's contract.
func TestBuildAnchorNilLocationFallsBackToUTC(t *testing.T) {
	got := BuildAnchor(ExampleProgram(), mustTime(t, anchorWeek2Day), nil)

	assert.Equal(t, 2, got.Week)
	require.NotEmpty(t, got.Items)
}

// --- Target selection across sparse week tables. ---

// TestEffectiveTargetInheritsHoldsAndFallsBack proves the "highest defined week at
// or below the current one" rule: an undefined intermediate week inherits the last
// defined entry, a finished ramp holds at its last target, an item no entry names
// keeps its base target, and a malformed or non-positive week key is ignored
// rather than shadowing a valid one.
func TestEffectiveTargetInheritsHoldsAndFallsBack(t *testing.T) {
	item := AnchorItem{Name: "squats", Target: 10}
	byWeek := map[string]map[string]int{
		"1":  {"squats": 11},
		"3":  {"squats": 13},
		"0":  {"squats": 99}, // not a valid 1-indexed week — ignored
		"wk": {"squats": 98}, // unparseable key — ignored
		"9":  {"core": 60},   // a later week that does not name this item
	}

	tests := []struct {
		name string
		week int
		want int
	}{
		{name: "week 1 takes its own entry", week: 1, want: 11},
		{name: "an undefined week inherits the last defined one", week: 2, want: 11},
		{name: "week 3 takes the newer entry", week: 3, want: 13},
		{name: "past the last entry the target holds", week: 12, want: 13},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, effectiveTarget(item, tt.week, byWeek))
		})
	}

	t.Run("an item no entry names keeps its base target", func(t *testing.T) {
		assert.Equal(t, 5, effectiveTarget(AnchorItem{Name: "hangs", Target: 5}, 12, byWeek))
	})

	t.Run("no week table at all keeps the base target", func(t *testing.T) {
		assert.Equal(t, 10, effectiveTarget(item, 4, nil))
	})

	t.Run("a zero base target with no entry stays zero", func(t *testing.T) {
		assert.Equal(t, 0, effectiveTarget(AnchorItem{Name: "hangs"}, 4, byWeek))
	})
}
