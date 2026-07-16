package schedrun

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/scheduler"
	"github.com/mrz1836/lucid/internal/storage"
)

// capture is one delivered send recorded by the fake notifier.
type capture struct{ channel, text string }

// fakeNotifier captures every delivered send, so a test can assert exactly what
// fired without a real token or socket (mirrors internal/scheduler's fake).
type fakeNotifier struct{ sent []capture }

func (f *fakeNotifier) Send(channel, text string) error {
	f.sent = append(f.sent, capture{channel, text})
	return nil
}

func (f *fakeNotifier) count(channel string) int {
	n := 0
	for _, c := range f.sent {
		if c.channel == channel {
			n++
		}
	}
	return n
}

func (f *fakeNotifier) first(channel string) (capture, bool) {
	for _, c := range f.sent {
		if c.channel == channel {
			return c, true
		}
	}
	return capture{}, false
}

// rewriteDoer redirects a Discord REST request at an httptest server by
// rewriting only its scheme+host, so a schedrun→notify→HTTP test exercises the
// real notifier's URL, auth header, and body without touching discord.com. It
// satisfies the notifier's httpDoer seam.
type rewriteDoer struct {
	base   string
	client *http.Client
}

func (d rewriteDoer) Do(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(d.base)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return d.client.Do(req)
}

// rig is a fully wired schedrun runtime over an isolated Ledger and a
// throwaway job DB: the scheduler (BackfillCap 1) and a deterministic runner
// share one registry, and the fake notifier captures the sends.
type rig struct {
	db     *gorm.DB
	store  *storage.Adapter
	notif  *fakeNotifier
	sched  *flywheel.Scheduler
	runner *flywheel.Runner
}

// newRig builds a rig whose tripwire worker evaluates against workerClock, using
// the capturing fake notifier.
func newRig(t *testing.T, workerClock models.Clock) *rig {
	t.Helper()
	return newRigWith(t, workerClock, &fakeNotifier{})
}

// newRigWith builds a rig over an arbitrary notifier — the seam for the real
// notify.Discord in the full-path HTTP test.
func newRigWith(t *testing.T, workerClock models.Clock, notif scheduler.Notifier) *rig {
	t.Helper()

	store := storage.New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := store.Scaffold()
	require.NoError(t, err)
	require.NoError(t, store.ScaffoldEngine())

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "flywheel.db")), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, flywheel.Migrate(db))

	fake, _ := notif.(*fakeNotifier)
	sc := scheduler.New(store, notif)
	reg := buildRegistry(sc, workerClock, store, false)

	sched := flywheel.NewSchedulerWithConfig(flywheel.SchedulerConfig{
		DB: db, Client: flywheel.NewClient(db), BackfillCap: backfillCap,
	})
	runner, err := flywheel.NewRunner(flywheel.RunnerConfig{
		DB: db, Driver: flywheel.NewSQLiteDriver(db), Registry: reg,
		Queues: []string{queueName}, ClaimAnyClass: true, Concurrency: 1,
	})
	require.NoError(t, err)

	return &rig{db: db, store: store, notif: fake, sched: sched, runner: runner}
}

// at builds a UTC instant.
//
//nolint:unparam // y is kept explicit for fixture readability even though the cases share 2026
func at(y int, mo time.Month, d, hh, mm int) time.Time {
	return time.Date(y, mo, d, hh, mm, 0, 0, time.UTC)
}

// ctxAt returns a context carrying a fixed clock at t — the clock the flywheel
// scheduler tick and the runner both read.
func ctxAt(t time.Time) context.Context {
	return models.WithClock(context.Background(), models.NewFixedClock(t))
}

// completedRec is a done close-out for date (mirrors internal/scheduler's fixture).
func completedRec(date string) engine.DayRecord {
	d, _ := time.Parse("2006-01-02", date)
	return engine.DayRecord{
		DayID: engine.DayID(d), LogicalDate: date, RecordedAt: date + "T22:00:00Z",
		Mode: engine.ModeGreen, Links: map[string]string{"journal": engine.StatusDone},
		Completed: true, Profile: engine.DefaultProfile, Corrections: []engine.Correction{},
	}
}

// seedDay writes day records and stamps chain_start via a rebuild so the
// escalation ladder is active, as it is after any real first close-out.
func seedDay(t *testing.T, a *storage.Adapter, recs ...engine.DayRecord) {
	t.Helper()
	for _, r := range recs {
		require.NoError(t, a.WriteEngineDay(r))
	}
	_, err := a.RebuildEngineStatus(time.UTC)
	require.NoError(t, err)
}

// upsertTripwire declares the tripwire periodic at upsertClock so its next fire
// lands after upsertClock; a later Tick then makes it due.
func upsertTripwire(t *testing.T, r *rig, upsertClock time.Time) {
	t.Helper()
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(upsertClock), r.db, flywheel.PeriodicSpec{
		Slug: slugTripwire, Kind: kindTripwire, Cron: "0 9 * * *", Queue: queueName, Active: true,
	}))
}

// ── End-to-end: a silent day fires exactly one L1 (AC-11) ────────────────────

// TestRun_SilentDayFiresOneL1EndToEnd drives a synthetic silent day all the way
// through the flywheel machinery: seed a chain start, leave yesterday absent,
// let the scheduler enqueue the due tripwire, and let the runner fire it. The
// captured send must be a single L1 to the user channel naming the survival floor.
func TestRun_SilentDayFiresOneL1EndToEnd(t *testing.T) {
	workerNow := at(2026, 7, 6, 9, 5) // reference day 07-05 (absent)
	r := newRig(t, models.NewFixedClock(workerNow))
	seedDay(t, r.store, completedRec("2026-07-04")) // 07-05 left absent -> one miss

	upsertTripwire(t, r, at(2026, 7, 5, 12, 0)) // next fire 07-06 09:00

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the due tripwire enqueues exactly once")

	require.NoError(t, r.runner.RunUntilIdle(tickCtx))

	require.Equal(t, 1, r.notif.count(engine.ChannelUser), "exactly one L1 fires")
	assert.Zero(t, r.notif.count(engine.ChannelWitness))
	msg, ok := r.notif.first(engine.ChannelUser)
	require.True(t, ok)
	assert.Contains(t, msg.text, "one line, spoken or typed", "the L1 names the survival floor")
}

// TestRun_SilentDayThroughRealNotifierHTTP runs the same silent-day fire through
// the concrete notify.Discord against an httptest server, proving the full
// schedrun -> scheduler -> notify -> HTTP path: the request lands on the resolved
// user channel with the Bot auth header and the JSON content body.
func TestRun_SilentDayThroughRealNotifierHTTP(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		b, _ := io.ReadAll(req.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	discord := notify.New("bot-secret", "USERCHAN", "WITCHAN", rewriteDoer{base: srv.URL, client: srv.Client()})

	workerNow := at(2026, 7, 6, 9, 5)
	r := newRigWith(t, models.NewFixedClock(workerNow), discord)
	seedDay(t, r.store, completedRec("2026-07-04"))
	upsertTripwire(t, r, at(2026, 7, 5, 12, 0))

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, r.runner.RunUntilIdle(tickCtx))

	assert.True(t, strings.HasSuffix(gotPath, "/channels/USERCHAN/messages"), "path resolved the user channel, got %q", gotPath)
	assert.Equal(t, "Bot bot-secret", gotAuth)
	assert.True(t, strings.HasPrefix(gotBody, `{"content":`), "body is the JSON content envelope, got %q", gotBody)
	assert.Contains(t, gotBody, "one line, spoken or typed")
}

// TestRun_BellPeriodicFiresToUser proves a real bell periodic fires: the due
// bell enqueues and the runner posts the chain label to the user channel.
func TestRun_BellPeriodicFiresToUser(t *testing.T) {
	workerNow := at(2026, 7, 5, 21, 35)
	r := newRig(t, models.NewFixedClock(workerNow))
	require.NoError(t, flywheel.UpsertPeriodic(ctxAt(at(2026, 7, 5, 20, 0)), r.db, flywheel.PeriodicSpec{
		Slug: slugBell, Kind: kindBell, Cron: "30 21 * * *", Queue: queueName, Active: true,
	}))

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, r.runner.RunUntilIdle(tickCtx))

	require.Equal(t, 1, r.notif.count(engine.ChannelUser))
	msg, _ := r.notif.first(engine.ChannelUser)
	assert.Contains(t, msg.text, "Journal. Dock. Read.", "the bell names the chain")
}

// ── Missed-fire catch-up, bounded (AC-3) ─────────────────────────────────────

// TestRun_MissedFireCatchUpBoundedToOne is the "killed mid-evening still fires
// next morning" proof. The daemon is down across several tripwire fires; on the
// supervised restart the scheduler tick backfills, but BackfillCap:1 enqueues
// only the single most-recent missed bucket — one catch-up fire, not a week of
// them — and the worker still fires the morning tripwire.
func TestRun_MissedFireCatchUpBoundedToOne(t *testing.T) {
	// missed-fire catch-up: a supervised restart backfills only the most recent
	// bucket after the daemon was down.
	workerNow := at(2026, 7, 8, 9, 5) // reference day 07-07 (absent)
	r := newRig(t, models.NewFixedClock(workerNow))
	seedDay(t, r.store, completedRec("2026-07-06")) // 07-07 left absent -> one miss

	// Next fire seeded at 07-06 09:00; node then "down" until 07-08 => the
	// 07-06, 07-07 and 07-08 buckets are all missed.
	upsertTripwire(t, r, at(2026, 7, 5, 12, 0))

	tickCtx := ctxAt(workerNow)
	n, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "BackfillCap:1 catches up only the most-recent missed bucket")

	require.NoError(t, r.runner.RunUntilIdle(tickCtx))
	require.Equal(t, 1, r.notif.count(engine.ChannelUser), "the missed morning still fires after restart")
}

// ── Idempotency (A5) ─────────────────────────────────────────────────────────

// TestScheduler_TwoTicksEnqueueOneTripwire: two scheduler ticks in one day
// enqueue at most one tripwire job (the definition advances past the fired
// bucket), and exactly one L1 fires.
func TestScheduler_TwoTicksEnqueueOneTripwire(t *testing.T) {
	workerNow := at(2026, 7, 6, 9, 5)
	r := newRig(t, models.NewFixedClock(workerNow))
	seedDay(t, r.store, completedRec("2026-07-04"))
	upsertTripwire(t, r, at(2026, 7, 5, 12, 0))

	tickCtx := ctxAt(workerNow)
	n1, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 1, n1, "first tick enqueues the due bucket")

	n2, err := r.sched.Tick(tickCtx)
	require.NoError(t, err)
	require.Equal(t, 0, n2, "the second same-day tick enqueues nothing")

	require.NoError(t, r.runner.RunUntilIdle(tickCtx))
	assert.Equal(t, 1, r.notif.count(engine.ChannelUser), "exactly one L1")
}

// TestTripwireWorker_SecondRunSameDayIsNoOp locks the per-day worker guard (A5):
// a second Work in the same wall day is a no-op, so a redundant fire never sends
// a second L1 even if a job were somehow re-dispatched.
func TestTripwireWorker_SecondRunSameDayIsNoOp(t *testing.T) {
	workerNow := at(2026, 7, 6, 9, 5)
	r := newRig(t, models.NewFixedClock(workerNow))
	seedDay(t, r.store, completedRec("2026-07-04")) // 07-05 absent -> L1

	w := tripwireWorker{sc: scheduler.New(r.store, r.notif), clock: models.NewFixedClock(workerNow), store: r.store}
	ctx := ctxAt(workerNow)

	_, err := w.Work(ctx, &flywheel.Job[tripwireArgs]{})
	require.NoError(t, err)
	_, err = w.Work(ctx, &flywheel.Job[tripwireArgs]{})
	require.NoError(t, err)

	assert.Equal(t, 1, r.notif.count(engine.ChannelUser), "the per-day guard suppresses the second run")
}

// ── Companion presentation: user-channel suppression ─────────────────────────

// TestTripwireWorker_PresentedSuppressesUserSend proves the presented worker
// keeps the teeth modeless while withholding the user-channel send: on a
// one-miss day it fires no user L1 (the companion presents it) yet still
// persists escalation_state to l1_fired. A default (non-presented) worker over
// an identical Ledger still fires exactly one user L1 — today's behavior intact.
func TestTripwireWorker_PresentedSuppressesUserSend(t *testing.T) {
	workerNow := at(2026, 7, 6, 9, 5) // reference 07-05 absent -> one miss

	// Presented: no user send, escalation still persisted.
	r := newRig(t, models.NewFixedClock(workerNow))
	seedDay(t, r.store, completedRec("2026-07-04"))
	w := tripwireWorker{sc: scheduler.New(r.store, r.notif), clock: models.NewFixedClock(workerNow), store: r.store, presented: true}
	_, err := w.Work(ctxAt(workerNow), &flywheel.Job[tripwireArgs]{})
	require.NoError(t, err)
	assert.Zero(t, r.notif.count(engine.ChannelUser), "the companion presents the verdict; the Engine sends nothing to the user")
	st, err := r.store.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, engine.EscalationL1, st.EscalationState, "escalation_state still persists in presented mode")

	// Default: an identical Ledger fires exactly one user L1.
	r2 := newRig(t, models.NewFixedClock(workerNow))
	seedDay(t, r2.store, completedRec("2026-07-04"))
	w2 := tripwireWorker{sc: scheduler.New(r2.store, r2.notif), clock: models.NewFixedClock(workerNow), store: r2.store}
	_, err = w2.Work(ctxAt(workerNow), &flywheel.Job[tripwireArgs]{})
	require.NoError(t, err)
	assert.Equal(t, 1, r2.notif.count(engine.ChannelUser), "SuppressUserChannel:false reproduces today's user L1")
}

// TestUpsertPeriodics_SuppressDeactivatesBell: when the companion owns the user
// windows the evening bell periodic is reconciled inactive (so it never fires)
// while the morning tripwire stays active — one night send, owned by the
// companion.
func TestUpsertPeriodics_SuppressDeactivatesBell(t *testing.T) {
	r := newRig(t, models.NewFixedClock(at(2026, 7, 5, 12, 0)))
	ctx := ctxAt(at(2026, 7, 5, 12, 0))

	require.NoError(t, upsertPeriodics(ctx, r.db, r.store, true))

	views, err := flywheel.ListPeriodics(ctx, r.db)
	require.NoError(t, err)
	active := map[string]bool{}
	for _, v := range views {
		active[v.Slug] = v.Active
	}
	assert.False(t, active[slugBell], "the bell is deactivated when the companion presents the night window")
	assert.True(t, active[slugTripwire], "the tripwire stays active")
}

// TestUpsertPeriodics_SuppressTogglesBellBackOn: reconciling suppressed then
// un-suppressed flips the bell inactive and back to active on the next boot —
// the reconcile makes the live state match the config every time.
func TestUpsertPeriodics_SuppressTogglesBellBackOn(t *testing.T) {
	r := newRig(t, models.NewFixedClock(at(2026, 7, 5, 12, 0)))
	ctx := ctxAt(at(2026, 7, 5, 12, 0))

	require.NoError(t, upsertPeriodics(ctx, r.db, r.store, true))
	require.NoError(t, upsertPeriodics(ctxAt(at(2026, 7, 6, 12, 0)), r.db, r.store, false))

	views, err := flywheel.ListPeriodics(ctx, r.db)
	require.NoError(t, err)
	require.Len(t, views, 2, "no duplicate definitions across a toggle")
	for _, v := range views {
		if v.Slug == slugBell {
			assert.True(t, v.Active, "the bell re-activates when suppression is disabled")
		}
	}
}

// TestRun_SuppressUserChannelDeactivatesBell proves Options.SuppressUserChannel
// threads through Run's reconcile: the daemon comes up with the bell periodic
// inactive so the companion is the single night send.
func TestRun_SuppressUserChannelDeactivatesBell(t *testing.T) {
	store := storage.New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := store.Scaffold()
	require.NoError(t, err)

	dbPath := filepath.Join(t.TempDir(), "flywheel.db")
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
		if lerr != nil || len(views) != 2 {
			return false
		}
		for _, v := range views {
			if v.Slug == slugBell {
				return !v.Active
			}
		}
		return false
	}, 5*time.Second, 25*time.Millisecond, "Run reconciles the bell inactive under SuppressUserChannel")

	cancel()
	select {
	case rerr := <-done:
		require.NoError(t, rerr, "a canceled node drains cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// ── Periodic registration from chain marks (AC-2) ────────────────────────────

// TestUpsertPeriodics_RegistersBellAndTripwireFromChainMarks: reconciling from
// the default chain marks (19:00 bell, 06:00 tripwire) registers both durable
// periodics with the expected daily cron expressions.
func TestUpsertPeriodics_RegistersBellAndTripwireFromChainMarks(t *testing.T) {
	r := newRig(t, models.NewFixedClock(at(2026, 7, 5, 12, 0)))
	ctx := ctxAt(at(2026, 7, 5, 12, 0))

	require.NoError(t, upsertPeriodics(ctx, r.db, r.store, false))

	views, err := flywheel.ListPeriodics(ctx, r.db)
	require.NoError(t, err)
	crons := map[string]string{}
	for _, v := range views {
		crons[v.Slug] = v.Cron
		assert.True(t, v.Active, "periodic %s is active", v.Slug)
		assert.Equal(t, queueName, v.Queue)
	}
	assert.Equal(t, "0 19 * * *", crons[slugBell], "bell fires at 19:00")
	assert.Equal(t, "0 6 * * *", crons[slugTripwire], "tripwire fires at 06:00")
}

// TestUpsertPeriodics_IsIdempotentAcrossRestart: re-reconciling the same config
// on a restart neither duplicates a definition nor changes its cadence.
func TestUpsertPeriodics_IsIdempotentAcrossRestart(t *testing.T) {
	r := newRig(t, models.NewFixedClock(at(2026, 7, 5, 12, 0)))
	ctx := ctxAt(at(2026, 7, 5, 12, 0))

	require.NoError(t, upsertPeriodics(ctx, r.db, r.store, false))
	require.NoError(t, upsertPeriodics(ctxAt(at(2026, 7, 6, 12, 0)), r.db, r.store, false))

	views, err := flywheel.ListPeriodics(ctx, r.db)
	require.NoError(t, err)
	assert.Len(t, views, 2, "no duplicate definitions after a restart reconcile")
}

// ── cronFromHM ───────────────────────────────────────────────────────────────

// TestCronFromHM tables the clock-mark to cron translation and its rejections.
func TestCronFromHM(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "21:30", want: "30 21 * * *"},
		{in: "09:00", want: "0 9 * * *"},
		{in: "00:00", want: "0 0 * * *"},
		{in: "23:59", want: "59 23 * * *"},
		{in: "", wantErr: true},
		{in: "2130", wantErr: true},
		{in: "21:30:00", wantErr: true},
		{in: "24:00", wantErr: true},
		{in: "21:60", wantErr: true},
		{in: "-1:00", wantErr: true},
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

// ── DB path resolution ───────────────────────────────────────────────────────

// TestDefaultDBPath covers the --db > env > default precedence.
func TestDefaultDBPath(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "custom.db")
	got, err := DefaultDBPath(explicit)
	require.NoError(t, err)
	assert.Equal(t, explicit, got, "an explicit path wins")

	envPath := filepath.Join(t.TempDir(), "env.db")
	t.Setenv(envSchedulerDB, envPath)
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.Equal(t, envPath, got, "the env override is next")

	t.Setenv(envSchedulerDB, "")
	got, err = DefaultDBPath("")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, filepath.Join("lucid", "flywheel.db")), "the default lands under the config dir, got %q", got)
	assert.NotContains(t, got, filepath.Join(".lucid"), "the job DB is outside the Ledger")
}

// ── Run wiring ───────────────────────────────────────────────────────────────

// TestRun_ReconcilesThenDrainsCleanly exercises the production Run wiring — DB
// open, migrate, engine scaffold, periodic reconcile, node build/run — end to
// end: it runs the daemon, waits until both periodics are reconciled (proving
// setup ran), then cancels and asserts the node drains to a clean nil return.
func TestRun_ReconcilesThenDrainsCleanly(t *testing.T) {
	store := storage.New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := store.Scaffold()
	require.NoError(t, err)

	dbPath := filepath.Join(t.TempDir(), "flywheel.db")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Store: store, Notifier: &fakeNotifier{}, DBPath: dbPath})
	}()

	require.Eventually(t, func() bool {
		db, oerr := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
		if oerr != nil {
			return false
		}
		defer closeGorm(db)
		views, lerr := flywheel.ListPeriodics(context.Background(), db)
		return lerr == nil && len(views) == 2
	}, 5*time.Second, 25*time.Millisecond, "Run reconciles the bell and tripwire periodics")

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
	require.ErrorIs(t, Run(context.Background(), Options{Notifier: &fakeNotifier{}}), errNoStore)
	require.ErrorIs(t, Run(context.Background(), Options{Store: storage.New(t.TempDir())}), errNoNotifier)
}

// TestRun_MkdirFailsOnFileParent covers the job-DB directory creation error: a
// DB path nested under a regular file cannot have its parent created.
func TestRun_MkdirFailsOnFileParent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))

	err := Run(context.Background(), Options{
		Store:    storage.New(filepath.Join(t.TempDir(), ".lucid")),
		Notifier: &fakeNotifier{},
		DBPath:   filepath.Join(file, "sub", "flywheel.db"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create job db dir")
}

// TestRun_PropagatesReconcileError: a malformed clock mark in an existing
// chain.json (ScaffoldEngine preserves it) fails the periodic reconcile, and Run
// surfaces the error instead of starting the node.
func TestRun_PropagatesReconcileError(t *testing.T) {
	store := storage.New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := store.Scaffold()
	require.NoError(t, err)
	require.NoError(t, store.ScaffoldEngine())

	chain, err := store.ReadChainConfig()
	require.NoError(t, err)
	chain.BellTime = "99:99"
	require.NoError(t, store.WriteChainConfig(chain))

	err = Run(context.Background(), Options{
		Store:    store,
		Notifier: &fakeNotifier{},
		DBPath:   filepath.Join(t.TempDir(), "flywheel.db"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hour")
}

// TestUpsertPeriodics_RejectsMalformedClockMark: an out-of-range tripwire mark
// is rejected at reconcile rather than scheduled.
func TestUpsertPeriodics_RejectsMalformedClockMark(t *testing.T) {
	r := newRig(t, models.NewFixedClock(at(2026, 7, 5, 12, 0)))
	chain, err := r.store.ReadChainConfig()
	require.NoError(t, err)
	chain.Escalation.TripwireTime = "24:61"
	require.NoError(t, r.store.WriteChainConfig(chain))

	err = upsertPeriodics(ctxAt(at(2026, 7, 5, 12, 0)), r.db, r.store, false)
	require.Error(t, err)
}

// TestRun_SurfacesEngineScaffoldError covers Run's scaffold-engine error branch:
// the job DB opens and migrates, but a Ledger whose home is a regular file
// cannot scaffold its engine tree.
func TestRun_SurfacesEngineScaffoldError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ledger-file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))

	err := Run(context.Background(), Options{
		Store:    storage.New(file),
		Notifier: &fakeNotifier{},
		DBPath:   filepath.Join(t.TempDir(), "flywheel.db"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scaffold engine")
}

// TestTripwireWorker_SurfacesRunError covers the worker's RunTripwire error
// branch: tripwire state reads fine (fresh Ledger), but a corrupt chain.json
// fails the run itself.
func TestTripwireWorker_SurfacesRunError(t *testing.T) {
	r := newRig(t, models.NewFixedClock(at(2026, 7, 6, 9, 5)))
	require.NoError(t, os.WriteFile(filepath.Join(r.store.Home(), "engine", "chain.json"), []byte("{not json"), 0o600))

	tw := tripwireWorker{sc: scheduler.New(r.store, r.notif), clock: models.NewFixedClock(at(2026, 7, 6, 9, 5)), store: r.store}
	_, err := tw.Work(ctxAt(at(2026, 7, 6, 9, 5)), &flywheel.Job[tripwireArgs]{})
	require.Error(t, err)
}

// TestUpsertPeriodics_SurfacesChainReadError covers the reconcile's chain-read
// error branch on a corrupt chain.json.
func TestUpsertPeriodics_SurfacesChainReadError(t *testing.T) {
	r := newRig(t, models.NewFixedClock(at(2026, 7, 5, 12, 0)))
	require.NoError(t, os.WriteFile(filepath.Join(r.store.Home(), "engine", "chain.json"), []byte("{not json"), 0o600))

	err := upsertPeriodics(ctxAt(at(2026, 7, 5, 12, 0)), r.db, r.store, false)
	require.Error(t, err)
}

// TestWorkers_SurfaceStoreErrors: when the Ledger is unusable (its home is a
// regular file, so the engine tree cannot be scaffolded/read), both workers
// surface the error rather than swallow a failed send.
func TestWorkers_SurfaceStoreErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	broken := storage.New(file)
	sc := scheduler.New(broken, &fakeNotifier{})

	_, err := bellWorker{sc: sc}.Work(context.Background(), &flywheel.Job[bellArgs]{})
	require.Error(t, err, "the bell worker surfaces a store failure")

	tw := tripwireWorker{sc: sc, clock: models.NewFixedClock(at(2026, 7, 6, 9, 5)), store: broken}
	_, err = tw.Work(ctxAt(at(2026, 7, 6, 9, 5)), &flywheel.Job[tripwireArgs]{})
	require.Error(t, err, "the tripwire worker surfaces a store failure")
}
