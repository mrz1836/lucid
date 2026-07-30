package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backupTo runs `lucid backup --out <path>` and returns the archive path so the
// restore tests have a real archive to work from.
func backupTo(t *testing.T, path string) {
	t.Helper()
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "backup", "--out", path)
	require.NoError(t, err)
}

// TestRestoreCmd_RoundTrip: backup, wipe the raw tree, restore via --in, and the
// entry returns byte-for-byte.
func TestRestoreCmd_RoundTrip(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)

	require.NoError(t, os.RemoveAll(filepath.Join(home, "raw")))
	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out)
	require.NoError(t, err)
	assert.Contains(t, stdout, "Restored")

	b, rerr := os.ReadFile(filepath.Join(home, "raw", "raw_2026_07_29_09_00.md"))
	require.NoError(t, rerr)
	assert.Equal(t, "# entry\nhello\n", string(b))
}

// TestRestoreCmd_PositionalPath: the archive may be passed positionally.
func TestRestoreCmd_PositionalPath(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)

	require.NoError(t, os.RemoveAll(filepath.Join(home, "raw")))
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", out)
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(home, "raw", "raw_2026_07_29_09_00.md"))
	require.NoError(t, statErr)
}

// TestRestoreCmd_JSON: `--json` decodes to the documented payload.
func TestRestoreCmd_JSON(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)
	require.NoError(t, os.RemoveAll(filepath.Join(home, "raw")))

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out, "--json")
	require.NoError(t, err)
	var payload restoreJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload))
	assert.Equal(t, "restore", payload.Command)
	assert.Equal(t, out, payload.Path)
	assert.Equal(t, 1, payload.Files)
	assert.Positive(t, payload.Bytes)
}

// TestRestoreCmd_OccupiedRefusalThenForce: restore refuses an occupied home,
// then overlays it with --force.
func TestRestoreCmd_OccupiedRefusalThenForce(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)

	// The raw entry still occupies the home.
	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
	assert.Contains(t, stderr, "--force", "the refusal is printed for the user, not just returned")

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out, "--force")
	require.NoError(t, err)
}

// TestRestoreCmd_EmptyArchive: restoring an archive with no in-scope files is a
// clean no-op that says so.
func TestRestoreCmd_EmptyArchive(t *testing.T) {
	isolatedHome(t) // never scaffolded → the manifest trees are all absent
	out := filepath.Join(t.TempDir(), "empty.tar.gz")
	backupTo(t, out)

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out)
	require.NoError(t, err)
	assert.Contains(t, stdout, "Nothing to restore")
}

// TestRestoreCmd_InputResolution: neither/both input forms are refused with a
// clear message.
func TestRestoreCmd_InputResolution(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name the archive")

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", "a.tar.gz", "b.tar.gz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}
