// Package schedrun is the production driver for the Engine's scheduled jobs:
// the standalone-install runtime that fires the evening bell and the morning
// tripwire (which also carries the monthly heartbeat) on the chain's own clocks.
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

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/scheduler"
	"github.com/mrz1836/lucid/internal/storage"
)

// Job runtime identifiers. The queue is shared by both periodics; the kinds
// name the two workers; the slugs are the stable periodic identities that
// UpsertPeriodic reconciles by, so restarting the daemon never duplicates them.
const (
	queueName    = "lucid"
	kindBell     = "lucid_bell"
	kindTripwire = "lucid_tripwire"
	slugBell     = "lucid-bell"
	slugTripwire = "lucid-tripwire"

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
}

// bellArgs / tripwireArgs are the (empty) typed payloads for the two periodics:
// neither job carries input — the decision is a pure function of the Ledger at
// fire time. Their JSON form is the empty object the scheduler enqueues.
type (
	bellArgs     struct{}
	tripwireArgs struct{}
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

// tripwireWorker fires the morning tripwire (which also carries the monthly
// heartbeat). It holds a clock so the reference day is deterministic under test
// and the store so it can honor the per-day idempotency guard.
type tripwireWorker struct {
	sc    *scheduler.Scheduler
	clock models.Clock
	store *storage.Adapter
}

// Kind names the worker dispatched for kindTripwire.
func (tripwireWorker) Kind() string { return kindTripwire }

// Work runs the morning dead-man for `now`. A per-day guard (A5) skips a second
// run in the same wall day — a backstop under the flywheel per-bucket unique key
// so a redundant fire never sends a second L1.
func (w tripwireWorker) Work(ctx context.Context, _ *flywheel.Job[tripwireArgs]) (flywheel.Result, error) {
	now := w.clock.Now(ctx)
	tw, err := w.store.ReadTripwireState()
	if err != nil {
		return flywheel.Result{}, fmt.Errorf("schedrun: read tripwire state: %w", err)
	}
	if tw.LastRunDate == engine.DateString(engine.DateOf(now)) {
		return flywheel.Result{}, nil
	}
	if _, err := w.sc.RunTripwire(now); err != nil {
		return flywheel.Result{}, err
	}
	return flywheel.Result{}, nil
}

// Run opens the disposable job DB, migrates it, registers the bell and tripwire
// workers, reconciles the two periodics from chain.json's clocks, and runs the
// flywheel node until ctx is canceled (SIGINT/SIGTERM upstream). It returns nil
// on a clean drain.
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

	dbPath, err := resolveDBPath(opts.DBPath)
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
	if err = upsertPeriodics(ctx, db, opts.Store); err != nil {
		return err
	}

	sc := scheduler.New(opts.Store, opts.Notifier)
	reg := buildRegistry(sc, clock, opts.Store)

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

// buildRegistry registers the bell and tripwire workers over one scheduler.
func buildRegistry(sc *scheduler.Scheduler, clock models.Clock, store *storage.Adapter) *flywheel.Registry {
	reg := flywheel.NewRegistry()
	flywheel.Register(reg, bellWorker{sc: sc})
	flywheel.Register(reg, tripwireWorker{sc: sc, clock: clock, store: store})
	return reg
}

// upsertPeriodics reconciles the bell and tripwire periodics from the default
// profile's clock marks (chain.json). It is idempotent by slug, so it is safe to
// call on every daemon boot. Only the fire clock is fixed to the default marks;
// the tripwire's decision resolves the governing profile internally.
func upsertPeriodics(ctx context.Context, db *gorm.DB, store *storage.Adapter) error {
	chain, err := store.ReadChainConfig()
	if err != nil {
		return fmt.Errorf("schedrun: read chain config: %w", err)
	}
	bellMark, tripwireMark, err := scheduler.Marks(chain, engine.DefaultProfile)
	if err != nil {
		return fmt.Errorf("schedrun: resolve clock marks: %w", err)
	}
	bellCron, err := cronFromHM(bellMark)
	if err != nil {
		return err
	}
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
		Slug: slugTripwire, Kind: kindTripwire, Cron: tripwireCron, Queue: queueName, Active: true,
	}); err != nil {
		return fmt.Errorf("schedrun: upsert tripwire periodic: %w", err)
	}
	return nil
}

// cronFromHM turns an "HH:MM" clock mark into a daily 5-field cron expression:
// "21:30" -> "30 21 * * *". A malformed mark (wrong shape, out-of-range hour or
// minute, non-numeric field) is rejected rather than silently mis-scheduled.
func cronFromHM(hm string) (string, error) {
	parts := strings.Split(hm, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("schedrun: malformed clock mark %q: want HH:MM", hm)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return "", fmt.Errorf("schedrun: invalid hour in clock mark %q", hm)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return "", fmt.Errorf("schedrun: invalid minute in clock mark %q", hm)
	}
	return fmt.Sprintf("%d %d * * *", m, h), nil
}

// resolveDBPath resolves the job-DB path: an explicit --db wins, then the
// LUCID_SCHEDULER_DB override, then the default under the user config dir. The
// DB is disposable machinery kept outside the ~/.lucid Ledger (ADR-0004).
func resolveDBPath(dbPath string) (string, error) {
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
