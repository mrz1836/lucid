package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnsureDir covers the shared write-path dir guard: it creates a missing
// tree, is idempotent, and wraps a MkdirAll failure with the record label.
func TestEnsureDir(t *testing.T) {
	t.Run("creates missing dir and is idempotent", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "a", "b", "c")
		require.NoError(t, ensureDir(dir, "widget"))
		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		require.NoError(t, ensureDir(dir, "widget"), "second call is a no-op")
	})
	t.Run("wraps a MkdirAll failure with the label", func(t *testing.T) {
		// A regular file where a parent directory is expected makes MkdirAll
		// fail with "not a directory".
		file := filepath.Join(t.TempDir(), "afile")
		require.NoError(t, os.WriteFile(file, []byte("x"), filePerm))
		err := ensureDir(filepath.Join(file, "child"), "widget")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "storage: prepare widget dir:")
	})
}

// TestWriteFileAtomic covers the durable write: it publishes the content at the
// adapter's file mode, stages nothing that outlives the call, and — the case it
// exists for — leaves a pre-existing record untouched when the write fails.
func TestWriteFileAtomic(t *testing.T) {
	t.Run("writes the content and the mode, leaving no temp behind", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "self.json")

		require.NoError(t, writeFileAtomic(path, []byte("{\"version\": 1}\n"), "self.json"))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "{\"version\": 1}\n", string(got))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, filePerm, info.Mode().Perm(), "the mode is set explicitly, so it does not depend on the umask")

		assert.Empty(t, stagedTempFiles(t, dir), "a successful write stages nothing that outlives it")
	})

	t.Run("replaces an existing file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "self.json")
		require.NoError(t, os.WriteFile(path, []byte("old"), filePerm))

		require.NoError(t, writeFileAtomic(path, []byte("new"), "self.json"))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "new", string(got))
		assert.Empty(t, stagedTempFiles(t, dir))
	})

	// The crash-durability proof, and the reason this helper exists: when the
	// write cannot complete, the record already on disk is still the record on
	// disk. A bare os.WriteFile would have truncated it before failing.
	t.Run("a failed write leaves the previous file byte-identical", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("chmod permission bits are a no-op as root")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "self.json")
		prior := []byte("{\n  \"version\": 1,\n  \"history\": [{\"id\": \"self_2026_08_05_a\"}]\n}\n")
		require.NoError(t, os.WriteFile(path, prior, filePerm))

		require.NoError(t, os.Chmod(dir, 0o500)) // no new entries may be created here
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		err := writeFileAtomic(path, []byte("truncated"), "self.json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "self.json")

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(prior), string(got), "a decade of history survives a write that could not complete")
	})
}

// stagedTempFiles lists the helper's dot-prefixed staging files left in dir, so
// a test can assert a call cleaned up after itself.
func stagedTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".*.tmp"))
	require.NoError(t, err)
	return matches
}
