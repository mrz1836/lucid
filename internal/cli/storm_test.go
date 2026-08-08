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

// TestStormCLI_BackdatedDeclare dates the storm event itself.
func TestStormCLI_BackdatedDeclare(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "unwritten", "--day", "@yesterday")
	require.NoError(t, err)
	assert.Contains(t, out, "storm declared (unwritten)")
}

// TestStormCLI_BackdatedLabelWithSpaces: the flag composes with a spaced clause
// label, which is still joined from the trailing args.
func TestStormCLI_BackdatedLabelWithSpaces(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "wrist", "flare", "--day", "@yesterday")
	require.ErrorIs(t, err, errStormRejected)
	assert.Contains(t, errOut, "No clause or window by that name",
		"the joined label reached the router intact rather than being split by the flag")
}

// TestStormCLI_UnreadableDay surfaces the strict-tier refusal on stderr with a
// non-zero exit, like an unknown clause label.
func TestStormCLI_UnreadableDay(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "unwritten", "--day", "@yesterdya")
	require.ErrorIs(t, err, errStormRejected)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "could not read the day")
}

// TestStormCLI_UnreadableDayJSON: a harness branching on the JSON shape sees a
// refusal as rejected, not as a bare exit code.
func TestStormCLI_UnreadableDayJSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "unwritten", "--day", "@yesterdya", "--json")
	require.ErrorIs(t, err, errStormRejected)
	assert.Contains(t, out, "\"rejected\": true")
}

// seedStormHistory writes storm.json with the given history events and no
// episodes index — the shape a storm.json written before episodes existed has —
// so the backfill command has runs to index. Every label is synthetic.
func seedStormHistory(t *testing.T, home string, events ...engine.StormEvent) {
	t.Helper()
	dir := filepath.Join(home, "engine")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	h := engine.StormHistory{
		Clauses:      []string{},
		Windows:      []engine.StormWindow{},
		DurationDays: engine.StormDurationDefault,
		History:      events,
	}
	b, err := json.MarshalIndent(h, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "storm.json"), b, 0o600))
}

// stormFileBytes reads the raw storm.json bytes so a CLI test can assert a dry
// run wrote nothing.
func stormFileBytes(t *testing.T, home string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "engine", "storm.json"))
	require.NoError(t, err)
	return b
}

// TestStormCmd_PositionalFormsStillDispatch is the A2 regression guard: with the
// `backfill-episodes` subcommand registered, every positional `storm` form still
// reaches StormCommand rather than being read as an unknown subcommand, and a
// bare `storm` still fails the MinimumNArgs(1) usage check.
func TestStormCmd_PositionalFormsStillDispatch(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	seedStormClauses(t, home, "clause-a")

	// A registered clause label reaches the declare path.
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "clause-a")
	require.NoError(t, err)
	assert.Contains(t, out, "storm declared (clause-a)")

	// `unwritten` reaches the declare path (always declarable).
	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "storm", "unwritten")
	require.NoError(t, err)
	assert.Contains(t, out, "storm declared (unwritten)")

	// `end` reaches the end path — nothing stands, so it is the fixed rejection,
	// not a cobra "unknown command for storm".
	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "end")
	require.ErrorIs(t, err, errStormRejected)
	assert.Contains(t, errOut, "No standing storm to end")

	// A bare `storm` is still the MinimumNArgs(1) usage error (exit 2).
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "storm")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestStormCLI_BackfillEpisodes_DryRunWritesNothing: a dry run prints what it
// would append, exits 0, and leaves storm.json byte-identical.
func TestStormCLI_BackfillEpisodes_DryRunWritesNothing(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	seedStormHistory(t, home,
		engine.StormEvent{At: "2026-07-01T08:00:00Z", Event: engine.StormDeclared, Label: "clause-a"},
	)

	before := stormFileBytes(t, home)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "backfill-episodes", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "storm_2026_07_01_a")
	assert.Contains(t, out, "nothing was written")
	assert.Equal(t, before, stormFileBytes(t, home), "a dry run must not write")
}

// TestStormCLI_BackfillEpisodes_JSON is the shape guard on `storm
// backfill-episodes --json`: the planned episodes are published with their
// minted ids and `written` reports the real write happened.
func TestStormCLI_BackfillEpisodes_JSON(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	seedStormHistory(t, home,
		engine.StormEvent{At: "2026-07-01T08:00:00Z", Event: engine.StormDeclared, Label: "clause-a"},
	)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "backfill-episodes", "--json")
	require.NoError(t, err)

	var got struct {
		Planned []map[string]any `json:"planned"`
		Written bool             `json:"written"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.True(t, got.Written)
	require.Len(t, got.Planned, 1)
	assert.Equal(t, "storm_2026_07_01_a", got.Planned[0]["id"])
	assert.Equal(t, "2026-07-01T08:00:00Z", got.Planned[0]["opener_at"])
}

// TestStormCLI_BackfillEpisodes_SecondRunReportsNothing: the backfill is
// idempotent, so a second run appends nothing and says so.
func TestStormCLI_BackfillEpisodes_SecondRunReportsNothing(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	seedStormHistory(t, home,
		engine.StormEvent{At: "2026-07-01T08:00:00Z", Event: engine.StormDeclared, Label: "clause-a"},
	)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "backfill-episodes")
	require.NoError(t, err)
	assert.Contains(t, out, "Indexed 1 storm episode")
	assert.Contains(t, out, "storm_2026_07_01_a")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "storm", "backfill-episodes")
	require.NoError(t, err)
	assert.Contains(t, out, "Every storm run already carries an episode id")
}

// TestStormCLI_BackfillEpisodes_BootError: a Ledger that cannot be scaffolded
// surfaces the boot error rather than a false success.
func TestStormCLI_BackfillEpisodes_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "storm", "backfill-episodes")
	require.Error(t, err)
}
