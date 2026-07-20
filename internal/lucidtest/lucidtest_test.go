package lucidtest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/lucidtest"
)

func assertDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "%s should be a directory", path)
}

func TestLedger(t *testing.T) {
	t.Run("bare ledger scaffolds at tempdir", func(t *testing.T) {
		home, a := lucidtest.Ledger(t)
		require.NotNil(t, a)
		assert.Equal(t, home, a.Home())
		assertDir(t, home)
	})
	t.Run("nested home roots under .lucid", func(t *testing.T) {
		home, a := lucidtest.Ledger(t, lucidtest.NestedHome())
		require.NotNil(t, a)
		assert.Equal(t, ".lucid", filepath.Base(home))
		assertDir(t, home)
	})
	t.Run("engine and observations subtrees scaffold cleanly", func(t *testing.T) {
		home, a := lucidtest.Ledger(t, lucidtest.WithEngine(), lucidtest.WithObservations())
		require.NotNil(t, a)
		assertDir(t, home)
	})
}
