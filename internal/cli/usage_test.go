package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// assertUsageErrorOnce asserts stderr holds exactly one rendered usage error
// for the command at path: it opens with "<path>: <reason>", closes with the
// "Run '<path> --help' for usage." hint, and neither the error line nor the
// hint appears twice.
func assertUsageErrorOnce(t *testing.T, stderr, path string) {
	t.Helper()
	hint := "Run '" + path + " --help' for usage.\n"
	assert.True(t, strings.HasPrefix(stderr, path+": "), "stderr does not open with %q: %q", path+": ", stderr)
	assert.True(t, strings.HasSuffix(stderr, hint), "stderr does not close with %q: %q", hint, stderr)
	assert.Equal(t, 1, strings.Count(stderr, "for usage."), "hint not printed exactly once: %q", stderr)
	assert.Equal(t, 1, strings.Count(stderr, path+": "), "error line not printed exactly once: %q", stderr)
}

// usageProbeCommands returns every command in a freshly built tree, depth
// first, root included. It adds cobra's default help and completion commands
// before installing the usage-error wiring, mirroring what run does before a
// real dispatch, so the probe tree matches the one a user reaches.
func usageProbeCommands(t *testing.T) []*cobra.Command {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	installUsageErrors(root)

	var cmds []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		cmds = append(cmds, c)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return cmds
}

// commandArgs returns the arguments that dispatch to c from the root.
func commandArgs(c *cobra.Command) []string {
	return strings.Fields(c.CommandPath())[1:]
}

// findUsageProbe returns the command at path in cmds, failing the test when
// the tree has no such command.
func findUsageProbe(t *testing.T, cmds []*cobra.Command, path string) *cobra.Command {
	t.Helper()
	for _, c := range cmds {
		if c.CommandPath() == path {
			return c
		}
	}
	require.FailNow(t, "command not found in the tree", "path %q", path)
	return nil
}

// TestUsageError_FlagErrors asserts a flag-parse error exits with the usage
// code, leaves stdout empty, and is printed once on stderr under the failing
// command's canonical path with a pointer to its help.
func TestUsageError_FlagErrors(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	cases := []struct {
		args []string
		path string
		want string
	}{
		{
			args: []string{"status", "--bogus"},
			path: "lucid status",
			want: "lucid status: unknown flag: --bogus\nRun 'lucid status --help' for usage.\n",
		},
		{
			args: []string{"update", "--bogus"},
			path: "lucid update",
			want: "lucid update: unknown flag: --bogus\nRun 'lucid update --help' for usage.\n",
		},
		{
			args: []string{"upgrade", "--bogus"},
			path: "lucid update",
			want: "lucid update: unknown flag: --bogus\nRun 'lucid update --help' for usage.\n",
		},
		{
			args: []string{"--bogus"},
			path: "lucid",
			want: "lucid: unknown flag: --bogus\nRun 'lucid --help' for usage.\n",
		},
		{
			args: []string{"completion", "--bogus"},
			path: "lucid completion",
			want: "lucid completion: unknown flag: --bogus\nRun 'lucid completion --help' for usage.\n",
		},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, tc.args...)

			assert.Equal(t, ExitUsage, exitCodeForError(err))
			assert.Empty(t, stdout)
			assertUsageErrorOnce(t, stderr, tc.path)
			assert.Equal(t, tc.want, stderr)
			assert.Equal(t, 0, strings.Count(stderr, "lucid: update:"), "stderr = %q", stderr)
		})
	}
}

// TestUsageError_EveryCommandUnknownFlag walks the live command tree and
// asserts an unknown flag on every command is reported once under that
// command's path. Flag parsing fails before any hook or run function, so the
// walk has no side effects, and a new command is covered automatically.
func TestUsageError_EveryCommandUnknownFlag(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	visited := map[string]bool{}
	for _, c := range usageProbeCommands(t) {
		if c.DisableFlagParsing {
			continue
		}
		path := c.CommandPath()
		visited[path] = true
		t.Run(path, func(t *testing.T) {
			args := append(commandArgs(c), "--lucid-no-such-flag")
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)

			assert.Equal(t, ExitUsage, exitCodeForError(err), "err = %v", err)
			assert.Empty(t, stdout)
			assertUsageErrorOnce(t, stderr, path)
			assert.Equal(t, 0, strings.Count(stderr, "lucid: update:"), "stderr = %q", stderr)
		})
	}

	for _, anchor := range []string{"lucid status", "lucid update"} {
		assert.True(t, visited[anchor], "the walk did not visit %q", anchor)
	}
}

// TestUsageError_EmitErrSkipsUsageErrors asserts emitErr still prints a plain
// command error but leaves a usage error to the root, returning either
// unchanged, so a usage error is never printed twice.
func TestUsageError_EmitErrSkipsUsageErrors(t *testing.T) {
	cmd := &cobra.Command{Use: "probe"}
	var buf bytes.Buffer
	cmd.SetErr(&buf)

	plain := errors.New("plain failure")
	got := emitErr(cmd, plain)
	assert.Same(t, plain, got)
	assert.Equal(t, "plain failure\n", buf.String())

	buf.Reset()
	typed := newUsageError(cmd, errors.New("bad flag"))
	got = emitErr(cmd, typed)
	assert.Same(t, typed, got)
	assert.Empty(t, buf.String())

	require.NoError(t, emitErr(cmd, nil))
	assert.Empty(t, buf.String())
}

// TestUsageError_JSONKeepsStdoutEmpty asserts a usage error under --json still
// goes to stderr and leaves stdout, which carries JSON only, empty.
func TestUsageError_JSONKeepsStdoutEmpty(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	cases := []struct {
		args []string
		path string
	}{
		{args: []string{"status", "--json", "--bogus"}, path: "lucid status"},
		{args: []string{"update", "--json", "extra"}, path: "lucid update"},
		{args: []string{"stats", "--json", "--last", "0"}, path: "lucid stats"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			withClock(t, afternoon())
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, tc.args...)

			assert.Equal(t, ExitUsage, exitCodeForError(err))
			assert.Empty(t, stdout)
			assertUsageErrorOnce(t, stderr, tc.path)
		})
	}
}

// TestUsageError_ArgErrors asserts a positional-argument error exits with the
// usage code, leaves stdout empty, and is printed once under the rejecting
// command's canonical path, including on the library-built update command and
// cobra's default completion commands.
func TestUsageError_ArgErrors(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	cases := []struct {
		args []string
		path string
	}{
		{args: []string{"status", "extra"}, path: "lucid status"},
		{args: []string{"update", "extra"}, path: "lucid update"},
		{args: []string{"upgrade", "extra"}, path: "lucid update"},
		{args: []string{"storm"}, path: "lucid storm"},
		{args: []string{"secret", "extra"}, path: "lucid secret"},
		{args: []string{"version", "extra"}, path: "lucid version"},
		{args: []string{"completion", "bash", "extra"}, path: "lucid completion bash"},
		{args: []string{"structure", "a", "b"}, path: "lucid structure"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, tc.args...)

			assert.Equal(t, ExitUsage, exitCodeForError(err), "err = %v", err)
			assert.Empty(t, stdout)
			assertUsageErrorOnce(t, stderr, tc.path)
			assert.Equal(t, 0, strings.Count(stderr, "lucid: update:"), "stderr = %q", stderr)
		})
	}

	t.Run("update extra renders in full", func(t *testing.T) {
		_, stderr, _ := runRoot(t, BuildInfo{Version: "dev"}, "update", "extra")
		assert.Equal(t, "lucid update: unknown command \"extra\" for \"lucid update\"\n"+
			"Run 'lucid update --help' for usage.\n", stderr)
	})
}

// TestUsageError_RequiredAndExclusiveFlags asserts a missing required flag or
// a conflicting flag combination is typed at the args stage and reported once
// with the hint, while the flag declarations themselves stay in place.
func TestUsageError_RequiredAndExclusiveFlags(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	const (
		required  = "required flag(s)"
		exclusive = "none of the others can be"
	)
	cases := []struct {
		args   []string
		path   string
		reason string
	}{
		{args: []string{"link", "a"}, path: "lucid link", reason: required},
		{args: []string{"annotate", "a", "--to", "media:x"}, path: "lucid annotate", reason: required},
		{args: []string{"retro", "import"}, path: "lucid retro import", reason: required},
		{args: []string{"companion", "fire", "--deliver", "--dry-run"}, path: "lucid companion fire", reason: exclusive},
		{args: []string{"recall", "--era", "a", "--thread", "b"}, path: "lucid recall", reason: exclusive},
		{args: []string{"reflect", "week", "--week", "--days", "3"}, path: "lucid reflect week", reason: exclusive},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, tc.args...)

			assert.Equal(t, ExitUsage, exitCodeForError(err), "err = %v", err)
			assert.Empty(t, stdout)
			assertUsageErrorOnce(t, stderr, tc.path)
			assert.Equal(t, 1, strings.Count(stderr, tc.reason), "stderr = %q", stderr)
		})
	}
}

// TestUsageError_RootUnknownCommand asserts a stray word at the root is
// reported once as an unknown command, with cobra's suggestions when there are
// any, while a bare `lucid` still prints help and succeeds.
func TestUsageError_RootUnknownCommand(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	t.Run("bogus", func(t *testing.T) {
		stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "bogus")

		assert.Equal(t, ExitUsage, exitCodeForError(err))
		assert.Empty(t, stdout)
		assert.True(t, strings.HasPrefix(stderr, `lucid: unknown command "bogus" for "lucid"`), "stderr = %q", stderr)
		assert.True(t, strings.HasSuffix(stderr, "Run 'lucid --help' for usage.\n"), "stderr = %q", stderr)
		assertUsageErrorOnce(t, stderr, "lucid")
	})

	t.Run("suggestions", func(t *testing.T) {
		stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "stauts")

		assert.Equal(t, ExitUsage, exitCodeForError(err))
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "Did you mean this?")
		assert.Contains(t, stderr, "\tstatus")
		assertUsageErrorOnce(t, stderr, "lucid")
	})

	t.Run("bare lucid prints help", func(t *testing.T) {
		stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"})

		require.NoError(t, err)
		assert.Equal(t, ExitOK, exitCodeForError(err))
		assert.Contains(t, stdout, "Available Commands")
		assert.Empty(t, stderr)
	})
}

// TestUsageError_HelpTopics asserts the root's own Args validator leaves the
// help command's topic lookup as it was: an unknown top-level topic is still
// reported as one (once, even when the wrapping is installed twice), and known
// topics still print their help. Cobra writes the report with Printf, which
// goes to the root's output writer when one is set (stderr when none is), so
// these tests read it from stdout; none of it is a usage error.
func TestUsageError_HelpTopics(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	t.Run("unknown topic", func(t *testing.T) {
		stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "help", "bogus")

		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(stdout, "Unknown help topic [`bogus`]\n"), "stdout = %q", stdout)
		assert.Equal(t, 1, strings.Count(stdout, "Unknown help topic"))
		assert.Contains(t, stdout, "Available Commands")
		assert.NotContains(t, stdout, "for usage.")
		assert.Empty(t, stderr)
	})

	t.Run("wrapped twice reports once", func(t *testing.T) {
		root := newRootCmd(BuildInfo{Version: "dev"})
		root.InitDefaultHelpCmd()
		keepUnknownHelpTopic(root) // run wraps it again; the annotation stops a second layer
		var out, errBuf bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errBuf)

		code, err := run(context.Background(), root, []string{"help", "bogus"})

		require.NoError(t, err)
		assert.Equal(t, ExitOK, code)
		assert.Equal(t, 1, strings.Count(out.String(), "Unknown help topic"))
		assert.Empty(t, errBuf.String())
	})

	t.Run("known topic", func(t *testing.T) {
		stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "help", "status")

		require.NoError(t, err)
		assert.Contains(t, stdout, "lucid status")
		assert.NotContains(t, stdout, "Unknown help topic")
		assert.Empty(t, stderr)
	})

	t.Run("no topic", func(t *testing.T) {
		stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "help")

		require.NoError(t, err)
		assert.Contains(t, stdout, "Available Commands")
		assert.NotContains(t, stdout, "Unknown help topic")
		assert.Empty(t, stderr)
	})
}

// TestUsageError_HiddenCompletionArgs asserts cobra's hidden shell-completion
// command, which cobra creates during execution (after the tree walk), keeps
// the usage exit code when called with no command line and reports it once
// under its canonical name, while a real completion request still returns the
// completion protocol without any usage output.
func TestUsageError_HiddenCompletionArgs(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	directive := regexp.MustCompile(`(^|\n):\d+\n$`)
	for _, name := range []string{cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd} {
		t.Run(name+" bare", func(t *testing.T) {
			root := newRootCmd(BuildInfo{Version: "dev"})
			var out, errBuf bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errBuf)

			code, err := run(context.Background(), root, []string{name})

			var ue *usageError
			require.ErrorAs(t, err, &ue)
			assert.Equal(t, ExitUsage, code)
			assert.Empty(t, out.String())
			assert.Equal(t, "lucid __complete: requires at least 1 arg(s), only received 0\n"+
				"Run 'lucid __complete --help' for usage.\n", errBuf.String())
		})

		t.Run(name+" valid request", func(t *testing.T) {
			root := newRootCmd(BuildInfo{Version: "dev"})
			var out, errBuf bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errBuf)

			code, err := run(context.Background(), root, []string{name, ""})

			require.NoError(t, err)
			assert.Equal(t, ExitOK, code)
			assert.Regexp(t, directive, out.String())
			assert.NotContains(t, errBuf.String(), "for usage.")
			assert.NotContains(t, errBuf.String(), "lucid __complete: ")
		})
	}
}

// TestUsageError_EveryCommandExtraPositionals walks the live command tree and
// asserts extra positionals are reported once under the rejecting command's
// path. Only commands whose own validator rejects the extras are run, checked
// on a separate probe tree first, so no command body ever executes; a new
// command is covered automatically.
func TestUsageError_EveryCommandExtraPositionals(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	extras := make([]string, 9)
	for i := range extras {
		extras[i] = fmt.Sprintf("lucid-extra-%d", i+1)
	}

	visited := map[string]bool{}
	for _, c := range usageProbeCommands(t) {
		if !c.Runnable() || c.Args == nil {
			continue
		}
		if c.ValidateArgs(extras) == nil {
			continue // the command accepts them, so its body would run
		}
		path := c.CommandPath()
		visited[path] = true
		t.Run(path, func(t *testing.T) {
			args := append(commandArgs(c), extras...)
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)

			assert.Equal(t, ExitUsage, exitCodeForError(err), "err = %v", err)
			assert.Empty(t, stdout)
			assertUsageErrorOnce(t, stderr, path)
		})
	}

	for _, anchor := range []string{"lucid status", "lucid update"} {
		assert.True(t, visited[anchor], "the walk did not visit %q", anchor)
	}
}

// TestUsageError_WalkIsIdempotent asserts a second walk neither wraps a
// validator twice nor disturbs the library-built update command, and that an
// already-typed error is never wrapped again.
func TestUsageError_WalkIsIdempotent(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	installUsageErrors(root)

	var cmds []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		cmds = append(cmds, c)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	status := findUsageProbe(t, cmds, "lucid status")
	err := status.ValidateArgs([]string{"x"})
	var ue *usageError
	require.ErrorAs(t, err, &ue)
	var nested *usageError
	assert.NotErrorAs(t, ue.Unwrap(), &nested, "the validator was wrapped twice: %v", err)
	assert.Same(t, status, ue.cmd)

	update := findUsageProbe(t, cmds, "lucid update")
	assert.NotNil(t, update.RunE, "the update command keeps the library's RunE")
	assert.GreaterOrEqual(t, len(update.Annotations), 2, "the walk replaced the update command's annotations: %v", update.Annotations)
	assert.Equal(t, "true", update.Annotations[usageWrappedAnnotation])

	assert.Same(t, err, newUsageError(update, err), "an already-typed error was wrapped again")
}

// Cobra records flag groups under annotation keys it does not export
// (flag_groups.go); they are mirrored here so the guard below can find every
// command that declares a group.
const (
	cobraFlagGroupRequiredIfOthersSet = "cobra_annotation_required_if_others_set"
	cobraFlagGroupOneRequired         = "cobra_annotation_one_required"
	cobraFlagGroupMutuallyExclusive   = "cobra_annotation_mutually_exclusive"
)

// visitedFlags returns every flag a flag set's VisitAll visits. The flag type
// is inferred from VisitAll, so the test reaches flags through cobra's API
// alone; lucid depends on the flag package only through cobra.
func visitedFlags[F any](visitAll func(func(F))) []F {
	var flags []F
	visitAll(func(f F) { flags = append(flags, f) })
	return flags
}

// TestUsageError_FlagGroupCommandsHaveArgs guards the args-stage flag checks:
// they run inside the wrapped Args validator, so every command that declares a
// required flag or a flag group must declare Args for its misuse to be typed.
func TestUsageError_FlagGroupCommandsHaveArgs(t *testing.T) {
	keys := []string{
		cobra.BashCompOneRequiredFlag,
		cobraFlagGroupRequiredIfOthersSet,
		cobraFlagGroupOneRequired,
		cobraFlagGroupMutuallyExclusive,
	}

	declared := map[string]bool{}
	for _, c := range usageProbeCommands(t) {
		for _, f := range visitedFlags(c.LocalFlags().VisitAll) {
			for _, k := range keys {
				if _, ok := f.Annotations[k]; ok {
					declared[c.CommandPath()] = true
				}
			}
		}
		if declared[c.CommandPath()] {
			assert.NotNil(t, c.Args, "%q declares a required flag or flag group but no Args", c.CommandPath())
		}
	}

	for _, anchor := range []string{"lucid link", "lucid retro import", "lucid reflect week"} {
		assert.True(t, declared[anchor], "%q no longer declares a required flag or flag group", anchor)
	}
}

// TestUsageError_NilArgsUntouched asserts the walk leaves a command without an
// Args validator on cobra's default, which accepts any positional, and that a
// group command given an unknown subcommand still prints its help and succeeds.
func TestUsageError_NilArgsUntouched(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	for _, c := range usageProbeCommands(t) {
		if c.Annotations[usageWrappedAnnotation] != "" {
			continue
		}
		assert.Nil(t, c.Args, "%q has Args but was not wrapped", c.CommandPath())
		require.NoError(t, c.ValidateArgs([]string{"x"}), "%q rejects a positional", c.CommandPath())
	}

	for _, args := range [][]string{{"wheel", "bogus"}, {"scheduler", "bogus"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)

			require.NoError(t, err)
			assert.Equal(t, ExitOK, exitCodeForError(err))
			assert.Contains(t, stdout, "Usage:")
			assert.Empty(t, stderr)
		})
	}
}

// TestUsageError_RuntimeEINVALIsNotUsage asserts a runtime failure whose text
// happens to read like misuse — a rename refused with EINVAL renders as
// "rename a b: invalid argument" — stays a runtime failure: it exits 1 and
// gets no usage line or --help hint, because usage errors are found by type.
func TestUsageError_RuntimeEINVALIsNotUsage(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")

	root := newRootCmd(BuildInfo{Version: "dev"})
	root.AddCommand(&cobra.Command{
		Use:  "einval-probe",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EINVAL}
		},
	})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)

	code, err := run(context.Background(), root, []string{"einval-probe"})

	require.Error(t, err)
	assert.Equal(t, ExitErr, code)
	assert.Contains(t, err.Error(), "invalid argument")
	require.ErrorIs(t, err, syscall.EINVAL)
	var ue *usageError
	assert.NotErrorAs(t, err, &ue, "a runtime failure is never typed as a usage error")
	assert.NotContains(t, errBuf.String(), "for usage.")
	assert.NotContains(t, errBuf.String(), "lucid einval-probe: ")
}

// TestUsageError_CommandLevelChecks asserts the checks a command makes itself
// once its flags parse — a stats window the flags cannot describe, a bad
// reflect week start date, a structure invocation naming neither or both
// modes, and a wheel missing its ratings or mixing --input with other flags —
// exit with the usage code, leave stdout empty, and are reported once with the
// command's --help hint, the reason itself never printed twice.
func TestUsageError_CommandLevelChecks(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		stdin  string
		at     time.Time
		path   string
		reason string
		want   string
	}{
		{
			name: "stats last zero", args: []string{"stats", "--last", "0"}, at: afternoon(),
			path: "lucid stats", reason: "--last must be a positive number of days",
			want: "lucid stats: invalid argument: --last must be a positive number of days\n" +
				"Run 'lucid stats --help' for usage.\n",
		},
		{
			name: "stats last with from", args: []string{"stats", "--last", "2", "--from", "2026-07-01"}, at: afternoon(),
			path: "lucid stats", reason: "--last cannot be combined with --from/--to",
		},
		{
			name: "reflect week bad since", args: []string{"reflect", "week", "--since", "nope"}, at: reflectNow(),
			path: "lucid reflect week", reason: `invalid argument "nope" for "--since" flag`,
		},
		{
			name: "structure until without since", args: []string{"structure", "--until", "2026-01-01"}, at: structureNow(),
			path: "lucid structure", reason: "it closes a window opened by --since",
		},
		{
			name: "structure bare", args: []string{"structure"}, at: structureNow(),
			path: "lucid structure", reason: "requires at least a raw entry id",
			want: "lucid structure: requires at least a raw entry id or --since <YYYY-MM-DD>\n" +
				"Run 'lucid structure --help' for usage.\n",
		},
		{
			name: "wheel add missing pillars", args: []string{"wheel", "add"}, at: wheelClock(),
			path: "lucid wheel add", reason: "every pillar needs your own",
		},
		{
			name: "wheel add input with flags", args: []string{"wheel", "add", "--input", "-", "--health", "5"},
			stdin: syntheticWheelDoc, at: wheelClock(),
			path: "lucid wheel add", reason: "cannot be given with it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolatedHome(t)
			t.Setenv("LUCID_NO_UPDATE_CHECK", "1")
			withClock(t, tc.at)
			fake := &provider.Fake{}
			withServeProvider(t, fake)

			stdout, stderr, err := runRootWithStdin(t, tc.stdin, tc.args...)

			require.Error(t, err)
			assert.Equal(t, ExitUsage, exitCodeForError(err))
			assert.Empty(t, stdout)
			assertUsageErrorOnce(t, stderr, tc.path)
			assert.Equal(t, 1, strings.Count(stderr, tc.reason), "reason not printed exactly once: %q", stderr)
			if tc.want != "" {
				assert.Equal(t, tc.want, stderr)
			}
			assert.Equal(t, 0, fake.Calls(), "a rejected invocation never reaches the model")
		})
	}
}
