package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDaysSince_DSTBoundary is the AC-6 correctness case: across a
// spring-forward day (America/New_York, 2024-03-10) a naive truncated span
// under-counts by one, so DaysSince re-anchors both dates to UTC civil
// midnight and returns the exact whole-day count. Anchor day = day 0.
func TestDaysSince_DSTBoundary(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	anchor := time.Date(2024, 3, 8, 0, 0, 0, 0, ny)
	current := time.Date(2024, 3, 11, 0, 0, 0, 0, ny)

	assert.Equal(t, 3, DaysSince(anchor, current), "UTC re-anchor is DST-exact")
	// The chain-math DaysBetween truncates in the host location and loses the
	// 23-hour spring-forward day — the exact reason DaysSince does not reuse it.
	assert.Equal(t, 2, DaysBetween(anchor, current), "naive truncation under-counts")
}

// TestDaysSince_DayZeroAndForward pins the day-0 convention: recorded today
// reads 0, tomorrow 1, and a same-instant anchor is 0.
func TestDaysSince_DayZeroAndForward(t *testing.T) {
	anchor := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 0, DaysSince(anchor, anchor))
	assert.Equal(t, 0, DaysSince(anchor, time.Date(2026, 4, 5, 23, 0, 0, 0, time.UTC)))
	assert.Equal(t, 1, DaysSince(anchor, time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)))
	assert.Equal(t, 5, DaysSince(anchor, time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)))
}

// TestBuildMetrics_DaysSinceAtRollover proves days-since increments at the
// chain's rollover boundary (the current logical day), not at naive midnight:
// with a 04:00 rollover, 03:00 the next civil morning still belongs to the
// anchor day (0), while 05:00 has rolled to day 1.
func TestBuildMetrics_DaysSinceAtRollover(t *testing.T) {
	loc := time.UTC
	clocks, err := defChain().ClocksFor(DefaultProfile) // rollover 04:00
	require.NoError(t, err)
	anchors := []Anchor{{Label: "quit-x", Date: "2026-03-15", RecordedAt: "2026-03-15T21:00:00Z"}}

	before := time.Date(2026, 3, 16, 3, 0, 0, 0, loc) // pre-rollover ⇒ logical day still 03-15
	after := time.Date(2026, 3, 16, 5, 0, 0, 0, loc)  // post-rollover ⇒ logical day 03-16

	mBefore := BuildMetrics(MetricsInput{Anchors: anchors, Chain: defChain(), Now: before, Clocks: clocks, Loc: loc})
	mAfter := BuildMetrics(MetricsInput{Anchors: anchors, Chain: defChain(), Now: after, Clocks: clocks, Loc: loc})

	require.Len(t, mBefore.Anchors, 1)
	require.Len(t, mAfter.Anchors, 1)
	assert.Equal(t, 0, mBefore.Anchors[0].DaysSince, "pre-rollover holds the anchor day")
	assert.Equal(t, 1, mAfter.Anchors[0].DaysSince, "post-rollover has advanced a logical day")
}

// TestBuildMetrics_AdherenceAndBudgetOver seeds a shaped Ledger and asserts the
// 30/60/90 gate rollups and the error budget's Exceeded flag when isolated
// misses burn past the per-30d budget (default 4).
func TestBuildMetrics_AdherenceAndBudgetOver(t *testing.T) {
	loc := time.UTC
	recs := make([]DayRecord, 0, 14)
	// Five isolated (2-apart) misses inside the 30-day window ⇒ burn 5 > budget 4.
	for _, d := range []string{"2026-04-01", "2026-04-03", "2026-04-05", "2026-04-07", "2026-04-09"} {
		recs = append(recs, missedDay(d))
	}
	// Five completes inside 30d; ref (latest recorded day) = 2026-04-10.
	for _, d := range []string{"2026-04-02", "2026-04-04", "2026-04-06", "2026-04-08", "2026-04-10"} {
		recs = append(recs, completedDay(d, ModeGreen))
	}
	// Two completes in the 31–60d band, two in the 61–90d band.
	for _, d := range []string{"2026-03-01", "2026-03-05", "2026-02-05", "2026-01-20"} {
		recs = append(recs, completedDay(d, ModeGreen))
	}

	m := BuildMetrics(MetricsInput{Records: recs, Chain: defChain(), Loc: loc})

	require.NotNil(t, m.Ref)
	assert.Equal(t, "2026-04-10", *m.Ref)

	// Default adherence == the 30d gate rollup.
	assert.Equal(t, 30, m.Adherence.Length)
	assert.Equal(t, 5, m.Adherence.Completed)
	assert.Equal(t, 10, m.Adherence.Decided)
	assert.InDelta(t, 0.5, m.Adherence.Adherence, 1e-9)
	assert.Equal(t, 5, m.MissesInWindow)

	require.Len(t, m.Gates, 3)
	assert.Equal(t, 30, m.Gates[0].Length)
	assert.Equal(t, 5, m.Gates[0].Adherence.Completed)
	assert.Equal(t, 10, m.Gates[0].Adherence.Decided)

	assert.Equal(t, 60, m.Gates[1].Length)
	assert.Equal(t, 7, m.Gates[1].Adherence.Completed)
	assert.Equal(t, 12, m.Gates[1].Adherence.Decided)

	assert.Equal(t, 90, m.Gates[2].Length)
	assert.Equal(t, 9, m.Gates[2].Adherence.Completed)
	assert.Equal(t, 14, m.Gates[2].Adherence.Decided)

	assert.Equal(t, 4, m.ErrorBudget.Budget)
	assert.Equal(t, 5, m.ErrorBudget.Burn)
	assert.Equal(t, 0, m.ErrorBudget.Remaining)
	assert.True(t, m.ErrorBudget.Exceeded, "5 isolated misses exceed the budget of 4")
}

// TestBuildMetrics_ErrorBudgetAtBudget is the boundary companion: exactly
// budget-many isolated misses spends the budget to zero but does not exceed it.
func TestBuildMetrics_ErrorBudgetAtBudget(t *testing.T) {
	loc := time.UTC
	recs := make([]DayRecord, 0, 5)
	for _, d := range []string{"2026-04-01", "2026-04-03", "2026-04-05", "2026-04-07"} {
		recs = append(recs, missedDay(d)) // 4 isolated misses == budget
	}
	recs = append(recs, completedDay("2026-04-10", ModeGreen)) // ref
	m := BuildMetrics(MetricsInput{Records: recs, Chain: defChain(), Loc: loc})

	assert.Equal(t, 4, m.ErrorBudget.Burn)
	assert.Equal(t, 0, m.ErrorBudget.Remaining)
	assert.False(t, m.ErrorBudget.Exceeded, "at budget is not over budget")
}

// TestBuildMetrics_StreakCurrentVsLongest covers a broken-then-resumed run:
// the longest run is the earlier three-day stretch, the current run is the
// two-day tail ending at the latest completed day.
func TestBuildMetrics_StreakCurrentVsLongest(t *testing.T) {
	recs := []DayRecord{
		completedDay("2026-04-01", ModeGreen),
		completedDay("2026-04-02", ModeGreen),
		completedDay("2026-04-03", ModeGreen),
		missedDay("2026-04-04"),
		// 2026-04-05 absent (a gap) breaks the run
		completedDay("2026-04-06", ModeGreen),
		completedDay("2026-04-07", ModeGreen),
	}
	m := BuildMetrics(MetricsInput{Records: recs, Chain: defChain(), Loc: time.UTC})
	assert.Equal(t, 2, m.CurrentStreak)
	assert.Equal(t, 3, m.LongestStreak)
}

// TestBuildMetrics_EmptyLedgerHonest checks the no-decided-days shape: zero
// streak, a nil ref, honest zero-length windows (never a hollow 1.0), a full
// error budget, and the empty (non-null) gates/anchors arrays in the JSON.
func TestBuildMetrics_EmptyLedgerHonest(t *testing.T) {
	m := BuildMetrics(MetricsInput{Chain: defChain(), Loc: time.UTC})

	assert.Equal(t, 0, m.CurrentStreak)
	assert.Equal(t, 0, m.LongestStreak)
	assert.Nil(t, m.Ref)
	assert.Equal(t, 30, m.Adherence.Length)
	assert.Equal(t, 0, m.Adherence.Decided)
	assert.InDelta(t, 0.0, m.Adherence.Adherence, 0)
	assert.Equal(t, 0, m.MissesInWindow)
	assert.Equal(t, 4, m.ErrorBudget.Budget)
	assert.Equal(t, 0, m.ErrorBudget.Burn)
	assert.Equal(t, 4, m.ErrorBudget.Remaining)
	assert.False(t, m.ErrorBudget.Exceeded)

	require.Len(t, m.Gates, 3)
	for i, want := range []int{30, 60, 90} {
		assert.Equal(t, want, m.Gates[i].Length)
		assert.Equal(t, 0, m.Gates[i].Adherence.Decided)
	}
	assert.Empty(t, m.Anchors)

	b, err := json.Marshal(m)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"ref":null`)
	assert.Contains(t, s, `"gates":[`)
	assert.Contains(t, s, `"anchors":[]`)
	assert.Contains(t, s, `"current_streak":0`)
}

// TestBuildMetrics_AnchorsDaysSinceAndSkipMalformed folds the latest anchors,
// carries the note through, and skips a malformed date rather than panicking.
func TestBuildMetrics_AnchorsDaysSinceAndSkipMalformed(t *testing.T) {
	loc := time.UTC
	clocks, err := defChain().ClocksFor(DefaultProfile)
	require.NoError(t, err)
	log := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{Label: "quit-x", Date: "2026-04-05", Note: "big one", RecordedAt: "2026-04-05T21:00:00Z"},
		{Label: "bad", Date: "not-a-date", RecordedAt: "2026-04-05T21:00:00Z"},
	}}
	now := time.Date(2026, 4, 10, 12, 0, 0, 0, loc) // logical day 2026-04-10

	m := BuildMetrics(MetricsInput{
		Anchors: LatestAnchors(log),
		Chain:   defChain(),
		Now:     now,
		Clocks:  clocks,
		Loc:     loc,
	})

	require.Len(t, m.Anchors, 1, "malformed-date anchor is skipped")
	assert.Equal(t, "quit-x", m.Anchors[0].Label)
	assert.Equal(t, "2026-04-05", m.Anchors[0].Date)
	assert.Equal(t, "big one", m.Anchors[0].Note)
	assert.Equal(t, 5, m.Anchors[0].DaysSince)
}
