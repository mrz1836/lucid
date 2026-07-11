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

// seedStormClauses writes storm.json with the given registered clause labels
// before any command runs, so a declaration of a real clause is accepted. The
// engine scaffold only writes storm.json when missing, so this seed survives.
func seedStormClauses(t *testing.T, home string, clauses ...string) {
	t.Helper()
	dir := filepath.Join(home, "engine")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	h := engine.StormHistory{
		Clauses:      clauses,
		Windows:      []engine.StormWindow{},
		DurationDays: engine.StormDurationDefault,
		History:      []engine.StormEvent{},
	}
	b, err := json.MarshalIndent(h, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "storm.json"), b, 0o600))
}

// TestStormCLI_DeclareUnwritten declares `/storm unwritten` — always declarable
// — and prints the pending-confirmation ack.
func TestStormCLI_DeclareUnwritten(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "unwritten")
	require.NoError(t, err)
	assert.Contains(t, out, "storm declared (unwritten)")
	assert.Contains(t, out, "pending witness confirmation")
}

// TestStormCLI_DeclareJSON emits the structured stormView for a declaration.
func TestStormCLI_DeclareJSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "unwritten", "--json")
	require.NoError(t, err)

	var v stormView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.Equal(t, engine.StormDeclared, v.Event)
	assert.Equal(t, "unwritten", v.Label)
	assert.False(t, v.Rejected)
}

// TestStormCLI_JoinsSpacedLabel proves trailing args are joined: a two-word
// registered clause is accepted only when "family emergency" is treated as one
// label (a non-joining verb would pass just "family" → unknown → rejected).
func TestStormCLI_JoinsSpacedLabel(t *testing.T) {
	home := isolatedHome(t)
	seedStormClauses(t, home, "family emergency")
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "family", "emergency")
	require.NoError(t, err)
	assert.Contains(t, out, "storm declared (family emergency)")
}

// TestStormCLI_EndNoStandingRejected: `storm end` with nothing standing prints
// the fixed copy to stderr and exits non-zero.
func TestStormCLI_EndNoStandingRejected(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "end")
	require.ErrorIs(t, err, errStormRejected)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out)
	assert.Contains(t, errOut, "No standing storm to end")
}

// TestStormCLI_UnknownLabelRejected: an unregistered label is rejected with the
// fixed copy and a non-zero exit.
func TestStormCLI_UnknownLabelRejected(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "no-such-clause")
	require.ErrorIs(t, err, errStormRejected)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Contains(t, errOut, "No clause or window by that name")
}

// TestStormCLI_RejectedJSON: a rejection under --json carries rejected:true on
// stdout and still exits non-zero.
func TestStormCLI_RejectedJSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "no-such-clause", "--json")
	require.ErrorIs(t, err, errStormRejected)
	assert.Equal(t, ExitErr, exitCodeForError(err))

	var v stormView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.True(t, v.Rejected)
}

// TestStormCLI_NoArgUsage: a bare `lucid storm` is a cobra usage error (exit 2).
func TestStormCLI_NoArgUsage(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestStormCLI_CorruptStateSurfaced: a malformed storm.json surfaces as a real
// error, distinct from a storm rejection.
func TestStormCLI_CorruptStateSurfaced(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, "engine")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "storm.json"), []byte("{bad"), 0o600))

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "unwritten")
	require.Error(t, err)
	assert.NotErrorIs(t, err, errStormRejected)
}

// TestStormCLI_BootError: a Ledger that cannot be scaffolded surfaces the boot
// error rather than a false success.
func TestStormCLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "unwritten")
	require.Error(t, err)
}
