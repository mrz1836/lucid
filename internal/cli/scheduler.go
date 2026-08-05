package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/mrz1836/lucid/internal/companion"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/schedrun"
	"github.com/mrz1836/lucid/internal/scheduler"
	"github.com/mrz1836/lucid/internal/storage"
	"github.com/mrz1836/lucid/internal/witnessreport"
	"github.com/mrz1836/lucid/internal/workout"
)

// Flag names shared by the `scheduler` subcommands. schedulerFlagDB is the
// `--db` job-store override carried by both `run` and `reconcile` (same
// resolution in both, so an operator points either at the same store); the
// reconcile pair narrows and softens the repair. Centralized so a test can read
// the values back via cmd.Flags().
const (
	schedulerFlagDB     = "db"
	reconcileFlagSlug   = "slug"
	reconcileFlagNoFire = "no-fire"
)

// reconcileTimeLayout formats a re-armed periodic's next run in the human
// report — minute resolution with the zone named, the same layout
// `scheduler status` prints its next-run column in, so the two read alike.
const reconcileTimeLayout = "2006-01-02 15:04 MST"

// The two job stores `reconcile` spans, named in the report and in the `store`
// field of every re-armed row. They are the same two `scheduler status` inspects,
// which is what makes the documented status -> reconcile pair whole: a report that
// can name a parked companion send is now matched by a lever that reaches it.
const (
	storeEngine    = "engine"
	storeCompanion = "companion"
)

// The two reasons the companion store contributes no scan, said plainly rather
// than left as an absent line — a store silently skipped reads exactly like a
// store that was clean.
const (
	noteCompanionDisabled = "not enabled — skipped"
	noteCompanionMissing  = "not found — start the daemon once to create it"
)

// newSchedulerCmd wires `lucid scheduler run` — the composition root for the
// autonomous accountability daemon (ADR-0004, build-plan Stage 6). It joins
// three already-built halves: the Ledger storage adapter, the concrete
// Discord-bot [notify.Discord] transport (built entirely from injected env, so
// the binary stays credential-dumb — ADR-0005), and the go-flywheel job runtime
// in [schedrun]. The daemon fires the evening bell and the morning tripwire, and
// honors bounded missed-fire catch-up on a supervised restart. The write path is
// agent-free: no model is reachable from here.
//
// The parent `scheduler` verb groups three children — `run` (the daemon),
// `status` (its read-only health surface), and `reconcile` (the sanctioned
// repair for a parked send) — so later scheduled-job subcommands attach without
// reshaping the tree.
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
	// `reconcile` is the repair verb that completes the pair: `status` names a
	// parked send, `reconcile` re-arms it — so the durable job store is never
	// hand-edited (ADR-0007: state the binary owns is repaired only through the
	// binary).
	parent.AddCommand(newSchedulerReconcileCmd())
	// `install`/`uninstall` are the host-install pair: render+lint the supervised
	// launchd + hush artifacts (and, with --apply, load them). Pure CLI ops verbs
	// like `run`/`status` — no router intent (ADR-0007).
	parent.AddCommand(newSchedulerInstallCmd())
	parent.AddCommand(newSchedulerUninstallCmd())
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

// newSchedulerReconcileCmd builds the `reconcile` child: the sanctioned lever
// that re-arms a **parked** scheduled send — one the configuration says should
// be running, while the durable job store has it switched off or its cursor
// frozen in the past. That is the state a transient delivery outage can leave
// behind, and it is silent: nothing is broken, a send simply never comes again.
//
// `run` performs the same pass at startup, so a restart normally repairs drift
// on its own; this is the on-demand lever for when bouncing the daemon is not
// wanted, and the command `scheduler status` names when it reports a parked
// periodic. It is credential-dumb and sends nothing itself: it re-arms the
// periodic, and the daemon's next tick does the sending.
//
// It spans both disposable job stores — the Engine's and the companion's — which
// is the same pair `status` inspects. A repair lever reaching only half of what
// the report can name would leave the other half with a fault and no remedy.
func newSchedulerReconcileCmd() *cobra.Command {
	var slug, dbPath, companionDBPath string
	var noFire bool
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Re-arm a parked scheduled send (the sanctioned repair)",
		Long: `reconcile re-arms a parked periodic: one the configuration says
should be running, while the job store has it inactive or its next run stuck in
the past so the due scan never reaches it again.

The pass covers both disposable job stores — the Engine's and the companion's —
the same two ` + "`scheduler status`" + ` inspects, so the pair is whole: status
names a parked send, reconcile re-arms it.

Whether a send should be running is derived from configuration per slug — the
morning tripwire always, the evening bell when the companion is disabled, the
evening backstop when it is enabled, and the companion's three sends whenever the
companion is enabled — so the pass never fights a deliberate setting: a bell
suppressed because the companion owns the evening send is left suppressed.
Re-arming leaves the stale cursor in place, so the occurrence that was missed is
delivered late rather than never; --no-fire skips it and resumes at the next
scheduled one.

--db still means the Engine store alone, so no existing invocation changes
behavior. With the companion disabled its store is skipped; with the companion
enabled but its store absent the line names it and the pass still succeeds,
unless --companion-db pointed at it explicitly.

It is safe to run any time: idempotent, it creates no periodic, edits no cron
expression, moves no healthy periodic's next run, and sends nothing itself.`,
		Args: cobra.NoArgs,
		Example: `  # Repair everything parked, in both stores.
  lucid scheduler reconcile

  # Just the morning dead-man.
  lucid scheduler reconcile --slug lucid-tripwire

  # Just the morning companion (the slug matches across both stores).
  lucid scheduler reconcile --slug lucid-companion-morning

  # Re-arm, but skip the send that was missed.
  lucid scheduler reconcile --no-fire`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSchedulerReconcile(cmd, slug, dbPath, companionDBPath, noFire)
		},
	}
	cmd.Flags().StringVar(&slug, reconcileFlagSlug, "", "Reconcile one periodic instead of all of them (an unknown slug is a no-op); matches across both stores")
	cmd.Flags().BoolVar(&noFire, reconcileFlagNoFire, false, "Re-arm without delivering the missed occurrence: reset the cursor to the next scheduled run")
	cmd.Flags().StringVar(&dbPath, schedulerFlagDB, "", "Override the Engine job-store path (default: LUCID_SCHEDULER_DB or a flywheel.db under the OS user-config dir, outside ~/.lucid)")
	cmd.Flags().StringVar(&companionDBPath, statusFlagCompanionDB, "", "Override the companion job-store path (default: LUCID_COMPANION_DB or the daemon's default)")
	return cmd
}

// runSchedulerReconcile is the wiring the cobra layer delegates to: boot the
// router for the configuration the guards need (who owns the evening window, and
// whether the companion runs at all), resolve each job store exactly as its own
// daemon does, run the pass over both, and report them together. Each store is
// opened inside its package's ReconcileStore so the command layer never touches a
// database directly and each daemon's own path resolution stays the single source
// of its store's location.
//
// A failure is named on stderr as "lucid: scheduler: <message>" before it is
// returned, mirroring `run`: the root silences cobra's own error printing, and a
// repair lever that exited non-zero without saying what it could not repair
// would be the same silence it exists to end.
func runSchedulerReconcile(cmd *cobra.Command, slug, dbFlag, companionDBFlag string, noFire bool) error {
	r, err := bootedRouter(cmd)
	if err != nil {
		return reconcileError(cmd, err)
	}
	companionEnabled := r.Config().Companion.Enabled

	out := reconcileOutput{Stores: []reconcileStoreLine{}, Reconciled: []reconciledRow{}}

	dbPath, err := schedrun.DefaultDBPath(dbFlag)
	if err != nil {
		return reconcileError(cmd, err)
	}
	report, err := schedrun.ReconcileStore(cmd.Context(), dbPath, schedrun.ReconcileOptions{
		CompanionEnabled: companionEnabled,
		NoFire:           noFire,
		Slug:             slug,
	})
	if err != nil {
		return reconcileError(cmd, err)
	}
	out.add(storeEngine, dbPath, report)

	if err = reconcileCompanionStore(cmd, &out, companionEnabled, companionDBFlag, slug, noFire); err != nil {
		return reconcileError(cmd, err)
	}
	return emit(cmd, out, reconcileLines(out))
}

// reconcileCompanionStore runs the pass over the companion's own job store and
// folds the result into out. It returns an error only for a failure that should
// stop the command.
//
// Three shapes are not failures. A disabled companion has no store to repair, so
// it is skipped and said so. An enabled companion whose store does not exist has
// simply never run here — a real condition worth naming, but not one that should
// discard the Engine repair that already succeeded, so the line reports it and the
// command still exits zero. Passing --companion-db reverses that last case: an
// operator who named the target explicitly gets the missing file as an error,
// exactly as --db has always behaved.
func reconcileCompanionStore(cmd *cobra.Command, out *reconcileOutput, enabled bool, dbFlag, slug string, noFire bool) error {
	if !enabled {
		out.skip(storeCompanion, "", noteCompanionDisabled)
		return nil
	}
	dbPath, err := companion.DefaultDBPath(dbFlag)
	if err != nil {
		return err
	}
	report, err := companion.ReconcileStore(cmd.Context(), dbPath, companion.ReconcileOptions{
		NoFire: noFire,
		Slug:   slug,
	})
	switch {
	case err == nil:
		out.add(storeCompanion, dbPath, report)
		return nil
	case errors.Is(err, flynode.ErrJobStoreMissing) && dbFlag == "":
		out.skip(storeCompanion, dbPath, noteCompanionMissing)
		return nil
	default:
		return err
	}
}

// reconcileStoreLine is one job store the pass covered: which daemon it belongs
// to, the path that was resolved for it, and — when it contributed no scan — the
// short reason why. A store that was skipped is reported rather than omitted: an
// absent line reads exactly like a store that was clean.
type reconcileStoreLine struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Note string `json:"note,omitempty"`
}

// reconciledRow is one re-armed periodic tagged with the store it came from, so a
// report spanning two stores says where each repair landed. The embedded struct is
// anonymous, so its fields stay flat in JSON and every key a --json consumer read
// before is unchanged.
type reconciledRow struct {
	flynode.ReconciledPeriodic

	Store string `json:"store"`
}

// reconcileOutput is the whole pass as both surfaces render it: the stores
// covered, every periodic re-armed across them, and the total inspected.
//
// The pre-existing keys keep their names and meanings — reconciled[] rows still
// carry slug/was_active/next_run/fires_missed, and scanned is still the count of
// definitions inspected, now summed across stores — so automation written against
// the single-store shape still parses.
type reconcileOutput struct {
	Stores     []reconcileStoreLine `json:"stores"`
	Reconciled []reconciledRow      `json:"reconciled"`
	Scanned    int                  `json:"scanned"`
}

// add folds one store's report into the output, tagging its rows with the store.
func (o *reconcileOutput) add(name, path string, report flynode.ReconcileReport) {
	o.Stores = append(o.Stores, reconcileStoreLine{Name: name, Path: path})
	for _, p := range report.Reconciled {
		o.Reconciled = append(o.Reconciled, reconciledRow{Store: name, ReconciledPeriodic: p})
	}
	o.Scanned += report.Scanned
}

// skip records a store that contributed no scan, with the reason.
func (o *reconcileOutput) skip(name, path, note string) {
	o.Stores = append(o.Stores, reconcileStoreLine{Name: name, Path: path, Note: note})
}

// reconcileError prints one "lucid: scheduler: <message>" line to stderr and
// returns the error unchanged, so the exit code and the explanation always
// travel together.
func reconcileError(cmd *cobra.Command, err error) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "lucid: scheduler: %s\n", err)
	return err
}

// reconcileLines renders the pass as the calm, human-first output the command
// prints by default. It always names every resolved job store — a path drift is
// the difference between repairing the daemon's schedule and repairing nothing —
// and says plainly when there was nothing to repair, so a healthy run reads as
// an answer rather than as silence.
func reconcileLines(out reconcileOutput) []string {
	lines := make([]string, 0, len(out.Stores)+len(out.Reconciled)+2)
	for _, s := range out.Stores {
		lines = append(lines, storeLine(s))
	}
	lines = append(lines, fmt.Sprintf("Scanned: %d periodic(s)", out.Scanned))
	if len(out.Reconciled) == 0 {
		return append(lines, "Nothing parked — no periodic needed re-arming.")
	}
	lines = append(lines, fmt.Sprintf("Re-armed: %d", len(out.Reconciled)))
	for _, p := range out.Reconciled {
		lines = append(lines, fmt.Sprintf("  [%s] %s: %s; next run %s%s",
			p.Store, p.Slug, wasState(p.WasActive), p.NextRun.Format(reconcileTimeLayout), missedSuffix(p.FiresMissed)))
	}
	return lines
}

// storeLine names one store on its own line, keeping the resolved path visible
// even when a note explains why the store contributed nothing.
func storeLine(s reconcileStoreLine) string {
	label := fmt.Sprintf("Job store (%s): ", s.Name)
	switch {
	case s.Note == "":
		return label + s.Path
	case s.Path == "":
		return label + s.Note
	default:
		return label + s.Path + " — " + s.Note
	}
}

// wasState names the state the periodic was repaired from, so the report says
// what was actually wrong rather than only that something was.
func wasState(wasActive bool) string {
	if wasActive {
		return "was active with a stuck next run"
	}
	return "was inactive"
}

// missedSuffix marks whether the occurrence that was missed is still coming, so
// the operator knows whether to expect a late send.
func missedSuffix(firesMissed bool) string {
	if firesMissed {
		return " (the missed occurrence fires on the next tick)"
	}
	return ""
}

// runScheduler is the pure wiring the cobra layer delegates to: open storage,
// build the env-injected notifier, boot the router (for config + the honest live
// numbers), install the signal-canceled context, and hand off to the flywheel
// driver. When the companion is enabled it presents both user windows, so the
// Engine runs with its user-channel send suppressed and the companion node runs
// beside it under one canceled context; when the workout slot is enabled its
// node runs beside it too; when the weekly witness report is enabled its node
// runs beside it too. When no companion-class node is enabled only the Engine
// runs — byte-for-byte today's behavior. Every startup error is funneled through a
// single "lucid: scheduler: <message>" stderr line (mirroring `upgrade`) before
// being returned, so exitCodeForError still classifies it.
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

	if !cfg.Companion.Enabled && !cfg.Workout.Enabled && !cfg.WitnessReport.Enabled {
		// Engine only: bell + tripwire both deliver to the user, exactly as before.
		if err := schedrun.Run(ctx, schedrun.Options{Store: store, Notifier: notifier, DBPath: dbPath}); err != nil {
			_, _ = fmt.Fprintf(stderr, "lucid: scheduler: %s\n", err)
			return fmt.Errorf("lucid: scheduler: %w", err)
		}
		return nil
	}

	// At least one companion-class node runs beside the Engine. When the companion
	// presents both user windows the Engine runs with its user-channel send
	// suppressed (the modeless decision, witness L2, and escalation_state
	// persistence all unchanged); the workout slot never
	// suppresses the Engine (it is an additive midday send). They share one
	// context, so a failure in any node drains the process and the supervisor
	// restarts the set together — the Engine is never left suppressed-but-silent.
	if err := runSchedulerWithCompanions(ctx, store, r, notifier, cfg, dbPath); err != nil {
		_, _ = fmt.Fprintf(stderr, "lucid: scheduler: %s\n", err)
		return fmt.Errorf("lucid: scheduler: %w", err)
	}
	return nil
}

// runSchedulerWithCompanions runs the Engine and every enabled companion-class
// node concurrently under one errgroup: the first to fail cancels the rest, so
// the whole process exits and the supervisor restarts the set. The Engine suppresses
// its user-channel send only when the companion presents those windows; the
// companion reads the send-free tripwire verdict through its own scheduler (a
// no-op notifier — the verdict read never sends), the workout slot reads its
// deterministic recommendation through the router's metrics/observation seams,
// and the weekly witness report reads the same honest metrics projection plus the
// engine day records for its 7-day window. All nodes deliver through the same
// env-injected Discord transport the Engine uses.
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
			// a workout.db under the OS config dir) rather than sharing the Engine's
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
	if cfg.WitnessReport.Enabled {
		g.Go(func() error {
			// The weekly report keeps its own disposable job DB
			// (LUCID_WITNESS_REPORT_DB, or a witness-report.db under the OS config
			// dir), separate from the Engine/companion/workout files for the same
			// single-writer reason. It reads the honest numbers from the router and
			// the engine day records from the store; it never suppresses the Engine
			// (an additive witness-channel send, like the workout slot).
			return witnessreport.Run(gctx, witnessreport.Options{
				Store:    store,
				Config:   cfg.WitnessReport,
				Provider: cfg.Provider,
				Numbers:  r,
				Records:  store,
				Notifier: notifier,
			})
		})
	}
	return g.Wait()
}
