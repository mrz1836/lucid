package flynode

import (
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// alwaysActive is the intended-active guard the re-arm error cases use: every
// slug is owned and should be running, so a parked row is always a repair
// candidate and the pass reaches the write it is meant to fail on.
func alwaysActive(string) (bool, bool) { return true, true }

// closeSeamDB closes a gorm handle's underlying pool. database/sql's Close is
// idempotent, so a later t.Cleanup close on the same handle is a harmless no-op.
func closeSeamDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// seededStorePath seeds a single parked periodic into a fresh job store on disk
// and returns its path with the seeding handle fully closed — so a test can
// reopen it however it needs (query-only, say) the way the command layer does.
func seededStorePath(t *testing.T, slug string, seedAt time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job.db")
	seed, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, MigrateJobStore(seed))
	seedParked(t, seed, slug, seedAt)
	closeSeamDB(t, seed)
	return path
}

// queryOnlyDB reopens an existing job store with writes disabled at the
// connection level (PRAGMA query_only), so a SELECT (ListPeriodics) still
// succeeds while any write (SetPeriodicActive, the reset transaction) fails.
// That isolates the re-arm write failures without corrupting the store or
// depending on file-mode behavior that is a no-op as root.
func queryOnlyDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Set("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	db, err := gorm.Open(sqlite.Open(u.String()), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	t.Cleanup(func() { closeSeamDB(t, db) })
	return db
}

// TestReconcile_ListPeriodicsError_IsTagged: when the store cannot even be
// listed (here, the handle is closed under it), the pass fails loudly with the
// caller's package tag rather than silently reporting a clean scan.
func TestReconcile_ListPeriodicsError_IsTagged(t *testing.T) {
	now := reconcileNow()
	db := newSeamDB(t)
	seedParked(t, db, "seam-periodic", now.Add(-24*time.Hour))
	closeSeamDB(t, db) // the list below now runs against a closed pool

	_, err := Reconcile(ctxAt(now), db, ReconcileOptions{Pkg: "seam", Intended: alwaysActive})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seam: list periodics")
}

// TestReconcile_RearmWriteError_IsPropagated: the default re-arm flips the
// active flag with a single write; when that write cannot land the pass returns
// the tagged error rather than reporting a repair that never happened.
func TestReconcile_RearmWriteError_IsPropagated(t *testing.T) {
	now := reconcileNow()
	path := seededStorePath(t, "seam-periodic", now.Add(-24*time.Hour))
	db := queryOnlyDB(t, path)

	_, err := Reconcile(ctxAt(now), db, ReconcileOptions{Pkg: "seam", Intended: alwaysActive})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seam: re-arm periodic")
}

// TestReconcile_NoFireResetError_IsPropagated: the NoFire re-arm rewrites the
// cursor inside a transaction; a store that rejects the write surfaces the
// tagged reset error rather than half-applying it.
func TestReconcile_NoFireResetError_IsPropagated(t *testing.T) {
	now := reconcileNow()
	path := seededStorePath(t, "seam-periodic", now.Add(-24*time.Hour))
	db := queryOnlyDB(t, path)

	_, err := Reconcile(ctxAt(now), db, ReconcileOptions{Pkg: "seam", Intended: alwaysActive, NoFire: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seam: reset periodic")
}

// TestReconcile_NoFireResetsAnIntervalPeriodic covers the interval arm of the
// cursor reset: an every-N periodic is re-declared from its own interval (not a
// cron) and resumes at a future occurrence, the missed one skipped.
func TestReconcile_NoFireResetsAnIntervalPeriodic(t *testing.T) {
	const slug = "seam-interval"
	now := reconcileNow()
	db := newSeamDB(t)
	seedAt := now.Add(-24 * time.Hour)
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(seedAt), db, flywheel.PeriodicSpec{
		Slug: slug, Kind: slug, Every: 6 * time.Hour, Queue: reconcileQueue, Active: true,
	}))
	require.NoError(t, flywheel.SetPeriodicActive(ctxAt(seedAt), db, slug, false))

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{Pkg: "seam", Intended: alwaysActive, NoFire: true})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.False(t, report.Reconciled[0].FiresMissed, "the missed interval bucket is skipped, not backfilled")
	assert.True(t, report.Reconciled[0].NextRun.After(now), "the cursor advanced to a future occurrence")
	assert.True(t, activeOf(t, db, slug, now), "the interval periodic is armed again")
}

// TestNextRunOf_MissingSlugAfterRearm: the cursor read-back names the package
// and the slug when the periodic it expects is not in the store — the "missing
// after re-arm" invariant a corrupted store would otherwise hide.
func TestNextRunOf_MissingSlugAfterRearm(t *testing.T) {
	now := reconcileNow()
	db := newSeamDB(t)
	seedParked(t, db, "present", now.Add(-24*time.Hour))

	_, err := nextRunOf(ctxAt(now), db, "absent", "seam")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `seam: periodic "absent" missing after re-arm`)
}

// TestNextRunOf_ListError_IsTagged: a store that cannot be listed surfaces the
// tagged list error from the cursor read-back too, not only from the top-level
// scan.
func TestNextRunOf_ListError_IsTagged(t *testing.T) {
	now := reconcileNow()
	db := newSeamDB(t)
	closeSeamDB(t, db)

	_, err := nextRunOf(ctxAt(now), db, "any", "seam")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seam: list periodics")
}
