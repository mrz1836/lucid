package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// persistedBootstrapMode reads the bootstrap_mode flag straight from the
// scaffolded lucid.json so a test asserts the toggle actually reached disk, not
// just the in-memory ack.
func persistedBootstrapMode(t *testing.T, home string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "lucid.json"))
	require.NoError(t, err)
	var cfg struct {
		BootstrapMode bool `json:"bootstrap_mode"`
	}
	require.NoError(t, json.Unmarshal(b, &cfg))
	return cfg.BootstrapMode
}

// TestBootstrapCLI_On: `lucid bootstrap` turns historical-entry mode on, prints
// the enter ack, and persists bootstrap_mode:true.
func TestBootstrapCLI_On(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "bootstrap")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, "Bootstrap mode on")
	assert.True(t, persistedBootstrapMode(t, home))
}

// TestBootstrapCLI_OnJSON: the --json view carries bootstrap_mode:true.
func TestBootstrapCLI_OnJSON(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "bootstrap", "--json")
	require.NoError(t, err)

	var v bootstrapView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.True(t, v.BootstrapMode)
	assert.True(t, persistedBootstrapMode(t, home))
}

// TestBootstrapCLI_Done: `lucid bootstrap done` turns a previously-on mode off,
// prints the exit ack, and persists bootstrap_mode:false.
func TestBootstrapCLI_Done(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "bootstrap")
	require.NoError(t, err)
	require.True(t, persistedBootstrapMode(t, home))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "bootstrap", "done")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, "Done.")
	assert.False(t, persistedBootstrapMode(t, home))
}

// TestBootstrapCLI_DoneJSON: the --json view carries bootstrap_mode:false.
func TestBootstrapCLI_DoneJSON(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "bootstrap", "done", "--json")
	require.NoError(t, err)

	var v bootstrapView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.False(t, v.BootstrapMode)
	assert.False(t, persistedBootstrapMode(t, home))
}

// TestBootstrapCLI_BadArgUsage: an unknown positional is an OnlyValidArgs usage
// error (exit 2) — `done` is the only accepted arg.
func TestBootstrapCLI_BadArgUsage(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "bootstrap", "foo")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestBootstrapCLI_TooManyArgsUsage: more than one positional is a usage error
// (exit 2).
func TestBootstrapCLI_TooManyArgsUsage(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "bootstrap", "done", "extra")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestBootstrapCLI_BootError: a Ledger that cannot be scaffolded surfaces the
// boot error rather than a false toggle.
func TestBootstrapCLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "bootstrap")
	require.Error(t, err)
}
