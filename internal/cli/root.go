// Package cli is the cobra command tree for the lucid binary. It maps
// user-facing commands one-to-one onto router intents (ADR-0003,
// ADR-0007); no product logic lives here beyond dispatch, output
// shaping, and the self-upgrade wiring. Feature commands that a later
// build stage fills in are registered as explicit stubs so the command
// spine is complete and discoverable from day one.
package cli

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"
)

// Exit codes returned by [Execute]. Kept small and stable so scripts
// and the supervised-ops layer can branch on them.
const (
	// ExitOK is returned on success.
	ExitOK = 0
	// ExitErr is returned for any runtime error.
	ExitErr = 1
	// ExitUsage is returned for a flag-parse / usage error.
	ExitUsage = 2
)

// errModeNotAccepted is returned when `lucid mode` declined the declaration
// (an invalid mode name, or a declaration made after the bell). The fixed
// user copy is already printed; this sentinel just maps to a non-zero exit so
// a script never reads a rejected declaration as success.
var errModeNotAccepted = errors.New("lucid: mode declaration not accepted")

// BuildInfo carries the build metadata injected into cmd/lucid/main.go
// via ldflags. It is threaded through the command tree so `version`
// and `upgrade` report the running build without a mutable package
// global.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Execute builds the root command, dispatches the subcommand matching
// os.Args, and returns the resolved process exit code. The caller
// (cmd/lucid/main.go) owns os.Exit; Execute performs none so tests can
// exercise it directly.
func Execute(ctx context.Context, bi BuildInfo) int {
	root := newRootCmd(bi)
	root.SetArgs(os.Args[1:])
	if err := root.ExecuteContext(ctx); err != nil {
		return exitCodeForError(err)
	}
	return ExitOK
}

// exitCodeForError maps a returned error to a process exit code.
// Flag/usage errors from cobra map to [ExitUsage]; everything else
// (a runtime failure, or a breached `validate` gate) maps to [ExitErr].
func exitCodeForError(err error) int {
	if err == nil {
		return ExitOK
	}
	if isUsageError(err) {
		return ExitUsage
	}
	return ExitErr
}

// newRootCmd constructs the cobra root command fresh on every call
// (no mutable package-level command state). It registers the full
// command spine from ADR-0003 (`init|log|closeout|mode|status|day|
// validate|export`) plus the `version` and `upgrade` verbs added in
// ADR-0007.
func newRootCmd(bi BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:   "lucid",
		Short: "lucid — a local-first personal operating system",
		Long: `lucid is a local-first companion with two cooperating subsystems:
the Mirror (capture → structure → recall) and the Engine (one committed
daily practice with real accountability), both writing one user-owned,
append-only Ledger under ~/.lucid/.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().Bool(jsonFlag, false, "Emit machine-readable JSON output where supported")

	// Full command spine. Feature verbs are stubs until their build
	// stage lands; version + upgrade are wired now (Stage 0).
	root.AddCommand(newInitCmd())
	root.AddCommand(newLogCmd())
	root.AddCommand(newCloseoutCmd())
	root.AddCommand(newModeCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newObsCmd())
	root.AddCommand(newDayCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newExportCmd())
	root.AddCommand(newVersionCmd(bi))
	root.AddCommand(newUpgradeCmd(bi))
	root.AddCommand(newSchedulerCmd())
	root.AddCommand(newAnchorCmd())
	root.AddCommand(newMetricsCmd())

	return root
}

// isUsageError reports whether err looks like a cobra flag-parse or
// unknown-command error, which should map to [ExitUsage] rather than a
// generic runtime failure.
func isUsageError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand", "invalid argument", "flag needs an argument", "required flag"} {
		if containsFold(msg, p) {
			return true
		}
	}
	return false
}
