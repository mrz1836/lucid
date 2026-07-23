package witnessreport

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

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/storage"
)

// --- test doubles -----------------------------------------------------------

// runFakeComposer is a scripted ReportComposer: it returns a fixed Report (or an
// error) so the node's missed-fire, idempotency, and alert logic is exercised
// without a live provider. It counts calls so a test can assert an idempotent or
// past-cut-off path never composed.
type runFakeComposer struct {
	report Report
	err    error
	calls  int
}

func (f *runFakeComposer) Compose(_ context.Context, _ time.Time) (Report, error) {
	f.calls++
	if f.err != nil {
		return Report{}, f.err
	}
	return f.report, nil
}

// alertRecord is one loud alert the fake deliverer captured.
type alertRecord struct{ channel, text string }

// embedRecord is one delivered rich embed the fake deliverer captured.
type embedRecord struct {
	channel string
	embed   notify.Embed
}

// runFakeDeliverer captures every SendEmbedReturningID (real delivery), Send
// (alert), and VerifyPresent (read-back / idempotency probe), and lets a test
// drive the failure branches: sendErr fails delivery, verifyErr fails every
// read-back, and verifyErrFor fails one specific message id (the "receipt's
// message is gone" case). It needs no token or socket.
type runFakeDeliverer struct {
	sends        []embedRecord
	alerts       []alertRecord
	verifies     []string
	sendErr      error
	verifyErr    error
	verifyErrFor map[string]error
	idSeq        int
}

func (f *runFakeDeliverer) SendEmbedReturningID(channel string, e notify.Embed) (string, error) {
	f.sends = append(f.sends, embedRecord{channel, e})
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.idSeq++
	return fmt.Sprintf("msg-%d", f.idSeq), nil
}

func (f *runFakeDeliverer) VerifyPresent(_, messageID string) error {
	f.verifies = append(f.verifies, messageID)
	if f.verifyErrFor != nil {
		if e, ok := f.verifyErrFor[messageID]; ok {
			return e
		}
	}
	return f.verifyErr
}

func (f *runFakeDeliverer) Send(channel, text string) error {
	f.alerts = append(f.alerts, alertRecord{channel, text})
	return nil
}

// --- fixtures ---------------------------------------------------------------

// runStore builds a scaffolded Ledger with the engine tree so the weekly
// receipt reads/writes land under engine/witness/.
func runStore(t *testing.T) *storage.Adapter {
	t.Helper()
	_, store := lucidtest.Ledger(t, lucidtest.NestedHome(), lucidtest.WithEngine())
	return store
}

// newRunner wires a Runner over a scripted composer + deliverer and a real
// scaffolded store in preview mode on the default Monday-09:00 mark, and returns
// the store so a test can read back the receipt.
func newRunner(t *testing.T, comp ReportComposer, del Deliverer) (*Runner, *storage.Adapter) {
	t.Helper()
	store := runStore(t)
	return &Runner{
		compose: comp, deliver: del, store: store,
		mode: config.WitnessReportModePreview, weekday: 1, markHM: "09:00",
	}, store
}

// sampleReport is a minimal non-fallback Report the fakes deliver.
func sampleReport() Report { return Report{ISOWeek: "2026-W29", Streak: 5} }

// ctxAt returns a context carrying a fixed clock at t.
func ctxAt(t time.Time) context.Context {
	return models.WithClock(context.Background(), models.NewFixedClock(t))
}

// newJobDB opens and migrates a throwaway weekly-report job DB.
func newJobDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "witness-report.db")), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))
	return db
}

// --- Fire: on-time delivery + receipt (AC-1) --------------------------------

// TestFire_OnTime_DeliversAndWritesReceipt: a fire at the Monday mark delivers
// the composed embed to the preview (user) channel, reads it back, and persists a
// verified receipt keyed on the ISO week.
func TestFire_OnTime_DeliversAndWritesReceipt(t *testing.T) {
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{}
	r, store := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), reportNow())
	require.NoError(t, err)

	assert.True(t, out.Delivered)
	assert.Equal(t, "2026-W29", out.Week)
	require.Len(t, del.sends, 1)
	assert.Equal(t, engine.ChannelUser, del.sends[0].channel, "preview mode posts to the user channel")
	assert.Empty(t, del.alerts, "a clean delivery raises no alert")
	assert.Equal(t, []string{out.MessageID}, del.verifies, "the delivered id is read back")

	rec, ok, rerr := store.ReadWitnessReportReceipt()
	require.NoError(t, rerr)
	require.True(t, ok)
	assert.Equal(t, "2026-W29", rec.Week)
	assert.Equal(t, out.MessageID, rec.MessageID)
	assert.Equal(t, engine.ChannelUser, rec.ChannelID)
	assert.True(t, rec.Verified)
}

// TestFire_AutoModePostsToWitness: in auto mode the report posts to the
// friend-facing witness channel — flipping preview → auto is a mode change only.
func TestFire_AutoModePostsToWitness(t *testing.T) {
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{}
	r, _ := newRunner(t, comp, del)
	r.mode = config.WitnessReportModeAuto

	out, err := r.Fire(context.Background(), reportNow())
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	require.Len(t, del.sends, 1)
	assert.Equal(t, engine.ChannelWitness, del.sends[0].channel)
	assert.Equal(t, engine.ChannelWitness, out.Channel)
}

// --- Fire: idempotency (AC-1) -----------------------------------------------

// TestFire_RetrySameWeek_IsIdempotentSkip: a second fire in the same ISO week
// with a receipt whose message still reads back is an idempotent skip — no second
// send, no re-compose.
func TestFire_RetrySameWeek_IsIdempotentSkip(t *testing.T) {
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), reportNow())
	require.NoError(t, err)

	out2, err := r.Fire(context.Background(), reportNow().Add(2*time.Hour))
	require.NoError(t, err)
	assert.True(t, out2.Skipped)
	assert.Equal(t, skipAlreadyDelivered, out2.SkipReason)
	assert.Len(t, del.sends, 1, "the retry re-uses the receipt; no second send")
	assert.Equal(t, 1, comp.calls, "the retry does not re-compose")
}

// TestFire_ReceiptMessageGone_ReDelivers: a receipt whose message no longer reads
// back (deleted, or never really landed) falls through to a fresh delivery so the
// week is never left silently empty.
func TestFire_ReceiptMessageGone_ReDelivers(t *testing.T) {
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{verifyErrFor: map[string]error{}}
	r, _ := newRunner(t, comp, del)

	out1, err := r.Fire(context.Background(), reportNow())
	require.NoError(t, err)

	// The first message is now gone: the idempotency read-back on the next fire
	// fails, so the week re-delivers rather than skipping into silence.
	del.verifyErrFor[out1.MessageID] = errors.New("404 unknown message")
	out2, err := r.Fire(context.Background(), reportNow().Add(3*time.Hour))
	require.NoError(t, err)

	assert.True(t, out2.Delivered)
	assert.NotEqual(t, out1.MessageID, out2.MessageID)
	assert.Len(t, del.sends, 2, "a receipt whose message is gone re-delivers")
}

// --- Fire: missed-fire cut-off (AC-1) ---------------------------------------

// TestFire_PastCutoff_SkipsAndAlerts: past the missed-fire cut-off (the Monday
// mark + 48h ⇒ Wednesday morning) the fire refuses to post a stale report — it
// skips, alerts the user, never composes, and writes no receipt.
func TestFire_PastCutoff_SkipsAndAlerts(t *testing.T) {
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{}
	r, store := newRunner(t, comp, del)

	// Monday 09:00 + 49h = Wednesday 10:00, past the 48h cut-off.
	out, err := r.Fire(context.Background(), reportNow().Add(49*time.Hour))
	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Equal(t, skipPastCutoff, out.SkipReason)
	assert.Empty(t, del.sends, "no stale report is posted past the cut-off")
	require.Len(t, del.alerts, 1, "past the cut-off the user is alerted instead of left silent")
	assert.Equal(t, engine.ChannelUser, del.alerts[0].channel)
	assert.Contains(t, del.alerts[0].text, "cut-off")
	assert.Equal(t, 0, comp.calls, "past the cut-off there is no compose")

	_, ok, _ := store.ReadWitnessReportReceipt()
	assert.False(t, ok, "a skipped week writes no receipt")
}

// TestFire_WithinCutoff_StillDelivers: a Tuesday fire (mark + ~24h, still inside
// the 48h window) is a healthy backfill after an overnight outage — it delivers.
func TestFire_WithinCutoff_StillDelivers(t *testing.T) {
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), reportNow().Add(24*time.Hour))
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	require.Len(t, del.sends, 1)
}

// --- Fire: never silent -----------------------------------------------------

// TestFire_DeliveryError_AlertsAndErrors: a total send failure fires a loud alert
// and returns a loud error — and writes no receipt, so a retry re-sends.
func TestFire_DeliveryError_AlertsAndErrors(t *testing.T) {
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{sendErr: errors.New("discord 503")}
	r, store := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), reportNow())
	require.Error(t, err)
	require.Len(t, del.alerts, 1, "a total send failure fires a loud alert")

	_, ok, _ := store.ReadWitnessReportReceipt()
	assert.False(t, ok, "a failed delivery writes no receipt")
}

// TestFire_VerifyError_AlertsAndErrors: a send that will not read back is a loud
// failure — the alert fires and the error surfaces, and no receipt is written.
func TestFire_VerifyError_AlertsAndErrors(t *testing.T) {
	comp := &runFakeComposer{report: sampleReport()}
	del := &runFakeDeliverer{verifyErr: errors.New("404 after send")}
	r, store := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), reportNow())
	require.Error(t, err)
	require.Len(t, del.alerts, 1)

	_, ok, _ := store.ReadWitnessReportReceipt()
	assert.False(t, ok, "an unverified send writes no receipt")
}

// TestFire_ComposeError_AlertsAndErrors: a loud compose failure (a missing prompt
// file, an unreadable projection) alerts and errors rather than sending an empty
// report.
func TestFire_ComposeError_AlertsAndErrors(t *testing.T) {
	comp := &runFakeComposer{err: errors.New("prompt file missing")}
	del := &runFakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	_, err := r.Fire(context.Background(), reportNow())
	require.Error(t, err)
	require.Len(t, del.alerts, 1)
	assert.Empty(t, del.sends, "no report goes out on a compose failure")
}

// TestFire_SafetyTripped_DeliversAndAlerts: a report whose model prose tripped the
// witness-safe scan still delivers the metrics-only card, but alerts the operator
// to review — the send is never blocked, and the trip is never silent.
func TestFire_SafetyTripped_DeliversAndAlerts(t *testing.T) {
	rep := sampleReport()
	rep.SafetyTripped = true
	rep.Fallback = true
	comp := &runFakeComposer{report: rep}
	del := &runFakeDeliverer{}
	r, store := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), reportNow())
	require.NoError(t, err)
	assert.True(t, out.Delivered, "the metrics-only report still lands")
	assert.True(t, out.SafetyTripped)
	require.Len(t, del.sends, 1)
	require.Len(t, del.alerts, 1, "a tripped scan alerts the operator to review")
	assert.Contains(t, del.alerts[0].text, "witness-safe scan")

	_, ok, _ := store.ReadWitnessReportReceipt()
	assert.True(t, ok, "a delivered report writes its receipt even when the scan tripped")
}

// TestFire_FallbackFlagPropagates: a deterministic-fallback report still delivers
// and the Outcome records the fallback (only the warmth was lost) with no extra
// alert.
func TestFire_FallbackFlagPropagates(t *testing.T) {
	rep := sampleReport()
	rep.Fallback = true
	comp := &runFakeComposer{report: rep}
	del := &runFakeDeliverer{}
	r, _ := newRunner(t, comp, del)

	out, err := r.Fire(context.Background(), reportNow())
	require.NoError(t, err)
	assert.True(t, out.Delivered)
	assert.True(t, out.Fallback)
	assert.Empty(t, del.alerts, "a plain fallback is not an alert — only the warmth was lost")
}

// TestFire_UnknownMode_Errors rejects a Runner built with a bogus mode before any
// send.
func TestFire_UnknownMode_Errors(t *testing.T) {
	r, _ := newRunner(t, &runFakeComposer{report: sampleReport()}, &runFakeDeliverer{})
	r.mode = "bogus"
	_, err := r.Fire(context.Background(), reportNow())
	require.Error(t, err)
}

// TestFire_MalformedMark_Errors: a Runner with a malformed clock mark rejects the
// fire rather than mis-scheduling the cut-off.
func TestFire_MalformedMark_Errors(t *testing.T) {
	r, _ := newRunner(t, &runFakeComposer{report: sampleReport()}, &runFakeDeliverer{})
	r.markHM = "nope"
	_, err := r.Fire(context.Background(), reportNow())
	require.Error(t, err)
}

// --- markFor / channelForMode ------------------------------------------------

// TestMarkFor: the scheduled mark is the configured weekday of now's ISO week at
// the configured time, regardless of which day the (possibly backfilled) fire
// actually runs.
func TestMarkFor(t *testing.T) {
	monday := &Runner{weekday: 1, markHM: "09:00"}
	// A Wednesday fire still resolves the Monday-09:00 mark of the same ISO week.
	got, err := monday.markFor(reportNow().Add(48 * time.Hour))
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC), got, "the mark is this week's Monday 09:00")

	// Weekday 0 (Sunday) resolves the end of the ISO week.
	sunday := &Runner{weekday: 0, markHM: "18:30"}
	got, err = sunday.markFor(reportNow())
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 7, 19, 18, 30, 0, 0, time.UTC), got, "the mark is this week's Sunday 18:30")
}

// TestChannelForMode covers the preview/auto routing and the unknown-mode error.
func TestChannelForMode(t *testing.T) {
	c, err := channelForMode(config.WitnessReportModePreview)
	require.NoError(t, err)
	assert.Equal(t, engine.ChannelUser, c)

	c, err = channelForMode(config.WitnessReportModeAuto)
	require.NoError(t, err)
	assert.Equal(t, engine.ChannelWitness, c)

	_, err = channelForMode("bogus")
	require.Error(t, err)
}

// --- Node wiring: the weekly Monday periodic (AC-11) ------------------------

// TestUpsertWeeklyPeriodic_RegistersMondayCron: reconciling the default block
// registers exactly one weekly periodic on the Monday-09:00 mark (`0 9 * * 1`),
// active on the witness-report queue.
func TestUpsertWeeklyPeriodic_RegistersMondayCron(t *testing.T) {
	db := newJobDB(t)
	ctx := ctxAt(reportNow())

	cfg := config.WitnessReportConfig{Enabled: true, Mode: config.WitnessReportModePreview, Time: "09:00", Weekday: 1}
	require.NoError(t, upsertWeeklyPeriodic(ctx, db, cfg))

	views, err := flywheel.ListPeriodics(ctx, db)
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, slugWeekly, views[0].Slug)
	assert.Equal(t, kindWeekly, views[0].Kind)
	assert.Equal(t, "0 9 * * 1", views[0].Cron, "the report rides the Monday 09:00 weekly mark")
	assert.Equal(t, queueName, views[0].Queue)
	assert.True(t, views[0].Active)
}

// TestUpsertWeeklyPeriodic_IsIdempotentAcrossRestart: re-reconciling neither
// duplicates the definition nor changes its cadence.
func TestUpsertWeeklyPeriodic_IsIdempotentAcrossRestart(t *testing.T) {
	db := newJobDB(t)
	cfg := config.WitnessReportConfig{Enabled: true, Mode: config.WitnessReportModePreview, Time: "09:00", Weekday: 1}

	require.NoError(t, upsertWeeklyPeriodic(ctxAt(reportNow()), db, cfg))
	require.NoError(t, upsertWeeklyPeriodic(ctxAt(reportNow().Add(7*24*time.Hour)), db, cfg))

	views, err := flywheel.ListPeriodics(ctxAt(reportNow()), db)
	require.NoError(t, err)
	assert.Len(t, views, 1, "no duplicate definition after a restart reconcile")
}

// TestUpsertWeeklyPeriodic_RejectsMalformedConfig: a bogus time or weekday is
// rejected at reconcile rather than scheduled.
func TestUpsertWeeklyPeriodic_RejectsMalformedConfig(t *testing.T) {
	db := newJobDB(t)
	badTime := config.WitnessReportConfig{Time: "24:61", Weekday: 1}
	require.Error(t, upsertWeeklyPeriodic(ctxAt(reportNow()), db, badTime))
	badDay := config.WitnessReportConfig{Time: "09:00", Weekday: 9}
	require.Error(t, upsertWeeklyPeriodic(ctxAt(reportNow()), db, badDay))
}

// --- Node wiring: end-to-end fire through the flywheel machinery -------------

// rig is a weekly-report flywheel runtime over an isolated Ledger and throwaway
// job DB: the scheduler (BackfillCap 1) and a deterministic runner share one
// registry, and the fake deliverer captures the sends.
type rig struct {
	db     *gorm.DB
	store  *storage.Adapter
	del    *runFakeDeliverer
	comp   *runFakeComposer
	sched  *flywheel.Scheduler
	runner *flywheel.Runner
}

func newRig(t *testing.T, clock models.Clock) *rig {
	t.Helper()
	store := runStore(t)
	db := newJobDB(t)
	del := &runFakeDeliverer{}
	comp := &runFakeComposer{report: sampleReport()}
	r := &Runner{compose: comp, deliver: del, store: store, mode: config.WitnessReportModePreview, weekday: 1, markHM: "09:00"}
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

// TestRun_WeeklyPeriodicFiresAndDelivers drives a due weekly periodic all the way
// through the machinery: the scheduler enqueues it and the runner fires the
// worker, which composes and delivers to the preview channel.
func TestRun_WeeklyPeriodicFiresAndDelivers(t *testing.T) {
	workerNow := reportNow().Add(5 * time.Minute) // Monday 09:05
	r := newRig(t, models.NewFixedClock(workerNow))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(reportNow().Add(-time.Hour)), r.db, flywheel.PeriodicSpec{
		Slug: slugWeekly, Kind: kindWeekly, Cron: "0 9 * * 1", Queue: queueName, Active: true,
	}))

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the due weekly periodic enqueues once")

	require.NoError(t, r.runner.RunUntilIdle(tickCtx))

	require.Len(t, r.del.sends, 1, "the weekly report delivers")
	assert.Equal(t, engine.ChannelUser, r.del.sends[0].channel)

	rec, ok, err := r.store.ReadWitnessReportReceipt()
	require.NoError(t, err)
	require.True(t, ok, "the fired report persists its receipt")
	assert.Equal(t, "2026-W29", rec.Week)
}

// --- Run: production wiring --------------------------------------------------

// TestRun_ReconcilesThenDrainsCleanly exercises the production Run wiring end to
// end: DB open/migrate, engine scaffold, periodic reconcile, node build/run. It
// waits until the weekly periodic is reconciled, then cancels and asserts the
// node drains to a clean nil return.
func TestRun_ReconcilesThenDrainsCleanly(t *testing.T) {
	store := runStore(t)
	dbPath := filepath.Join(t.TempDir(), "witness-report.db")
	cfg := config.WitnessReportConfig{
		Enabled: true, Mode: config.WitnessReportModePreview, Time: "09:00", Weekday: 1,
		SystemPrompt: "system.md", Template: "template.md",
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Store:    store,
			Config:   cfg,
			Numbers:  fakeNumbers{},
			Records:  fakeRecords{},
			Notifier: &runFakeDeliverer{},
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
	}, 5*time.Second, 25*time.Millisecond, "Run reconciles the weekly periodic")

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
		Notifier: &runFakeDeliverer{}, Numbers: fakeNumbers{}, Records: fakeRecords{},
	}), errNoStore)
	require.ErrorIs(t, Run(context.Background(), Options{
		Store: runStore(t), Numbers: fakeNumbers{}, Records: fakeRecords{},
	}), errNoNotifier)
	require.ErrorIs(t, Run(context.Background(), Options{
		Store: runStore(t), Notifier: &runFakeDeliverer{}, Records: fakeRecords{},
	}), errNoNumbers)
	require.ErrorIs(t, Run(context.Background(), Options{
		Store: runStore(t), Notifier: &runFakeDeliverer{}, Numbers: fakeNumbers{},
	}), errNoRecords)
}

// --- helpers -----------------------------------------------------------------

// TestWeeklyCron tables the mark+weekday to cron translation and its rejections.
func TestWeeklyCron(t *testing.T) {
	cases := []struct {
		hm      string
		weekday int
		want    string
		wantErr bool
	}{
		{hm: "09:00", weekday: 1, want: "0 9 * * 1"},
		{hm: "21:30", weekday: 0, want: "30 21 * * 0"},
		{hm: "06:05", weekday: 6, want: "5 6 * * 6"},
		{hm: "", weekday: 1, wantErr: true},
		{hm: "24:00", weekday: 1, wantErr: true},
		{hm: "09:60", weekday: 1, wantErr: true},
		{hm: "09:00", weekday: -1, wantErr: true},
		{hm: "09:00", weekday: 7, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%d", tc.hm, tc.weekday), func(t *testing.T) {
			got, err := weeklyCron(tc.hm, tc.weekday)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDefaultDBPath covers the explicit > env > default precedence and that the
// default lands outside the ~/.lucid Ledger, distinct from the sibling job DBs.
func TestDefaultDBPath(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "custom.db")
	got, err := DefaultDBPath(explicit)
	require.NoError(t, err)
	assert.Equal(t, explicit, got, "an explicit path wins")

	envPath := filepath.Join(t.TempDir(), "env.db")
	t.Setenv(envWitnessReportDB, envPath)
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.Equal(t, envPath, got, "the env override is next")

	t.Setenv(envWitnessReportDB, "")
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, filepath.Join("lucid", "witness-report.db")), "the default lands under the config dir, got %q", got)
	assert.NotContains(t, got, filepath.Join(".lucid"), "the job DB is outside the Ledger")
}
