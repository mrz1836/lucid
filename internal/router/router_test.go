package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/storage"
)

// newScaffolded returns an adapter over a fresh, scaffolded temp Ledger.
func newScaffolded(t *testing.T) *storage.Adapter {
	t.Helper()
	_, a := lucidtest.Ledger(t)
	return a
}

func TestRouter_New_ZeroConfigBeforeBoot(t *testing.T) {
	r := New(newScaffolded(t))
	assert.Equal(t, config.Config{}, r.Config())
	assert.NotNil(t, r.Store())
}

// TestBoot_InRangeNoRewrite boots a healthy Ledger: no warnings and
// lucid.json is left byte-identical.
func TestBoot_InRangeNoRewrite(t *testing.T) {
	a := newScaffolded(t)
	before, err := os.ReadFile(a.ConfigPath())
	require.NoError(t, err)

	r := New(a)
	warnings, err := r.Boot()
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, 7, r.Config().RecentWindow)

	after, err := os.ReadFile(a.ConfigPath())
	require.NoError(t, err)
	assert.Equal(t, before, after, "in-range boot must not rewrite lucid.json")
}

// TestBoot_ClipsAndRewrites is acceptance test case 1.4: a hand-edited
// recent_window: 999 is clipped to 14 on boot, warned, and rewritten to
// disk.
func TestBoot_ClipsAndRewrites(t *testing.T) {
	a := newScaffolded(t)

	// Hand-edit the config above the ceiling.
	cfg := config.Default()
	cfg.RecentWindow = 999
	require.NoError(t, a.SaveConfig(cfg))

	r := New(a)
	warnings, err := r.Boot()
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "clipped to 14")
	assert.Equal(t, 14, r.Config().RecentWindow)

	// The persisted file now carries the clipped value.
	b, err := os.ReadFile(a.ConfigPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.EqualValues(t, 14, m["recent_window"])
}

func TestBoot_LoadError(t *testing.T) {
	// No scaffold: lucid.json is missing, so Boot's load fails.
	r := New(storage.New(t.TempDir()))
	_, err := r.Boot()
	assert.Error(t, err)
}

// TestBoot_SaveErrorSurfacesWarnings covers the clip-but-can't-persist
// path: warnings are still returned alongside the write error.
func TestBoot_SaveErrorSurfacesWarnings(t *testing.T) {
	home := t.TempDir()
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)

	cfg := config.Default()
	cfg.RecentWindow = 999
	require.NoError(t, a.SaveConfig(cfg))

	// Make lucid.json unwritable so SaveConfig fails during boot.
	cfgPath := filepath.Join(home, "lucid.json")
	require.NoError(t, os.Chmod(cfgPath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(cfgPath, 0o600) })

	// If running as root (chmod is ignored), skip: the write would
	// succeed and there is nothing to assert.
	if f, openErr := os.OpenFile(cfgPath, os.O_WRONLY, 0); openErr == nil {
		_ = f.Close()
		t.Skip("filesystem ignores 0400 (running as root?); skip unwritable-config case")
	}

	warnings, err := New(a).Boot()
	require.Error(t, err)
	assert.NotEmpty(t, warnings, "warnings must surface even when the rewrite fails")
}
