package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestScheduler_ScaffoldFailureSurfaces: every entry point that prepares the
// engine tree surfaces a scaffold failure rather than proceeding on a broken
// tree. With a file sitting where engine/days must be a directory, the first
// os.MkdirAll fails, and each of the three jobs reports "prepare engine tree".
func TestScheduler_ScaffoldFailureSurfaces(t *testing.T) {
	cases := []struct {
		name string
		run  func(sc *Scheduler) error
	}{
		{"bell", func(sc *Scheduler) error {
			_, err := sc.RunBell(context.Background())
			return err
		}},
		{"bell_fallback", func(sc *Scheduler) error {
			_, err := sc.RunBellFallback(context.Background(), at(2026, 7, 6, 22, 15))
			return err
		}},
		{"tripwire", func(sc *Scheduler) error {
			_, err := sc.RunTripwire(context.Background(), at(2026, 7, 6, 9, 0))
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, a, _ := newSched(t)
			// Replace engine/days (a directory) with a plain file so the
			// scaffold's MkdirAll fails at ENOTDIR.
			daysDir := filepath.Join(a.Home(), "engine", "days")
			require.NoError(t, os.RemoveAll(daysDir))
			require.NoError(t, os.WriteFile(daysDir, []byte("x"), 0o600))

			err := tc.run(sc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "prepare engine tree")
		})
	}
}

// TestRunBellFallback_CorruptChainErrors: the backstop reads the bell's own
// consent from chain.json — a malformed file surfaces rather than firing a
// garbled bell, and nothing is delivered.
func TestRunBellFallback_CorruptChainErrors(t *testing.T) {
	sc, a, n := newSched(t)
	corrupt(t, a, "chain.json")

	_, err := sc.RunBellFallback(context.Background(), at(2026, 7, 6, 22, 15))
	require.Error(t, err)
	assert.Zero(t, n.count(engine.ChannelUser), "an unreadable consent never becomes a send")
}

// TestTripwireUserVerdict_GatherFailureSurfaces: the send-free verdict walks the
// same read path as a live run, so a corrupt engine file surfaces as a verdict
// error rather than a silent empty string.
func TestTripwireUserVerdict_GatherFailureSurfaces(t *testing.T) {
	sc, a, n := newSched(t)
	corrupt(t, a, "chain.json")

	_, err := sc.TripwireUserVerdict(at(2026, 7, 6, 9, 0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user verdict")
	assert.Empty(t, n.sent, "a failed verdict read delivers nothing")
}

// TestRunTripwire_CorruptDayRecordErrors: gathering the run folds every day
// record; a corrupt one surfaces rather than proceeding on a partial history.
func TestRunTripwire_CorruptDayRecordErrors(t *testing.T) {
	sc, a, n := newSched(t)
	seed(t, a, completedRec("2026-07-05"))
	dayPath := filepath.Join(a.Home(), "engine", "days", "2026", "07", "day_2026_07_05.json")
	require.NoError(t, os.WriteFile(dayPath, []byte("{bad"), 0o600))

	_, err := sc.RunTripwire(context.Background(), at(2026, 7, 6, 9, 0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine day")
	assert.Empty(t, n.sent, "a broken read never becomes a send")
}

// TestRunTripwire_EscalationWriteFailureSurfaced: a run whose escalation_state
// cannot be persisted (read-only status.json) surfaces the write failure after
// the pure decision, rather than reporting a clean run over an unwritten state.
func TestRunTripwire_EscalationWriteFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	sc, a, _ := newSched(t)
	seed(t, a, completedRec("2026-07-05")) // completed reference → no sends, no storm write
	statusPath := filepath.Join(a.Home(), "engine", "status.json")
	require.NoError(t, os.Chmod(statusPath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(statusPath, 0o600) })

	_, err := sc.RunTripwire(context.Background(), at(2026, 7, 6, 9, 0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status.json")
}

// TestRunTripwire_TripwireStateWriteFailureSurfaced: after the escalation is
// persisted, a run whose last-run bookkeeping cannot be written (read-only
// tripwire.json) surfaces that failure — the idempotency memory is not silently
// dropped. status.json stays writable, so the failure is isolated to the
// tripwire-state write.
func TestRunTripwire_TripwireStateWriteFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	sc, a, _ := newSched(t)
	seed(t, a, completedRec("2026-07-05"))
	twPath := filepath.Join(a.Home(), "engine", "tripwire.json")
	require.NoError(t, os.WriteFile(twPath, []byte("{}"), 0o600)) // readable, valid, but unwritable
	require.NoError(t, os.Chmod(twPath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(twPath, 0o600) })

	_, err := sc.RunTripwire(context.Background(), at(2026, 7, 6, 9, 0))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tripwire.json")
}
