package companion

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/storage"
)

// --- fixtures ---------------------------------------------------------------

// seedCompanion reconciles the production periodic set into a job store at
// seedClock — the exact state a node boot leaves behind, so a reconcile test
// starts from real rows rather than hand-built ones.
func seedCompanion(t *testing.T, db *gorm.DB, store *storage.Adapter, seedClock time.Time) {
	t.Helper()
	require.NoError(t, upsertPeriodics(ctxAt(seedClock), db, store))
}

// parkCompanion switches a periodic off, reproducing the state this whole pass
// exists to repair: the definition survives, but nothing will ever fire it
// again.
func parkCompanion(t *testing.T, db *gorm.DB, slug string, parkClock time.Time) {
	t.Helper()
	require.NoError(t, flywheel.SetPeriodicActive(ctxAt(parkClock), db, slug, false))
}

// stall freezes one periodic's cursor in the past while leaving the definition
// switched on — the second parked shape, and the one a re-seed never repairs.
// There is no cursor writer upstream, so the row is re-declared from its own
// current definition at an older reference instant, which is exactly what an
// occurrence elapsing with no tick looks like.
func stall(t *testing.T, db *gorm.DB, slug string, stallClock time.Time) {
	t.Helper()
	v, ok := findPeriodic(t, db, slug, stallClock)
	require.True(t, ok, "cannot stall a periodic that is not there: %s", slug)
	require.NoError(t, flywheel.DeletePeriodic(ctxAt(stallClock), db, slug))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(stallClock), db, flywheel.PeriodicSpec{
		Slug: v.Slug, Kind: v.Kind, Cron: v.Cron, Queue: v.Queue, Active: true,
	}))
}

// findPeriodic reads one periodic back out of a store by slug.
func findPeriodic(t *testing.T, db *gorm.DB, slug string, readClock time.Time) (flywheel.PeriodicView, bool) {
	t.Helper()
	views, err := flywheel.ListPeriodics(ctxAt(readClock), db)
	require.NoError(t, err)
	for _, v := range views {
		if v.Slug == slug {
			return v, true
		}
	}
	return flywheel.PeriodicView{}, false
}

// activeBySlug reads every periodic's active flag back out of a store.
func activeBySlug(t *testing.T, db *gorm.DB, readClock time.Time) map[string]bool {
	t.Helper()
	views, err := flywheel.ListPeriodics(ctxAt(readClock), db)
	require.NoError(t, err)
	active := map[string]bool{}
	for _, v := range views {
		active[v.Slug] = v.Active
	}
	return active
}

// cursorsBySlug reads every periodic's next run back out of a store, so a
// no-op assertion can prove a healthy cursor was left exactly where it was.
func cursorsBySlug(t *testing.T, db *gorm.DB, readClock time.Time) map[string]time.Time {
	t.Helper()
	views, err := flywheel.ListPeriodics(ctxAt(readClock), db)
	require.NoError(t, err)
	cursors := map[string]time.Time{}
	for _, v := range views {
		cursors[v.Slug] = v.NextRunAt
	}
	return cursors
}

// --- The intended-active guard ----------------------------------------------

// TestIntendedActive_CoversTheThreeCompanionSlugs tables the guard the whole
// pass turns on. All three companion periodics are unconditionally intended
// active — this node only runs when the companion is enabled, so there is no
// setting for repair to fight — and anything else in the store is not ours to
// touch.
func TestIntendedActive_CoversTheThreeCompanionSlugs(t *testing.T) {
	cases := []struct {
		name        string
		slug        string
		want, known bool
	}{
		{"the morning card is always intended", slugMorning, true, true},
		{"so is the night card", slugNight, true, true},
		{"and so is the morning backstop", slugMorningBackstop, true, true},
		{"an Engine slug belongs to the Engine's guard", "lucid-tripwire", false, false},
		{"an unrecognized slug is not ours to repair", "lucid-something-else", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, known := intendedActive(tc.slug)
			assert.Equal(t, tc.want, want, "intended-active")
			assert.Equal(t, tc.known, known, "known slug")
		})
	}
}

// --- Re-arming a parked send -------------------------------------------------

// TestReconcile_ReArmsAParkedCompanionPeriodic is the headline promise: a
// morning card switched off in the companion's own store is armed again, and the
// re-arm leaves the cursor alone so the schedule it was on survives the repair.
// Before this pass existed the definition would have sat inactive forever, with
// `scheduler status` naming the fault and no command able to fix it.
func TestReconcile_ReArmsAParkedCompanionPeriodic(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	store := newStore(t)
	db := newJobDB(t)
	seedCompanion(t, db, store, seedAt)
	parkCompanion(t, db, slugMorning, seedAt)

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1, "the parked morning is the one thing repaired")
	assert.Equal(t, 3, report.Scanned, "all three definitions are inspected")
	assert.Equal(t, slugMorning, report.Reconciled[0].Slug)
	assert.False(t, report.Reconciled[0].WasActive, "it was found switched off")
	assert.Equal(t, at(2026, 7, 6, 6, 0), report.Reconciled[0].NextRun.UTC(), "the cursor is left in place")

	morning, ok := findPeriodic(t, db, slugMorning, now)
	require.True(t, ok)
	assert.True(t, morning.Active, "the definition is armed again")
	assert.Equal(t, "0 6 * * *", morning.Cron, "repair never edits the schedule")
	assert.Equal(t, queueName, morning.Queue, "nor the queue")
}

// TestReconcile_ReArmsTheParkedBackstop: the morning backstop is inside the
// guard too. It is the newest of the three and the one most easily forgotten —
// a safety net that could itself be parked with no lever would be no safety net.
func TestReconcile_ReArmsTheParkedBackstop(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	store := newStore(t)
	db := newJobDB(t)
	seedCompanion(t, db, store, seedAt)
	parkCompanion(t, db, slugMorningBackstop, seedAt)

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, slugMorningBackstop, report.Reconciled[0].Slug)

	active := activeBySlug(t, db, now)
	assert.True(t, active[slugMorningBackstop], "the morning safety net is armed again")
	assert.True(t, active[slugMorning], "and the sends that were fine are still fine")
	assert.True(t, active[slugNight])
}

// TestReconcile_CompanionStuckCursor covers the second parked shape, and the one
// that motivated the whole lever: the definition is on, but a whole occurrence
// elapsed with no tick advancing it. A re-seed on the next boot never repairs
// this — an upsert restores the active flag and deliberately preserves the
// cursor — so without this pass a frozen morning survives every restart.
func TestReconcile_CompanionStuckCursor(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	store := newStore(t)
	db := newJobDB(t)
	seedCompanion(t, db, store, seedAt)
	stall(t, db, slugMorning, at(2026, 7, 2, 12, 0)) // next run 07-03 06:00, days stale

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{NoFire: true})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1, "a cursor a full day behind is parked, not merely due")
	assert.Equal(t, slugMorning, report.Reconciled[0].Slug)
	assert.True(t, report.Reconciled[0].WasActive, "the report names what was actually wrong")
	assert.Equal(t, at(2026, 7, 6, 6, 0), report.Reconciled[0].NextRun.UTC(), "it is pulled back onto the schedule")

	morning, ok := findPeriodic(t, db, slugMorning, now)
	require.True(t, ok)
	assert.Equal(t, "0 6 * * *", morning.Cron, "the cadence survives the cursor reset")
}

// --- No-op on a healthy store ------------------------------------------------

// TestReconcile_HealthyCompanionStoreChangesNothing is the promise that makes an
// automatic repair pass safe to run on every boot and on an operator's whim: on
// a store with nothing wrong it reports nothing and — the part worth asserting —
// moves nothing. A pass that quietly re-declared healthy cursors would silently
// skip sends every time it ran.
func TestReconcile_HealthyCompanionStoreChangesNothing(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	store := newStore(t)
	db := newJobDB(t)
	seedCompanion(t, db, store, seedAt)
	before := cursorsBySlug(t, db, now)

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{})
	require.NoError(t, err)
	assert.Empty(t, report.Reconciled, "nothing was parked")
	assert.Equal(t, 3, report.Scanned, "and all three were looked at")

	assert.Equal(t, before, cursorsBySlug(t, db, now), "every healthy cursor is exactly where it was")
	active := activeBySlug(t, db, now)
	assert.True(t, active[slugMorning])
	assert.True(t, active[slugNight])
	assert.True(t, active[slugMorningBackstop])
}

// TestReconcile_IsIdempotent: running the pass twice repairs once. The second
// run finds a healthy store and reports nothing, so a boot hook and an anxious
// operator can both run it repeatedly without consequence.
func TestReconcile_IsIdempotent(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	store := newStore(t)
	db := newJobDB(t)
	seedCompanion(t, db, store, seedAt)
	parkCompanion(t, db, slugNight, seedAt)

	first, err := Reconcile(ctxAt(now), db, ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, first.Reconciled, 1)

	second, err := Reconcile(ctxAt(now), db, ReconcileOptions{})
	require.NoError(t, err)
	assert.Empty(t, second.Reconciled, "the second pass has nothing left to repair")

	views, err := flywheel.ListPeriodics(ctxAt(now), db)
	require.NoError(t, err)
	assert.Len(t, views, 3, "repair never duplicates a definition")
}

// --- The opt-out and the narrowing flag --------------------------------------

// TestReconcile_NoFireSkipsTheMissedOccurrence covers the opt-out: the periodic
// is armed again, but its cursor is reset forward so the send that was missed is
// not delivered late — the next send is the next scheduled one. The schedule
// itself is preserved through the reset, which is the risk of doing it by
// delete-and-redeclare.
func TestReconcile_NoFireSkipsTheMissedOccurrence(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 6, 6, 5) // just past the morning mark it missed
	store := newStore(t)
	db := newJobDB(t)
	seedCompanion(t, db, store, seedAt)
	parkCompanion(t, db, slugMorning, at(2026, 7, 5, 12, 30))

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{NoFire: true})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.False(t, report.Reconciled[0].FiresMissed, "the missed occurrence is deliberately skipped")
	assert.Equal(t, at(2026, 7, 7, 6, 0), report.Reconciled[0].NextRun.UTC(), "it resumes at the next scheduled run")

	views, err := flywheel.ListPeriodics(ctxAt(now), db)
	require.NoError(t, err)
	require.Len(t, views, 3, "the reset re-declares one definition, never a duplicate")
	morning, ok := findPeriodic(t, db, slugMorning, now)
	require.True(t, ok)
	assert.True(t, morning.Active, "it is armed again")
	assert.Equal(t, "0 6 * * *", morning.Cron, "the cadence survives the cursor reset")
	assert.Equal(t, queueName, morning.Queue, "so does the queue")
	assert.True(t, morning.NextRunAt.After(now), "the stale bucket is behind it")
}

// TestReconcile_SlugNarrowsToOneCompanionPeriodic: --slug repairs one send and
// inspects only that one, so an operator acting on a single status line touches
// nothing else.
func TestReconcile_SlugNarrowsToOneCompanionPeriodic(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	store := newStore(t)
	db := newJobDB(t)
	seedCompanion(t, db, store, seedAt)
	parkCompanion(t, db, slugMorning, seedAt)
	parkCompanion(t, db, slugNight, seedAt)

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{Slug: slugMorning})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, slugMorning, report.Reconciled[0].Slug)
	assert.Equal(t, 1, report.Scanned, "only the named definition is inspected")

	active := activeBySlug(t, db, now)
	assert.True(t, active[slugMorning])
	assert.False(t, active[slugNight], "the definition that was not named is untouched")
}

// --- The guard's edge --------------------------------------------------------

// TestReconcile_EngineSlugInTheCompanionStoreIsUntouched: the pass repairs what
// its guard understands and nothing else. The two stores are separate by design,
// but the guarantee is the guard's rather than the store layout's — a foreign
// periodic sharing the file is inspected, reported in the scan count, and left
// exactly as it was.
func TestReconcile_EngineSlugInTheCompanionStoreIsUntouched(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	store := newStore(t)
	db := newJobDB(t)
	seedCompanion(t, db, store, seedAt)
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(seedAt), db, flywheel.PeriodicSpec{
		Slug: "lucid-tripwire", Kind: "lucid_tripwire", Cron: "0 6 * * *", Queue: queueName, Active: true,
	}))
	parkCompanion(t, db, "lucid-tripwire", seedAt)

	report, err := Reconcile(ctxAt(now), db, ReconcileOptions{})
	require.NoError(t, err)
	assert.Empty(t, report.Reconciled, "an Engine slug is not the companion's to repair")
	assert.Equal(t, 4, report.Scanned, "though it is still inspected")
	assert.False(t, activeBySlug(t, db, now)["lucid-tripwire"], "and left exactly as it was")
}

// --- The on-disk seam --------------------------------------------------------

// TestReconcileStore_ReArmsAParkedPeriodicOnDisk drives the seam the CLI uses:
// open the companion's store by path, repair it, close. The command layer never
// opens a database itself, so this is where a path-resolution or migration
// mistake would surface.
func TestReconcileStore_ReArmsAParkedPeriodicOnDisk(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	store := newStore(t)
	dbPath := filepath.Join(t.TempDir(), "companion.db")

	seed, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(seed))
	seedCompanion(t, seed, store, seedAt)
	parkCompanion(t, seed, slugNight, seedAt)
	closeGorm(seed)

	report, err := ReconcileStore(ctxAt(now), dbPath, ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, slugNight, report.Reconciled[0].Slug)

	read, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	defer closeGorm(read)
	assert.True(t, activeBySlug(t, read, now)[slugNight], "the repair reached the file on disk")
}

// TestReconcileStore_MissingStoreIsNamed: a store that does not exist is an
// error rather than an empty scan. The periodics are created by the node, so a
// missing file means the companion has never run here — a different problem than
// a parked send, and one a silent "scanned 0" would hide. The message names this
// package, so a two-store pass says which half failed.
func TestReconcileStore_MissingStoreIsNamed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.db")

	_, err := ReconcileStore(context.Background(), missing, ReconcileOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, flynode.ErrJobStoreMissing)
	assert.Contains(t, err.Error(), "companion:", "the message names the store that was missing")
	assert.Contains(t, err.Error(), missing)
}
