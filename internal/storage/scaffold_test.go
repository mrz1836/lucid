package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
)

// mirrorDirNames is the expected six-directory Mirror set.
func mirrorDirNames() []string {
	return []string{"raw", "processed", "insights", "people", "sessions", "reflections"}
}

func assertLedgerLayout(t *testing.T, home string) {
	t.Helper()
	for _, d := range mirrorDirNames() {
		dir := filepath.Join(home, d)
		info, err := os.Stat(dir)
		require.NoErrorf(t, err, "dir %s missing", d)
		assert.Truef(t, info.IsDir(), "%s is not a directory", d)

		keep := filepath.Join(dir, ".keep")
		_, err = os.Stat(keep)
		require.NoErrorf(t, err, ".keep missing under %s", d)
	}
	_, err := os.Stat(filepath.Join(home, "lucid.json"))
	require.NoError(t, err, "lucid.json missing")
}

// TestScaffold_FreshHost is acceptance test case 1.1: a fresh host
// gets all six Mirror dirs (each with a .keep) and a schema-valid
// lucid.json.
func TestScaffold_FreshHost(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	res, err := a.Scaffold()
	require.NoError(t, err)
	assert.Equal(t, home, res.Home)
	assert.ElementsMatch(t, mirrorDirNames(), res.CreatedDirs)
	assert.True(t, res.WroteConfig)

	assertLedgerLayout(t, home)

	// lucid.json validates against the documented jq predicate.
	b, err := os.ReadFile(filepath.Join(home, "lucid.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.EqualValues(t, 1, m["version"])
	assert.EqualValues(t, 14, m["recent_window_max"])
	assert.EqualValues(t, 50, m["ask_insights_cap"])
}

// TestScaffold_Idempotent is acceptance test case 1.2: a second run
// changes nothing, reports nothing created, and leaves lucid.json
// byte-identical.
func TestScaffold_Idempotent(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	_, err := a.Scaffold()
	require.NoError(t, err)
	before, err := os.ReadFile(filepath.Join(home, "lucid.json"))
	require.NoError(t, err)

	res, err := a.Scaffold()
	require.NoError(t, err)
	assert.Empty(t, res.CreatedDirs)
	assert.False(t, res.WroteConfig)

	after, err := os.ReadFile(filepath.Join(home, "lucid.json"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "lucid.json must be untouched on re-run")
}

// TestScaffold_PreservesExistingEntry is acceptance test case 1.3: a
// host with an existing raw entry keeps it byte-identical; only the
// missing pieces are filled.
func TestScaffold_PreservesExistingEntry(t *testing.T) {
	home := t.TempDir()
	entryDir := filepath.Join(home, "raw", "2026", "05")
	require.NoError(t, os.MkdirAll(entryDir, 0o700))
	entry := filepath.Join(entryDir, "raw_2026_05_05_19_42.md")
	const body = "---\nid: raw_2026_05_05_19_42\n---\n# Entry\nsynthetic\n"
	require.NoError(t, os.WriteFile(entry, []byte(body), 0o600))

	a := New(home)
	res, err := a.Scaffold()
	require.NoError(t, err)

	// raw/ already existed, so it is not reported as created; the other
	// five are.
	assert.NotContains(t, res.CreatedDirs, "raw")
	assert.ElementsMatch(t,
		[]string{"processed", "insights", "people", "sessions", "reflections"},
		res.CreatedDirs)

	got, err := os.ReadFile(entry)
	require.NoError(t, err)
	assert.Equal(t, body, string(got), "pre-existing entry must be untouched")

	assertLedgerLayout(t, home)
}

// TestScaffold_RejectsFileWhereDirExpected covers the guard that a
// plain file sitting where a Mirror directory should be is a hard
// error, not a silent overwrite.
func TestScaffold_RejectsFileWhereDirExpected(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "processed"), []byte("x"), 0o600))

	_, err := New(home).Scaffold()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
}

func TestLoadSaveConfig_RoundTrip(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	require.NoError(t, os.MkdirAll(home, 0o700))

	cfg := config.Default()
	cfg.RecentWindow = 9
	require.NoError(t, a.SaveConfig(cfg))

	got, err := a.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, 9, got.RecentWindow)
	assert.Equal(t, cfg, got)
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := New(t.TempDir()).LoadConfig()
	assert.Error(t, err)
}

func TestLoadConfig_BadJSON(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "lucid.json"), []byte("{bad"), 0o600))
	_, err := New(home).LoadConfig()
	assert.Error(t, err)
}

// TestSaveConfig_WriteError covers the write-failure branch: an adapter
// whose home directory does not exist cannot write lucid.json.
func TestSaveConfig_WriteError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	err := New(missing).SaveConfig(config.Default())
	assert.Error(t, err)
}

// TestScaffold_HomeCreateError covers the failure to create the Ledger
// root when a plain file already occupies the home path.
func TestScaffold_HomeCreateError(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

	// home would have to be created *under* a regular file — MkdirAll fails.
	_, err := New(filepath.Join(blocker, "home")).Scaffold()
	assert.Error(t, err)
}

func TestPathAccessors(t *testing.T) {
	a := New("/tmp/x")
	assert.Equal(t, "/tmp/x", a.Home())
	assert.Equal(t, filepath.Join("/tmp/x", "lucid.json"), a.ConfigPath())
	assert.Equal(t,
		[]string{
			filepath.Join("/tmp/x", "raw"),
			filepath.Join("/tmp/x", "processed"),
			filepath.Join("/tmp/x", "insights"),
			filepath.Join("/tmp/x", "people"),
			filepath.Join("/tmp/x", "sessions"),
			filepath.Join("/tmp/x", "reflections"),
		},
		a.MirrorDirPaths(config.Default()))
}

func TestDefaultHome_EnvOverride(t *testing.T) {
	t.Setenv(EnvHome, "/custom/ledger")
	got, err := DefaultHome()
	require.NoError(t, err)
	assert.Equal(t, "/custom/ledger", got)
}

func TestDefaultHome_Fallback(t *testing.T) {
	t.Setenv(EnvHome, "")
	got, err := DefaultHome()
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, ".lucid"), "fallback home should end with .lucid, got %q", got)
}

func TestOpen_UsesEnvHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	a, err := Open()
	require.NoError(t, err)
	assert.Equal(t, home, a.Home())
}
