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
