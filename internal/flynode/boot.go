// Package flynode extracts the flywheel "node" scaffolding shared, verbatim, by
// every Lucid background daemon — the Engine scheduler (schedrun), the companion, the
// workout slot, and the weekly witness report. Each ran its own disposable
// flywheel node with a byte-identical boot spine (open+migrate a job DB,
// scaffold the engine, reconcile periodics, build a single-writer node, run it
// until ctx is canceled) and — for the three that deliver a message — a
// byte-identical send-path reliability sequence ([Fire]). flynode owns that
// scaffolding once so each daemon carries only what actually differs: its
// workers, its periodics, and its per-window delivery policy.
//
// It is delivery-transport- and model-free by construction: it names no
// notifier, provider, or agent type — send, verify, compose, and alert are
// injected as closures — so it can host the Engine's agent-free write path
// without breaching the P9 "no LLM in the write path" guard. purity_test.go
// enforces the absence of any provider/agent/LLM import.
package flynode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	// dbDirPerm is the mode for a disposable job-DB parent directory.
	dbDirPerm = 0o755

	// backfillCap fires only the single most-recent missed bucket on a supervised
	// restart — the bounded catch-up every Lucid daemon shares (ADR-0004) — so a
	// host asleep across a fire time wakes to one late attempt, not a replay.
	backfillCap = 1
)

// ScaffoldStore is the single storage capability [Boot] needs: ensure the engine
// tree exists before the periodics reconcile against it. *storage.Adapter
// satisfies it. Kept this narrow so flynode depends on a capability, not on the
// whole Ledger adapter.
type ScaffoldStore interface {
	ScaffoldEngine() error
}

// BootConfig is everything [Boot] needs that differs between daemons. Registry
// is built by the caller (each daemon's workers and their dependencies differ
// entirely); Reconcile declares the daemon's periodics and may grade a canceled
// boot as a clean stop (as the Engine does); Pkg tags the boot errors with the
// daemon name so a supervised log still says which node failed.
type BootConfig struct {
	Pkg       string                                // daemon name, for error wrapping ("companion", …)
	Queue     string                                // the node's single queue
	DBPath    string                                // resolved job-DB path (see [ResolveDBPath])
	Clock     models.Clock                          // resolved reference clock (see [ResolveClock])
	Store     ScaffoldStore                         // engine scaffolder
	Registry  *flywheel.Registry                    // the caller's worker registry
	Reconcile func(context.Context, *gorm.DB) error // declare/repair the periodics
}

// Boot runs the shared daemon boot spine: put the clock on ctx, create the job
// DB's parent dir, open and migrate the disposable job DB, scaffold the engine,
// reconcile the periodics, build a single-writer flywheel node over the caller's
// registry, and run it until ctx is canceled — a clean drain returns nil. It is
// the text-identical sequence the four daemons each previously carried inline.
func Boot(ctx context.Context, cfg BootConfig) error {
	// One clock drives both the flywheel machinery (via the ctx) and each
	// worker's reference instant, so a test's fixed clock moves them together and
	// production inherits the real wall clock.
	ctx = models.WithClock(ctx, ResolveClock(ctx, cfg.Clock))

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), dbDirPerm); err != nil {
		return fmt.Errorf("%s: create job db dir: %w", cfg.Pkg, err)
	}

	// The disposable job DB is machinery, not truth: gorm's per-statement SQL
	// logging (including the expected record-not-found on a first-boot upsert) is
	// noise the daemon's flywheel slog does not need. Silence it.
	db, err := gorm.Open(sqlite.Open(cfg.DBPath), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return fmt.Errorf("%s: open job db %q: %w", cfg.Pkg, cfg.DBPath, err)
	}
	if err = MigrateJobStore(db); err != nil {
		return fmt.Errorf("%s: migrate job db: %w", cfg.Pkg, err)
	}
	if err = cfg.Store.ScaffoldEngine(); err != nil {
		return fmt.Errorf("%s: scaffold engine: %w", cfg.Pkg, err)
	}
	if err = cfg.Reconcile(ctx, db); err != nil {
		return err
	}

	// One Driver instance shared by the runner and the scheduler: flywheel wants
	// the two halves of a node speaking to the store through the same driver.
	driver := flywheel.NewSQLiteDriver(db)
	node, err := flywheel.NewNode(flywheel.NodeConfig{
		Runners: []flywheel.RunnerConfig{{
			DB:       db,
			Driver:   driver,
			Registry: cfg.Registry,
			Queues:   []string{cfg.Queue},
			// SQLite is single-writer: one runner claiming every class.
			ClaimAnyClass: true,
			Concurrency:   1,
		}},
		Scheduler: &flywheel.SchedulerConfig{
			DB:          db,
			Client:      flywheel.NewClient(db),
			Driver:      driver,
			BackfillCap: backfillCap,
		},
	})
	if err != nil {
		return fmt.Errorf("%s: build node: %w", cfg.Pkg, err)
	}
	return node.Run(ctx)
}

// ResolveClock returns clock when the caller supplied one (tests inject a fixed
// clock), else the clock already on ctx — so production inherits the real wall
// clock and a test's clock drives the machinery and the workers together. It is
// exported so a daemon resolves the same clock it embeds in its workers.
func ResolveClock(ctx context.Context, clock models.Clock) models.Clock {
	if clock != nil {
		return clock
	}
	return models.ClockFrom(ctx)
}

// ResolveDBPath resolves a disposable job-DB path: an explicit override wins,
// then the named environment override, then <user-config-dir>/lucid/<filename>.
// The job DB is disposable machinery, not Ledger truth, so it lives outside
// ~/.lucid by default (ADR-0004). Each daemon's exported DefaultDBPath is a
// one-line wrapper over this, so a read-only inspector resolves the exact same
// path the daemon writes to.
func ResolveDBPath(override, env, filename string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv(env); v != "" {
		return v, nil
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("flynode: resolve user config dir: %w", err)
	}
	return filepath.Join(cfgDir, "lucid", filename), nil
}
