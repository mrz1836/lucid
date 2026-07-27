package schedrun

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

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// seedTeeth reconciles the production periodic set into a rig's store at
// seedClock — the exact state a daemon boot leaves behind, so a reconcile test
// starts from real rows rather than hand-built ones.
func seedTeeth(t *testing.T, r *rig, seedClock time.Time, suppressBell bool) {
	t.Helper()
	require.NoError(t, upsertPeriodics(ctxAt(seedClock), r.db, r.store, suppressBell))
}

// park switches a periodic off, reproducing the state this whole pass exists to
// repair: the definition survives, but nothing will ever fire it again.
func park(t *testing.T, r *rig, slug string, parkClock time.Time) {
	t.Helper()
	require.NoError(t, flywheel.SetPeriodicActive(ctxAt(parkClock), r.db, slug, false))
}

// activeBySlug reads every periodic's active flag back out of the store.
func activeBySlug(t *testing.T, r *rig, readClock time.Time) map[string]bool {
	t.Helper()
	views, err := flywheel.ListPeriodics(ctxAt(readClock), r.db)
	require.NoError(t, err)
	active := map[string]bool{}
	for _, v := range views {
		active[v.Slug] = v.Active
	}
	return active
}

// ── The intended-active guard ────────────────────────────────────────────────

// TestIntendedActive tables the guard the whole pass turns on: whether a send
// *should* be running is derived from configuration per slug, never guessed from
// the job store. It is what keeps repair from fighting a deliberate setting, so
// each row here is a promise about what reconcile will and will not touch.
func TestIntendedActive(t *testing.T) {
	cases := []struct {
		name        string
		slug        string
		companion   bool
		want, known bool
	}{
		{"the morning dead-man is unconditional", slugTripwire, false, true, true},
		{"the dead-man stays unconditional behind a companion", slugTripwire, true, true, true},
		{"the bell owns the evening when nothing else does", slugBell, false, true, true},
		{"a companion-owned evening intends the bell inactive", slugBell, true, false, true},
		{"the backstop arms only behind a companion", slugBellFallback, true, true, true},
		{"the backstop stands down when the real bell fires", slugBellFallback, false, false, true},
		{"an unrecognized slug is not ours to repair", "lucid-something-else", true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, known := intendedActive(tc.slug, tc.companion)
			assert.Equal(t, tc.want, want, "intended-active")
			assert.Equal(t, tc.known, known, "known slug")
		})
	}
}

// TestParked separates the state repair exists for from the states that only
// look like it. A live scheduler advances a fired periodic's cursor past now in
// one tick, so an active periodic sitting a few hours behind is *due*, not
// parked — mistaking the two would report a healthy daemon as broken every
// evening between the mark and the next poll.
func TestParked(t *testing.T) {
	now := at(2026, 7, 27, 12, 0)
	cases := []struct {
		name string
		view flywheel.PeriodicView
		want bool
	}{
		{"inactive with a future cursor is parked", flywheel.PeriodicView{Active: false, NextRunAt: now.Add(6 * time.Hour)}, true},
		{"inactive with a frozen cursor is parked", flywheel.PeriodicView{Active: false, NextRunAt: now.Add(-72 * time.Hour)}, true},
		{"active and due is not parked", flywheel.PeriodicView{Active: true, NextRunAt: now.Add(-30 * time.Minute)}, false},
		{"active and ahead is not parked", flywheel.PeriodicView{Active: true, NextRunAt: now.Add(6 * time.Hour)}, false},
		{"active with a whole occurrence missed is stuck", flywheel.PeriodicView{Active: true, NextRunAt: now.Add(-25 * time.Hour)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parked(tc.view, now))
		})
	}
}

// ── Re-arming a parked send ──────────────────────────────────────────────────

// TestReconcile_ReArmsParkedTripwireAndFiresTheMissedSend is the headline
// promise, driven end to end: a parked morning dead-man is switched back on, and
// because the re-arm leaves the stale cursor in place the occurrence it missed
// still goes out on the next tick — late, but delivered. Without the pass the
// definition would sit inactive forever and the send would simply never come
// again.
func TestReconcile_ReArmsParkedTripwireAndFiresTheMissedSend(t *testing.T) {
	workerNow := at(2026, 7, 6, 9, 5)
	r := newRig(t, models.NewFixedClock(workerNow))
	seedDay(t, r.store, completedRec("2026-07-04")) // 07-05 left absent -> a real miss
	upsertTripwire(t, r, at(2026, 7, 5, 12, 0))     // next fire 07-06 09:00
	park(t, r, slugTripwire, at(2026, 7, 5, 12, 30))

	report, err := Reconcile(ctxAt(workerNow), r.db, ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1, "the parked dead-man is the one thing repaired")
	assert.Equal(t, 1, report.Scanned)
	assert.Equal(t, slugTripwire, report.Reconciled[0].Slug)
	assert.False(t, report.Reconciled[0].WasActive, "it was found switched off")
	assert.True(t, report.Reconciled[0].FiresMissed, "the occurrence it missed is still coming")
	assert.Equal(t, at(2026, 7, 6, 9, 0), report.Reconciled[0].NextRun.UTC(), "the stale cursor is left in place")

	views, err := flywheel.ListPeriodics(ctxAt(workerNow), r.db)
	require.NoError(t, err)
	tripwire, ok := findPeriodic(views, slugTripwire)
	require.True(t, ok)
	assert.True(t, tripwire.Active, "the definition is armed again")
	assert.Equal(t, "0 9 * * *", tripwire.Cron, "repair never edits the schedule")

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the re-armed definition is due and enqueues the missed bucket")
	require.NoError(t, r.runner.RunUntilIdle(tickCtx))
	assert.Equal(t, 1, r.notif.count(engine.ChannelUser), "the send the outage swallowed is delivered")
}

// TestReconcile_HealthyStoreChangesNothing: the pass is safe to run any time.
// On a freshly reconciled store it inspects every definition and repairs none —
// an empty result over a non-zero scan is the healthy answer.
func TestReconcile_HealthyStoreChangesNothing(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	r := newRig(t, models.NewFixedClock(seedAt))
	seedTeeth(t, r, seedAt, false)

	report, err := Reconcile(ctxAt(seedAt), r.db, ReconcileOptions{})
	require.NoError(t, err)
	assert.Empty(t, report.Reconciled, "nothing was parked")
	assert.Equal(t, 3, report.Scanned, "every definition was still inspected")

	active := activeBySlug(t, r, seedAt)
	assert.True(t, active[slugTripwire])
	assert.True(t, active[slugBell])
	assert.False(t, active[slugBellFallback], "the backstop stays stood down behind a firing bell")
}

// TestReconcile_IsIdempotent: running the pass twice repairs once. The second
// run finds a healthy store and reports nothing, so a cron or an anxious
// operator can run it repeatedly without consequence.
func TestReconcile_IsIdempotent(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	r := newRig(t, models.NewFixedClock(now))
	seedTeeth(t, r, seedAt, false)
	park(t, r, slugTripwire, seedAt)

	first, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, first.Reconciled, 1)

	second, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{})
	require.NoError(t, err)
	assert.Empty(t, second.Reconciled, "the second pass has nothing left to repair")

	views, err := flywheel.ListPeriodics(ctxAt(now), r.db)
	require.NoError(t, err)
	assert.Len(t, views, 3, "repair never duplicates a definition")
}

// TestReconcile_NoFireSkipsTheMissedOccurrence covers the opt-out: the periodic
// is armed again, but its cursor is reset forward so the send that was missed is
// not delivered late — the next send is the next scheduled one. The schedule
// itself is preserved through the reset, which is the risk of doing it by
// delete-and-redeclare.
func TestReconcile_NoFireSkipsTheMissedOccurrence(t *testing.T) {
	now := at(2026, 7, 6, 9, 5)
	r := newRig(t, models.NewFixedClock(now))
	upsertTripwire(t, r, at(2026, 7, 5, 12, 0)) // next fire 07-06 09:00
	park(t, r, slugTripwire, at(2026, 7, 5, 12, 30))

	report, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{NoFire: true})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.False(t, report.Reconciled[0].FiresMissed, "the missed occurrence is deliberately skipped")
	assert.Equal(t, at(2026, 7, 7, 9, 0), report.Reconciled[0].NextRun.UTC(), "it resumes at the next scheduled run")

	views, err := flywheel.ListPeriodics(ctxAt(now), r.db)
	require.NoError(t, err)
	require.Len(t, views, 1, "the reset re-declares one definition, never a duplicate")
	tripwire, ok := findPeriodic(views, slugTripwire)
	require.True(t, ok)
	assert.True(t, tripwire.Active, "it is armed again")
	assert.Equal(t, "0 9 * * *", tripwire.Cron, "the cadence survives the cursor reset")
	assert.Equal(t, queueName, tripwire.Queue, "so does the queue")
	assert.True(t, tripwire.NextRunAt.After(now), "the stale bucket is behind it")

	n, err := r.sched.Tick(ctxAt(at(2026, 7, 6, 9, 10)))
	require.NoError(t, err)
	assert.Equal(t, 0, n, "nothing is backfilled after a skip")
}

// TestReconcile_StuckCursorOnAnActivePeriodic covers the second parked shape: the
// definition is on, but a whole occurrence elapsed with no tick advancing it.
// With --no-fire that is a real repair — the cursor is pulled back onto the
// schedule instead of being left a day behind.
func TestReconcile_StuckCursorOnAnActivePeriodic(t *testing.T) {
	now := at(2026, 7, 8, 12, 0)
	r := newRig(t, models.NewFixedClock(now))
	upsertTripwire(t, r, at(2026, 7, 5, 12, 0)) // next fire 07-06 09:00, three days stale

	report, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{NoFire: true})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1, "a cursor a full day behind is parked, not merely due")
	assert.True(t, report.Reconciled[0].WasActive, "the report names what was actually wrong")
	assert.Equal(t, at(2026, 7, 9, 9, 0), report.Reconciled[0].NextRun.UTC())
}

// ── The guard in practice ────────────────────────────────────────────────────

// TestReconcile_LeavesTheCompanionSuppressedBellAlone is the guard proof, and the
// one behavior that makes an automatic repair pass safe to run at all: when the
// companion owns the evening the bell is *intended* inactive, so the pass leaves
// it suppressed rather than "repairing" the evening into two sends.
func TestReconcile_LeavesTheCompanionSuppressedBellAlone(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	r := newRig(t, models.NewFixedClock(now))
	seedTeeth(t, r, seedAt, true)

	report, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{CompanionEnabled: true})
	require.NoError(t, err)
	assert.Empty(t, report.Reconciled, "an intended-inactive bell is a healthy state, not a parked one")

	active := activeBySlug(t, r, now)
	assert.False(t, active[slugBell], "the bell stays suppressed")
	assert.True(t, active[slugBellFallback], "the backstop stays armed")
	assert.True(t, active[slugTripwire])
}

// TestReconcile_ReArmsTheParkedBackstop: behind a companion the backstop is the
// intended-active evening definition, so it is the one repaired — and repairing
// it must not drag the suppressed bell back on with it.
func TestReconcile_ReArmsTheParkedBackstop(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	r := newRig(t, models.NewFixedClock(now))
	seedTeeth(t, r, seedAt, true)
	park(t, r, slugBellFallback, seedAt)

	report, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{CompanionEnabled: true})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, slugBellFallback, report.Reconciled[0].Slug)

	active := activeBySlug(t, r, now)
	assert.True(t, active[slugBellFallback], "the evening backstop is armed again")
	assert.False(t, active[slugBell], "the suppressed bell is still suppressed")
}

// TestReconcile_SlugNarrowsToOnePeriodic: --slug repairs one send and inspects
// only that one, so an operator acting on a status line touches nothing else.
func TestReconcile_SlugNarrowsToOnePeriodic(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	r := newRig(t, models.NewFixedClock(now))
	seedTeeth(t, r, seedAt, false)
	park(t, r, slugTripwire, seedAt)
	park(t, r, slugBell, seedAt)

	report, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{Slug: slugTripwire})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, slugTripwire, report.Reconciled[0].Slug)
	assert.Equal(t, 1, report.Scanned, "only the named definition is inspected")

	active := activeBySlug(t, r, now)
	assert.True(t, active[slugTripwire])
	assert.False(t, active[slugBell], "the definition that was not named is untouched")
}

// TestReconcile_UnknownSlugIsANoOp: the pass repairs what it understands and
// nothing else. A foreign periodic sharing the store is never re-armed, and
// naming one explicitly is a quiet no-op rather than a guess.
func TestReconcile_UnknownSlugIsANoOp(t *testing.T) {
	now := at(2026, 7, 5, 13, 0)
	r := newRig(t, models.NewFixedClock(now))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(at(2026, 7, 5, 12, 0)), r.db, flywheel.PeriodicSpec{
		Slug: "some-other-tool", Kind: "some_other_kind", Cron: "0 3 * * *", Queue: queueName, Active: true,
	}))
	park(t, r, "some-other-tool", at(2026, 7, 5, 12, 30))

	report, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{})
	require.NoError(t, err)
	assert.Empty(t, report.Reconciled, "a slug outside the guard is never touched")

	named, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{Slug: "some-other-tool"})
	require.NoError(t, err)
	assert.Empty(t, named.Reconciled, "naming it explicitly does not override the guard")
	assert.Equal(t, 1, named.Scanned)

	active := activeBySlug(t, r, now)
	assert.False(t, active["some-other-tool"], "it is left exactly as it was found")
}

// TestReconcile_SurfacesJobStoreError: an unusable store fails loudly. A repair
// lever that silently reported "nothing parked" against a broken store would be
// worse than no lever at all.
func TestReconcile_SurfacesJobStoreError(t *testing.T) {
	now := at(2026, 7, 5, 13, 0)
	r := newRig(t, models.NewFixedClock(now))
	closeGorm(r.db)

	_, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list periodics")
}

// openReadOnlyStore opens an existing job store read-only, so a write attempted
// against it fails — the stand-in for a store the process cannot repair.
func openReadOnlyStore(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+path+"?mode=ro"), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	t.Cleanup(func() { closeGorm(db) })
	return db
}

// seedParkedStoreOnDisk writes a job store holding one parked tripwire and
// returns its path.
func seedParkedStoreOnDisk(t *testing.T, seedClock time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flywheel.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(seedClock), db, flywheel.PeriodicSpec{
		Slug: slugTripwire, Kind: kindTripwire, Cron: "0 6 * * *", Queue: queueName, Active: true,
	}))
	require.NoError(t, flywheel.SetPeriodicActive(ctxAt(seedClock), db, slugTripwire, false))
	closeGorm(db)
	return path
}

// TestReconcile_SurfacesAFailedReArm: a repair that could not be written must
// fail loudly. Reporting a re-arm that never reached the store would be the
// worst possible outcome for this command — an operator would walk away
// believing tomorrow's send is armed when it is still parked.
func TestReconcile_SurfacesAFailedReArm(t *testing.T) {
	seedClock := at(2026, 7, 5, 12, 0)
	db := openReadOnlyStore(t, seedParkedStoreOnDisk(t, seedClock))

	_, err := Reconcile(ctxAt(at(2026, 7, 6, 7, 0)), db, ReconcileOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "re-arm periodic")
}

// TestReconcile_SurfacesAFailedCursorReset is the same promise on the --no-fire
// path, where the repair is a delete-and-redeclare rather than a flag flip.
func TestReconcile_SurfacesAFailedCursorReset(t *testing.T) {
	seedClock := at(2026, 7, 5, 12, 0)
	db := openReadOnlyStore(t, seedParkedStoreOnDisk(t, seedClock))

	_, err := Reconcile(ctxAt(at(2026, 7, 6, 7, 0)), db, ReconcileOptions{NoFire: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reset periodic")
}

// ── ReconcileStore: the on-disk seam ─────────────────────────────────────────

// TestReconcileStore_ReArmsThroughAPath drives the seam the command uses: given
// only a path, it opens the durable store, repairs it, and closes — the command
// layer never touches a database itself.
func TestReconcileStore_ReArmsThroughAPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flywheel.db")
	seedAt := at(2026, 7, 5, 12, 0)
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(seedAt), db, flywheel.PeriodicSpec{
		Slug: slugTripwire, Kind: kindTripwire, Cron: "0 6 * * *", Queue: queueName, Active: true,
	}))
	require.NoError(t, flywheel.SetPeriodicActive(ctxAt(seedAt), db, slugTripwire, false))
	closeGorm(db)

	now := at(2026, 7, 6, 7, 0)
	report, err := ReconcileStore(ctxAt(now), path, ReconcileOptions{})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, slugTripwire, report.Reconciled[0].Slug)

	reopened, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	defer closeGorm(reopened)
	views, err := flywheel.ListPeriodics(ctxAt(now), reopened)
	require.NoError(t, err)
	tripwire, ok := findPeriodic(views, slugTripwire)
	require.True(t, ok)
	assert.True(t, tripwire.Active, "the repair reached the store on disk")
}

// TestReconcileStore_MissingStoreIsNamed: a store that does not exist means the
// daemon has never run here — a different problem than a parked send, and one a
// silent "scanned 0 periodics" would hide behind a clean bill of health.
func TestReconcileStore_MissingStoreIsNamed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "flywheel.db")

	_, err := ReconcileStore(context.Background(), path, ReconcileOptions{})
	require.ErrorIs(t, err, errJobStoreMissing)
	assert.Contains(t, err.Error(), path, "the error names the path it looked at")
}

// TestReconcileStore_UnusableStoreIsSurfaced: a file that is not a job store
// fails on the schema check rather than being silently reported as empty.
func TestReconcileStore_UnusableStoreIsSurfaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flywheel.db")
	require.NoError(t, os.WriteFile(path, []byte("this is not a database"), 0o600))

	_, err := ReconcileStore(context.Background(), path, ReconcileOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "job db")
}

// ── Startup self-heal ────────────────────────────────────────────────────────

// TestRun_StartupSelfHealArmsTheIntendedSends is the boot-time guarantee, driven
// through the production Run wiring: a daemon started against a store whose
// every definition was switched off comes up with the intended sends armed and
// the intended-inactive one still suppressed. Seeding and the reconcile pass
// both defend this invariant — the test asserts the invariant rather than which
// of them repaired it, because the point of running both is that neither is
// load-bearing alone.
func TestRun_StartupSelfHealArmsTheIntendedSends(t *testing.T) {
	store := storage.New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := store.Scaffold()
	require.NoError(t, err)

	// A fully parked store, seeded with the production marks so the boot
	// reconcile preserves each cursor rather than re-declaring the schedule.
	dbPath := filepath.Join(t.TempDir(), "flywheel.db")
	seedClock := at(2026, 7, 5, 12, 0)
	seed, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(seed))
	for _, spec := range []flywheel.PeriodicSpec{
		{Slug: slugTripwire, Kind: kindTripwire, Cron: "0 6 * * *", Queue: queueName, Active: true},
		{Slug: slugBell, Kind: kindBell, Cron: "0 19 * * *", Queue: queueName, Active: true},
		{Slug: slugBellFallback, Kind: kindBellFallback, Cron: "0 23 * * *", Queue: queueName, Active: true},
	} {
		require.NoError(t, flywheel.UpsertPeriodic(ctxAt(seedClock), seed, spec))
		require.NoError(t, flywheel.SetPeriodicActive(ctxAt(seedClock), seed, spec.Slug, false))
	}
	closeGorm(seed)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Store: store, Notifier: &fakeNotifier{}, DBPath: dbPath, SuppressUserChannel: true})
	}()

	require.Eventually(t, func() bool {
		db, oerr := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
		if oerr != nil {
			return false
		}
		defer closeGorm(db)
		views, lerr := flywheel.ListPeriodics(context.Background(), db)
		if lerr != nil || len(views) != 3 {
			return false
		}
		active := map[string]bool{}
		for _, v := range views {
			active[v.Slug] = v.Active
		}
		return active[slugTripwire] && active[slugBellFallback] && !active[slugBell]
	}, 5*time.Second, 25*time.Millisecond,
		"a parked store boots back to armed sends, with the companion-suppressed bell left suppressed")

	cancel()
	select {
	case rerr := <-done:
		require.NoError(t, rerr, "a canceled node drains cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// TestRun_StopDuringStartupIsACleanDrain: a stop signal that lands while the
// daemon is still seeding and repairing its schedule is an ordinary shutdown —
// the supervisor stops and restarts this daemon constantly, and a canceled boot
// reported as a failure would put a spurious error in the supervised log every
// time. A real startup failure still surfaces (TestRun_PropagatesReconcileError).
func TestRun_StopDuringStartupIsACleanDrain(t *testing.T) {
	store := storage.New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := store.Scaffold()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stopped before the boot sequence can finish

	require.NoError(t, Run(ctx, Options{
		Store:    store,
		Notifier: &fakeNotifier{},
		DBPath:   filepath.Join(t.TempDir(), "flywheel.db"),
	}))
}

// TestRun_StartupSequenceReconcilesAfterSeeding pins the boot order at the unit
// level: seed the schedule, then repair it. Reconciling before the seed would
// inspect definitions that do not exist yet on a first boot; reconciling after
// means the pass runs against exactly the schedule the daemon is about to serve.
func TestRun_StartupSequenceReconcilesAfterSeeding(t *testing.T) {
	seedAt := at(2026, 7, 5, 12, 0)
	now := at(2026, 7, 5, 13, 0)
	r := newRig(t, models.NewFixedClock(now))

	seedTeeth(t, r, seedAt, true)
	// Drift the seed cannot have caused, but a boot must still not leave behind.
	park(t, r, slugBellFallback, seedAt)

	report, err := Reconcile(ctxAt(now), r.db, ReconcileOptions{CompanionEnabled: true})
	require.NoError(t, err)
	require.Len(t, report.Reconciled, 1)
	assert.Equal(t, slugBellFallback, report.Reconciled[0].Slug)

	active := activeBySlug(t, r, now)
	assert.True(t, active[slugBellFallback])
	assert.False(t, active[slugBell], "boot never re-arms a suppressed bell")
}
