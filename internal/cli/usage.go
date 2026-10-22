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

// usageWrappedAnnotation marks a command whose Args validator the tree walk
// has already wrapped, so a second walk leaves it alone.
const usageWrappedAnnotation = "lucid_usage_wrapped"

// installUsageErrors wires usage-error typing into the command tree from the
// root, in one walk. Cobra hands every flag-parse failure to the root's
// flag-error func, which every subcommand inherits (including the
// library-built `update`), and every command that declares an Args validator
// gets it wrapped so its positional, required-flag and flag-group checks are
// typed too. The walk is idempotent, so it can run again after cobra adds its
// default commands.
func installUsageErrors(root *cobra.Command) {
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return newUsageError(cmd, err)
	})
	wrapUsageArgs(root)
}

// wrapUsageArgs wraps the Args validator of c and of every command below it,
// once each. A command without an Args validator keeps cobra's default, which
// accepts any positional. The marker is added to the command's existing
// annotations rather than replacing them, because the library-built `update`
// command is identified by an annotation of its own.
func wrapUsageArgs(c *cobra.Command) {
	if c.Args != nil && c.Annotations[usageWrappedAnnotation] == "" {
		c.Args = usageArgs(c.Args)
		if c.Annotations == nil {
			c.Annotations = map[string]string{}
		}
		c.Annotations[usageWrappedAnnotation] = "true"
	}
	for _, sub := range c.Commands() {
		wrapUsageArgs(sub)
	}
}

// usageArgs types the positional-argument check, then the required-flag and
// flag-group checks, in cobra's own order. Running the flag checks here, at
// the Args stage, types their errors before any run hook fires; cobra's own
// later re-check passes on the same flag state.
func usageArgs(inner cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := inner(cmd, args); err != nil {
			return newUsageError(cmd, err)
		}
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return newUsageError(cmd, err)
		}
		if err := cmd.ValidateFlagGroups(); err != nil {
			return newUsageError(cmd, err)
		}
		return nil
	}
}

// rootArgs rejects a positional at the root in cobra's own words, adding its
// "Did you mean this?" suggestions when there are any. Without it, cobra
// raises an unknown command while resolving the command, before any Args
// validator could type it.
func rootArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if !cmd.DisableSuggestions {
		if cmd.SuggestionsMinimumDistance <= 0 {
			cmd.SuggestionsMinimumDistance = 2 // cobra's own default
		}
		if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
			msg += "\n\nDid you mean this?\n\t" + strings.Join(s, "\n\t")
		}
	}
	return errors.New(msg)
}

// helpTopicAnnotation marks a help command whose topic lookup has already been
// wrapped, so a second call leaves it alone.
const helpTopicAnnotation = "lucid_help_topic_wrapped"

// keepUnknownHelpTopic keeps `lucid help <unknown>` reporting an unknown help
// topic. Cobra's help command looks its topic up with Find, which rejected an
// unknown top-level word only while the root had no Args validator; with
// [rootArgs] in place, Find resolves the word to the root itself. The topic is
// therefore checked here by the command found and the words left over, and
// reported exactly as cobra reports it.
func keepUnknownHelpTopic(root *cobra.Command) {
	for _, c := range root.Commands() {
		if c.Name() != "help" || c.Run == nil || c.Annotations[helpTopicAnnotation] != "" {
			continue
		}
		inner := c.Run
		c.Run = func(cmd *cobra.Command, args []string) {
			found, rest, err := cmd.Root().Find(args)
			if err == nil && found == cmd.Root() && len(rest) > 0 {
				cmd.Printf("Unknown help topic %#q\n", args)
				cobra.CheckErr(cmd.Root().Usage())
				return
			}
			inner(cmd, args)
		}
		if c.Annotations == nil {
			c.Annotations = map[string]string{}
		}
		c.Annotations[helpTopicAnnotation] = "true"
	}
}

// run is the single execution seam: [Execute] and the test helpers all go
// through it, so tests see exactly what a user sees. It executes root with
// args, renders a usage error once on the rejecting command's stderr, and
// returns the process exit code together with the command error.
func run(ctx context.Context, root *cobra.Command, args []string) (int, error) {
	root.SetArgs(args)
	// Cobra adds its default help and completion commands lazily, inside
	// execution. Adding them here first lets the walk wrap them too, and binds
	// the completion scripts to the caller's stdout.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd(args...)
	keepUnknownHelpTopic(root)
	installUsageErrors(root)
	cmd, err := root.ExecuteContextC(ctx)
	// The hidden shell-completion command is created during execution, after
	// the walk, so its missing-argument rejection arrives untyped. It is
	// recognized by the command and the arguments it received, never by text.
	if err != nil && cmd != nil && cmd.Parent() == root &&
		cmd.Name() == cobra.ShellCompRequestCmd && cmd.DisableFlagParsing {
		found, remaining, findErr := root.Find(args)
		if findErr == nil && found == cmd && len(remaining) == 0 {
			err = newUsageError(cmd, err)
		}
	}
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
