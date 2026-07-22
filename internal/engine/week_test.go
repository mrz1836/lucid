package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// weekRef is the inclusive end of the 7-day window the WeekWindow tests fold
// against: the 04-04..04-10 stretch is the "this week" the witness report reads.
func weekRef() time.Time { return time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC) }

// inProgressDay is a mode-declared but not-yet-decided day — accounted, but it
// inflates no adherence (neither completed nor missed).
func inProgressDay(date string) DayRecord {
	return rec(date, ModeGreen, false, false, false, false)
}

// TestWeekWindow_FullWeek folds a fully committed week: seven completed days
// end at the ref, so every day is accounted, decided, and completed, adherence
// is a clean 1.0, and misses (Decided - Completed) are zero.
func TestWeekWindow_FullWeek(t *testing.T) {
	recs := []DayRecord{
		completedDay("2026-04-04", ModeGreen),
		completedDay("2026-04-05", ModeGreen),
		completedDay("2026-04-06", ModeGreen),
		completedDay("2026-04-07", ModeGreen),
		completedDay("2026-04-08", ModeGreen),
		completedDay("2026-04-09", ModeGreen),
		completedDay("2026-04-10", ModeGreen),
	}
	w := WeekWindow(recs, weekRef(), time.UTC)

	assert.Equal(t, 7, w.Length)
	assert.Equal(t, 7, w.DaysAccounted)
	assert.Equal(t, 7, w.Decided)
	assert.Equal(t, 7, w.Completed)
	assert.InDelta(t, 1.0, w.Adherence, 1e-9)
	assert.Equal(t, 0, w.Decided-w.Completed, "a full week has no misses")
}

// TestWeekWindow_ThinWeek folds a sparsely logged week: only two committed
// days land inside the window, so the accounted/decided co-numbers stay honest
// at two rather than masquerading as a full week.
func TestWeekWindow_ThinWeek(t *testing.T) {
	recs := []DayRecord{
		completedDay("2026-04-09", ModeGreen),
		completedDay("2026-04-10", ModeGreen),
		// 2026-04-01 falls outside the 7-day window and must be excluded.
		completedDay("2026-04-01", ModeGreen),
	}
	w := WeekWindow(recs, weekRef(), time.UTC)

	assert.Equal(t, 2, w.DaysAccounted, "the pre-window day is excluded")
	assert.Equal(t, 2, w.Decided)
	assert.Equal(t, 2, w.Completed)
}

// TestWeekWindow_ZeroDecided folds a week with only in-progress (mode-only)
// days: they are accounted but nothing is decided, so adherence stays 0.0 (not
// a hollow 1.0) and misses are zero.
func TestWeekWindow_ZeroDecided(t *testing.T) {
	recs := []DayRecord{
		inProgressDay("2026-04-08"),
		inProgressDay("2026-04-09"),
		inProgressDay("2026-04-10"),
	}
	w := WeekWindow(recs, weekRef(), time.UTC)

	assert.Equal(t, 3, w.DaysAccounted)
	assert.Equal(t, 0, w.Decided)
	assert.Equal(t, 0, w.Completed)
	assert.InDelta(t, 0.0, w.Adherence, 0)
	assert.Equal(t, 0, w.Decided-w.Completed)
}

// TestWeekWindow_MissesAreDecidedMinusCompleted pins the witness report's
// weekly-miss arithmetic: three completed and two missed days give five decided
// and two misses.
func TestWeekWindow_MissesAreDecidedMinusCompleted(t *testing.T) {
	recs := []DayRecord{
		completedDay("2026-04-06", ModeGreen),
		missedDay("2026-04-07"),
		completedDay("2026-04-08", ModeGreen),
		missedDay("2026-04-09"),
		completedDay("2026-04-10", ModeGreen),
	}
	w := WeekWindow(recs, weekRef(), time.UTC)

	assert.Equal(t, 5, w.DaysAccounted)
	assert.Equal(t, 5, w.Decided)
	assert.Equal(t, 3, w.Completed)
	assert.Equal(t, 2, w.Decided-w.Completed, "two missed days are two weekly misses")
}

// TestWeekWindow_NilLocationDefaultsUTC guards the loc fallback: windowStats
// resolves a nil location to UTC, so the wrapper must not panic on it.
func TestWeekWindow_NilLocationDefaultsUTC(t *testing.T) {
	recs := []DayRecord{completedDay("2026-04-10", ModeGreen)}
	assert.NotPanics(t, func() { _ = WeekWindow(recs, weekRef(), nil) })
}
