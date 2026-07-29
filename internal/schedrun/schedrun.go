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
	"time"

	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"gorm.io/gorm"

	"github.com/mrz1836/lucid/internal/clockmark"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/flynode"
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

	// backfillCap is the bounded missed-fire catch-up the flynode boot spine
	// applies (a single most-recent bucket, ADR-0004). It is mirrored here only
	// so the in-process test rig builds its scheduler with the same cap.
	backfillCap = 1

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
// and tripwire workers, reconciles their periodics from chain.json's clocks,
// re-arms any that were left parked, and runs the flywheel node until ctx is
// canceled (SIGINT/SIGTERM upstream). It returns nil on a clean drain.
//
// The startup re-arm is the schedule's own durability guarantee: a periodic that
// should be running but is found switched off or with a frozen cursor is
// repaired on the spot and its missed occurrence fires, so an outage costs at
// most one restart rather than a send that silently never comes again
// (engine-module.md §"Durability of the schedule itself").
func Run(ctx context.Context, opts Options) error {
	if opts.Store == nil {
		return errNoStore
	}
	if opts.Notifier == nil {
		return errNoNotifier
	}

	clock := flynode.ResolveClock(ctx, opts.Clock)

	dbPath, err := DefaultDBPath(opts.DBPath)
	if err != nil {
		return err
	}

	sc := scheduler.New(opts.Store, opts.Notifier)
	reg := buildRegistry(sc, clock, opts.Store, opts.SuppressUserChannel)

	return flynode.Boot(ctx, flynode.BootConfig{
		Pkg:      "schedrun",
		Queue:    queueName,
		DBPath:   dbPath,
		Clock:    clock,
		Store:    opts.Store,
		Registry: reg,
		Reconcile: func(ctx context.Context, db *gorm.DB) error {
			if err := upsertPeriodics(ctx, db, opts.Store, opts.SuppressUserChannel); err != nil {
				return startupErr(ctx, err)
			}
			// Seeding declares the schedule; this pass repairs it. They are not the
			// same job: an upsert restores each periodic's active flag but
			// deliberately preserves its cursor, so a periodic whose next run was
			// left frozen in the past would come back up still unreachable by the
			// due scan. Running the same pass `lucid scheduler reconcile` exposes —
			// rather than a second, boot-only notion of "parked" — is what keeps the
			// daemon and the command agreeing on what a healthy schedule looks like.
			// It is a no-op on a healthy store, and the intended-active guard means
			// it never re-arms a suppressed bell.
			if _, err := Reconcile(ctx, db, ReconcileOptions{CompanionEnabled: opts.SuppressUserChannel}); err != nil {
				return startupErr(ctx, err)
			}
			return nil
		},
	})
}

// startupErr grades a failure from the boot sequence: a stop signal that lands
// mid-startup aborts the in-flight DB work with a context error, and that is an
// ordinary shutdown, not a failure. Reporting it as one would put a spurious
// error line in the supervised log every time the daemon is stopped or restarted
// during its first moments — the drain path a supervisor exercises constantly.
// A genuine startup failure (a malformed clock mark, an unusable job store) has
// no canceled context and still surfaces.
//
//nolint:nilerr // discarding the error is the point: a canceled boot is a stop request, and node.Run reports the same clean nil for a canceled drain
func startupErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
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
	return max(bellMin, backstop)
}

// markMinutes parses an "HH:MM" clock mark into minutes since local midnight. A
// malformed mark (wrong shape, out-of-range hour or minute, non-numeric field)
// is rejected rather than silently mis-scheduled.
func markMinutes(hm string) (int, error) {
	mark, err := clockmark.Parse(hm)
	if err != nil {
		return 0, fmt.Errorf("schedrun: %w", err)
	}
	return mark.Minutes(), nil
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
	return flynode.ResolveDBPath(dbPath, envSchedulerDB, "flywheel.db")
}
