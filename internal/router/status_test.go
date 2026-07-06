package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// writeCorruptChain replaces the scaffolded chain.json with malformed JSON so
// a command's chain read fails — shared by the engine-command error tests.
func writeCorruptChain(t *testing.T, home string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(home, "engine", "chain.json"), []byte("{bad"), 0o600))
}

// statusPath returns the engine/status.json path under an isolated home.
func statusJSONPath(home string) string {
	return filepath.Join(home, "engine", "status.json")
}

// TestStatus_ReadsPersistedProjection: /status reads the derived status.json
// rebuilt by the last close-out and writes nothing — proven by planting a
// sentinel current_streak the read surfaces verbatim (a rebuild would recompute it).
func TestStatus_ReadsPersistedProjection(t *testing.T) {
	r, _, home := newBootedRouter(t)
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.NoError(t, err)

	// Plant a valid-but-distinctive status.json; a read returns it untouched.
	require.NoError(t, os.WriteFile(statusJSONPath(home), []byte(`{"current_streak":999}`), 0o600))
	res, err := r.Status(atUTC(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.Equal(t, 999, res.Status.CurrentStreak, "/status must read, not rebuild")

	after, err := os.ReadFile(statusJSONPath(home))
	require.NoError(t, err)
	assert.JSONEq(t, `{"current_streak":999}`, string(after), "/status writes nothing on the happy path")
	assert.NotEmpty(t, res.Lines)
}

// TestStatus_RebuildsWhenMissing: on a fresh Ledger with no status.json yet,
// /status regenerates it silently from days/ (error-states row).
func TestStatus_RebuildsWhenMissing(t *testing.T) {
	r, _, home := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	require.NoFileExists(t, statusJSONPath(home))

	res, err := r.Status(atUTC(2026, 7, 5, 9, 0))
	require.NoError(t, err)
	assert.Equal(t, 0, res.Status.CurrentStreak)
	assert.FileExists(t, statusJSONPath(home), "a missing status.json is regenerated")
}

// TestStatus_RebuildsWhenCorrupt: a corrupt status.json is silently rebuilt
// from the day records rather than surfaced as an error.
func TestStatus_RebuildsWhenCorrupt(t *testing.T) {
	r, _, home := newBootedRouter(t)
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(statusJSONPath(home), []byte("{bad"), 0o600))

	res, err := r.Status(atUTC(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Status.CurrentStreak, "rebuilt from the completed day")

	// The regenerated file parses again.
	_, err = r.store.ReadEngineStatus()
	require.NoError(t, err)
}

// TestStatus_ScaffoldFails surfaces an engine-tree scaffold failure.
func TestStatus_ScaffoldFails(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	require.NoError(t, os.Chmod(home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	_, err := r.Status(atUTC(2026, 7, 5, 9, 0))
	assert.Error(t, err)
}

// TestStatus_RebuildErrorSurfaced: when status.json is missing and the
// silent rebuild itself fails (corrupt chain.json), the error is surfaced.
func TestStatus_RebuildErrorSurfaced(t *testing.T) {
	r, _, home := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	writeCorruptChain(t, home) // no status.json yet ⇒ read fails ⇒ rebuild ⇒ chain read fails
	_, err := r.Status(atUTC(2026, 7, 5, 9, 0))
	assert.Error(t, err)
}

// TestStatusLines_HonestNumberPairing renders a populated status and checks
// the L0 surface co-presents adherence with its floor-day ratio and accounted
// co-number, and surfaces budget, gate, storm, stake, and witness lines.
func TestStatusLines_HonestNumberPairing(t *testing.T) {
	gate := 12
	through := "2026-07-28"
	st := engine.Status{
		CurrentStreak: 5, LongestStreak: 9,
		Adherence7d:       engine.Window{Length: 7, Adherence: 6.0 / 7.0, Completed: 6, Decided: 7, FloorDays: 2, FloorDayRatio: 2.0 / 7.0, DaysAccounted: 7},
		Adherence30d:      engine.Window{Length: 30, Adherence: 1, Completed: 3, Decided: 3, DaysAccounted: 3},
		ErrorBudget:       engine.ErrorBudget{Budget: 4, Burn: 1, Remaining: 3},
		ConsecutiveMisses: 2,
		StormState:        engine.StormStandingState, StormThrough: &through,
		StakeOwed: true, WitnessLapsed: true, DaysToNextGate: &gate,
	}
	lines := statusLines(st)
	joined := "\n" + join(lines)

	assert.Contains(t, joined, "Streak: 5 (longest 9).")
	assert.Contains(t, joined, "7-day adherence: 86% (6/7 decided, 7 accounted; floor-days 2, 29% floor).")
	assert.Contains(t, joined, "30-day adherence: 100% (3/3 decided, 3 accounted")
	assert.Contains(t, joined, "Error budget: 1/4 isolated misses spent (3 left).")
	assert.Contains(t, joined, "Consecutive misses: 2.")
	assert.Contains(t, joined, "Storm standing through 2026-07-28")
	assert.Contains(t, joined, "Days to next gate: 12.")
	assert.Contains(t, joined, "Stake owed")
	assert.Contains(t, joined, "Witness lapsed — L2 disarmed.")
}

// TestStatusLines_QuietDefaults: a clean status omits the conditional lines
// and reports a window with no decided days honestly.
func TestStatusLines_QuietDefaults(t *testing.T) {
	st := engine.Status{
		Adherence7d:  engine.Window{Length: 7, DaysAccounted: 0},
		Adherence30d: engine.Window{Length: 30, DaysAccounted: 0},
		ErrorBudget:  engine.ErrorBudget{Budget: 4, Remaining: 4, Burn: 5, Exceeded: true},
		StormState:   engine.StormNone,
	}
	joined := "\n" + join(statusLines(st))
	assert.Contains(t, joined, "adherence: no decided days yet (0 accounted).")
	assert.Contains(t, joined, "Over budget — gates hold.")
	assert.NotContains(t, joined, "Storm standing")
	assert.NotContains(t, joined, "Stake owed")
	assert.NotContains(t, joined, "Witness lapsed")
	assert.NotContains(t, joined, "Days to next gate")
	assert.NotContains(t, joined, "Consecutive misses")
}

// TestStatusLines_StandingStormWithoutThrough tolerates a standing storm with
// no through date (defensive — the derivation always sets it, but the render
// must not panic).
func TestStatusLines_StandingStormWithoutThrough(t *testing.T) {
	st := engine.Status{
		Adherence7d: engine.Window{Length: 7}, Adherence30d: engine.Window{Length: 30},
		ErrorBudget: engine.ErrorBudget{Budget: 4, Remaining: 4},
		StormState:  engine.StormStandingState,
	}
	joined := join(statusLines(st))
	assert.Contains(t, joined, "Storm standing —")
}

// join concatenates lines with newlines for substring assertions.
func join(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
