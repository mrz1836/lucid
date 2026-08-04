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

// TestAnchorAdd_CLI_AcceptsRelativeDay: the shared grammar reaches `anchor
// add`, so both spellings of the relative word record the previous day.
func TestAnchorAdd_CLI_AcceptsRelativeDay(t *testing.T) {
	for _, spelling := range []string{"@yesterday", "yesterday"} {
		t.Run(spelling, func(t *testing.T) {
			home := isolatedHome(t)
			withClock(t, afternoon()) // 2026-07-05 14:00, past the rollover

			out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "streak-restart", spelling)
			require.NoError(t, err)
			assert.Contains(t, out, "2026-07-04")

			log := readAnchorLog(t, home)
			require.Len(t, log.History, 1)
			assert.Equal(t, "2026-07-04", log.History[0].Date)
		})
	}
}

// TestAnchorAdd_CLI_RelativeDayRespectsRollover: before the 04:00 rollover the
// relative word steps back from the *logical* base day, not the calendar one —
// at 02:00 on the 5th, `yesterday` is the 3rd. An explicit date never shifts.
func TestAnchorAdd_CLI_RelativeDayRespectsRollover(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, time.Date(2026, 7, 5, 2, 0, 0, 0, time.UTC))

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "streak-restart", "@yesterday")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-07-04")
	require.NoError(t, err)

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 2)
	assert.Equal(t, "2026-07-03", log.History[0].Date, "the relative word rolls over")
	assert.Equal(t, "2026-07-04", log.History[1].Date, "an explicit date is literal")
}

// TestAnchorAdd_CLI_AcceptsFutureDate pins the first documented carve-out: an
// anchor may be a forward commitment, so `anchor add` is the one date surface
// with no future ceiling. A tidy-up that "restores" the ceiling breaks here.
func TestAnchorAdd_CLI_AcceptsFutureDate(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate", "2027-01-01")
	require.NoError(t, err)
	assert.Contains(t, out, "Anchor recorded: gate — 2027-01-01.")

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 1)
	assert.Equal(t, "2027-01-01", log.History[0].Date)
}

// TestAnchorAdd_CLI_RejectsPartialDate pins the second carve-out: a whole year
// or month is a clean error, never a count from a guessed January 1st — and
// never, via the bare-clock branch, an anchor silently dated today.
func TestAnchorAdd_CLI_RejectsPartialDate(t *testing.T) {
	for _, partial := range []string{"2014", "2014-09"} {
		t.Run(partial, func(t *testing.T) {
			home := isolatedHome(t)
			withClock(t, afternoon())
			// Scaffold so "nothing was written" is a real assertion: the
			// rejection returns before the engine tree would be created.
			_, err := storage.New(home).Scaffold()
			require.NoError(t, err)
			require.NoError(t, storage.New(home).ScaffoldEngine())

			out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sober", partial)
			require.ErrorIs(t, err, errAnchorNotRecorded)
			assert.Equal(t, ExitErr, exitCodeForError(err))
			assert.Empty(t, out)
			assert.Contains(t, errOut, "a days-since count needs a real day")
			assert.NotContains(t, errOut, "2026-07-05", "a partial date must never resolve to today")

			assert.Empty(t, readAnchorLog(t, home).History, "a rejected partial date must not write")
		})
	}
}

// TestAnchorAdd_CLI_RejectsUnreadableDate: free text is rejected with the
// accepted forms named, and nothing is written.
func TestAnchorAdd_CLI_RejectsUnreadableDate(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	_, err := storage.New(home).Scaffold()
	require.NoError(t, err)
	require.NoError(t, storage.New(home).ScaffoldEngine())

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sober", "spring 2015")
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
