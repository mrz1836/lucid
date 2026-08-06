package router

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// TestScaffoldFailsSurfaces covers the first tree-scaffold guard every
// observations- or engine-touching verb runs before it reads or writes: with the
// home read-only, the idempotent scaffold cannot create its subtree, and the verb
// surfaces that rather than proceeding over a half-built Ledger.
func TestScaffoldFailsSurfaces(t *testing.T) {
	skipIfRoot(t)
	fake := &provider.Fake{Script: []provider.Exchange{{Content: "{}"}}}
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"recall", func(r *Router) error { _, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: "x"}); return err }},
		{"writeMemory", func(r *Router) error {
			_, err := r.WriteMemory(MemoryWriteRequest{Text: "x", Now: fixedNow()})
			return err
		}},
		{"capture", func(r *Router) error {
			_, err := r.Capture(CaptureRequest{Tokens: []string{"pain", "6", "knee"}, Now: fixedNow()})
			return err
		}},
		{"dayView", func(r *Router) error { _, err := r.DayView("", fixedNow()); return err }},
		{"excavate", func(r *Router) error { _, err := r.Excavate(fixedNow()); return err }},
		{"buildWeekBundle", func(r *Router) error { _, err := r.BuildWeekBundle(fixedNow(), ReflectWindow{}); return err }},
		{"workoutLog", func(r *Router) error { _, err := r.WorkoutLog(WorkoutLogRequest{Now: fixedNow()}); return err }},
		{"workoutLogFromText", func(r *Router) error {
			_, err := r.WorkoutLogFromText(context.Background(), WorkoutLogTextRequest{Text: "did pull", Now: fixedNow()}, fake)
			return err
		}},
		{"workout", func(r *Router) error { _, err := r.Workout(context.Background(), fixedNow(), fake); return err }},
		{"stats", func(r *Router) error { _, err := r.Stats(StatsOptions{}, fixedNow()); return err }},
		{"injuryContext", func(r *Router) error { _, err := r.InjuryContext(); return err }},
		{"writeInjury", func(r *Router) error {
			_, err := r.WriteInjury(InjuryWriteRequest{Name: "knee", Now: fixedNow()})
			return err
		}},
		{"writeEra", func(r *Router) error {
			_, err := r.WriteEra(EraWriteRequest{Name: "summer", Now: fixedNow()})
			return err
		}},
		{"writeThread", func(r *Router) error {
			_, err := r.WriteThread(ThreadWriteRequest{Name: "piano", Intent: "fun", Now: fixedNow()})
			return err
		}},
		{"selfSet", func(r *Router) error {
			_, err := r.SelfSet(SelfSetRequest{Key: "body.handedness", Value: "left", Now: fixedNow()})
			return err
		}},
		{"selfRetire", func(r *Router) error {
			_, err := r.SelfRetire(SelfRetireRequest{Key: "body.handedness", Now: fixedNow()})
			return err
		}},
		{"selfMove", func(r *Router) error {
			_, err := r.SelfMove(SelfMoveRequest{Key: "body.handedness", NewKey: "body.height", Now: fixedNow()})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, home := newBootedRouter(t)
			require.NoError(t, os.Chmod(home, 0o500))
			t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
			assert.Error(t, tc.call(r))
		})
	}
}

// TestPrepareEngineAfterObservationsFails covers the SECOND scaffold guard on the
// join surfaces that touch both trees: the observations tree already exists (so
// its prepare is a no-op), and the engine-tree scaffold is what fails under a
// read-only home.
func TestPrepareEngineAfterObservationsFails(t *testing.T) {
	skipIfRoot(t)
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"dayView", func(r *Router) error { _, err := r.DayView("", fixedNow()); return err }},
		{"stats", func(r *Router) error { _, err := r.Stats(StatsOptions{}, fixedNow()); return err }},
		{"buildWeekBundle", func(r *Router) error { _, err := r.BuildWeekBundle(fixedNow(), ReflectWindow{}); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a, home := newBootedRouter(t)
			require.NoError(t, a.ScaffoldObservations()) // the first prepare is now a no-op
			require.NoError(t, os.Chmod(home, 0o500))
			t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
			assert.Error(t, tc.call(r))
		})
	}
}
