package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// TestSelf_AppendFailureSurfaced proves every self-fact write verb surfaces a
// read-only self.json rather than silently dropping the append.
func TestSelf_AppendFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"set", func(r *Router) error {
			_, err := r.SelfSet(SelfSetRequest{Key: "body.height", Value: "180cm", Now: fixedNow()})
			return err
		}},
		{"retire", func(r *Router) error {
			_, err := r.SelfRetire(SelfRetireRequest{Key: "body.handedness", Now: fixedNow()})
			return err
		}},
		{"move", func(r *Router) error {
			_, err := r.SelfMove(SelfMoveRequest{Key: "body.handedness", NewKey: "body.height", Now: fixedNow()})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, home := newBootedRouter(t)
			// Seed one active fact so the retire/move verbs resolve a target and the
			// self.json file exists to read back before the failing append.
			_, err := r.SelfSet(SelfSetRequest{Key: "body.handedness", Value: "left", Now: fixedNow()})
			require.NoError(t, err)

			// The self store writes atomically (temp + rename), so the append fails
			// on an un-writable engine dir rather than a read-only file.
			engineDir := filepath.Join(home, "engine")
			require.NoError(t, os.Chmod(engineDir, 0o500))
			t.Cleanup(func() { _ = os.Chmod(engineDir, 0o700) })

			err = tc.call(r)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "nothing was saved")
		})
	}
}

// TestStorm_PersistFailureSurfaced proves a renewal and an early end surface a
// read-only storm.json — the persistStorm tail every accepted command shares.
func TestStorm_PersistFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	for _, tc := range []struct {
		name string
		arg  string
	}{
		{"renew", "clause-1"},
		{"end", "end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a, home := newBootedRouter(t)
			standingStorm(t, a) // a confirmed storm stands through 2026-07-28

			stormPath := filepath.Join(home, "engine", "storm.json")
			require.NoError(t, os.Chmod(stormPath, 0o400))
			t.Cleanup(func() { _ = os.Chmod(stormPath, 0o600) })

			_, err := r.Storm(tc.arg, atUTC(2026, 7, 20, 8, 0))
			require.Error(t, err)
		})
	}
}

// TestCloseout_FormatDeclaredNonZero exercises the mode-declaration timestamp
// render on the close-out path: a non-zero ModeDeclaredAt is stamped onto the
// day record rather than dropped.
func TestCloseout_FormatDeclaredNonZero(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.Closeout(CloseoutRequest{
		Now:            atUTC(2026, 7, 5, 22, 0),
		Links:          compactLinks(),
		Journal:        "declared yellow at noon",
		Mode:           "yellow",
		ModeDeclaredAt: atUTC(2026, 7, 5, 12, 0),
	})
	require.NoError(t, err)
	rec := readDay(t, a, res.DayID)
	assert.Equal(t, "2026-07-05T12:00:00Z", rec.ModeDeclaredAt)
}

// TestCloseout_SessionWriteFailureSurfaces proves the close-out journal writer
// surfaces a session-record write failure after the raw entry lands.
func TestCloseout_SessionWriteFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	sessDir := filepath.Join(home, "sessions")
	require.NoError(t, os.Chmod(sessDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(sessDir, 0o700) })

	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session record")
}

// TestCloseout_IdempotentStreakReadFailure proves an idempotent re-close surfaces
// a folded-days read failure rather than reporting a wrong streak: a completed
// day plus a corrupt sibling day fails the streak recompute.
func TestCloseout_IdempotentStreakReadFailure(t *testing.T) {
	r, _, home := newBootedRouter(t)
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.NoError(t, err)
	// A corrupt sibling day makes the ReadEngineDays fold currentStreak runs fail,
	// while the completed target day still folds so the idempotent path is taken.
	require.NoError(t, writeFileHelper(home, "engine/days/2026/07/day_2026_07_01.json", "{bad"))
	_, err = r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.Error(t, err)
}

// TestCloseout_SkipIdempotentStreakReadFailure covers the skip path's idempotent
// streak read: a recorded day re-skipped over a corrupt sibling surfaces the fold
// failure.
func TestCloseout_SkipIdempotentStreakReadFailure(t *testing.T) {
	r, _, home := newBootedRouter(t)
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Skip: true})
	require.NoError(t, err)
	require.NoError(t, writeFileHelper(home, "engine/days/2026/07/day_2026_07_01.json", "{bad"))
	_, err = r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Skip: true})
	require.Error(t, err)
}

// TestCloseout_ReadFoldedTargetCorruptSurfaces proves a corrupt target day record
// fails the close-out rather than closing over garbage (closeout.go:83).
func TestCloseout_ReadFoldedTargetCorruptSurfaces(t *testing.T) {
	r, home := scaffoldedEngine(t)
	require.NoError(t, writeFileHelper(home, "engine/days/2026/07/day_2026_07_05.json", "{bad"))
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.Error(t, err)
}

// TestCloseout_ResolveLogicalDayPrevCorruptSurfaces proves a corrupt previous-day
// record fails the logical-day resolution rather than mis-attributing the close
// (closeout.go:120).
func TestCloseout_ResolveLogicalDayPrevCorruptSurfaces(t *testing.T) {
	r, home := scaffoldedEngine(t)
	require.NoError(t, writeFileHelper(home, "engine/days/2026/07/day_2026_07_04.json", "{bad"))
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.Error(t, err)
}

// TestCloseout_AppendCorrectionFailureSurfaces proves completing an existing
// (mode-only) day surfaces a correction-append failure rather than a lost close
// (closeout.go:180).
func TestCloseout_AppendCorrectionFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	// A same-day /mode writes a mode-only record, so the close-out takes the
	// "append a completing correction" branch over an existing day.
	_, err := r.Mode("yellow", atUTC(2026, 7, 5, 14, 2))
	require.NoError(t, err)

	dayFile := filepath.Join(home, "engine", "days", "2026", "07", "day_2026_07_05.json")
	require.NoError(t, os.Chmod(dayFile, 0o400)) // readable so the fold reads; the correction write fails
	t.Cleanup(func() { _ = os.Chmod(dayFile, 0o600) })

	_, err = r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.Error(t, err)
}

// TestChain_ScaffoldFailureSurfaces proves Chain surfaces an engine-tree scaffold
// failure under a read-only home.
func TestChain_ScaffoldFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	require.NoError(t, os.Chmod(home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	_, err := r.Chain()
	require.Error(t, err)
}

// TestBackfill_JournalWriteFailureSurfaces proves an in-window backfill surfaces
// a raw-write failure rather than recording a day with no journal line.
func TestBackfill_JournalWriteFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	rawDir := filepath.Join(home, "raw")
	require.NoError(t, os.Chmod(rawDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(rawDir, 0o700) })

	target := atUTC(2026, 7, 4, 0, 0)
	_, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 5, 9, 0), Target: &target, Links: compactLinks(), Journal: "x"})
	require.Error(t, err)
}

// TestRegistryWrite_FailuresSurface proves the shared registry writer surfaces a
// key-resolution read failure and a record write failure rather than a partial
// registry.
func TestRegistryWrite_FailuresSurface(t *testing.T) {
	skipIfRoot(t)
	t.Run("resolveKeyFails", func(t *testing.T) {
		r, _, home := bootedMemoryRouter(t)
		injuriesDir := filepath.Join(home, "registries", "injuries")
		require.NoError(t, os.Chmod(injuriesDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(injuriesDir, 0o700) })
		_, err := r.WriteInjury(InjuryWriteRequest{Name: "left knee", Now: fixedNow()})
		require.Error(t, err)
	})

	t.Run("recordWriteFails", func(t *testing.T) {
		r, _, home := bootedMemoryRouter(t)
		injuriesDir := filepath.Join(home, "registries", "injuries")
		require.NoError(t, os.Chmod(injuriesDir, 0o500)) // readable so key resolves; write fails
		t.Cleanup(func() { _ = os.Chmod(injuriesDir, 0o700) })
		_, err := r.WriteInjury(InjuryWriteRequest{Name: "left knee", Now: fixedNow()})
		require.Error(t, err)
	})
}

// enableLocationCapture turns on the sticky-location kind so a `where` capture
// reaches resolvePlace rather than the disabled-kind reject.
func enableLocationCapture(t *testing.T, a *storage.Adapter) {
	t.Helper()
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = append(cfg.KindsEnabled, observations.KindLocation)
	require.NoError(t, a.SaveObservationsConfig(cfg))
}

// TestCapture_ResolvePlaceFailuresSurface proves a sticky-location capture
// surfaces a place-registry read failure and a place-registry write failure
// rather than filing an event with a dangling place ref.
func TestCapture_ResolvePlaceFailuresSurface(t *testing.T) {
	skipIfRoot(t)
	t.Run("resolveKeyFails", func(t *testing.T) {
		r, a, home := bootedMemoryRouter(t)
		enableLocationCapture(t, a)
		placesDir := filepath.Join(home, "registries", "places")
		require.NoError(t, os.Chmod(placesDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(placesDir, 0o700) })
		_, err := r.Capture(CaptureRequest{Tokens: []string{"where", "Lisbon"}, Now: fixedNow()})
		require.Error(t, err)
	})

	t.Run("updateRegistryFails", func(t *testing.T) {
		r, a, home := bootedMemoryRouter(t)
		enableLocationCapture(t, a)
		placesDir := filepath.Join(home, "registries", "places")
		require.NoError(t, os.Chmod(placesDir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(placesDir, 0o700) })
		_, err := r.Capture(CaptureRequest{Tokens: []string{"where", "Lisbon"}, Now: fixedNow()})
		require.Error(t, err)
	})
}

// TestCapture_AppendObservationFailureSurfaces proves a capture surfaces an
// append failure rather than reporting a phantom event id.
func TestCapture_AppendObservationFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, _, home := bootedMemoryRouter(t)
	obsDir := filepath.Join(home, "observations")
	require.NoError(t, os.Chmod(obsDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(obsDir, 0o700) })
	_, err := r.Capture(CaptureRequest{Tokens: []string{"pain", "6", "knee"}, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestWorkoutLog_AppendFailureSurfaces proves the workout capture surfaces an
// append failure with nothing saved.
func TestWorkoutLog_AppendFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, a, home := bootedMemoryRouter(t)
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = append(cfg.KindsEnabled, observations.KindWorkout)
	require.NoError(t, a.SaveObservationsConfig(cfg))

	obsDir := filepath.Join(home, "observations")
	require.NoError(t, os.Chmod(obsDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(obsDir, 0o700) })
	_, err = r.WorkoutLog(WorkoutLogRequest{Type: "push", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestCuriosity_WriteStateFailureSurfaces proves the ask-state persist failure is
// surfaced rather than swallowed.
func TestCuriosity_WriteStateFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, _, home := bootedMemoryRouter(t)
	projDir := filepath.Join(home, "projections")
	require.NoError(t, os.Chmod(projDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(projDir, 0o700) })
	_, err := r.Curiosity(fixedNow(), false)
	require.Error(t, err)
}

// TestMode_GapFillWriteFailureSurfaces proves a mode gap-fill onto a day with no
// record surfaces a day-write failure rather than a silent gap.
func TestMode_GapFillWriteFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	daysDir := filepath.Join(home, "engine", "days")
	require.NoError(t, os.Chmod(daysDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(daysDir, 0o700) })
	_, err := r.DeclareMode(ModeRequest{Mode: "yellow", DayArg: "2026-07-04", Now: atUTC(2026, 7, 5, 9, 0)})
	require.Error(t, err)
}

// TestValidate_Now defaults the turn's instant from the wall clock on a bootstrap
// short-circuit, exercising the zero-Now branch deterministically (the result is
// the fixed no-pattern outcome regardless of the clock).
func TestValidate_Now(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	res, err := r.Validate(context.Background(), ValidateRequest{
		ProcessedID: curr, Bootstrap: true, Provider: proposalFake(), Responder: acceptResp(),
	})
	require.NoError(t, err)
	assert.Equal(t, reflection.OutcomeNoPattern, res.Outcome)
}
