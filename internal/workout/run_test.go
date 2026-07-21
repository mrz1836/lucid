package workout

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/storage"
)

// --- test doubles -----------------------------------------------------------

// fakeComposer is a scripted MessageComposer: it returns a fixed Result (or an
// error) so the node's missed-fire, idempotency, and alert logic is exercised
// without a live provider. It counts calls so a test can assert an idempotent or
// past-cut-off path never composed.
type fakeComposer struct {
	res   Result
	err   error
	calls int
}

func (f *fakeComposer) Compose(_ context.Context, _ time.Time) (Result, error) {
	f.calls++
	if f.err != nil {
		return Result{}, f.err
	}
	return f.res, nil
}

// sendRecord is one delivered send or alert the fake deliverer captured.
type sendRecord struct{ channel, text string }

// fakeDeliverer captures every SendReturningID (real delivery), Send (alert),
// and VerifyPresent (read-back / idempotency probe), and lets a test drive the
// failure branches: sendErr fails delivery, verifyErr fails every read-back, and
// verifyErrFor fails one specific message id (the "receipt's message is gone"
// case). It needs no token or socket.
type fakeDeliverer struct {
	sends        []sendRecord
	alerts       []sendRecord
	verifies     []string
	sendErr      error
	verifyErr    error
	verifyErrFor map[string]error
	idSeq        int
}

func (f *fakeDeliverer) SendReturningID(channel, text string) (string, error) {
	f.sends = append(f.sends, sendRecord{channel, text})
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.idSeq++
	return fmt.Sprintf("msg-%d", f.idSeq), nil
}

func (f *fakeDeliverer) VerifyPresent(_, messageID string) error {
	f.verifies = append(f.verifies, messageID)
	if f.verifyErrFor != nil {
		if e, ok := f.verifyErrFor[messageID]; ok {
			return e
		}
	}
	return f.verifyErr
}

func (f *fakeDeliverer) Send(channel, text string) error {
	f.alerts = append(f.alerts, sendRecord{channel, text})
	return nil
}

// --- fixtures ---------------------------------------------------------------

// newStore builds a scaffolded Ledger over a temp dir with the engine tree in
// place, so the delivery receipts have somewhere to land.
func newStore(t *testing.T) *storage.Adapter {
	t.Helper()
	_, store := lucidtest.Ledger(t, lucidtest.NestedHome(), lucidtest.WithEngine())
	return store
}

// newRunner wires a Runner over a scripted composer + deliverer and a real
// scaffolded store, with the default midday slot, and returns the store so a
// test can read back the receipt.
func newRunner(t *testing.T, comp MessageComposer, del Deliverer) (*Runner, *storage.Adapter) {
	t.Helper()
	store := newStore(t)
	return &Runner{compose: comp, deliver: del, store: store, slot: "12:00"}, store
}

// at builds a UTC instant (the slot mark compares within one location, so UTC is
// fine). July 20 2026 is a Monday.
//
//nolint:unparam // y is kept explicit for fixture readability even though the cases share 2026
func at(y int, mo time.Month, d, hh, mm int) time.Time {
	return time.Date(y, mo, d, hh, mm, 0, 0, time.UTC)
}

// ctxAt returns a context carrying a fixed clock at t.
func ctxAt(t time.Time) context.Context {
	return models.WithClock(context.Background(), models.NewFixedClock(t))
}

// newJobDB opens and migrates a throwaway workout job DB.
func newJobDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "workout.db")), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))
	return db
}

// --- Fire: on-time delivery + receipt (AC-7, AC-9) --------------------------

// TestFire_OnTime_DeliversAndWritesReceipt: a fire at the slot mark delivers the
// composed message unmodified (no late note), reads it back, and persists a
// verified receipt keyed on the day + the workout window.
func TestFire_OnTime_DeliversAndWritesReceipt(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "TODAY: LEGS"}}
	del := &fakeDeliverer{}
	r, store := newRunner(t, comp, del)

	now := at(2026, 7, 20, 12, 0)
	out, err := r.Fire(context.Background(), now)
	require.NoError(t, err)

	assert.True(t, out.Delivered)
	assert.False(t, out.Late)
	require.Len(t, del.sends, 1)
	assert.Equal(t, engine.ChannelUser, del.sends[0].channel)
	assert.Equal(t, "TODAY: LEGS", del.sends[0].text, "an on-time fire carries no late note")
	assert.Empty(t, del.alerts, "a clean delivery raises no alert")
	assert.Equal(t, []string{out.MessageID}, del.verifies, "the delivered id is read back")

	rec, ok, rerr := store.ReadCompanionReceipt("workout")
	require.NoError(t, rerr)
	require.True(t, ok)
	assert.Equal(t, "2026-07-20", rec.Date)
	assert.Equal(t, "workout", rec.Window)
	assert.Equal(t, out.MessageID, rec.MessageID)
	assert.True(t, rec.Verified)
}

// TestFire_EarlyBeforeMark_DeliversOnTime: a manual fire before the slot mark
// (an operator previewing/forcing the send) delivers on-time with no late note.
func TestFire_EarlyBeforeMark_DeliversOnTime(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "MORNING PREVIEW"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), at(2026, 7, 20, 8, 0))
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.False(t, out.Late)
	assert.Equal(t, "MORNING PREVIEW", del.sends[0].text)
}

// TestFire_ConfiguredSlotHonored: the slot mark is the configured time, not a
// hard-coded noon — an evening slot's on-time / late boundary tracks it.
func TestFire_ConfiguredSlotHonored(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "EVENING"}}
	del := &fakeDeliverer{}
	store := newStore(t)
	r := &Runner{compose: comp, deliver: del, store: store, slot: "18:00"}

	// 18:00 is exactly the mark → on-time.
	out, err := r.Fire(context.Background(), at(2026, 7, 20, 18, 0))
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.False(t, out.Late, "an 18:00 slot at 18:00 is on-time, not stale")
}

// --- Fire: idempotency ------------------------------------------------------

// TestFire_RetrySameDay_IsIdempotentSkip: a second fire on the same day with a
// receipt whose message still reads back is an idempotent skip — no second send,
// no re-compose.
func TestFire_RetrySameDay_IsIdempotentSkip(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "TODAY: LEGS"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)
	now := at(2026, 7, 20, 12, 0)

	_, err := r.Fire(context.Background(), now)
	require.NoError(t, err)

	out2, err := r.Fire(context.Background(), now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.True(t, out2.Skipped)
	assert.Equal(t, skipAlreadyDelivered, out2.SkipReason)
	assert.Len(t, del.sends, 1, "the retry re-uses the receipt; no second send")
	assert.Equal(t, 1, comp.calls, "the retry does not re-compose")
}

// TestFire_ReceiptMessageGone_ReDelivers: a receipt whose message no longer reads
// back (deleted, or never really landed) falls through to a fresh delivery so the
// day is never left silently empty.
func TestFire_ReceiptMessageGone_ReDelivers(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "TODAY: PUSH"}}
	del := &fakeDeliverer{verifyErrFor: map[string]error{}}
	r, _ := newRunner(t, comp, del)
	now := at(2026, 7, 20, 12, 0)

	out1, err := r.Fire(context.Background(), now)
	require.NoError(t, err)

	// The first message is now gone: the idempotency read-back on the next fire
	// fails, so the day re-delivers rather than skipping into silence.
	del.verifyErrFor[out1.MessageID] = errors.New("404 unknown message")
	out2, err := r.Fire(context.Background(), now.Add(time.Minute))
	require.NoError(t, err)

	assert.True(t, out2.Delivered)
	assert.NotEqual(t, out1.MessageID, out2.MessageID)
	assert.Len(t, del.sends, 2, "a receipt whose message is gone re-delivers")
}

// --- Fire: bounded missed-fire catch-up -------------------------------------

// TestFire_Late_PrependsLateNote: a fire after the mark but within the window (a
// backfill on a host that overslept the slot) prefixes the honest late note.
func TestFire_Late_PrependsLateNote(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "TODAY: LEGS"}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), at(2026, 7, 20, 13, 0))
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.True(t, out.Late)
	require.Len(t, del.sends, 1)
	assert.Equal(t, lateNote+"\n\nTODAY: LEGS", del.sends[0].text)
	assert.True(t, strings.HasPrefix(del.sends[0].text, "(late — host was asleep)"))
}

// TestFire_PastCutoff_SkipsAndAlerts: past the delivery window (a 12:00 slot's
// 18:00 cut-off) the fire refuses to post a stale recommendation — it skips,
// alerts Z, and never composes.
func TestFire_PastCutoff_SkipsAndAlerts(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "TODAY: LEGS"}}
	del := &fakeDeliverer{}
	r, store := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), at(2026, 7, 20, 18, 30))
	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Equal(t, skipPastCutoff, out.SkipReason)
	assert.Empty(t, del.sends, "no stale message is posted past the cut-off")
	require.Len(t, del.alerts, 1, "past the cut-off Z is alerted instead of left silent")
	assert.Contains(t, del.alerts[0].text, "delivery window")
	assert.Equal(t, 0, comp.calls, "past the cut-off there is no compose")

	_, ok, _ := store.ReadCompanionReceipt("workout")
	assert.False(t, ok, "a skipped day writes no receipt")
}

// --- Fire: never silent -----------------------------------------------------

// TestFire_DeliveryError_AlertsAndErrors: a total send failure fires a loud alert
// and returns a loud error — and writes no receipt, so a retry re-sends.
func TestFire_DeliveryError_AlertsAndErrors(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "TODAY: LEGS"}}
	del := &fakeDeliverer{sendErr: errors.New("discord 503")}
	r, store := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), at(2026, 7, 20, 12, 0))
	require.Error(t, err)
	require.Len(t, del.alerts, 1, "a total send failure fires a loud alert")

	_, ok, _ := store.ReadCompanionReceipt("workout")
	assert.False(t, ok, "a failed delivery writes no receipt")
}

// TestFire_VerifyError_AlertsAndErrors: a send that will not read back is a loud
// failure — the alert fires and the error surfaces, and no receipt is written.
func TestFire_VerifyError_AlertsAndErrors(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "TODAY: LEGS"}}
	del := &fakeDeliverer{verifyErr: errors.New("404 after send")}
	r, store := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), at(2026, 7, 20, 12, 0))
	require.Error(t, err)
	require.Len(t, del.alerts, 1)

	_, ok, _ := store.ReadCompanionReceipt("workout")
	assert.False(t, ok, "an unverified send writes no receipt")
}

// TestFire_ComposeError_AlertsAndErrors: a loud compose failure (a missing
// program or prompt file, an unreadable live-number read) alerts and errors
// rather than sending an empty message.
func TestFire_ComposeError_AlertsAndErrors(t *testing.T) {
	comp := &fakeComposer{err: errors.New("program file missing")}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), at(2026, 7, 20, 12, 0))
	require.Error(t, err)
	require.Len(t, del.alerts, 1)
	assert.Empty(t, del.sends, "no message goes out on a compose failure")
}

// TestFire_FallbackFlagPropagates: a deterministic-fallback compose still
// delivers, and the Outcome records the fallback so the caller can log it (AC-9:
// the provider-down message still goes out, rendered deterministically).
func TestFire_FallbackFlagPropagates(t *testing.T) {
	comp := &fakeComposer{res: Result{Text: "DETERMINISTIC", Fallback: true}}
	del := &fakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), at(2026, 7, 20, 12, 0))
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.True(t, out.Fallback)
	assert.Equal(t, "DETERMINISTIC", del.sends[0].text)
}

// TestFire_MalformedSlot_Errors rejects a slot the config gate would never have
// admitted (a defensive guard on the runner's own mark).
func TestFire_MalformedSlot_Errors(t *testing.T) {
	store := newStore(t)
	r := &Runner{compose: &fakeComposer{}, deliver: &fakeDeliverer{}, store: store, slot: "24:61"}
	_, err := r.Fire(context.Background(), at(2026, 7, 20, 12, 0))
	require.Error(t, err)
}

// --- Node wiring: periodic from the configured slot (AC-7) ------------------

// TestUpsertPeriodic_RegistersDailyFromSlot: reconciling from a configured slot
// registers the daily periodic on that time's cron, active on the workout queue.
func TestUpsertPeriodic_RegistersDailyFromSlot(t *testing.T) {
	db := newJobDB(t)
	ctx := ctxAt(at(2026, 7, 19, 12, 0))

	require.NoError(t, upsertPeriodic(ctx, db, "09:30"))

	views, err := flywheel.ListPeriodics(ctx, db)
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, slugDaily, views[0].Slug)
	assert.Equal(t, "30 9 * * *", views[0].Cron, "the daily slot rides the configured 09:30 mark")
	assert.True(t, views[0].Active)
	assert.Equal(t, queueName, views[0].Queue)
}

// TestUpsertPeriodic_EmptySlotDefaultsMidday: an empty slot falls to the midday
// default — the feature is off by default, but a programmatic Run with an unset
// slot still schedules a sane noon fire.
func TestUpsertPeriodic_EmptySlotDefaultsMidday(t *testing.T) {
	db := newJobDB(t)
	ctx := ctxAt(at(2026, 7, 19, 12, 0))

	require.NoError(t, upsertPeriodic(ctx, db, ""))

	views, err := flywheel.ListPeriodics(ctx, db)
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, "0 12 * * *", views[0].Cron, "an empty slot defaults to midday")
}

// TestUpsertPeriodic_IsIdempotentAcrossRestart: re-reconciling the same slot
// neither duplicates the definition nor changes its cadence.
func TestUpsertPeriodic_IsIdempotentAcrossRestart(t *testing.T) {
	db := newJobDB(t)

	require.NoError(t, upsertPeriodic(ctxAt(at(2026, 7, 19, 12, 0)), db, "12:00"))
	require.NoError(t, upsertPeriodic(ctxAt(at(2026, 7, 20, 12, 0)), db, "12:00"))

	views, err := flywheel.ListPeriodics(ctxAt(at(2026, 7, 20, 12, 0)), db)
	require.NoError(t, err)
	assert.Len(t, views, 1, "no duplicate definition after a restart reconcile")
}

// TestUpsertPeriodic_RejectsMalformedSlot: an out-of-range slot is rejected at
// reconcile rather than scheduled.
func TestUpsertPeriodic_RejectsMalformedSlot(t *testing.T) {
	db := newJobDB(t)
	require.Error(t, upsertPeriodic(ctxAt(at(2026, 7, 19, 12, 0)), db, "24:61"))
}

// --- Node wiring: end-to-end fire through the flywheel machinery -------------

// rig is a workout flywheel runtime over an isolated Ledger and throwaway job DB:
// the scheduler (BackfillCap 1) and a deterministic runner share one registry,
// and the fake deliverer captures the sends.
type rig struct {
	db     *gorm.DB
	store  *storage.Adapter
	del    *fakeDeliverer
	comp   *fakeComposer
	sched  *flywheel.Scheduler
	runner *flywheel.Runner
}

func newRig(t *testing.T, clock models.Clock) *rig {
	t.Helper()
	store := newStore(t)
	db := newJobDB(t)
	del := &fakeDeliverer{}
	comp := &fakeComposer{res: Result{Text: "COMPOSED"}}
	r := &Runner{compose: comp, deliver: del, store: store, slot: "12:00"}
	reg := buildRegistry(r, clock)

	sched := flywheel.NewSchedulerWithConfig(flywheel.SchedulerConfig{
		DB: db, Client: flywheel.NewClient(db), BackfillCap: backfillCap,
	})
	runner, err := flywheel.NewRunner(flywheel.RunnerConfig{
		DB: db, Driver: flywheel.NewSQLiteDriver(db), Registry: reg,
		Queues: []string{queueName}, ClaimAnyClass: true, Concurrency: 1,
	})
	require.NoError(t, err)
	return &rig{db: db, store: store, del: del, comp: comp, sched: sched, runner: runner}
}

// TestRun_DailyPeriodicFiresAndDelivers drives a due daily periodic all the way
// through the machinery: the scheduler enqueues it and the runner fires the
// worker, which composes and delivers to the user channel.
func TestRun_DailyPeriodicFiresAndDelivers(t *testing.T) {
	workerNow := at(2026, 7, 20, 12, 5)
	r := newRig(t, models.NewFixedClock(workerNow))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(at(2026, 7, 19, 12, 0)), r.db, flywheel.PeriodicSpec{
		Slug: slugDaily, Kind: kindDaily, Cron: "0 12 * * *", Queue: queueName, Active: true,
	}))

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the due daily periodic enqueues once")

	require.NoError(t, r.runner.RunUntilIdle(tickCtx))

	require.Len(t, r.del.sends, 1, "the workout slot delivers")
	assert.Equal(t, engine.ChannelUser, r.del.sends[0].channel)
	assert.Equal(t, "COMPOSED", r.del.sends[0].text)
}

// --- Run: production wiring --------------------------------------------------

// TestRun_ReconcilesThenDrainsCleanly exercises the production Run wiring end to
// end: DB open/migrate, engine scaffold, periodic reconcile, node build/run. It
// waits until the daily periodic is reconciled (setup ran), then cancels and
// asserts the node drains to a clean nil return.
func TestRun_ReconcilesThenDrainsCleanly(t *testing.T) {
	store := newStore(t)
	dbPath := filepath.Join(t.TempDir(), "workout.db")
	cfg := writeWorkoutConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Store:    store,
			Notifier: &fakeDeliverer{},
			Metrics:  fakeMetrics{},
			Config:   cfg,
			DBPath:   dbPath,
		})
	}()

	require.Eventually(t, func() bool {
		db, oerr := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
		if oerr != nil {
			return false
		}
		defer closeGorm(db)
		views, lerr := flywheel.ListPeriodics(context.Background(), db)
		return lerr == nil && len(views) == 1
	}, 5*time.Second, 25*time.Millisecond, "Run reconciles the daily periodic")

	cancel()
	select {
	case rerr := <-done:
		require.NoError(t, rerr, "a canceled node drains cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// closeGorm closes a gorm handle's underlying pool so the poll loop leaks no FDs.
func closeGorm(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// TestRun_RejectsMissingCollaborators guards the required-field errors.
func TestRun_RejectsMissingCollaborators(t *testing.T) {
	require.ErrorIs(t, Run(context.Background(), Options{
		Notifier: &fakeDeliverer{}, Metrics: fakeMetrics{},
	}), errNoStore)
	require.ErrorIs(t, Run(context.Background(), Options{
		Store: newStore(t), Metrics: fakeMetrics{},
	}), errNoNotifier)
	require.ErrorIs(t, Run(context.Background(), Options{
		Store: newStore(t), Notifier: &fakeDeliverer{},
	}), errNoMetrics)
}

// --- helpers -----------------------------------------------------------------

// TestCronFromHM tables the slot-mark to cron translation and its rejections.
func TestCronFromHM(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "12:00", want: "0 12 * * *"},
		{in: "06:00", want: "0 6 * * *"},
		{in: "09:30", want: "30 9 * * *"},
		{in: "00:00", want: "0 0 * * *"},
		{in: "23:59", want: "59 23 * * *"},
		{in: "", wantErr: true},
		{in: "1200", wantErr: true},
		{in: "12:00:00", wantErr: true},
		{in: "24:00", wantErr: true},
		{in: "12:60", wantErr: true},
		{in: "aa:bb", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := cronFromHM(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSlotOrDefault covers the empty-slot midday fallback.
func TestSlotOrDefault(t *testing.T) {
	assert.Equal(t, "12:00", slotOrDefault(""))
	assert.Equal(t, "12:00", slotOrDefault("   "))
	assert.Equal(t, "07:45", slotOrDefault("07:45"))
}

// TestAtClock builds the mark instant on now's date and rejects a malformed mark.
func TestAtClock(t *testing.T) {
	now := at(2026, 7, 20, 15, 42)
	got, err := atClock(now, "12:00")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), got)

	_, err = atClock(now, "nope")
	require.Error(t, err)
}

// TestDefaultDBPath covers the explicit > env > default precedence and that the
// default lands outside the ~/.lucid Ledger, distinct from the teeth's and the
// companion's job DBs.
func TestDefaultDBPath(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "custom.db")
	got, err := DefaultDBPath(explicit)
	require.NoError(t, err)
	assert.Equal(t, explicit, got, "an explicit path wins")

	envPath := filepath.Join(t.TempDir(), "env.db")
	t.Setenv(envWorkoutDB, envPath)
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.Equal(t, envPath, got, "the env override is next")

	t.Setenv(envWorkoutDB, "")
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, filepath.Join("lucid", "workout.db")), "the default lands under the config dir, got %q", got)
	assert.NotContains(t, got, filepath.Join(".lucid"), "the job DB is outside the Ledger")
}
