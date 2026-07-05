package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolatedHome points LUCID_HOME at a fresh temp dir so `lucid init`
// never touches the real ~/.lucid/. It returns the home path.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".lucid")
	t.Setenv("LUCID_HOME", home)
	return home
}

// TestInit_FreshHost runs `lucid init` on an isolated home and asserts
// the full Mirror layout plus a jq-valid lucid.json (acceptance test
// cases 1.1).
func TestInit_FreshHost(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	assert.Contains(t, out, "Scaffolded Ledger")

	for _, d := range []string{"raw", "processed", "insights", "people", "sessions", "reflections"} {
		info, statErr := os.Stat(filepath.Join(home, d))
		require.NoErrorf(t, statErr, "dir %s", d)
		assert.True(t, info.IsDir())
		_, keepErr := os.Stat(filepath.Join(home, d, ".keep"))
		require.NoErrorf(t, keepErr, ".keep under %s", d)
	}

	b, err := os.ReadFile(filepath.Join(home, "lucid.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.EqualValues(t, 1, m["version"])
	assert.EqualValues(t, 14, m["recent_window_max"])
	assert.EqualValues(t, 50, m["ask_insights_cap"])
}

// TestInit_Idempotent runs init twice; the second run reports the
// Ledger is already present and leaves lucid.json untouched (case 1.2).
func TestInit_Idempotent(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	before, err := os.ReadFile(filepath.Join(home, "lucid.json"))
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	assert.Contains(t, out, "already present")

	after, err := os.ReadFile(filepath.Join(home, "lucid.json"))
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

// TestInit_JSON checks the machine-readable contract.
func TestInit_JSON(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init", "--json")
	require.NoError(t, err)

	var res initResult
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	assert.NotEmpty(t, res.Home)
	assert.ElementsMatch(t,
		[]string{"raw", "processed", "insights", "people", "sessions", "reflections"},
		res.CreatedDirs)
	assert.True(t, res.WroteConfig)
	assert.Empty(t, res.Warnings)

	// Second run: nothing created, still valid JSON.
	out2, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init", "--json")
	require.NoError(t, err)
	var res2 initResult
	require.NoError(t, json.Unmarshal([]byte(out2), &res2))
	assert.Empty(t, res2.CreatedDirs)
	assert.False(t, res2.WroteConfig)
}

// TestInit_ClipsOutOfRangeConfig is acceptance test case 1.4 through
// the CLI: hand-edit recent_window above the cap, re-run init, and the
// value is clipped, warned on stderr, and rewritten.
func TestInit_ClipsOutOfRangeConfig(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)

	// Hand-edit lucid.json to an out-of-range recent_window.
	cfgPath := filepath.Join(home, "lucid.json")
	b, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	m["recent_window"] = 999
	edited, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, edited, 0o600))

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	assert.Contains(t, errOut, "warning:")
	assert.Contains(t, errOut, "clipped to 14")

	// The persisted value is now clipped.
	b2, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var m2 map[string]any
	require.NoError(t, json.Unmarshal(b2, &m2))
	assert.EqualValues(t, 14, m2["recent_window"])
}

// TestInit_JSONClipCarriesWarnings verifies the clip warning also
// travels through the machine-readable path.
func TestInit_JSONClipCarriesWarnings(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)

	cfgPath := filepath.Join(home, "lucid.json")
	b, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	m["recent_window"] = 999
	edited, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, edited, 0o600))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init", "--json")
	require.NoError(t, err)
	var res initResult
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "clipped to 14")
}
