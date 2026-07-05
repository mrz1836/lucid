package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
}

// TestCloseout_MissNotPartial: a non-partial close-out where the survival
// link did not run is recorded as a miss with the honest ack.
func TestCloseout_MissNotPartial(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.Closeout(CloseoutRequest{
		Now: atUTC(2026, 7, 5, 22, 0), Links: map[string]string{"dock": engine.StatusDone}, Journal: "no survival",
	})
	require.NoError(t, err)
	assert.False(t, res.Completed)
	assert.True(t, res.Missed)
	assert.Contains(t, res.Ack, "survival link didn't run")

	rec := readDay(t, a, "day_2026_07_05")
	assert.False(t, rec.Completed)
	assert.True(t, rec.Missed)
}

// TestCloseout_JournalWriteFails surfaces a raw-write failure (nothing
// saved) — the Engine still never blocks, but the router reports honestly.
func TestCloseout_JournalWriteFails(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	rawDir := filepath.Join(home, "raw")
	require.NoError(t, os.Chmod(rawDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(rawDir, 0o700) })

	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestEngineCommands_ScaffoldFails covers the engine-tree scaffold failure
// branch shared by all three commands.
func TestEngineCommands_ScaffoldFails(t *testing.T) {
	skipIfRoot(t)
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"closeout", func(r *Router) error {
			_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
			return err
		}},
		{"backfill", func(r *Router) error {
			_, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
			return err
		}},
		{"profile", func(r *Router) error {
			_, err := r.Profile("nights", atUTC(2026, 7, 5, 22, 0))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, home := newBootedRouter(t)
			require.NoError(t, os.Chmod(home, 0o500))
			t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
			err := tc.call(r)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "engine tree")
		})
	}
}

// TestEngineCommands_WriteDayFails: with the engine day tree read-only,
// each command that writes a record surfaces the failure rather than
// corrupting state. (The engine tree already exists, so scaffold is a
// no-op and the failure lands on the day write.)
func TestEngineCommands_WriteDayFails(t *testing.T) {
	skipIfRoot(t)
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"closeout", func(r *Router) error {
			_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
			return err
		}},
		{"skip", func(r *Router) error {
			_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Skip: true})
			return err
		}},
		{"backfill", func(r *Router) error {
			target := atUTC(2026, 7, 4, 0, 0)
			_, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 5, 9, 0), Target: &target, Links: compactLinks(), Journal: "x"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _ := newBootedRouter(t)
			require.NoError(t, r.store.ScaffoldEngine()) // materialize engine/days
			daysDir := filepath.Join(r.store.Home(), "engine", "days")
			require.NoError(t, os.Chmod(daysDir, 0o500))
			t.Cleanup(func() { _ = os.Chmod(daysDir, 0o700) })
			assert.Error(t, tc.call(r))
		})
	}
}

// TestEngineCommands_RebuildFails: the day write succeeds but the derived
// status.json cannot be written (engine dir read-only, days writable), so
// the command surfaces the rebuild failure.
func TestEngineCommands_RebuildFails(t *testing.T) {
	skipIfRoot(t)
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"closeout", func(r *Router) error {
			_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
			return err
		}},
		{"skip", func(r *Router) error {
			_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Skip: true})
			return err
		}},
		{"backfill", func(r *Router) error {
			target := atUTC(2026, 7, 4, 0, 0)
			_, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 5, 9, 0), Target: &target, Links: compactLinks(), Journal: "x"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _ := newBootedRouter(t)
			require.NoError(t, r.store.ScaffoldEngine())
			engineDir := filepath.Join(r.store.Home(), "engine")
			require.NoError(t, os.Chmod(engineDir, 0o500)) // days stays 0o700; status.json write fails
			t.Cleanup(func() { _ = os.Chmod(engineDir, 0o700) })
			assert.Error(t, tc.call(r))
		})
	}
}

// TestEngineCommands_CorruptChain: a malformed chain.json is surfaced by
// every engine command rather than silently defaulted.
func TestEngineCommands_CorruptChain(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"closeout", func(r *Router) error {
			_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
			return err
		}},
		{"backfill", func(r *Router) error {
			_, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 5, 9, 0), Links: compactLinks(), Journal: "x"})
			return err
		}},
		{"profile", func(r *Router) error {
			_, err := r.Profile("nights", atUTC(2026, 7, 5, 22, 0))
			return err
		}},
		{"chain", func(r *Router) error {
			_, err := r.Chain()
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, home := newBootedRouter(t)
			require.NoError(t, r.store.ScaffoldEngine())
			require.NoError(t, os.WriteFile(filepath.Join(home, "engine", "chain.json"), []byte("{bad"), 0o600))
			assert.Error(t, tc.call(r))
		})
	}
}

// TestProfile_AppendFails surfaces a profile.json write failure.
func TestProfile_AppendFails(t *testing.T) {
	skipIfRoot(t)
	r, _, _ := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	profilePath := filepath.Join(r.store.Home(), "engine", "profile.json")
	require.NoError(t, os.Chmod(profilePath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(profilePath, 0o600) })

	_, err := r.Profile("nights", atUTC(2026, 7, 7, 21, 50))
	assert.Error(t, err)
}

// TestSkip_JournalNotWritten confirms a skip path rebuilds status without a
// raw write even after several days.
func TestSkip_JournalNotWritten(t *testing.T) {
	r, a, home := newBootedRouter(t)
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Skip: true})
	require.NoError(t, err)
	assert.Equal(t, 0, countFiles(t, home, "raw"))

	status, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, 0, status.CurrentStreak)
	assert.Equal(t, 1, status.RawDaysAccounted)
}
