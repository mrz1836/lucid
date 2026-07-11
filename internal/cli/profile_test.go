package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestProfileCLI_Accepted switches to the built-in `nights` profile; the switch
// is effective the next logical day (07-06 for an afternoon on 07-05).
func TestProfileCLI_Accepted(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "profile", "nights")
	require.NoError(t, err)
	assert.Contains(t, out, "Profile switches to nights")
	assert.Contains(t, out, "effective 2026-07-06")
}

// TestProfileCLI_JSON emits the structured profileView, carrying the next-day
// effective date.
func TestProfileCLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "profile", "nights", "--json")
	require.NoError(t, err)

	var v profileView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.Equal(t, engine.DefaultProfile, v.From)
	assert.Equal(t, "nights", v.To)
	assert.Equal(t, "2026-07-06", v.Effective)
	assert.False(t, v.Rejected)
}

// TestProfileCLI_UndefinedRejected: an undefined name is rejected with the fixed
// copy on stderr and a non-zero exit, no disk effect.
func TestProfileCLI_UndefinedRejected(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "profile", "weekends")
	require.ErrorIs(t, err, errProfileRejected)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out)
	assert.Contains(t, errOut, "No profile by that name")
}

// TestProfileCLI_RejectedJSON: a rejection under --json carries rejected:true on
// stdout and still exits non-zero.
func TestProfileCLI_RejectedJSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "profile", "weekends", "--json")
	require.ErrorIs(t, err, errProfileRejected)
	assert.Equal(t, ExitErr, exitCodeForError(err))

	var v profileView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.True(t, v.Rejected)
}

// TestProfileCLI_MissingArgUsage: a bare `lucid profile` is a cobra usage error
// (exit 2).
func TestProfileCLI_MissingArgUsage(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "profile")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestProfileCLI_TooManyArgsUsage: ExactArgs(1) rejects extra positionals as a
// usage error (exit 2).
func TestProfileCLI_TooManyArgsUsage(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "profile", "nights", "extra")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestProfileCLI_CorruptStateSurfaced: a malformed profile.json surfaces as a
// real error, distinct from a profile rejection.
func TestProfileCLI_CorruptStateSurfaced(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, "engine")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "profile.json"), []byte("{bad"), 0o600))

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "profile", "nights")
	require.Error(t, err)
	assert.NotErrorIs(t, err, errProfileRejected)
}

// TestProfileCLI_BootError: a Ledger that cannot be scaffolded surfaces the boot
// error rather than a false success.
func TestProfileCLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "profile", "nights")
	require.Error(t, err)
}
