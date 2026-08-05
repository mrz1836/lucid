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

// viewOf reads one periodic's whole current row back out of the store, so a test
// can assert on what the store actually holds rather than on what the report
// says about it.
func viewOf(t *testing.T, db *gorm.DB, slug string, readAt time.Time) flywheel.PeriodicView {
	t.Helper()
	views, err := flywheel.ListPeriodics(ctxAt(readAt), db)
	require.NoError(t, err)
	for _, v := range views {
		if v.Slug == slug {
			return v
		}
	}
	t.Fatalf("periodic %q not found in the store", slug)
	return flywheel.PeriodicView{}
}

// activeOf reads one periodic's active flag back out of the store.
func activeOf(t *testing.T, db *gorm.DB, slug string, readAt time.Time) bool {
	t.Helper()
	return viewOf(t, db, slug, readAt).Active
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

// TestReconcile_SlugNarrowsThePassToOneRow pins what `--slug` means at the seam:
// it narrows what is *inspected*, not merely what is repaired. A row filtered
// out is not scanned, not judged against the guard, and not touched — which is
// what lets an operator repair one parked send on a busy store without the pass
// forming an opinion about anything else in it.
func TestReconcile_SlugNarrowsThePassToOneRow(t *testing.T) {
	const target, bystander = "seam-target", "seam-bystander"
	now := reconcileNow()
	db := newSeamDB(t)
	seedParked(t, db, target, now.Add(-24*time.Hour))
	seedParked(t, db, bystander, now.Add(-24*time.Hour))

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{
		Pkg:      "seam",
		Intended: func(string) (bool, bool) { return true, true },
		Slug:     target,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Scanned, "the filtered row is never even inspected")
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, target, report.Reconciled[0].Slug)

	assert.True(t, activeOf(t, db, target, now), "the named periodic is repaired")
	assert.False(t, activeOf(t, db, bystander, now), "every other parked row is left exactly as it was")
}

// TestReconcile_NoFireResetsTheCursorForward covers the pass's second repair
// mode. The default path leaves the stale cursor where it is, so the scheduler's
// next tick backfills the bucket that was missed and the send goes out late;
// NoFire instead re-declares the periodic from its own definition, so it resumes
// at its next scheduled occurrence and the missed one is skipped. That is the
// difference between "deliver it late" and "let this one go" — the choice an
// operator makes, and the only path that writes a new cursor.
func TestReconcile_NoFireResetsTheCursorForward(t *testing.T) {
	const slug = "seam-periodic"
	now := reconcileNow()
	db := newSeamDB(t)
	seedParked(t, db, slug, now.Add(-24*time.Hour))
	require.False(t, viewOf(t, db, slug, now).NextRunAt.After(now),
		"the fixture has to start with a cursor in the past for the reset to mean anything")

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{
		Pkg:      "seam",
		Intended: func(string) (bool, bool) { return true, true },
		NoFire:   true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Scanned)
	require.Len(t, report.Reconciled, 1)

	entry := report.Reconciled[0]
	assert.False(t, entry.WasActive, "it was found switched off")
	assert.False(t, entry.FiresMissed, "nothing is backfilled when the missed bucket is deliberately skipped")
	assert.True(t, entry.NextRun.After(now), "the cursor was moved to an occurrence that has not happened yet")

	got := viewOf(t, db, slug, now)
	assert.True(t, got.Active, "the definition is armed again")
	assert.Equal(t, entry.NextRun, got.NextRunAt, "the report states the cursor the store actually holds")
	assert.Equal(t, "0 6 * * *", got.Cron, "re-declaring from the row's own view leaves the schedule untouched")
}

// TestReconcileStore_RepairsAStoreOnDisk exercises the on-disk seam the CLI
// actually calls: open the store by path, repair it, close it. Every case above
// hands Reconcile a live handle, so this is the only place the open/migrate/close
// round trip is proven — and the only place that shows the re-arm is written to
// the file rather than to one connection's view of it.
func TestReconcileStore_RepairsAStoreOnDisk(t *testing.T) {
	const slug = "seam-periodic"
	now := reconcileNow()
	path := filepath.Join(t.TempDir(), "job.db")

	// Seed a parked periodic and then close the handle completely, so the pass
	// has to open the store on its own the way the command layer makes it.
	seed, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, MigrateJobStore(seed))
	seedParked(t, seed, slug, now.Add(-24*time.Hour))
	seedSQL, err := seed.DB()
	require.NoError(t, err)
	require.NoError(t, seedSQL.Close())

	report, err := ReconcileStore(ctxAt(now), path, ReconcileOptions{
		Pkg:      "seam",
		Intended: func(string) (bool, bool) { return true, true },
	})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Scanned)
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, slug, report.Reconciled[0].Slug)
	assert.False(t, report.Reconciled[0].WasActive)

	// Re-open independently: the repair outlived the connection that made it.
	reopened, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	t.Cleanup(func() {
		if sqlDB, cerr := reopened.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	})
	assert.True(t, activeOf(t, reopened, slug, now), "the re-arm is durable")
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
