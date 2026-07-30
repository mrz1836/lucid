package schedinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the pure, host-free decisions on every platform (they run
// on Linux CI, where the darwin shell is absent), so the argv construction, path
// building, and manual guidance are covered even though the launchctl path is
// CI-invisible.

func TestBootstrapArgs(t *testing.T) {
	got := bootstrapArgs(501, "/Users/x/Library/LaunchAgents/com.lucid.scheduler.plist")
	assert.Equal(t, []string{"bootstrap", "gui/501", "/Users/x/Library/LaunchAgents/com.lucid.scheduler.plist"}, got)
}

func TestBootoutArgs(t *testing.T) {
	assert.Equal(t, []string{"bootout", "gui/501/com.lucid.scheduler"}, bootoutArgs(501, "com.lucid.scheduler"))
}

func TestPrintArgs(t *testing.T) {
	assert.Equal(t, []string{"print", "gui/501/com.lucid.scheduler"}, printArgs(501, "com.lucid.scheduler"))
}

func TestPlistPath(t *testing.T) {
	assert.Equal(t, filepath.Join("/dir", "com.lucid.scheduler.plist"), plistPath("/dir", "com.lucid.scheduler"))
}

func TestResolveLaunchAgentsDir_Override(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveLaunchAgentsDir(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, got)
}

func TestResolveLaunchAgentsDir_Default(t *testing.T) {
	got, err := resolveLaunchAgentsDir("")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, filepath.Join("Library", "LaunchAgents")), "default under ~/Library/LaunchAgents")
}

func TestRemovePlist(t *testing.T) {
	// Absent → clean no-op (idempotent uninstall).
	removed, err := removePlist(filepath.Join(t.TempDir(), "absent.plist"))
	require.NoError(t, err)
	assert.False(t, removed)

	// Present → removed.
	f := filepath.Join(t.TempDir(), "com.lucid.scheduler.plist")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	removed, err = removePlist(f)
	require.NoError(t, err)
	assert.True(t, removed)
	_, statErr := os.Stat(f)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestManualGuidance(t *testing.T) {
	lines := ManualGuidance(ApplyParams{
		Label:               "com.lucid.scheduler",
		LaunchAgentsDir:     "/agents",
		SuperviseConfigPath: "/hush/lucid-scheduler.toml",
	})
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "macOS-only")
	assert.Contains(t, joined, filepath.Join("/agents", "com.lucid.scheduler.plist"))
	assert.Contains(t, joined, "/hush/lucid-scheduler.toml")
	assert.Contains(t, joined, "hush supervise")
	assert.Contains(t, joined, "lucid scheduler status")
}

// TestManualGuidance_ResolvesDefaultDir: with no override, the guidance still
// prints a concrete plist path under the resolved LaunchAgents dir.
func TestManualGuidance_ResolvesDefaultDir(t *testing.T) {
	lines := ManualGuidance(ApplyParams{Label: "com.lucid.scheduler", SuperviseConfigPath: "/tmp/s.toml"})
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, filepath.Join("Library", "LaunchAgents", "com.lucid.scheduler.plist"))
}
