package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecute_DispatchesOSArgs drives the real Execute entrypoint through
// os.Args, covering the top-level dispatch + exit-code mapping.
func TestExecute_DispatchesOSArgs(t *testing.T) {
	prev := os.Args
	t.Cleanup(func() { os.Args = prev })

	os.Args = []string{"lucid", "version"}
	assert.Equal(t, ExitOK, Execute(context.Background(), BuildInfo{Version: "dev"}))

	os.Args = []string{"lucid", "does-not-exist"}
	assert.Equal(t, ExitUsage, Execute(context.Background(), BuildInfo{Version: "dev"}))
}

// runRoot executes the full cobra tree with args through the same run seam
// Execute uses, capturing stdout and stderr, so a usage error renders exactly
// as a user sees it. It returns the captured streams and the command error.
func runRoot(t *testing.T, bi BuildInfo, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd(bi)
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	_, err = run(context.Background(), root, args)
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

func TestSpine_AllVerbsRegistered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	want := []string{"init", "log", "attach", "link", "unlink", "annotate", "closeout", "mode", "status", "obs", "reframe", "wheel", "focus", "rep", "retro", "day", "stats", "validate", "export", "backup", "restore", "version", "update", "scheduler", "companion", "anchor", "metrics", "storm", "profile", "person", "bootstrap", "serve", "reflect", "ask", "injury", "era", "thread", "structure"}
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
	assert.Equal(t, ExitErr, exitCodeForError(errValidationFailed))
	assert.Equal(t, ExitErr, exitCodeForError(errors.New("boom")))
	// Text alone never classifies: an untyped error that reads like misuse is a
	// runtime failure, and only a typed usage error maps to ExitUsage — directly
	// or wrapped.
	assert.Equal(t, ExitErr, exitCodeForError(errors.New("unknown flag: --nope")))
	usage := newUsageError(nil, errors.New("unknown flag: --nope"))
	assert.Equal(t, ExitUsage, exitCodeForError(usage))
	assert.Equal(t, ExitUsage, exitCodeForError(fmt.Errorf("x: %w", usage)))
}

func TestUnknownCommand_IsUsageError(t *testing.T) {
	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "does-not-exist")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assertUsageErrorOnce(t, stderr, "lucid")
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

// TestAnchorCmd_HasEverySubcommand guards the anchor command tree: a dropped
// registration would ship a missing verb silently, since the group itself still
// renders. Mirrors TestSpine_AllVerbsRegistered one level down.
func TestAnchorCmd_HasEverySubcommand(t *testing.T) {
	got := map[string]bool{}
	for _, c := range newAnchorCmd().Commands() {
		got[c.Name()] = true
	}
	want := []string{"add", "kind", "slip", "use", "miss", "confirm", "void", "sunset", "rename", "backfill-ids"}
	for _, name := range want {
		assert.Truef(t, got[name], "anchor group missing %q", name)
	}
	assert.Len(t, got, len(want), "anchor exposes exactly %v", want)
}

// TestFocusCmd_HasSubcommands guards the focus command tree: a dropped
// registration would ship a missing verb silently, since the group itself still
// renders. Mirrors TestAnchorCmd_HasEverySubcommand one level down.
func TestFocusCmd_HasSubcommands(t *testing.T) {
	got := map[string]bool{}
	for _, c := range newFocusCmd().Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"add", "list", "surface", "retire"} {
		assert.Truef(t, got[want], "focus group missing %q", want)
	}
	assert.Len(t, got, 4, "focus exposes exactly add, list, surface, and retire")
}

// TestRepCmd_HasSubcommands guards the rep command tree the same way: exactly
// add, list, surface, and retire, so a dropped registration fails loudly.
func TestRepCmd_HasSubcommands(t *testing.T) {
	got := map[string]bool{}
	for _, c := range newRepCmd().Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"add", "list", "surface", "retire"} {
		assert.Truef(t, got[want], "rep group missing %q", want)
	}
	assert.Len(t, got, 4, "rep exposes exactly add, list, surface, and retire")
}
