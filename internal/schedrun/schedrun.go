// Package schedrun is the production driver for the Engine's scheduled jobs:
// the standalone-install runtime that fires the evening bell and the morning
// tripwire on the chain's own clocks.
// It wraps the pure [scheduler] decision in a durable go-flywheel job runtime
// (ADR-0004): periodics reconciled by slug from chain.json, per-bucket
// idempotency, and bounded missed-fire catch-up so a daemon killed mid-evening
// still fires the tripwire next morning after a supervised restart.
//
// It deliberately lives beside [scheduler] rather than inside it. The scheduler
// package carries a purity guard that forbids the substring "model" in any
// import, and go-flywheel legitimately imports go-foundation/models — so the
// flywheel wiring has to sit in this sibling, whose own guard
// (purity_test.go) forbids the model/agent/provider packages without the
// false-positive on "models". No LLM is reachable from the write path.
package schedrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/scheduler"
	"github.com/mrz1836/lucid/internal/storage"
)

// Job runtime identifiers. The queue is shared by every periodic; the kinds
// name the workers; the slugs are the stable periodic identities that
// UpsertPeriodic reconciles by, so restarting the daemon never duplicates them.
const (
	queueName        = "lucid"
	kindBell         = "lucid_bell"
	kindBellFallback = "lucid_bell_fallback"
	kindTripwire     = "lucid_tripwire"
	slugBell         = "lucid-bell"
	slugBellFallback = "lucid-bell-fallback"
	slugTripwire     = "lucid-tripwire"

	// backfillCap fires only the single most-recent missed bucket on a
	// supervised restart: a killed daemon catches the last morning up without
	// replaying a week of bells after a long outage (ADR-0004 catch-up).
	backfillCap = 1

	// dbDirPerm is the mode for the disposable job-DB parent directory.
	dbDirPerm = 0o755

	// envSchedulerDB overrides the job-DB path when --db is not passed. The
	// flywheel.db is disposable scheduler machinery, not Ledger truth, so it
	// lives outside ~/.lucid by default (ADR-0004).
	envSchedulerDB = "LUCID_SCHEDULER_DB"

	// companionNightCutoffMin mirrors the companion's night missed-fire cut-off
	// (22:00), in minutes since local midnight: past it the companion refuses to
	// post at all, alerting instead of delivering a stale evening message. It is
	// duplicated here rather than imported because the companion package reaches
	// a provider and this one is the agent-free write path (purity_test.go) — so
	// the two must move together, and a drift test in schedrun_test.go reads the
	// companion's own literal to lock them.
	companionNightCutoffMin = 22 * 60

	// bellFallbackGrace is how long past the companion's cut-off the evening
	// backstop waits before it speaks. The cut-off is when the companion stops
	// trying; the grace covers a delivery already in flight as it passed, so a
	// verified receipt is on disk before the backstop reads for one.
	bellFallbackGrace = 60 * time.Minute

	// minutesPerDay is the wrap modulus for clock-mark arithmetic.
	minutesPerDay = 24 * 60
)

var (
	errNoStore    = errors.New("schedrun: store is required")
	errNoNotifier = errors.New("schedrun: notifier is required")
)

// Options configures a scheduler daemon run. Store and Notifier are required;
// DBPath and Clock default when zero (the real clock, and the disposable job DB
// under the user config dir). Clock is injected by tests to drive deterministic
// fires; production leaves it nil and inherits the real wall clock.
type Options struct {
	Store    *storage.Adapter
	Notifier scheduler.Notifier
	DBPath   string
	Clock    models.Clock

	// SuppressUserChannel hands the user-facing windows to the companion. When
	// set, the tripwire worker runs [scheduler.Scheduler.RunTripwirePresented]
	// (the witness L2 still fires and escalation_state still persists — only the
	// user-channel L1/L2-blocked/storm-lapse send is withheld) and the evening
	// bell periodic is reconciled inactive, so the
	// companion is the single user send per window. When unset (the default) the
	// scheduler behaves exactly as before: bell and tripwire both deliver to the
	// user. It threads only a bool into this write path — no model is reachable.
	//
	// It is also what arms the evening backstop: handing a window to a single
	// sender is only safe if something notices when that sender fails, so the
	// backstop periodic is active exactly when the bell is suppressed.
	SuppressUserChannel bool
}

// bellArgs / bellFallbackArgs / tripwireArgs are the (empty) typed payloads for
// the periodics: no job carries input — the decision is a pure function of the
// Ledger at fire time. Their JSON form is the empty object the scheduler
// enqueues.
type (
	bellArgs         struct{}
	bellFallbackArgs struct{}
	tripwireArgs     struct{}
)

// bellWorker fires the evening bell.
type bellWorker struct {
	sc *scheduler.Scheduler
}

// Kind names the worker dispatched for kindBell.
func (bellWorker) Kind() string { return kindBell }

// Work posts the evening bell (a no-op when the bell is disabled in chain.json).
func (w bellWorker) Work(_ context.Context, _ *flywheel.Job[bellArgs]) (flywheel.Result, error) {
	if _, err := w.sc.RunBell(); err != nil {
		return flywheel.Result{}, err
	}
	return flywheel.Result{}, nil
}

// bellFallbackWorker fires the evening backstop. It holds a clock because,
// unlike the bell, its decision is date-sensitive: the send is gated on whether
// the companion left a delivery receipt for the logical day this fire belongs
// to, so the reference instant must be deterministic under test.
type bellFallbackWorker struct {
	sc    *scheduler.Scheduler
	clock models.Clock
}

// Kind names the worker dispatched for kindBellFallback.
func (bellFallbackWorker) Kind() string { return kindBellFallback }

// Work posts the evening backstop for `now` — a no-op when the companion
// already delivered its night message, and when the bell is disabled in
// chain.json (the backstop rides the bell's consent, not its own).
func (w bellFallbackWorker) Work(ctx context.Context, _ *flywheel.Job[bellFallbackArgs]) (flywheel.Result, error) {
	if _, err := w.sc.RunBellFallback(w.clock.Now(ctx)); err != nil {
		return flywheel.Result{}, err
	}
	return flywheel.Result{}, nil
}

// tripwireWorker fires the morning tripwire. It holds a clock so the reference
// day is deterministic under test and the store so it can honor the per-day
// idempotency guard. When presented
// is set the companion owns the user-channel verdict, so the worker runs the
// presented variant that withholds the Engine's own user send.
type tripwireWorker struct {
	sc        *scheduler.Scheduler
	clock     models.Clock
	store     *storage.Adapter
	presented bool
}

// Kind names the worker dispatched for kindTripwire.
func (tripwireWorker) Kind() string { return kindTripwire }

// Work runs the morning dead-man for `now`. A per-day guard (A5) skips a second
// run in the same wall day — a backstop under the flywheel per-bucket unique key
// so a redundant fire never sends a second L1. In presented mode it runs the
// companion-presented variant, which suppresses only the user-channel send.
func (w tripwireWorker) Work(ctx context.Context, _ *flywheel.Job[tripwireArgs]) (flywheel.Result, error) {
	now := w.clock.Now(ctx)
	tw, err := w.store.ReadTripwireState()
	if err != nil {
		return flywheel.Result{}, fmt.Errorf("schedrun: read tripwire state: %w", err)
	}
	if tw.LastRunDate == engine.DateString(engine.DateOf(now)) {
		return flywheel.Result{}, nil
	}
	run := w.sc.RunTripwire
	if w.presented {
		run = w.sc.RunTripwirePresented
	}
	if _, err := run(now); err != nil {
		return flywheel.Result{}, err
	}
	return flywheel.Result{}, nil
}

// Run opens the disposable job DB, migrates it, registers the bell, backstop,
// and tripwire workers, reconciles their periodics from chain.json's clocks, and
// runs the flywheel node until ctx is canceled (SIGINT/SIGTERM upstream). It
// returns nil on a clean drain.
func Run(ctx context.Context, opts Options) error {
	if opts.Store == nil {
		return errNoStore
	}
	if opts.Notifier == nil {
		return errNoNotifier
	}

	// One clock drives both the flywheel machinery (via the ctx) and the
	// tripwire worker's reference day, so a test's fixed clock moves them
	// together and production inherits the real wall clock.
	clock := opts.Clock
	if clock == nil {
		clock = models.ClockFrom(ctx)
	}
	ctx = models.WithClock(ctx, clock)

	dbPath, err := DefaultDBPath(opts.DBPath)
	if err != nil {
		return err
	}
	if mkErr := os.MkdirAll(filepath.Dir(dbPath), dbDirPerm); mkErr != nil {
		return fmt.Errorf("schedrun: create job db dir: %w", mkErr)
	}

	// The disposable job DB is machinery, not truth: gorm's per-statement SQL
	// logging (including the expected record-not-found on a first-boot upsert) is
	// noise the daemon's flywheel slog does not need. Silence it.
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return fmt.Errorf("schedrun: open job db %q: %w", dbPath, err)
	}
	if err = flywheel.Migrate(db); err != nil {
		return fmt.Errorf("schedrun: migrate job db: %w", err)
	}
	if err = opts.Store.ScaffoldEngine(); err != nil {
		return fmt.Errorf("schedrun: scaffold engine: %w", err)
	}
	if err = upsertPeriodics(ctx, db, opts.Store, opts.SuppressUserChannel); err != nil {
		return err
	}

	sc := scheduler.New(opts.Store, opts.Notifier)
	reg := buildRegistry(sc, clock, opts.Store, opts.SuppressUserChannel)

	node, err := flywheel.NewNode(flywheel.NodeConfig{
		Runners: []flywheel.RunnerConfig{{
			DB:       db,
			Driver:   flywheel.NewSQLiteDriver(db),
			Registry: reg,
			Queues:   []string{queueName},
			// SQLite is single-writer: one runner claiming every class.
			ClaimAnyClass: true,
			Concurrency:   1,
		}},
		Scheduler: &flywheel.SchedulerConfig{
			DB:          db,
			Client:      flywheel.NewClient(db),
			BackfillCap: backfillCap,
		},
	})
	if err != nil {
		return fmt.Errorf("schedrun: build node: %w", err)
	}
	return node.Run(ctx)
}

// buildRegistry registers the bell, backstop, and tripwire workers over one
// scheduler. When presented is set the tripwire worker runs the
// companion-presented variant (user-channel send suppressed). Every worker is
// registered in both modes — which of them can actually fire is decided by the
// periodics upsertPeriodics reconciles, not by what the registry knows how to
// dispatch, so a mode flip needs no re-registration.
func buildRegistry(sc *scheduler.Scheduler, clock models.Clock, store *storage.Adapter, presented bool) *flywheel.Registry {
	reg := flywheel.NewRegistry()
	flywheel.Register(reg, bellWorker{sc: sc})
	flywheel.Register(reg, bellFallbackWorker{sc: sc, clock: clock})
	flywheel.Register(reg, tripwireWorker{sc: sc, clock: clock, store: store, presented: presented})
	return reg
}

// upsertPeriodics reconciles the bell, evening-backstop, and tripwire periodics
// from the default profile's clock marks (chain.json). It is idempotent by slug,
// so it is safe to call on every daemon boot. Only the fire clock is fixed to the
// default marks; the tripwire's decision resolves the governing profile
// internally.
//
// suppressBell is the evening window's ownership switch, and it moves the bell
// and its backstop in opposite directions on every boot: when the companion owns
// the window the bell is reconciled inactive (so a previously-active bell is
// turned off cleanly) and the backstop is armed to catch a companion that never
// delivers; when it does not, the bell fires as it always has and the backstop
// stands down. Reconciling both flags on every boot is what makes the toggle
// survive a restart in either direction.
func upsertPeriodics(ctx context.Context, db *gorm.DB, store *storage.Adapter, suppressBell bool) error {
	chain, err := store.ReadChainConfig()
	if err != nil {
		return fmt.Errorf("schedrun: read chain config: %w", err)
	}
	bellMark, tripwireMark, err := scheduler.Marks(chain, engine.DefaultProfile)
	if err != nil {
		return fmt.Errorf("schedrun: resolve clock marks: %w", err)
	}
	// The bell mark is parsed once and everything evening-side is derived from
	// the resulting minutes, so the backstop can never disagree with the bell
	// about a mark they both read.
	bellMin, err := markMinutes(bellMark)
	if err != nil {
		return err
	}
	bellCron := cronFromMinutes(bellMin)
	fallbackCron := cronFromMinutes(bellFallbackMinutes(bellMin))
	tripwireCron, err := cronFromHM(tripwireMark)
	if err != nil {
		return err
	}
	if err := flywheel.UpsertPeriodic(ctx, db, flywheel.PeriodicSpec{
		Slug: slugBell, Kind: kindBell, Cron: bellCron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("schedrun: upsert bell periodic: %w", err)
	}
	if err := flywheel.UpsertPeriodic(ctx, db, flywheel.PeriodicSpec{
		Slug: slugBellFallback, Kind: kindBellFallback, Cron: fallbackCron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("schedrun: upsert bell fallback periodic: %w", err)
	}
	if err := flywheel.UpsertPeriodic(ctx, db, flywheel.PeriodicSpec{
		Slug: slugTripwire, Kind: kindTripwire, Cron: tripwireCron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("schedrun: upsert tripwire periodic: %w", err)
	}
	// The two evening flags are set explicitly rather than through the upserts
	// above, because an Active:false upsert cannot express "off": a false Active
	// on a fresh insert is overridden by the periodic row's default-true column,
	// whereas SetPeriodicActive updates the flag unconditionally. The upserts
	// therefore declare the schedule and these two calls declare who owns the
	// window.
	if suppressBell {
		if err := flywheel.SetPeriodicActive(ctx, db, slugBell, false); err != nil {
			return fmt.Errorf("schedrun: deactivate bell periodic: %w", err)
		}
	}
	if err := flywheel.SetPeriodicActive(ctx, db, slugBellFallback, suppressBell); err != nil {
		return fmt.Errorf("schedrun: reconcile bell fallback periodic: %w", err)
	}
	return nil
}

// bellFallbackMinutes derives the evening backstop's mark from the bell it backs
// up, both in minutes since local midnight (usage/commands.md §"The evening
// backstop"): the companion's night cut-off plus a short grace, and never
// earlier than the bell.
//
// Deriving it from the cut-off rather than offsetting the bell is the whole
// point. The companion's bounded catch-up may legitimately deliver a late night
// message right up to that cut-off, so a bell-relative mark would leave a real
// double-send window; past the cut-off the companion refuses to post at all, so
// the two can never race. The clamp covers a chain whose bell is configured past
// the cut-off — there the companion could never deliver at its own mark anyway,
// and a backstop scheduled ahead of the bell it backs up would be nonsense.
func bellFallbackMinutes(bellMin int) int {
	backstop := (companionNightCutoffMin + int(bellFallbackGrace/time.Minute)) % minutesPerDay
	if bellMin > backstop {
		return bellMin
	}
	return backstop
}

// markMinutes parses an "HH:MM" clock mark into minutes since local midnight. A
// malformed mark (wrong shape, out-of-range hour or minute, non-numeric field)
// is rejected rather than silently mis-scheduled.
func markMinutes(hm string) (int, error) {
	parts := strings.Split(hm, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("schedrun: malformed clock mark %q: want HH:MM", hm)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("schedrun: invalid hour in clock mark %q", hm)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("schedrun: invalid minute in clock mark %q", hm)
	}
	return h*60 + m, nil
}

// cronFromMinutes renders minutes since local midnight as a daily 5-field cron
// expression: 1290 -> "30 21 * * *".
func cronFromMinutes(total int) string {
	return fmt.Sprintf("%d %d * * *", total%60, total/60)
}

// cronFromHM turns an "HH:MM" clock mark into a daily 5-field cron expression:
// "21:30" -> "30 21 * * *".
func cronFromHM(hm string) (string, error) {
	total, err := markMinutes(hm)
	if err != nil {
		return "", err
	}
	return cronFromMinutes(total), nil
}

// DefaultDBPath resolves the disposable teeth job-DB path: an explicit override
// wins, then the LUCID_SCHEDULER_DB env override, then the default flywheel.db
// under the OS user-config dir (outside the ~/.lucid Ledger, ADR-0004). It is
// exported so a read-only inspector resolves the exact same path the daemon
// writes to, rather than duplicating the resolution and drifting from it.
func DefaultDBPath(dbPath string) (string, error) {
	if dbPath != "" {
		return dbPath, nil
	}
	if env := os.Getenv(envSchedulerDB); env != "" {
		return env, nil
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("schedrun: resolve user config dir: %w", err)
	}
	return filepath.Join(cfgDir, "lucid", "flywheel.db"), nil
}
