//go:build darwin

package schedinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive the real macOS Apply/Uninstall with a recording fake in
// place of the launchctl shell-out, so no actual launchctl runs and the plist
// lands in a temp dir. They are LOCAL-ONLY / CI-INVISIBLE: the darwin file is
// absent from the Linux coverage profile, so this path's correctness rests on
// these Mac-local tests plus review (see the plan's coverage note).

// swapLaunchctl installs a fake launchctl for the duration of a test.
func swapLaunchctl(t *testing.T, fake func(context.Context, ...string) error) {
	t.Helper()
	prev := runLaunchctl
	runLaunchctl = fake
	t.Cleanup(func() { runLaunchctl = prev })
}

// indexOfCall returns the index of the first recorded call whose verb matches, or
// -1.
func indexOfCall(calls [][]string, verb string) int {
	for i, c := range calls {
		if len(c) > 0 && c[0] == verb {
			return i
		}
	}
	return -1
}

func guiLabel() string  { return "gui/" + strconv.Itoa(os.Getuid()) + "/com.lucid.scheduler" }
func guiDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// TestApply_WritesAndLoads: a fresh install writes both artifacts at the right
// perms and loads the job with the correct launchctl argv.
func TestApply_WritesAndLoads(t *testing.T) {
	agents := t.TempDir()
	superviseCfg := filepath.Join(t.TempDir(), "supervisors", "lucid-scheduler.toml")
	var calls [][]string
	loaded := false
	swapLaunchctl(t, func(_ context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		switch args[0] {
		case "print":
			if loaded {
				return nil
			}
			return errors.New("could not find service")
		case "bootstrap":
			loaded = true
			return nil
		default:
			return nil
		}
	})

	res, err := Apply(t.Context(), ApplyParams{
		Label:               "com.lucid.scheduler",
		PlistBody:           "<plist>body</plist>",
		SuperviseBody:       "supervise body",
		SuperviseConfigPath: superviseCfg,
		LaunchAgentsDir:     agents,
	})
	require.NoError(t, err)
	assert.True(t, res.Loaded)
	assert.False(t, res.Replaced)

	pp := filepath.Join(agents, "com.lucid.scheduler.plist")
	assert.Equal(t, pp, res.PlistPath)

	plistInfo, err := os.Stat(pp)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), plistInfo.Mode().Perm())
	body, err := os.ReadFile(pp)
	require.NoError(t, err)
	assert.Equal(t, "<plist>body</plist>", string(body))

	supInfo, err := os.Stat(superviseCfg)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), supInfo.Mode().Perm())

	assert.Contains(t, calls, []string{"bootstrap", guiDomain(), pp})
	assert.Contains(t, calls, []string{"print", guiLabel()})
}

// TestWriteArtifacts_WritesWithoutLoading: staging writes both files and runs no
// launchctl (the hush-absent path).
func TestWriteArtifacts_WritesWithoutLoading(t *testing.T) {
	agents := t.TempDir()
	superviseCfg := filepath.Join(t.TempDir(), "supervisors", "lucid-scheduler.toml")
	called := false
	swapLaunchctl(t, func(_ context.Context, _ ...string) error { called = true; return nil })

	res, err := WriteArtifacts(ApplyParams{
		Label: "com.lucid.scheduler", PlistBody: "<plist/>", SuperviseBody: "s",
		SuperviseConfigPath: superviseCfg, LaunchAgentsDir: agents,
	})
	require.NoError(t, err)
	assert.False(t, res.Loaded)
	assert.False(t, called, "staging runs no launchctl")

	_, statErr := os.Stat(filepath.Join(agents, "com.lucid.scheduler.plist"))
	require.NoError(t, statErr)
	_, supErr := os.Stat(superviseCfg)
	require.NoError(t, supErr)
}

// TestApply_AlreadyLoadedRefused: an already-loaded job is refused without
// --force, and nothing is booted out.
func TestApply_AlreadyLoadedRefused(t *testing.T) {
	agents := t.TempDir()
	var calls [][]string
	swapLaunchctl(t, func(_ context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil // print always succeeds → the job is already loaded
	})

	_, err := Apply(t.Context(), ApplyParams{
		Label: "com.lucid.scheduler", PlistBody: "<plist/>", SuperviseBody: "s",
		SuperviseConfigPath: filepath.Join(t.TempDir(), "s.toml"), LaunchAgentsDir: agents,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already loaded")
	assert.Equal(t, -1, indexOfCall(calls, "bootout"), "no bootout without --force")
	assert.Equal(t, -1, indexOfCall(calls, "bootstrap"), "no bootstrap when refused")
}

// TestApply_ForceReplaces: --force boots the existing job out, then bootstraps
// the new one.
func TestApply_ForceReplaces(t *testing.T) {
	agents := t.TempDir()
	var calls [][]string
	swapLaunchctl(t, func(_ context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil // print succeeds (loaded); bootout/bootstrap succeed
	})

	res, err := Apply(t.Context(), ApplyParams{
		Label: "com.lucid.scheduler", PlistBody: "<plist/>", SuperviseBody: "s",
		SuperviseConfigPath: filepath.Join(t.TempDir(), "s.toml"), LaunchAgentsDir: agents,
		Force: true,
	})
	require.NoError(t, err)
	assert.True(t, res.Replaced)

	bootoutIdx := indexOfCall(calls, "bootout")
	bootstrapIdx := indexOfCall(calls, "bootstrap")
	require.NotEqual(t, -1, bootoutIdx)
	require.NotEqual(t, -1, bootstrapIdx)
	assert.Less(t, bootoutIdx, bootstrapIdx, "bootout precedes bootstrap under --force")
	assert.Contains(t, calls, []string{"bootout", guiLabel()})
}

// TestApply_BootstrapFailureSurfaces: a bootstrap failure surfaces and leaves the
// written plist in place so a re-run can retry.
func TestApply_BootstrapFailureSurfaces(t *testing.T) {
	agents := t.TempDir()
	swapLaunchctl(t, func(_ context.Context, args ...string) error {
		switch args[0] {
		case "print":
			return errors.New("not loaded")
		case "bootstrap":
			return errors.New("Bootstrap failed: 5: Input/output error")
		default:
			return nil
		}
	})

	res, err := Apply(t.Context(), ApplyParams{
		Label: "com.lucid.scheduler", PlistBody: "<plist/>", SuperviseBody: "s",
		SuperviseConfigPath: filepath.Join(t.TempDir(), "s.toml"), LaunchAgentsDir: agents,
	})
	require.Error(t, err)
	assert.False(t, res.Loaded)
	_, statErr := os.Stat(filepath.Join(agents, "com.lucid.scheduler.plist"))
	require.NoError(t, statErr, "the plist is left in place after a bootstrap failure")
}

// TestUninstall_BootoutAndRemove: uninstall boots the job out and removes the
// plist.
func TestUninstall_BootoutAndRemove(t *testing.T) {
	agents := t.TempDir()
	pp := filepath.Join(agents, "com.lucid.scheduler.plist")
	require.NoError(t, os.WriteFile(pp, []byte("<plist/>"), 0o644))
	var calls [][]string
	swapLaunchctl(t, func(_ context.Context, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	})

	res, err := Uninstall(t.Context(), UninstallParams{Label: "com.lucid.scheduler", LaunchAgentsDir: agents})
	require.NoError(t, err)
	assert.True(t, res.BootedOut)
	assert.True(t, res.PlistRemoved)
	_, statErr := os.Stat(pp)
	require.ErrorIs(t, statErr, os.ErrNotExist)
	assert.Contains(t, calls, []string{"bootout", guiLabel()})
}

// TestUninstall_Idempotent: an absent plist is a clean no-op even when launchctl
// reports the job was not loaded.
func TestUninstall_Idempotent(t *testing.T) {
	agents := t.TempDir()
	swapLaunchctl(t, func(_ context.Context, _ ...string) error {
		return errors.New("Boot-out failed: 3: No such process")
	})

	res, err := Uninstall(t.Context(), UninstallParams{Label: "com.lucid.scheduler", LaunchAgentsDir: agents})
	require.NoError(t, err)
	assert.False(t, res.PlistRemoved, "absent plist removed nothing")
}

// TestUninstall_DryRun: a dry run runs no launchctl and removes nothing.
func TestUninstall_DryRun(t *testing.T) {
	agents := t.TempDir()
	pp := filepath.Join(agents, "com.lucid.scheduler.plist")
	require.NoError(t, os.WriteFile(pp, []byte("<plist/>"), 0o644))
	called := false
	swapLaunchctl(t, func(_ context.Context, _ ...string) error { called = true; return nil })

	res, err := Uninstall(t.Context(), UninstallParams{Label: "com.lucid.scheduler", LaunchAgentsDir: agents, DryRun: true})
	require.NoError(t, err)
	assert.True(t, res.BootedOut)
	assert.True(t, res.PlistRemoved)
	assert.False(t, called, "dry-run runs no launchctl")
	_, statErr := os.Stat(pp)
	require.NoError(t, statErr, "dry-run removes nothing")
}
