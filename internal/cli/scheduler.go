package cli

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/mrz1836/lucid/internal/companion"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/schedrun"
	"github.com/mrz1836/lucid/internal/scheduler"
	"github.com/mrz1836/lucid/internal/storage"
	"github.com/mrz1836/lucid/internal/workout"
)

// schedulerFlagDB is the `--db` flag on `lucid scheduler run`. Centralized so
// the test can read the value back via cmd.Flags().GetString.
const schedulerFlagDB = "db"

// newSchedulerCmd wires `lucid scheduler run` — the composition root for the
// autonomous accountability daemon (ADR-0004, build-plan Stage 6). It joins
// three already-built halves: the Ledger storage adapter, the concrete
// Discord-bot [notify.Discord] transport (built entirely from injected env, so
// the binary stays credential-dumb — ADR-0005), and the go-flywheel job runtime
// in [schedrun]. The daemon fires the evening bell and the morning tripwire, and
// honors bounded missed-fire catch-up on a supervised restart. The write path is
// agent-free: no model is reachable from here.
//
// The parent `scheduler` verb currently exposes only `run`; it exists as a
// group so later scheduled-job subcommands attach without reshaping the tree.
func newSchedulerCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "scheduler",
		Short: "Run the autonomous Engine scheduler (bell, tripwire)",
		Long: `scheduler groups the standalone-install scheduled-job daemon that
delivers the Engine's autonomous accountability sends. It runs under
` + "`hush supervise`" + ` as a launchd sibling of the harness gateway; the
sends themselves are the pre-committed Engine templates and nothing else.`,
		Args: cobra.NoArgs,
	}
	parent.AddCommand(newSchedulerRunCmd())
	// `status` is the read-only, credential-dumb health sibling of `run`: it
	// inspects local state and reports a verdict, never sending or touching a
	// secret. See internal/cli/scheduler_status.go.
	parent.AddCommand(newSchedulerStatusCmd())
	return parent
}

// newSchedulerRunCmd builds the `run` child: parse the optional --db override,
// resolve storage + the env-injected notifier, and drive [schedrun.Run] until a
// SIGINT/SIGTERM (from launchd/hush on stop or drain) cancels the context. A
// startup failure — an unresolved Ledger home or a notifier whose injected
// token/channel env is unset — is reported on stderr and returned so the exit
// code is non-zero and the supervised log names the reason.
func newSchedulerRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the scheduler daemon until interrupted",
		Long: `run starts the durable go-flywheel scheduler and blocks until it is
interrupted (SIGINT/SIGTERM). It reconciles the bell and morning-tripwire
periodics from the active chain profile's clocks and fires them on time; a
daemon killed mid-evening still fires the missed tripwire on its next start
(bounded catch-up). The Discord bot token and the logical user/witness channel
IDs are read from the injected environment (LUCID_HARNESS_TOKEN,
LUCID_USER_CHANNEL_ID, LUCID_WITNESS_CHANNEL_ID); nothing secret lives in the
binary. The job store is disposable machinery kept outside the ~/.lucid Ledger
(override its path with --db or LUCID_SCHEDULER_DB).`,
		Args: cobra.NoArgs,
		Example: `  # Run under hush supervise (the launchd job invokes this).
  lucid scheduler run

  # Point the disposable job store at an explicit path.
  lucid scheduler run --db /usr/local/var/lucid/flywheel.db`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbPath, _ := cmd.Flags().GetString(schedulerFlagDB)
			return runScheduler(cmd.Context(), cmd.ErrOrStderr(), dbPath)
		},
	}
	cmd.Flags().String(schedulerFlagDB, "", "Override the disposable job-store path (default: LUCID_SCHEDULER_DB or a flywheel.db under the OS user-config dir, outside ~/.lucid)")
	return cmd
}

// runScheduler is the pure wiring the cobra layer delegates to: open storage,
// build the env-injected notifier, boot the router (for config + the honest live
// numbers), install the signal-canceled context, and hand off to the flywheel
// driver. When the companion is enabled it presents both user windows, so the
// teeth run with their user-channel send suppressed and the companion node runs
// beside them under one canceled context; when the workout slot is enabled its
// node runs beside them too. When neither companion-class node is enabled only
// the teeth run — byte-for-byte today's behavior. Every startup error is funneled
// through a single "lucid: scheduler: <message>" stderr line (mirroring
// `upgrade`) before being returned, so exitCodeForError still classifies it.
func runScheduler(parent context.Context, stderr io.Writer, dbPath string) error {
	store, err := storage.Open()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "lucid: scheduler: resolve home: %s\n", err)
		return fmt.Errorf("lucid: scheduler: resolve home: %w", err)
	}
	notifier, err := notify.NewDiscordFromEnv()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "lucid: scheduler: %s\n", err)
		return fmt.Errorf("lucid: scheduler: %w", err)
	}

	// Scaffold the Ledger idempotently so the daemon comes up on a fresh host
	// (lucid.json + the trees) before the router reads the config — the same
	// scaffold-then-boot order every stateful command runs.
	if _, err = store.Scaffold(); err != nil {
		_, _ = fmt.Fprintf(stderr, "lucid: scheduler: %s\n", err)
		return fmt.Errorf("lucid: scheduler: %w", err)
	}

	// Boot the router to load lucid.json (the companion gate + provider block)
	// and to serve the companion's honest live numbers from the same projection
	// `lucid metrics --json` exposes. Clip warnings surface once on stderr.
	r := router.New(store)
	warnings, err := r.Boot()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "lucid: scheduler: %s\n", err)
		return fmt.Errorf("lucid: scheduler: %w", err)
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	cfg := r.Config()

	// The supervisor stops the daemon (on shutdown or a managed-upgrade drain)
	// with SIGTERM; an operator uses Ctrl-C. Either cancels the context and the
	// flywheel node(s) drain to a clean return.
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if !cfg.Companion.Enabled && !cfg.Workout.Enabled {
		// Teeth only: bell + tripwire both deliver to the user, exactly as before.
		if err := schedrun.Run(ctx, schedrun.Options{Store: store, Notifier: notifier, DBPath: dbPath}); err != nil {
			_, _ = fmt.Fprintf(stderr, "lucid: scheduler: %s\n", err)
			return fmt.Errorf("lucid: scheduler: %w", err)
		}
		return nil
	}

	// At least one companion-class node runs beside the teeth. When the companion
	// presents both user windows the teeth run with their user-channel send
	// suppressed (the modeless decision, witness L2, and escalation_state
	// persistence all unchanged); the workout slot never
	// suppresses the teeth (it is an additive midday send). They share one
	// context, so a failure in any node drains the process and the supervisor
	// restarts the set together — the teeth are never left suppressed-but-silent.
	if err := runSchedulerWithCompanions(ctx, store, r, notifier, cfg, dbPath); err != nil {
		_, _ = fmt.Fprintf(stderr, "lucid: scheduler: %s\n", err)
		return fmt.Errorf("lucid: scheduler: %w", err)
	}
	return nil
}

// runSchedulerWithCompanions runs the teeth and every enabled companion-class
// node concurrently under one errgroup: the first to fail cancels the rest, so
// the whole process exits and the supervisor restarts the set. The teeth suppress
// their user-channel send only when the companion presents those windows; the
// companion reads the send-free tripwire verdict through its own scheduler (a
// no-op notifier — the verdict read never sends), and the workout slot reads its
// deterministic recommendation through the router's metrics/observation seams.
// All nodes deliver through the same env-injected Discord transport the teeth use.
func runSchedulerWithCompanions(
	ctx context.Context,
	store *storage.Adapter,
	r *router.Router,
	notifier *notify.Discord,
	cfg config.Config,
	dbPath string,
) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return schedrun.Run(gctx, schedrun.Options{
			Store: store, Notifier: notifier, DBPath: dbPath, SuppressUserChannel: cfg.Companion.Enabled,
		})
	})
	if cfg.Companion.Enabled {
		g.Go(func() error {
			return companion.Run(gctx, companion.Options{
				Store:    store,
				Config:   cfg.Companion,
				Provider: cfg.Provider,
				Numbers:  r,
				Verdict:  scheduler.New(store, noopNotifier{}),
				Notifier: notifier,
			})
		})
	}
	if cfg.Workout.Enabled {
		g.Go(func() error {
			// The workout slot keeps its own disposable job DB (LUCID_WORKOUT_DB, or
			// a workout.db under the OS config dir) rather than sharing the teeth's
			// --db file: SQLite is single-writer, so co-locating two flywheel nodes
			// on one file would contend. The default is separate files per node.
			return workout.Run(gctx, workout.Options{
				Store:        store,
				Config:       cfg.Workout,
				Provider:     cfg.Provider,
				Metrics:      r.WorkoutMetrics(),
				Observations: store,
				Injuries:     store,
				Notifier:     notifier,
			})
		})
	}
	return g.Wait()
}
