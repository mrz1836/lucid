package cli

import (
	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/companion"
	"github.com/mrz1836/lucid/internal/schedrun"
	"github.com/mrz1836/lucid/internal/schedstatus"
)

// Flag names for `lucid scheduler status`. The two DB overrides mirror the
// daemon's own resolution seam so an operator can point the inspector at the
// exact disposable job store the supervised daemon writes to.
const (
	statusFlagSchedulerDB = "scheduler-db"
	statusFlagCompanionDB = "companion-db"
)

// newHostProbe builds the best-effort host/supervisor probe for a resolved
// scheduler DB path. It is a package var seam — production wires the real
// platform probe ([schedstatus.NewHostProbe], a macOS launchd/Hush inspector or
// the portable all-Unknown default), while a test overrides it with a fake so
// `scheduler status` behaves identically on every platform under test.
//
//nolint:gochecknoglobals // a single injected host-probe seam so the host-dependent status command is deterministic under test
var newHostProbe = schedstatus.NewHostProbe

// newSchedulerStatusCmd wires `lucid scheduler status` — the read-only health
// surface that answers one plain question: "is the autonomous scheduler healthy,
// what fires next, and what happened last?" It aggregates the lucid.json
// companion/provider block, the chain bell/tripwire marks, the two disposable
// job DBs' periodics and recent failures, the companion delivery receipts, and a
// best-effort host/supervisor probe, classifies each against the documented
// verdict thresholds, and renders a calm human report by default or a stable
// `--json` document under the inherited persistent --json flag.
//
// It is deliberately credential-dumb: it reads local config/state/DB/process/log
// metadata only and never constructs the Discord notifier
// ([notify.NewDiscordFromEnv]) nor calls Hush, so it needs no token, socket, or
// secret to report status. The command owns its 3-tier exit code (0 ok / 1 warn
// / 2 error, identical in text and JSON) by returning an [ExitCoder] error after
// the report is rendered, so the output is always emitted first and a health
// cron can gate on the exit code alone.
func newSchedulerStatusCmd() *cobra.Command {
	var schedulerDB, companionDB string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report scheduler health: what fires next, what happened last, and what is broken",
		Long: `status is the read-only health surface for the autonomous Engine
scheduler. It reports the companion enabled/disabled state and its provider and
prompt paths, the chain bell and tripwire times, the teeth and companion
periodics (with their next run and last enqueue), the last companion delivery
receipt per window, a bounded recent-run summary, and a best-effort
host/supervisor probe — then rolls the checks into one verdict.

It never sends, never renews a secret, and never reads a prompt body: it inspects
local config, the disposable job stores, delivery receipts, and process metadata
only. The verdict maps to a 3-tier exit code identical in text and --json output:
0 (ok), 1 (warn), or 2 (error). Host/supervisor checks are best-effort — a signal
this platform cannot inspect is reported "unknown" and never lowers the verdict;
only a positively detected problem does.`,
		Args: cobra.NoArgs,
		Example: `  # Human-readable health summary (run before the morning/night windows).
  lucid scheduler status

  # Machine-readable verdict for a health cron or agent.
  lucid scheduler status --json

  # Inspect explicit disposable job-store paths (default: the daemon's).
  lucid scheduler status --scheduler-db /usr/local/var/lucid/flywheel.db`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSchedulerStatus(cmd, schedulerDB, companionDB)
		},
	}
	cmd.Flags().StringVar(&schedulerDB, statusFlagSchedulerDB, "", "Override the teeth job-store path to inspect (default: LUCID_SCHEDULER_DB or the daemon's default)")
	cmd.Flags().StringVar(&companionDB, statusFlagCompanionDB, "", "Override the companion job-store path to inspect (default: LUCID_COMPANION_DB or the daemon's default)")
	return cmd
}

// runSchedulerStatus is the impure gathering the cobra layer delegates to: boot
// the router (for the companion/provider config and the same Ledger the daemon
// reads), resolve the two disposable job-DB paths exactly as the daemon does
// (flag -> env -> default), gather all local state read-only, run the best-effort
// host probe, classify, and render. It returns nil for an ok verdict or an
// [ExitCoder] error for warn/error — always after rendering, so the report is
// emitted whatever the verdict. A genuine runtime failure (an unresolvable
// Ledger, or an unreadable chain config every scaffold writes) is returned
// unwrapped and maps to the normal runtime exit.
func runSchedulerStatus(cmd *cobra.Command, schedulerDBFlag, companionDBFlag string) error {
	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}

	// Scaffold the engine tree idempotently so chain.json (the bell/tripwire
	// marks the report reads) exists even on a host where only `scheduler status`
	// has ever run — the same lazy engine-tree scaffold every engine-touching
	// command performs. It writes the default chain.json only if absent and never
	// disturbs an existing one.
	if err = r.Store().ScaffoldEngine(); err != nil {
		return err
	}

	// Resolve the disposable job-DB paths through the daemon's own resolver so the
	// inspector reads the exact stores the supervised daemon writes to; the host
	// probe additionally flags a launchd/env path drift against these.
	schedulerDB, err := schedrun.DefaultDBPath(schedulerDBFlag)
	if err != nil {
		return err
	}
	companionDB, err := companion.DefaultDBPath(companionDBFlag)
	if err != nil {
		return err
	}

	inputs, err := schedstatus.Gather(schedstatus.GatherParams{
		Config:      r.Config(),
		Store:       r.Store(),
		SchedulerDB: schedulerDB,
		CompanionDB: companionDB,
		Probe:       newHostProbe(schedulerDB),
	})
	if err != nil {
		return err
	}

	report := schedstatus.Assemble(inputs, clockNow())

	if err := emit(cmd, report, report.TextLines()); err != nil {
		return err
	}

	// The report is now printed; carry the verdict's non-zero code out through the
	// ExitCoder seam so text and --json exit identically without re-printing.
	if code := report.ExitCode(); code != ExitOK {
		return statusExitError{verdict: report.Verdict, code: code}
	}
	return nil
}

// statusExitError carries a scheduler-status verdict's non-zero exit code out through
// cobra's RunE without printing anything itself — the report is already rendered
// and the root command has SilenceErrors set. It implements the [ExitCoder] seam
// that exitCodeForError honors, so a warn verdict exits 1 and an error verdict
// exits 2, identically in text and --json.
type statusExitError struct {
	verdict string
	code    int
}

// Error names the verdict so a stray log line stays legible; it is never printed
// on the normal path.
func (e statusExitError) Error() string { return "scheduler status: " + e.verdict }

// ExitCode returns the verdict's process exit code (1 warn / 2 error).
func (e statusExitError) ExitCode() int { return e.code }
