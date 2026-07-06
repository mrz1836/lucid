package scheduler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// skipIfRoot skips permission-dependent cases when the tests run as root,
// where mode bits do not deny access.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission checks are meaningless as root")
	}
}

// corrupt overwrites an engine file with unparseable JSON.
func corrupt(t *testing.T, a interface{ Home() string }, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(a.Home(), "engine", name), []byte("{bad"), 0o600))
}

// TestRunBell_CorruptChainErrors surfaces a malformed chain.json rather than
// posting a garbled bell.
func TestRunBell_CorruptChainErrors(t *testing.T) {
	sc, a, _ := newSched(t)
	corrupt(t, a, "chain.json")
	_, err := sc.RunBell()
	assert.Error(t, err)
}

// TestRunBell_SendFailureSurfaced: a bell whose delivery fails is surfaced.
func TestRunBell_SendFailureSurfaced(t *testing.T) {
	sc, _, n := newSched(t)
	n.failOn[engine.ChannelUser] = true
	_, err := sc.RunBell()
	assert.Error(t, err)
}

// TestRunTripwire_CorruptFilesErrors: each engine read the run depends on
// surfaces a corrupt file rather than proceeding on garbage.
func TestRunTripwire_CorruptFilesErrors(t *testing.T) {
	for _, name := range []string{"chain.json", "profile.json", "storm.json", "witness.json", "tripwire.json"} {
		t.Run(name, func(t *testing.T) {
			sc, a, _ := newSched(t)
			corrupt(t, a, name) // writes (or creates) the file with unparseable JSON
			_, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
			assert.Error(t, err)
		})
	}
}

// TestRunTripwire_BadClockErrors: a malformed clock string fails clock
// resolution rather than mis-scheduling the reference day.
func TestRunTripwire_BadClockErrors(t *testing.T) {
	sc, a, _ := newSched(t)
	chain, err := a.ReadChainConfig()
	require.NoError(t, err)
	chain.BellTime = "99:99"
	require.NoError(t, a.WriteChainConfig(chain))
	_, err = sc.RunTripwire(at(2026, 7, 6, 9, 0))
	assert.Error(t, err)
}

// TestRunTripwire_L1SendFailureSurfaced: an L1 whose delivery fails (not an L2
// with a witness fallback) is surfaced.
func TestRunTripwire_L1SendFailureSurfaced(t *testing.T) {
	sc, a, n := newSched(t)
	n.failOn[engine.ChannelUser] = true
	seed(t, a, completedRec("2026-07-04")) // 07-05 absent → L1 to the user
	_, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	assert.Error(t, err)
}

// TestRunTripwire_L2FallbackFailureSurfaced: an L2 whose witness send fails and
// whose user fallback also fails is surfaced.
func TestRunTripwire_L2FallbackFailureSurfaced(t *testing.T) {
	sc, a, n := newSched(t)
	armWitness(t, a)
	n.failOn[engine.ChannelWitness] = true
	n.failOn[engine.ChannelUser] = true
	seed(t, a, completedRec("2026-07-03"), missedRec("2026-07-04", false))
	_, err := sc.RunTripwire(at(2026, 7, 6, 9, 0))
	assert.Error(t, err)
}

// TestRunTripwire_StormAppendFailureSurfaced: a storm bookkeeping event that
// cannot be persisted (read-only storm.json) is surfaced.
func TestRunTripwire_StormAppendFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	sc, a, _ := newSched(t)
	require.NoError(t, a.AppendStormEvent(engine.StormEvent{At: "2026-07-01T07:00:00Z", Event: engine.StormDeclared, Label: "clause-1"}))
	seed(t, a, completedRec("2026-07-04"))
	stormPath := filepath.Join(a.Home(), "engine", "storm.json")
	require.NoError(t, os.Chmod(stormPath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(stormPath, 0o600) })
	_, err := sc.RunTripwire(at(2026, 7, 5, 9, 0)) // past 72h ⇒ a lapse event must persist
	assert.Error(t, err)
}
