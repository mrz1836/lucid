package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// readAnchorLog reads the append-only anchors store from an isolated home.
func readAnchorLog(t *testing.T, home string) engine.AnchorLog {
	t.Helper()
	log, err := storage.New(home).ReadAnchors()
	require.NoError(t, err)
	return log
}

// TestAnchorAdd_CLI_RecordsAndAcks records a backdated milestone and asserts
// the inventory ack plus one persisted record.
func TestAnchorAdd_CLI_RecordsAndAcks(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-01-01")
	require.NoError(t, err)
	assert.Contains(t, out, "Anchor recorded: sobriety — 2026-01-01.")

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 1)
	assert.Equal(t, "sobriety", log.History[0].Label)
	assert.Equal(t, "2026-01-01", log.History[0].Date)
}

// TestAnchorAdd_CLI_JSON emits the recorded anchor with a deterministic
// recorded_at from the pinned clock.
func TestAnchorAdd_CLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-01-01", "--json")
	require.NoError(t, err)

	var got engine.Anchor
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "sobriety", got.Label)
	assert.Equal(t, "2026-01-01", got.Date)
	assert.Equal(t, afternoon().Format(time.RFC3339), got.RecordedAt)
}

// TestAnchorAdd_CLI_JoinsNote joins the trailing note words into one string.
func TestAnchorAdd_CLI_JoinsNote(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate", "2026-03-15", "first", "ninety-day", "gate")
	require.NoError(t, err)

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 1)
	assert.Equal(t, "first ninety-day gate", log.History[0].Note)
}

// TestAnchorAdd_CLI_RejectsBadDate prints the fixed reason on stderr, exits
// non-zero, and writes nothing.
func TestAnchorAdd_CLI_RejectsBadDate(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	// Scaffold an empty store so "no write" is a meaningful assertion (the
	// rejection path returns before it would scaffold the engine tree).
	_, err := storage.New(home).Scaffold()
	require.NoError(t, err)
	require.NoError(t, storage.New(home).ScaffoldEngine())

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-13-40")
	require.ErrorIs(t, err, errAnchorNotRecorded)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out)
	assert.Contains(t, errOut, "YYYY-MM-DD")

	assert.Empty(t, readAnchorLog(t, home).History, "a rejected input must not write")
}

// TestAnchorAdd_CLI_MissingArgs guards the arg count: a label and a date are
// both required (mirrors TestModeCLI_RequiresExactlyOneArg).
func TestAnchorAdd_CLI_MissingArgs(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires at least 2 arg")
}

// TestAnchorAdd_CLI_BootError: a Ledger that cannot be scaffolded surfaces the
// boot error rather than a false success (mirrors TestModeCLI_BootError).
func TestAnchorAdd_CLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-01-01")
	require.Error(t, err)
}

// TestAnchorAdd_CLI_SecondAppendPersists records the same label twice; both
// entries survive in the append-only history and latest-wins folds to the newer.
func TestAnchorAdd_CLI_SecondAppendPersists(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-01-01")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2025-12-15")
	require.NoError(t, err)

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 2)

	latest := engine.LatestAnchors(log)
	require.Len(t, latest, 1)
	assert.Equal(t, "2025-12-15", latest[0].Date)
}
