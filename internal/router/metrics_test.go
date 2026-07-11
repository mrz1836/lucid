package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestMetrics_EmptyLedgerHonest: on a fresh Ledger metrics reports zero streak,
// a nil ref, the honest zero-length windows (never a hollow 1.0), the full
// gate set, and no anchors — and the prose says "no decided days yet" rather
// than a hollow percent.
func TestMetrics_EmptyLedgerHonest(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	res, err := r.Metrics(atUTC(2026, 7, 6, 9, 0))
	require.NoError(t, err)

	assert.Equal(t, 0, res.Metrics.CurrentStreak)
	assert.Equal(t, 0, res.Metrics.LongestStreak)
	assert.Nil(t, res.Metrics.Ref)
	assert.Equal(t, 30, res.Metrics.Adherence.Length)
	require.Len(t, res.Metrics.Gates, 3)
	for i, want := range []int{30, 60, 90} {
		assert.Equal(t, want, res.Metrics.Gates[i].Length)
	}
	assert.Empty(t, res.Metrics.Anchors)

	joined := join(res.Lines)
	assert.Contains(t, joined, "Streak: 0 (longest 0).")
	assert.Contains(t, joined, "no decided days yet")
}

// TestMetrics_SeededWithAnchor closes out one day (streak 1, ref = that day)
// and records an anchor, then asserts the folded rollup: the current streak,
// the resolved ref, and days-since counted to the current logical day (day 0
// on the anchor date). Byte-stable for the pinned clock.
func TestMetrics_SeededWithAnchor(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.NoError(t, err)
	_, err = r.AnchorAdd(AnchorAddRequest{Label: "quit-x", Date: "2026-07-01", Now: atUTC(2026, 7, 5, 22, 0)})
	require.NoError(t, err)

	res, err := r.Metrics(atUTC(2026, 7, 6, 9, 0)) // logical day 2026-07-06 (rollover 04:00)
	require.NoError(t, err)

	assert.Equal(t, 1, res.Metrics.CurrentStreak)
	assert.Equal(t, 1, res.Metrics.LongestStreak)
	require.NotNil(t, res.Metrics.Ref)
	assert.Equal(t, "2026-07-05", *res.Metrics.Ref)

	require.Len(t, res.Metrics.Anchors, 1)
	assert.Equal(t, "quit-x", res.Metrics.Anchors[0].Label)
	assert.Equal(t, "2026-07-01", res.Metrics.Anchors[0].Date)
	assert.Equal(t, 5, res.Metrics.Anchors[0].DaysSince) // 2026-07-01 → 2026-07-06

	joined := join(res.Lines)
	assert.Contains(t, joined, "Streak: 1 (longest 1).")
	assert.Contains(t, joined, "Days since quit-x: 5.")
}

// TestMetrics_LatestAnchorWins folds two records for one label to the
// most-recently-appended, so metrics counts days-since from the correction.
func TestMetrics_LatestAnchorWins(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.AnchorAdd(AnchorAddRequest{Label: "quit-x", Date: "2026-07-01", Now: atUTC(2026, 7, 5, 22, 0)})
	require.NoError(t, err)
	// A correction with an earlier date, appended later, still supersedes.
	_, err = r.AnchorAdd(AnchorAddRequest{Label: "quit-x", Date: "2026-06-25", Now: atUTC(2026, 7, 5, 22, 30)})
	require.NoError(t, err)

	res, err := r.Metrics(atUTC(2026, 7, 6, 9, 0))
	require.NoError(t, err)

	require.Len(t, res.Metrics.Anchors, 1, "latest-per-label folds to one")
	assert.Equal(t, "2026-06-25", res.Metrics.Anchors[0].Date)
	assert.Equal(t, 11, res.Metrics.Anchors[0].DaysSince) // 2026-06-25 → 2026-07-06
}

// TestMetricsLines_RendersGatesAndAnchors checks the human surface: the streak,
// the default 30-day window with honest co-numbers, the longer gate windows,
// the budget line, and one days-since line per anchor — with the 30-day window
// printed exactly once (the default line, not doubled by the 30-day gate).
func TestMetricsLines_RendersGatesAndAnchors(t *testing.T) {
	m := engine.Metrics{
		CurrentStreak: 5, LongestStreak: 9,
		Adherence:   engine.Window{Length: 30, Adherence: 1, Completed: 3, Decided: 3, DaysAccounted: 3},
		ErrorBudget: engine.ErrorBudget{Budget: 4, Burn: 1, Remaining: 3},
		Gates: []engine.GateWindow{
			{Length: 30, Adherence: engine.Window{Length: 30, Adherence: 1, Completed: 3, Decided: 3, DaysAccounted: 3}},
			{Length: 60, Adherence: engine.Window{Length: 60, Adherence: 0.8, Completed: 4, Decided: 5, DaysAccounted: 5}},
			{Length: 90, Adherence: engine.Window{Length: 90, Adherence: 0.9, Completed: 9, Decided: 10, DaysAccounted: 10}},
		},
		Anchors: []engine.AnchorDaysSince{{Label: "sobriety", Date: "2026-01-01", DaysSince: 191}},
	}
	joined := join(metricsLines(m))

	assert.Contains(t, joined, "Streak: 5 (longest 9).")
	assert.Contains(t, joined, "30-day adherence: 100% (3/3 decided")
	assert.Contains(t, joined, "60-day gate adherence: 80% (4/5 decided")
	assert.Contains(t, joined, "90-day gate adherence: 90% (9/10 decided")
	assert.Contains(t, joined, "Error budget: 1/4 isolated misses spent (3 left).")
	assert.Contains(t, joined, "Days since sobriety: 191.")
	assert.Equal(t, 1, strings.Count(joined, "30-day"), "the 30-day window is not printed twice")
}

// TestMetrics_CorruptChainErrors surfaces a corrupt chain.json rather than a
// silent zero rollup (the chain read carries the SLO budget and gate marks).
func TestMetrics_CorruptChainErrors(t *testing.T) {
	r, _, home := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	writeCorruptChain(t, home)
	_, err := r.Metrics(atUTC(2026, 7, 5, 9, 0))
	assert.Error(t, err)
}

// TestMetrics_FallsBackToDefaultClocks: a profile.json whose active profile is
// undefined (e.g. hand-edited) must not break the read — metrics falls back to
// the default clocks and still resolves days-since at the rollover boundary.
func TestMetrics_FallsBackToDefaultClocks(t *testing.T) {
	r, _, home := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "engine", "profile.json"),
		[]byte(`{"active":"ghost","history":[]}`), 0o600))

	_, err := r.AnchorAdd(AnchorAddRequest{Label: "quit-x", Date: "2026-07-01", Now: atUTC(2026, 7, 5, 22, 0)})
	require.NoError(t, err)

	res, err := r.Metrics(atUTC(2026, 7, 6, 9, 0))
	require.NoError(t, err, "an undefined active profile falls back to default clocks")
	require.Len(t, res.Metrics.Anchors, 1)
	assert.Equal(t, 5, res.Metrics.Anchors[0].DaysSince)
}

// TestMetrics_ScaffoldFails surfaces an engine-tree scaffold failure.
func TestMetrics_ScaffoldFails(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	require.NoError(t, os.Chmod(home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	_, err := r.Metrics(atUTC(2026, 7, 5, 9, 0))
	assert.Error(t, err)
}
