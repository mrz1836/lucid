package cli

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedRawEntry writes one raw entry into home so a backup has real content.
func seedRawEntry(t *testing.T, home string) {
	t.Helper()
	p := filepath.Join(home, "raw", "raw_2026_07_29_09_00.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, []byte("# entry\nhello\n"), 0o600))
}

// archiveNames returns the tar entry names inside a gzip archive, proving it is a
// well-formed tar.gz.
func archiveNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, herr := tr.Next()
		if errors.Is(herr, io.EOF) {
			break
		}
		require.NoError(t, herr)
		names = append(names, h.Name)
	}
	return names
}

// TestBackupCmd_WritesArchive: `backup --out` writes a valid archive carrying the
// seeded raw entry.
func TestBackupCmd_WritesArchive(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "backup", "--out", out)
	require.NoError(t, err)
	assert.Contains(t, stdout, out)
	assert.Contains(t, archiveNames(t, out), "raw/raw_2026_07_29_09_00.md")
}

// TestBackupCmd_JSON: `--json` decodes to the documented payload.
func TestBackupCmd_JSON(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "backup", "--out", out, "--json")
	require.NoError(t, err)
	var payload backupJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload))
	assert.Equal(t, "backup", payload.Command)
	assert.Equal(t, out, payload.Path)
	assert.Positive(t, payload.Bytes)
	assert.Equal(t, 1, payload.Files)
}

// TestBackupCmd_DefaultFilename: with --out unset, the archive name is derived
// from the pinned clock, in the current directory.
func TestBackupCmd_DefaultFilename(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	withClock(t, time.Date(2026, 7, 29, 13, 45, 5, 0, time.UTC))
	t.Chdir(t.TempDir())

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "backup")
	require.NoError(t, err)
	want := "lucid-backup-20260729-134505.tar.gz"
	assert.Contains(t, stdout, want)
	_, statErr := os.Stat(want)
	require.NoError(t, statErr)
}

// TestBackupCmd_ExclCollision: an existing target is never clobbered.
func TestBackupCmd_ExclCollision(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	require.NoError(t, os.WriteFile(out, []byte("existing"), 0o600))

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "backup", "--out", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file exists")
	b, readErr := os.ReadFile(out)
	require.NoError(t, readErr)
	assert.Equal(t, "existing", string(b), "the pre-existing file is untouched")
}

// TestBackupCmd_RejectsOutUnderHome: a --out inside ~/.lucid is refused, writing
// nothing (P8).
func TestBackupCmd_RejectsOutUnderHome(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(home, "backup.tar.gz")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "backup", "--out", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inside the Ledger home")
	_, statErr := os.Stat(out)
	assert.ErrorIs(t, statErr, fs.ErrNotExist, "nothing written under home")
}

// TestBackupCmd_UnscaffoldableHome: a home that cannot be walked surfaces an
// error and leaves no partial archive behind.
func TestBackupCmd_UnscaffoldableHome(t *testing.T) {
	unscaffoldableHome(t)
	out := filepath.Join(t.TempDir(), "b.tar.gz")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "backup", "--out", out)
	require.Error(t, err)
	_, statErr := os.Stat(out)
	assert.ErrorIs(t, statErr, fs.ErrNotExist, "partial archive removed on failure")
}
