package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, tc.args...)

			assert.Equal(t, ExitUsage, exitCodeForError(err))
			assert.Empty(t, stdout)
			assertUsageErrorOnce(t, stderr, tc.path)
		})
	}
}
