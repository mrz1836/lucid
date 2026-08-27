package storage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultHome_IsolatedOverride: an explicit LUCID_HOME pointing at an
// isolated tree is returned verbatim, with neither guard firing.
func TestDefaultHome_IsolatedOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)

	got, err := DefaultHome()
	require.NoError(t, err)
	assert.Equal(t, home, got)
}

// TestDefaultHome_RefusesRealHome: with the verification signal set and no
// override, DefaultHome refuses the real Ledger with a clean error instead of
// panicking (refuse is checked before the test-panic guard). HOME points at a
// tempdir so the real ~/.lucid is never even computed, let alone touched.
func TestDefaultHome_RefusesRealHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(EnvHome, "")
	t.Setenv(EnvRefuseRealHome, "1")

	got, err := DefaultHome()
	require.Error(t, err)
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), "refusing to use the real Ledger")
	assert.Contains(t, err.Error(), EnvRefuseRealHome)
	assert.Contains(t, err.Error(), filepath.Join(tmp, ".lucid"),
		"the refusal names the resolved real-home path")
}

// TestDefaultHome_RefusesExplicitRealHome: an explicit LUCID_HOME that names the
// real Ledger path cannot bypass the refuse guard — the guard compares the
// resolved path to the real home regardless of how it was selected.
func TestDefaultHome_RefusesExplicitRealHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	realHome := filepath.Join(tmp, ".lucid")
	t.Setenv(EnvHome, realHome)
	t.Setenv(EnvRefuseRealHome, "true")

	_, err := DefaultHome()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to use the real Ledger")
}

// TestRefuseRealHome_TruthyParsing: the signal is honored for 1/true
// (case-insensitive, space-trimmed) and ignored for everything else.
func TestRefuseRealHome_TruthyParsing(t *testing.T) {
	cases := map[string]bool{
		"1":     true,
		"true":  true,
		"TRUE":  true,
		"True":  true,
		" 1 ":   true,
		"":      false,
		"0":     false,
		"false": false,
		"yes":   false,
		"on":    false,
	}
	for value, want := range cases {
		t.Setenv(EnvRefuseRealHome, value)
		assert.Equalf(t, want, refuseRealHome(), "LUCID_REFUSE_REAL_HOME=%q", value)
	}
}
