package flynode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// These tests cover the reconcile *seam* only — the guard injection, the nil
// guard, the error tag, and what counts as parked. The store-specific behavior
// (which slugs each daemon owns, what a real parked send does on the next tick)
// is proven where the guards live, in internal/schedrun and internal/companion.

// reconcileQueue is the queue name the seam fixtures declare their periodics on.
// The pass never reads it; it exists so a seeded spec is well-formed.
const reconcileQueue = "flynode-reconcile-test"

// reconcileNow is the reference instant every seam test reconciles at.
func reconcileNow() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }

// ctxAt returns a context carrying a fixed clock at t — the clock Reconcile
// reads its reference instant from.
func ctxAt(t time.Time) context.Context {
	return models.WithClock(context.Background(), models.NewFixedClock(t))
}

// newSeamDB opens a migrated job store in a temp dir, so a test can drive
// Reconcile against real rows rather than hand-built ones. The on-disk seam
// ([ReconcileStore]) is exercised separately, by the error-tag cases.
func newSeamDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, MigrateJobStore(db))
	t.Cleanup(func() {
		if sqlDB, cerr := db.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// seedParked declares a periodic and switches it off — the state repair exists
// for: the definition survives, but nothing will ever fire it again.
func seedParked(t *testing.T, db *gorm.DB, slug string, seedAt time.Time) {
	t.Helper()
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(seedAt), db, flywheel.PeriodicSpec{
		Slug: slug, Kind: slug, Cron: "0 6 * * *", Queue: reconcileQueue, Active: true,
	}))
	require.NoError(t, flywheel.SetPeriodicActive(ctxAt(seedAt), db, slug, false))
}

// activeOf reads one periodic's active flag back out of the store.
func activeOf(t *testing.T, db *gorm.DB, slug string, readAt time.Time) bool {
	t.Helper()
	views, err := flywheel.ListPeriodics(ctxAt(readAt), db)
	require.NoError(t, err)
	for _, v := range views {
		if v.Slug == slug {
			return v.Active
		}
	}
	t.Fatalf("periodic %q not found in the store", slug)
	return false
}

// TestReconcile_InjectedGuardDecidesWhatIsRepaired is the whole point of the
// seam: the pass is generic over the store, and the *only* thing that decides
// whether a parked row is repaired is the guard the caller injected. Each row
// here is a promise one of the daemons relies on — an unknown slug is left
// alone (it belongs to somebody else, or to nobody), and a slug the
// configuration deliberately suppresses stays suppressed, so repair can never
// fight a setting.
func TestReconcile_InjectedGuardDecidesWhatIsRepaired(t *testing.T) {
	const slug = "seam-periodic"
	cases := []struct {
		name        string
		guard       IntendedActive
		wantRearmed bool
	}{
		{
			name:        "a slug the guard does not know is never touched",
			guard:       func(string) (bool, bool) { return false, false },
			wantRearmed: false,
		},
		{
			name:        "a known slug the configuration intends inactive stays parked",
			guard:       func(string) (bool, bool) { return false, true },
			wantRearmed: false,
		},
		{
			name:        "a known slug the configuration intends active is re-armed",
			guard:       func(string) (bool, bool) { return true, true },
			wantRearmed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := reconcileNow()
			db := newSeamDB(t)
			seedParked(t, db, slug, now.Add(-24*time.Hour))

			report, err := Reconcile(ctxAt(now), db, ReconcileOptions{Pkg: "seam", Intended: tc.guard})
			require.NoError(t, err)
			assert.Equal(t, 1, report.Scanned, "every row is inspected regardless of the verdict")

			if !tc.wantRearmed {
				assert.Empty(t, report.Reconciled, "the guard withheld repair")
				assert.False(t, activeOf(t, db, slug, now), "the store is unchanged")
				return
			}
			require.Len(t, report.Reconciled, 1)
			assert.Equal(t, slug, report.Reconciled[0].Slug)
			assert.False(t, report.Reconciled[0].WasActive, "it was found switched off")
			assert.True(t, activeOf(t, db, slug, now), "the definition is armed again")
		})
	}
}

// TestReconcile_NilGuardRepairsNothing pins the safe direction for a caller that
// forgot to inject a guard: a pass with no notion of what should be running must
// not invent one from the store it is inspecting. It scans, reports, and changes
// nothing.
func TestReconcile_NilGuardRepairsNothing(t *testing.T) {
	const slug = "seam-periodic"
	now := reconcileNow()
	db := newSeamDB(t)
	seedParked(t, db, slug, now.Add(-24*time.Hour))

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{Pkg: "seam"})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Scanned)
	assert.Empty(t, report.Reconciled, "a nil guard owns nothing")
	assert.False(t, activeOf(t, db, slug, now), "the store is untouched")
}

// TestReconcile_PkgTagsErrors proves the daemon name travels with the pass. It
// is what lets one shared implementation produce each caller's own error text —
// the property that keeps the Engine's messages byte-identical after the move,
// and that tells a supervised log which node's store actually failed.
func TestReconcile_PkgTagsErrors(t *testing.T) {
	t.Run("a missing store names the package and the path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent", "job.db")

		_, err := ReconcileStore(context.Background(), path, ReconcileOptions{Pkg: "seam"})
		require.ErrorIs(t, err, ErrJobStoreMissing)
		assert.Contains(t, err.Error(), "seam: job store not found at")
		assert.Contains(t, err.Error(), path, "the error names the path it looked at")
	})

	t.Run("an unusable store surfaces the package on the migrate failure", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "job.db")
		require.NoError(t, os.WriteFile(path, []byte("this is not a database"), 0o600))

		_, err := ReconcileStore(context.Background(), path, ReconcileOptions{Pkg: "seam"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "seam: ")
		assert.Contains(t, err.Error(), "job db")
	})

	t.Run("a caller that set no package defaults to this one", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent", "job.db")

		_, err := ReconcileStore(context.Background(), path, ReconcileOptions{})
		require.ErrorIs(t, err, ErrJobStoreMissing)
		assert.Contains(t, err.Error(), "flynode: job store not found at")
	})
}

// TestParked_InactiveOrStuckCursor separates the state repair exists for from
// the states that only look like it. A live scheduler advances a fired
// periodic's cursor past now in one tick, so an active periodic sitting a few
// hours behind is *due*, not parked — mistaking the two would report a healthy
// daemon as broken every day between the mark and the next poll, and would make
// this pass a writer on a healthy store rather than a no-op.
func TestParked_InactiveOrStuckCursor(t *testing.T) {
	now := reconcileNow()
	cases := []struct {
		name string
		view flywheel.PeriodicView
		want bool
	}{
		{"inactive with a future cursor is parked", flywheel.PeriodicView{Active: false, NextRunAt: now.Add(6 * time.Hour)}, true},
		{"inactive with a frozen cursor is parked", flywheel.PeriodicView{Active: false, NextRunAt: now.Add(-72 * time.Hour)}, true},
		{"active with a whole occurrence missed is stuck", flywheel.PeriodicView{Active: true, NextRunAt: now.Add(-25 * time.Hour)}, true},
		{"active and merely due is not parked", flywheel.PeriodicView{Active: true, NextRunAt: now.Add(-30 * time.Minute)}, false},
		{"active and just inside the grace is not parked", flywheel.PeriodicView{Active: true, NextRunAt: now.Add(-StaleCursorGrace + time.Minute)}, false},
		{"active and ahead is not parked", flywheel.PeriodicView{Active: true, NextRunAt: now.Add(6 * time.Hour)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Parked(tc.view, now))
		})
	}
}

// TestStartupErr grades the boot sequence's failures the way every daemon needs:
// a stop signal landing mid-startup is an ordinary shutdown, not an error line
// in a supervised log, while a genuine failure with a live context still
// surfaces.
func TestStartupErr(t *testing.T) {
	boom := context.DeadlineExceeded // any non-nil error stands in for a real failure

	t.Run("a live context surfaces the failure", func(t *testing.T) {
		require.ErrorIs(t, StartupErr(context.Background(), boom), boom)
	})

	t.Run("a canceled boot is a stop request, not a failure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.NoError(t, StartupErr(ctx, boom))
	})
}
