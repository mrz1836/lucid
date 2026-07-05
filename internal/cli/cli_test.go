package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runRoot executes the full cobra tree with args, capturing stdout and
// stderr. It returns the captured streams and the command error.
func runRoot(t *testing.T, bi BuildInfo, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd(bi)
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errBuf.String(), err
}

func TestVersion_Human(t *testing.T) {
	bi := BuildInfo{Version: "v1.2.3", Commit: "abc1234", Date: "2026-07-05"}
	out, _, err := runRoot(t, bi, "version")
	require.NoError(t, err)
	assert.Contains(t, out, "lucid v1.2.3")
	assert.Contains(t, out, "commit:")
	assert.Contains(t, out, "abc1234")
	assert.Contains(t, out, "2026-07-05")
	assert.Contains(t, out, "go:")
	assert.Contains(t, out, "platform:")
}

func TestVersion_JSON(t *testing.T) {
	bi := BuildInfo{Version: "v1.2.3", Commit: "abc1234", Date: "2026-07-05"}
	out, _, err := runRoot(t, bi, "version", "--json")
	require.NoError(t, err)

	var info versionInfo
	require.NoError(t, json.Unmarshal([]byte(out), &info))
	assert.Equal(t, "v1.2.3", info.Version)
	assert.Equal(t, "abc1234", info.Commit)
	assert.Equal(t, "2026-07-05", info.BuildDate)
	assert.NotEmpty(t, info.GoVersion)
	assert.Contains(t, info.Platform, "/")
}

func TestVersion_RejectsArgs(t *testing.T) {
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "version", "extra")
	require.Error(t, err)
}

func TestStub_HumanReturnsNotImplemented(t *testing.T) {
	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "hello")
	require.ErrorIs(t, err, errNotImplemented)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "not implemented yet")
	assert.Contains(t, errOut, "Stage 1")
}

func TestStub_MachineReadableEmitsJSON(t *testing.T) {
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "status", "--json")
	require.ErrorIs(t, err, errNotImplemented)

	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, "status", payload["command"])
	assert.Equal(t, "not_implemented", payload["status"])
	assert.Equal(t, "Stage 2", payload["stage"])
}

func TestStub_NonMachineReadableIgnoresJSON(t *testing.T) {
	// `closeout` is not a script-facing command; --json must not turn
	// it into a JSON no-op that looks successful.
	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "--json")
	require.ErrorIs(t, err, errNotImplemented)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "not implemented yet")
}

func TestSpine_AllVerbsRegistered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	want := []string{"init", "log", "closeout", "mode", "status", "day", "validate", "export", "version", "upgrade"}
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for _, w := range want {
		assert.Truef(t, got[w], "command spine missing %q", w)
	}
}

func TestExitCodeForError(t *testing.T) {
	assert.Equal(t, ExitOK, exitCodeForError(nil))
	assert.Equal(t, ExitErr, exitCodeForError(errNotImplemented))
	assert.Equal(t, ExitErr, exitCodeForError(errors.New("boom")))
	assert.Equal(t, ExitUsage, exitCodeForError(errors.New("unknown flag: --nope")))
	assert.Equal(t, ExitUsage, exitCodeForError(errors.New("unknown command \"nope\" for \"lucid\"")))
}

func TestIsUsageError(t *testing.T) {
	assert.False(t, isUsageError(nil))
	assert.False(t, isUsageError(errors.New("some runtime failure")))
	assert.True(t, isUsageError(errors.New("required flag(s) \"x\" not set")))
	assert.True(t, isUsageError(errors.New("flag needs an argument: --channel")))
}

func TestUnknownCommand_IsUsageError(t *testing.T) {
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "does-not-exist")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

func TestRootNoArgs_PrintsHelpNoError(t *testing.T) {
	out, _, err := runRoot(t, BuildInfo{Version: "dev"})
	require.NoError(t, err)
	assert.Contains(t, out, "lucid")
	assert.Contains(t, out, "Available Commands")
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeJSON(&buf, map[string]int{"a": 1}))
	assert.JSONEq(t, `{"a":1}`, buf.String())
	assert.True(t, strings.HasSuffix(buf.String(), "\n"))
}

func TestContainsFold(t *testing.T) {
	assert.True(t, containsFold("Unknown Flag", "unknown flag"))
	assert.True(t, containsFold("abc", "ABC"))
	assert.False(t, containsFold("abc", "xyz"))
}

// Guard: newRootCmd builds fresh state each call (no shared mutable
// command objects that would leak flags across invocations).
func TestNewRootCmd_FreshEachCall(t *testing.T) {
	a := newRootCmd(BuildInfo{Version: "a"})
	b := newRootCmd(BuildInfo{Version: "b"})
	assert.NotSame(t, a, b)
	// Each has its own persistent --json flag instance.
	assert.NotNil(t, a.PersistentFlags().Lookup(jsonFlag))
	assert.NotNil(t, b.PersistentFlags().Lookup(jsonFlag))
}
