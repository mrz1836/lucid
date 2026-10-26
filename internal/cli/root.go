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
// and `update` report the running build without a mutable package
// global.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Execute builds the root command, dispatches the subcommand matching
// os.Args, and returns the resolved process exit code. It delegates to the
// shared run seam, which renders a usage error once on stderr. The caller
// (cmd/lucid/main.go) owns os.Exit; Execute performs none so tests can
// exercise it directly.
func Execute(ctx context.Context, bi BuildInfo) int {
	code, _ := run(ctx, newRootCmd(bi), os.Args[1:])
	return code
}

// ExitCoder is an error that carries its own process exit code. A command whose
// success/failure is graded rather than binary (today: `scheduler status`, whose
// health verdict is ok/warn/error) returns one so its tiered exit code reaches
// the process identically in text and --json output, without exitCodeForError
// having to know the command's verdict rules.
type ExitCoder interface {
	error
	// ExitCode returns the process exit code this error maps to.
	ExitCode() int
}

// exitCodeForError maps a returned error to a process exit code. An error that
// carries its own code (an [ExitCoder], e.g. the `scheduler status` verdict) wins
// so a graded command owns its tiered exit; a usage error maps to [ExitUsage],
// found by its type and never by its text, so a runtime failure whose message
// happens to read like misuse is not mistaken for one; everything else (a
// runtime failure, or a breached `validate` gate) maps to [ExitErr].
func exitCodeForError(err error) int {
	if err == nil {
		return ExitOK
	}
	var coder ExitCoder
	if errors.As(err, &coder) {
		return coder.ExitCode()
	}
	var ue *usageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	return ExitErr
}

// newRootCmd constructs the cobra root command fresh on every call
// (no mutable package-level command state). It registers the full
// command spine from ADR-0003 (`init|log|closeout|mode|status|day|
// stats|validate|export`) plus the `version` and `update` verbs added
// in ADR-0007. `stats` is the read-only Ledger-volume sibling of `day`.
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
		// Same idiom as `secret`: a bare `lucid` prints help and exits 0, while
		// a stray word is rejected here as an unknown command so it is typed
		// and reported like any other usage error.
		Args: rootArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.PersistentFlags().Bool(jsonFlag, false, "Emit machine-readable JSON output where supported")

	// Full command spine. Feature verbs are stubs until their build
	// stage lands; version + update are wired now (Stage 0).
	root.AddCommand(newInitCmd())
	root.AddCommand(newLogCmd())
	root.AddCommand(newAttachCmd())
	root.AddCommand(newLinkCmd())
	root.AddCommand(newUnlinkCmd())
	root.AddCommand(newAnnotateCmd())
	root.AddCommand(newCloseoutCmd())
	root.AddCommand(newModeCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newObsCmd())
	root.AddCommand(newReframeCmd())
	root.AddCommand(newGratitudeCmd())
	root.AddCommand(newWheelCmd())
	root.AddCommand(newFocusCmd())
	root.AddCommand(newRepCmd())
	root.AddCommand(newRetroCmd())
	root.AddCommand(newDayCmd())
	root.AddCommand(newStatsCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newExportCmd())
	root.AddCommand(newBackupCmd())
	root.AddCommand(newRestoreCmd())
	root.AddCommand(newVersionCmd(bi))
	root.AddCommand(newSchedulerCmd())
	root.AddCommand(newCompanionCmd())
	root.AddCommand(newWitnessCmd())
	root.AddCommand(newAnchorCmd())
	root.AddCommand(newSecretCmd())
	root.AddCommand(newMetricsCmd())
	root.AddCommand(newStormCmd())
	root.AddCommand(newProfileCmd())
	root.AddCommand(newPersonCmd())
	root.AddCommand(newSelfCmd())
	root.AddCommand(newBootstrapCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newReflectCmd())
	root.AddCommand(newAskCmd())
	root.AddCommand(newWorkoutCmd())
	root.AddCommand(newInjuryCmd())
	root.AddCommand(newEraCmd())
	root.AddCommand(newThreadCmd())
	root.AddCommand(newPetCmd())
	root.AddCommand(newMemoryCmd())
	root.AddCommand(newExcavateCmd())
	root.AddCommand(newRecallCmd())
	root.AddCommand(newGalleryCmd())
	root.AddCommand(newStructureCmd())

	// Self-update (alias: upgrade) plus the passive "a new version is available"
	// notice. Wired last so the banner chains onto the root's run hooks; the
	// resolved build version tells a real release apart from a development build.
	attachUpdateCommand(root, newUpdateConfig(bi.Version))

	// Type usage errors across the finished tree, including the library-built
	// update command, so the root can render each one once with a --help hint.
	installUsageErrors(root)

	return root
}
