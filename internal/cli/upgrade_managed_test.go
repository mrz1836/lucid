package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pinClock overrides the package clock for a test and restores it after.
func pinClock(t *testing.T, at time.Time) {
	t.Helper()
	prev := clockNow
	clockNow = func() time.Time { return at }
	t.Cleanup(func() { clockNow = prev })
}

// TestRunUpgrade_ManagedDeferredInDrainWindow: a managed upgrade requested
// mid-evening (inside the default bell→close-out drain window) is refused — no
// install runs and the binary is untouched.
func TestRunUpgrade_ManagedDeferredInDrainWindow(t *testing.T) {
	t.Setenv("LUCID_HOME", filepath.Join(t.TempDir(), ".lucid"))
	execPath := filepath.Join(t.TempDir(), "lucid")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0o755))
	rel := newAssetServer(t, []byte("the new binary bytes"))
	useTestSource(t, fakeSource{rel: rel}, execPath)
	pinClock(t, time.Date(2026, 7, 6, 22, 0, 0, 0, time.UTC)) // 22:00 — inside 21:30→04:00

	var stdout, stderr bytes.Buffer
	err := runUpgrade(context.Background(), &stdout, &stderr, upgradeOptions{
		managed: true, currentVersion: "v0.1.0",
	})
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "deferred")
	got, rerr := os.ReadFile(execPath)
	require.NoError(t, rerr)
	assert.Equal(t, "old", string(got), "no install inside the drain window")
}

// TestRunUpgrade_ManagedUpgradesOutsideWindow: at noon (outside the drain
// window) the managed upgrade installs and the post-upgrade tripwire self-check
// passes against the scaffolded engine tree.
func TestRunUpgrade_ManagedUpgradesOutsideWindow(t *testing.T) {
	t.Setenv("LUCID_HOME", filepath.Join(t.TempDir(), ".lucid"))
	execPath := filepath.Join(t.TempDir(), "lucid")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0o755))
	rel := newAssetServer(t, []byte("the new binary bytes"))
	useTestSource(t, fakeSource{rel: rel}, execPath)
	pinClock(t, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)) // noon — outside the window

	var stdout, stderr bytes.Buffer
	err := runUpgrade(context.Background(), &stdout, &stderr, upgradeOptions{
		managed: true, currentVersion: "v0.1.0",
	})
	require.NoError(t, err)

	got, rerr := os.ReadFile(execPath)
	require.NoError(t, rerr)
	assert.Equal(t, "the new binary bytes", string(got), "the managed upgrade installed the new binary")
	assert.Contains(t, stdout.String(), "self-check passed")
}

// TestUpgradeCmd_ManagedFlagThroughRoot: the `--managed` flag is wired on the
// upgrade command and reachable through the root command tree.
func TestUpgradeCmd_ManagedFlagThroughRoot(t *testing.T) {
	t.Setenv("LUCID_HOME", filepath.Join(t.TempDir(), ".lucid"))
	execPath := filepath.Join(t.TempDir(), "lucid")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0o755))
	rel := newAssetServer(t, []byte("bin"))
	useTestSource(t, fakeSource{rel: rel}, execPath)
	pinClock(t, time.Date(2026, 7, 6, 22, 0, 0, 0, time.UTC)) // deferred → no install side effect

	out, _, err := runRoot(t, BuildInfo{Version: "v0.1.0"}, "upgrade", "--managed")
	require.NoError(t, err)
	assert.Contains(t, out, "deferred")
}
