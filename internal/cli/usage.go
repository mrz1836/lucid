package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// usageError marks an invocation the command rejected: an unknown flag, a
// malformed flag value, or another misuse the caller must fix before the
// command can run. It carries the rejecting command so the root can name it
// and point at its help, and it is how [exitCodeForError] tells a usage error
// apart from a runtime failure — by type, never by message text.
type usageError struct {
	cmd *cobra.Command
	err error
}

// Error returns the underlying reason unchanged; the root adds the command
// path and the --help hint when it renders the error.
func (e *usageError) Error() string { return e.err.Error() }

// Unwrap exposes the underlying reason to errors.Is and errors.As.
func (e *usageError) Unwrap() error { return e.err }

// newUsageError types err as a usage error raised by cmd. A nil err stays nil,
// and an error that is already a usage error is returned unchanged, so a
// reason is never wrapped (or rendered) twice.
func newUsageError(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	var ue *usageError
	if errors.As(err, &ue) {
		return err
	}
	return &usageError{cmd: cmd, err: err}
}

// installUsageErrors wires usage-error typing into the command tree from the
// root. Cobra hands every flag-parse failure to the root's flag-error func,
// which every subcommand inherits (including the library-built `update`), so
// one installation types them all.
func installUsageErrors(root *cobra.Command) {
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return newUsageError(cmd, err)
	})
}

// run is the single execution seam: [Execute] and the test helpers all go
// through it, so tests see exactly what a user sees. It executes root with
// args, renders a usage error once on the rejecting command's stderr, and
// returns the process exit code together with the command error.
func run(ctx context.Context, root *cobra.Command, args []string) (int, error) {
	root.SetArgs(args)
	installUsageErrors(root)
	err := root.ExecuteContext(ctx)
	reportUsageError(err)
	return exitCodeForError(err), err
}

// reportUsageError renders a usage error once on the rejecting command's
// stderr as "<command path>: <reason>" followed by a pointer to that command's
// help. The path is the canonical one, so an alias reports under the command's
// real name. Any other error is left to whoever already reported it: the root
// silences cobra, and a runtime failure names itself where it is raised.
func reportUsageError(err error) {
	var ue *usageError
	if !errors.As(err, &ue) {
		return
	}
	path := ue.cmd.CommandPath()
	w := ue.cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(w, "%s: %s\n", path, strings.TrimRight(ue.err.Error(), "\n"))
	_, _ = fmt.Fprintf(w, "Run '%s --help' for usage.\n", path)
}
