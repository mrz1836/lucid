package scheduler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// TestScheduler_KillMidEveningTripwireFiresNextMorning is the Stage-6 "Done
// when" durability drill (build-plan.md): kill the scheduler mid-evening and
// the tripwire still fires next morning after a supervised restart. The
// scheduler holds no durable state of its own — everything lives in the Ledger
// (the go-flywheel db is disposable machinery, ADR-0004) — so a fresh process
// over the same home loses nothing and the dead-man still fires.
func TestScheduler_KillMidEveningTripwireFiresNextMorning(t *testing.T) {
	// A completed 07-04 stamps chain_start and arms the escalation ladder.
	_, a, _ := newSched(t)
	seed(t, a, completedRec("2026-07-04"))

	// The night of 07-05: the scheduler process is killed mid-evening before
	// any close-out — nothing is recorded for the logical day. (We simply run
	// no close-out; the night passes unrecorded.)

	// Supervised restart: a brand-new process — a fresh adapter AND a fresh
	// notifier over the SAME Ledger home. No in-memory state carries over.
	restarted := New(storage.New(a.Home()), &fakeNotifier{failOn: map[string]bool{}})

	// Next morning the tripwire fires the dead-man for the missed 07-05.
	rep, err := restarted.RunTripwire(at(2026, 7, 6, 9, 0))
	require.NoError(t, err)

	require.Len(t, rep.Sends, 1, "the tripwire still fires after the restart")
	assert.Equal(t, engine.SendL1, rep.Sends[0].Kind)
	assert.Contains(t, rep.Sends[0].Text, "one line, spoken or typed", "the floor is named")
	assert.Equal(t, engine.EscalationL1, rep.Escalation)

	// And the escalation persisted to the restarted process's Ledger.
	st, err := storage.New(a.Home()).ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, engine.EscalationL1, st.EscalationState)
}

// TestSelfCheck_PassesOnHealthyLedger: the post-upgrade tripwire self-check
// returns nil on a readable Ledger and delivers nothing.
func TestSelfCheck_PassesOnHealthyLedger(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-05"))

	require.NoError(t, sc.SelfCheck(at(2026, 7, 6, 9, 0)))
	assert.Empty(t, n.sent, "a self-check sends nothing")
}

// TestSelfCheck_IsDryRunNoPersist: even in a state that would fire an L1, the
// self-check delivers nothing and persists no escalation — it is a pure dry
// run of the read-and-evaluate path.
func TestSelfCheck_IsDryRunNoPersist(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-04")) // 07-05 absent → a live run would L1

	before, err := a.ReadEngineStatus()
	require.NoError(t, err)

	require.NoError(t, sc.SelfCheck(at(2026, 7, 6, 9, 0)))

	assert.Empty(t, n.sent, "the self-check emits no send")
	after, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, before.EscalationState, after.EscalationState, "the self-check persists no escalation")
	assert.Equal(t, engine.EscalationNone, after.EscalationState)
}

// TestSelfCheck_SurfacesReadFailure: a corrupt engine file makes the self-check
// fail — the health-check error that fails a managed upgrade (P10).
func TestSelfCheck_SurfacesReadFailure(t *testing.T) {
	sc, a, _ := newSched(t)
	require.NoError(t, os.WriteFile(filepath.Join(a.Home(), "engine", "chain.json"), []byte("{ not valid json"), 0o600))

	err := sc.SelfCheck(at(2026, 7, 6, 9, 0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "self-check")
}
