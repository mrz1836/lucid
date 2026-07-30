package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/deploy"
	"github.com/mrz1836/lucid/internal/schedinstall"
)

// swapRenderLaunchd installs a fake launchd renderer for a test (used to inject a
// lint failure).
func swapRenderLaunchd(t *testing.T, fake func(deploy.LaunchdParams) (string, error)) {
	t.Helper()
	prev := renderLaunchd
	renderLaunchd = fake
	t.Cleanup(func() { renderLaunchd = prev })
}

// installArgs is the deterministic flag set for the render tests: an absolute
// lucid path and a hush path so the render lints clean and hush reads present,
// regardless of the test host's PATH.
func installArgs(extra ...string) []string {
	base := make([]string, 0, 6+len(extra))
	base = append(base, "scheduler", "install", "--lucid", "/usr/local/bin/lucid", "--hush", "/usr/local/bin/hush")
	return append(base, extra...)
}

// TestSchedulerInstall_TreeRegistered proves the install/uninstall children are
// registered under `scheduler` with their key flags.
func TestSchedulerInstall_TreeRegistered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})

	inst, _, err := root.Find([]string{"scheduler", "install"})
	require.NoError(t, err)
	assert.Equal(t, "install", inst.Name())
	assert.NotNil(t, inst.Flags().Lookup(installFlagApply), "install exposes --apply")
	assert.NotNil(t, inst.Flags().Lookup(installFlagOut), "install exposes --out")

	uninst, _, err := root.Find([]string{"scheduler", "uninstall"})
	require.NoError(t, err)
	assert.Equal(t, "uninstall", uninst.Name())
	assert.NotNil(t, uninst.Flags().Lookup(uninstallFlagDryRun), "uninstall exposes --dry-run")
}

// TestSchedulerInstall_PrintDefault: the default mode renders a lint-clean plist
// and supervise config with the load-bearing properties, mutating nothing.
func TestSchedulerInstall_PrintDefault(t *testing.T) {
	isolatedHome(t)
	t.Setenv(envUserChannelID, "")
	t.Setenv(envWitnessChannelID, "")

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, append(installArgs(), "--json")...)
	require.NoError(t, err)

	var p installJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &p))

	// Plist: lints clean, runs `hush supervise` (never lucid directly), no --config.
	require.NoError(t, deploy.LintLaunchd(p.Plist))
	assert.Contains(t, p.Plist, "<string>supervise</string>")
	assert.Contains(t, p.Plist, "<string>/usr/local/bin/hush</string>")
	assert.NotContains(t, p.Plist, "<string>--config</string>")

	// Supervise: lints clean, child ends in `scheduler run`, scope names the token
	// but carries no value.
	require.NoError(t, deploy.LintSupervise(p.Supervise))
	assert.Contains(t, p.Supervise, `"/usr/local/bin/lucid"`)
	assert.Contains(t, p.Supervise, `"scheduler"`)
	assert.Contains(t, p.Supervise, `"run"`)
	assert.Contains(t, p.Supervise, `"LUCID_HARNESS_TOKEN"`)
	assert.NotContains(t, strings.ToLower(p.Supervise), "bearer ")
	assert.NotContains(t, p.Supervise, `token = "`)
}

// TestSchedulerInstall_HumanPrint: the default human path prints both labeled
// artifacts.
func TestSchedulerInstall_HumanPrint(t *testing.T) {
	isolatedHome(t)
	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, installArgs()...)
	require.NoError(t, err)
	assert.Contains(t, stdout, "# launchd plist")
	assert.Contains(t, stdout, "<string>supervise</string>")
	assert.Contains(t, stdout, "# hush supervise config")
	assert.Contains(t, stdout, `session_type              = "supervisor"`)
}

// TestSchedulerInstall_DefaultBinaries: with no --lucid/--hush the command
// resolves this executable and still renders lint-clean artifacts.
func TestSchedulerInstall_DefaultBinaries(t *testing.T) {
	isolatedHome(t)
	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "install", "--json")
	require.NoError(t, err)
	var p installJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &p))
	require.NoError(t, deploy.LintLaunchd(p.Plist))
	require.NoError(t, deploy.LintSupervise(p.Supervise))
}

// TestSchedulerInstall_RelativeLucidRejected: --lucid must be absolute.
func TestSchedulerInstall_RelativeLucidRejected(t *testing.T) {
	isolatedHome(t)
	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "install", "--lucid", "relative/lucid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
	assert.Contains(t, stderr, "absolute")
}

// TestUninstallLines covers the three human summaries directly (the removed
// branch is otherwise macOS-host-only).
func TestUninstallLines(t *testing.T) {
	assert.Contains(t, uninstallLines(schedinstall.UninstallResult{PlistPath: "/p"}, true)[0], "Dry run")
	assert.Contains(t, uninstallLines(schedinstall.UninstallResult{PlistPath: "/p"}, false)[0], "Nothing to remove")
	assert.Contains(t, uninstallLines(schedinstall.UninstallResult{PlistPath: "/p", PlistRemoved: true}, false)[0], "Removed")
}

// TestSchedulerInstall_ChannelEnv: set channel IDs flow into the plist and
// suppress the placeholder note.
func TestSchedulerInstall_ChannelEnv(t *testing.T) {
	isolatedHome(t)
	t.Setenv(envUserChannelID, "100000000000000001")
	t.Setenv(envWitnessChannelID, "200000000000000002")

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, append(installArgs(), "--json")...)
	require.NoError(t, err)

	var p installJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &p))
	assert.Contains(t, p.Plist, "100000000000000001")
	assert.Contains(t, p.Plist, "200000000000000002")
	assert.NotContains(t, strings.Join(p.Notes, " "), "channel IDs are env-only")
}

// TestSchedulerInstall_ChannelPlaceholder: unset channel IDs fall back to the
// deploy placeholder and raise the provisioning note.
func TestSchedulerInstall_ChannelPlaceholder(t *testing.T) {
	isolatedHome(t)
	t.Setenv(envUserChannelID, "")
	t.Setenv(envWitnessChannelID, "")

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, append(installArgs(), "--json")...)
	require.NoError(t, err)

	var p installJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &p))
	assert.Contains(t, p.Plist, "replace-with-primary-channel-id")
	assert.Contains(t, strings.Join(p.Notes, " "), "channel IDs are env-only")
}

// TestSchedulerInstall_OutWritesFiles: --out writes two lint-clean artifacts.
func TestSchedulerInstall_OutWritesFiles(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, installArgs("--out", dir)...)
	require.NoError(t, err)

	pb, err := os.ReadFile(filepath.Join(dir, "com.lucid.scheduler.plist"))
	require.NoError(t, err)
	require.NoError(t, deploy.LintLaunchd(string(pb)))

	sb, err := os.ReadFile(filepath.Join(dir, "lucid-scheduler.toml"))
	require.NoError(t, err)
	require.NoError(t, deploy.LintSupervise(string(sb)))
}

// TestSchedulerInstall_LintFailureMutatesNothing: an injected lint failure
// surfaces deploy.ErrLint and writes nothing.
func TestSchedulerInstall_LintFailureMutatesNothing(t *testing.T) {
	isolatedHome(t)
	dir := t.TempDir()
	swapRenderLaunchd(t, func(deploy.LaunchdParams) (string, error) {
		return "", fmt.Errorf("%w: injected", deploy.ErrLint)
	})

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, installArgs("--out", dir)...)
	require.Error(t, err)
	require.ErrorIs(t, err, deploy.ErrLint)

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "a lint failure writes nothing")
}

// TestSchedulerInstall_OutAndApplyMutuallyExclusive: --out and --apply cannot be
// combined.
func TestSchedulerInstall_OutAndApplyMutuallyExclusive(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, installArgs("--out", t.TempDir(), "--apply")...)
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestSchedulerInstall_ApplyUnsupportedOffDarwin: on a non-macOS host --apply
// prints manual guidance and exits non-zero. Skipped on macOS (it would perform
// a real host install).
func TestSchedulerInstall_ApplyUnsupportedOffDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("would perform a real host install on macOS")
	}
	isolatedHome(t)

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, installArgs("--apply")...)
	require.Error(t, err)
	require.ErrorIs(t, err, schedinstall.ErrUnsupported)
	assert.Contains(t, stderr, "macOS-only")
}

// TestSchedulerInstall_OverrideFlags: the supervise-config, hush-server, and
// machine-index overrides flow into the rendered artifacts.
func TestSchedulerInstall_OverrideFlags(t *testing.T) {
	isolatedHome(t)
	args := installArgs(
		"--supervise-config", filepath.Join(t.TempDir(), "custom.toml"),
		"--hush-server", "http://10.0.0.1:7000/h/x",
		"--machine-index", "3",
		"--scheduler-db", filepath.Join(t.TempDir(), "sched.db"),
		"--json",
	)
	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoError(t, err)

	var p installJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &p))
	assert.Contains(t, p.Supervise, "http://10.0.0.1:7000/h/x")
	assert.Contains(t, p.Supervise, "client_machine_index      = 3")
}

// TestReportApply_Renders drives the post-apply success report directly (its
// real caller runs only on a macOS host install), covering the host-probe
// rendering on any platform.
func TestReportApply_Renders(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	err := reportApply(cmd, installPlan{schedulerDB: filepath.Join(t.TempDir(), "f.db")}, schedinstall.ApplyResult{
		PlistPath: "/p.plist", SuperviseConfigPath: "/s.toml", Loaded: true, Replaced: true,
	})
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "Installed the supervised scheduler")
	assert.Contains(t, out, "/p.plist")
	assert.Contains(t, out, "replaced an already-loaded job")
	assert.Contains(t, out, "Host checks")
	assert.Contains(t, out, "lucid scheduler status")
}

// TestStageInstall drives the hush-absent staging path directly with a temp
// LaunchAgents dir (so the darwin write never touches the real ~/Library). On a
// non-macOS host the write is unsupported and it falls back to guidance.
func TestStageInstall(t *testing.T) {
	var buf, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetErr(&errBuf)

	plan := installPlan{applyParams: schedinstall.ApplyParams{
		Label: "com.lucid.scheduler", PlistBody: "<plist/>", SuperviseBody: "s",
		SuperviseConfigPath: filepath.Join(t.TempDir(), "s.toml"),
		LaunchAgentsDir:     t.TempDir(),
	}}
	err := stageInstall(cmd, plan)
	require.Error(t, err)
	if runtime.GOOS == "darwin" {
		assert.Contains(t, err.Error(), "staged but not loaded")
		assert.Contains(t, buf.String(), "Staged")
	} else {
		require.ErrorIs(t, err, schedinstall.ErrUnsupported)
	}
}

// TestSchedulerUninstall_DryRunOrGuidance: uninstall --dry-run previews on macOS
// and prints manual guidance off it. Neither path mutates the host.
func TestSchedulerUninstall_DryRunOrGuidance(t *testing.T) {
	isolatedHome(t)

	stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "scheduler", "uninstall", "--dry-run")
	if runtime.GOOS == "darwin" {
		require.NoError(t, err)
		assert.Contains(t, stdout, "Dry run")
	} else {
		require.ErrorIs(t, err, schedinstall.ErrUnsupported)
		assert.Contains(t, stderr, "macOS-only")
	}
}
