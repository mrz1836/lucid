package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestMetrics_CLI_EmptyLedgerJSON: a fresh Ledger emits the full metrics shape
// with a null ref, the three gate windows, and no anchors — nothing hollow.
func TestMetrics_CLI_EmptyLedgerJSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "metrics", "--json")
	require.NoError(t, err)

	var m engine.Metrics
	require.NoError(t, json.Unmarshal([]byte(out), &m))
	assert.Equal(t, 0, m.CurrentStreak)
	assert.Equal(t, 0, m.LongestStreak)
	assert.Nil(t, m.Ref)
	require.Len(t, m.Gates, 3)
	for i, want := range []int{30, 60, 90} {
		assert.Equal(t, want, m.Gates[i].Length)
	}
	assert.Empty(t, m.Anchors)
}

// TestMetrics_CLI_EmptyLedgerProse: the human surface says so honestly rather
// than printing a hollow percent.
func TestMetrics_CLI_EmptyLedgerProse(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "metrics")
	require.NoError(t, err)
	assert.Contains(t, out, "Streak: 0 (longest 0).")
	assert.Contains(t, out, "no decided days yet")
}

// TestMetrics_CLI_SeededAnchorJSON records an anchor then reads metrics: the
// --json projection carries the three gate windows and the anchor with the
// right day-0-based days-since (afternoon() logical day 2026-07-05).
func TestMetrics_CLI_SeededAnchorJSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-07-01")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "metrics", "--json")
	require.NoError(t, err)

	var m engine.Metrics
	require.NoError(t, json.Unmarshal([]byte(out), &m))
	require.Len(t, m.Gates, 3)
	assert.Equal(t, 30, m.Gates[0].Length)
	assert.Equal(t, 60, m.Gates[1].Length)
	assert.Equal(t, 90, m.Gates[2].Length)

	require.Len(t, m.Anchors, 1)
	assert.Equal(t, "sobriety", m.Anchors[0].Label)
	assert.Equal(t, "2026-07-01", m.Anchors[0].Date)
	assert.Equal(t, 4, m.Anchors[0].DaysSince) // 2026-07-01 → 2026-07-05
}

// TestMetrics_CLI_ProseHasStreakAndDaysSince: the human surface carries the
// streak and one days-since line per anchor.
func TestMetrics_CLI_ProseHasStreakAndDaysSince(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-07-01")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "metrics")
	require.NoError(t, err)
	assert.Contains(t, out, "Streak: 0 (longest 0).")
	assert.Contains(t, out, "Days since sobriety: 4.")
}

// TestMetrics_CLI_BootError: a Ledger that cannot be scaffolded surfaces the
// boot error rather than a false success (mirrors TestModeCLI_BootError).
func TestMetrics_CLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "metrics")
	require.Error(t, err)
}

// TestMetrics_CLI_RejectsArgs guards the no-args contract.
func TestMetrics_CLI_RejectsArgs(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "metrics", "extra")
	require.Error(t, err)
}
