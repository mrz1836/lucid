package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestBackfill_CreatesBackfilledRecord: a backfill inside the window
// creates a backfilled:true record with a `command: /closeout backfill`
// journal line.
func TestBackfill_CreatesBackfilledRecord(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	target := atUTC(2026, 7, 5, 0, 0)
	now := atUTC(2026, 7, 7, 9, 0)

	res, err := r.Backfill(BackfillRequest{
		Now: now, Target: &target, Links: compactLinks(), Capacity: 2, Journal: "the chain ran, phone-alarm night",
	})
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.Equal(t, "2026-07-05", res.LogicalDate)

	rec := readDay(t, a, "day_2026_07_05")
	assert.True(t, rec.Backfilled)
	assert.True(t, rec.Completed)

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "/closeout backfill", doc.Fields["command"])
}

// TestBackfill_BeyondWindowRejected is the out-of-window rejection copy
// (engine-module.md §Error states).
func TestBackfill_BeyondWindowRejected(t *testing.T) {
	r, _, home := newBootedRouter(t)
	target := atUTC(2026, 6, 20, 0, 0) // > 7 days before now
	now := atUTC(2026, 7, 7, 9, 0)

	res, err := r.Backfill(BackfillRequest{Now: now, Target: &target, Links: compactLinks(), Journal: "too old"})
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Contains(t, res.Ack, "outside the backfill window (7 days)")
	assert.Equal(t, 0, countFiles(t, home, "raw"), "a rejected backfill writes nothing")
}

// TestBackfill_DefaultTargetMostRecentGap resolves the default target.
func TestBackfill_DefaultTargetMostRecentGap(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	// Close out 07-06 (yesterday) so the most recent gap is 07-05.
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 6, 22, 0), Links: compactLinks(), Journal: "y"})
	require.NoError(t, err)

	res, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 7, 9, 0), Links: compactLinks(), Journal: "gap"})
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", res.LogicalDate)
	assert.True(t, res.Created)
}

// TestBackfill_IdempotentOnCompleted: a backfill of an already-completed
// day is a no-op.
func TestBackfill_IdempotentOnCompleted(t *testing.T) {
	r, _, home := newBootedRouter(t)
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 6, 22, 0), Links: compactLinks(), Journal: "y"})
	require.NoError(t, err)
	rawBefore := countFiles(t, home, "raw")

	target := atUTC(2026, 7, 6, 0, 0)
	res, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 7, 9, 0), Target: &target, Links: compactLinks(), Journal: "dup"})
	require.NoError(t, err)
	assert.True(t, res.Idempotent)
	assert.Contains(t, res.Ack, "Already closed out")
	assert.Equal(t, rawBefore, countFiles(t, home, "raw"))
}

// TestBackfill_RestoresStreak shows retraction-is-arithmetic: a miss in the
// middle of a run, backfilled, restores the streak on rebuild.
func TestBackfill_RestoresStreak(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "d5"})
	require.NoError(t, err)
	_, err = r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 7, 22, 0), Links: compactLinks(), Journal: "d7"})
	require.NoError(t, err)
	// Streak is 1 (07-06 is a gap).
	status, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, 1, status.CurrentStreak)

	target := atUTC(2026, 7, 6, 0, 0)
	_, err = r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 7, 23, 0), Target: &target, Links: compactLinks(), Journal: "d6"})
	require.NoError(t, err)
	status, err = a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, 3, status.CurrentStreak, "backfilling the gap restores the 3-day streak")
}

// TestBackfill_YesterdayBeforeRolloverResolvesLogicalDay is the regression for
// a pre-rollover `/closeout backfill yesterday`: at 00:52 the logical day is
// still the prior calendar date, so "yesterday" must resolve to the day before
// *that* — the naive calendar yesterday would be the in-progress logical day
// (span 0) and read as out-of-window. Mirrors the CI clock that caught it.
func TestBackfill_YesterdayBeforeRolloverResolvesLogicalDay(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := atUTC(2026, 7, 11, 0, 52) // before the 04:00 rollover

	res, err := r.Backfill(BackfillRequest{
		Now: now, Yesterday: true, Links: compactLinks(), Journal: "the chain ran",
	})
	require.NoError(t, err)
	assert.False(t, res.Rejected, "yesterday is always inside the window")
	assert.True(t, res.Created)
	assert.Equal(t, "2026-07-09", res.LogicalDate)

	rec := readDay(t, a, "day_2026_07_09")
	assert.True(t, rec.Backfilled)
}

// TestBackfill_YesterdayAfterRolloverIsCalendarYesterday: after the rollover
// the logical day is the current calendar date, so "yesterday" is the calendar
// day before — the daytime path.
func TestBackfill_YesterdayAfterRolloverIsCalendarYesterday(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	res, err := r.Backfill(BackfillRequest{
		Now: atUTC(2026, 7, 11, 14, 0), Yesterday: true, Links: compactLinks(), Journal: "ran",
	})
	require.NoError(t, err)
	assert.False(t, res.Rejected)
	assert.Equal(t, "2026-07-10", res.LogicalDate)
}

func TestBackfill_CorrectsPartial(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	// A partial (not completed) record for 07-05.
	_, err := r.Closeout(CloseoutRequest{
		Now: atUTC(2026, 7, 5, 22, 0), Partial: true, Links: map[string]string{"dock": engine.StatusDone}, Journal: "partial",
	})
	require.NoError(t, err)

	target := atUTC(2026, 7, 5, 0, 0)
	res, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 6, 9, 0), Target: &target, Links: compactLinks(), Journal: "complete it"})
	require.NoError(t, err)
	assert.True(t, res.Corrected)
	folded := readDay(t, a, "day_2026_07_05")
	assert.True(t, folded.Completed)
}
