package cli

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// jsonObject unmarshals one JSON object into an untyped map. A typed struct
// would hide an omitted or renamed key, which is exactly what the shape guards
// exist to catch.
func jsonObject(t *testing.T, out string) map[string]any {
	t.Helper()
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	return raw
}

// sortedKeys returns an object's key set in a stable order, so a guard can
// assert the whole set at once.
func sortedKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}

// seedLegacyAnchor appends a record in the pre-id shape — no id, no state —
// directly through storage, so a test can exercise the read-time synthetic
// identity on data this binary never wrote.
func seedLegacyAnchor(t *testing.T, home, label, date, note string) {
	t.Helper()
	st := storage.New(home)
	_, err := st.Scaffold()
	require.NoError(t, err)
	require.NoError(t, st.ScaffoldEngine())
	require.NoError(t, st.AppendAnchor(engine.Anchor{
		Label:      label,
		Date:       date,
		Note:       note,
		RecordedAt: afternoon().Format(time.RFC3339),
	}))
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

// TestAnchorAdd_CLI_JSON is the shape guard on `anchor add --json`: it asserts
// the EXACT key set, so a renamed, removed, or unexpected key fails here. The
// surface is projected rather than marshaled from the stored record, and `id`
// is unconditional — a harness must be able to address what it just wrote.
// `note` is omitempty, so both the bare and the annotated shapes are pinned.
func TestAnchorAdd_CLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-01-01", "--json")
	require.NoError(t, err)

	got := jsonObject(t, out)
	assert.Equal(t, []string{"date", "id", "label", "recorded_at"}, sortedKeys(got))
	assert.Equal(t, "sobriety", got["label"])
	assert.Equal(t, "2026-01-01", got["date"])
	assert.Equal(t, afternoon().Format(time.RFC3339), got["recorded_at"])
	assert.Equal(t, "anchor_2026_07_05_a", got["id"], "a fresh anchor mints a per-day slot id")
}

// TestAnchorAdd_CLI_JSONWithNote pins the annotated shape: `note` is the one
// conditional key, so it must appear when a note was recorded.
func TestAnchorAdd_CLI_JSONWithNote(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(
		t, BuildInfo{Version: "dev"}, "anchor", "add", "gate", "2026-03-15", "first", "gate", "--json",
	)
	require.NoError(t, err)

	got := jsonObject(t, out)
	assert.Equal(t, []string{"date", "id", "label", "note", "recorded_at"}, sortedKeys(got))
	assert.Equal(t, "first gate", got["note"])
}

// TestAnchorAdd_CLI_JSON_LegacyID: correcting an anchor recorded before ids
// existed keeps that record's id-less identity, so the surface has to publish
// the synthesized `legacy:` address rather than an empty string. This is why
// the command projects instead of marshaling the stored record.
func TestAnchorAdd_CLI_JSON_LegacyID(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	seedLegacyAnchor(t, home, "gate-30", "2026-02-01", "")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-02", "--json")
	require.NoError(t, err)

	got := jsonObject(t, out)
	assert.Equal(t, "legacy:gate-30", got["id"])

	// The correction stayed id-less on disk: the synthetic address lives only
	// at the projection boundary.
	log := readAnchorLog(t, home)
	require.Len(t, log.History, 2)
	assert.Empty(t, log.History[1].ID)
	require.Len(t, engine.LatestAnchors(log), 1, "a correction must not fork the milestone")
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

// TestAnchorSunset_CLI_RetiresAndAcks retires an active milestone: the ack
// names what stopped counting, and the store grows by exactly one record with
// the prior one untouched.
func TestAnchorSunset_CLI_RetiresAndAcks(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)
	before := readAnchorLog(t, home).History[0]

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-30", "mistyped", "label")
	require.NoError(t, err)
	assert.Contains(t, out, "Anchor sunset: gate-30 — 2026-02-01.")
	assert.Contains(t, out, "the record is kept")

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 2)
	assert.Equal(t, before, log.History[0], "an append must never rewrite a prior record")
	assert.Equal(t, engine.AnchorStateSunset, log.History[1].State)
	assert.Equal(t, "2026-02-01", log.History[1].Date, "the record keeps the milestone's own date")
	assert.Equal(t, "mistyped label", log.History[1].Note, "trailing words are joined into the reason")
}

// TestAnchorSunset_CLI_JSON emits the appended retirement record, state
// included, through the same projection every anchor surface uses.
func TestAnchorSunset_CLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-30", "--json")
	require.NoError(t, err)

	got := jsonObject(t, out)
	assert.Equal(t, []string{"date", "id", "label", "recorded_at", "state"}, sortedKeys(got))
	assert.Equal(t, "sunset", got["state"])
	assert.Equal(t, "anchor_2026_07_05_a", got["id"], "a retirement keeps the milestone's identity")
}

// TestAnchorSunset_CLI_RejectsUnknownLabel: the remedy travels with the error,
// because there is no `anchor list` verb. Nothing is appended.
func TestAnchorSunset_CLI_RejectsUnknownLabel(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-01-01")
	require.NoError(t, err)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-3")
	require.ErrorIs(t, err, errAnchorNotRecorded)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out)
	assert.Contains(t, errOut, `no anchor named "gate-3" — recorded anchors: gate-30, sobriety`)

	assert.Len(t, readAnchorLog(t, home).History, 2, "a rejected sunset must not append")
}

// TestAnchorSunset_CLI_RejectsEmptyStore names the empty store rather than
// trailing off after a list with nothing in it.
func TestAnchorSunset_CLI_RejectsEmptyStore(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-3")
	require.ErrorIs(t, err, errAnchorNotRecorded)
	assert.Contains(t, errOut, "no anchors are recorded yet")

	assert.Empty(t, readAnchorLog(t, home).History)
}

// TestAnchorSunset_CLI_RejectsAlreadySunset: a retired anchor is history, not
// an editable record, and the error points at the supported path.
func TestAnchorSunset_CLI_RejectsAlreadySunset(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-30")
	require.NoError(t, err)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-30")
	require.ErrorIs(t, err, errAnchorNotRecorded)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out)
	assert.Contains(t, errOut, "was already sunset on 2026-07-05")
	assert.Contains(t, errOut, "nothing was saved")

	assert.Len(t, readAnchorLog(t, home).History, 2, "a rejected sunset must not append")
}

// TestAnchorSunset_CLI_ReaddNamesThePriorRetirement: recording the freed name
// again starts a new milestone with its own id, and the ack says so rather
// than implying the old count resumed.
func TestAnchorSunset_CLI_ReaddNamesThePriorRetirement(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-30")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)
	assert.Equal(t, "Anchor added: gate-30 — 2026-02-01 (a previous gate-30 was sunset 2026-07-05).\n", out)

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 3)
	assert.NotEqual(t, log.History[1].ID, log.History[2].ID, "a freed name is a new milestone, not a resumed one")
}

// TestAnchorSunset_CLI_MissingArgs guards the arg count: a label is required.
func TestAnchorSunset_CLI_MissingArgs(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestAnchorRename_CLI_RenamesAndAcks: the milestone keeps its id, its date
// and its note, so only the display name moves.
func TestAnchorRename_CLI_RenamesAndAcks(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01", "first", "gate")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "rename", "gate-30", "gate-thirty")
	require.NoError(t, err)
	assert.Equal(t, "Anchor renamed: gate-30 → gate-thirty — 2026-02-01.\n", out)

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 2)
	assert.Equal(t, log.History[0].ID, log.History[1].ID, "a rename keeps the identity")
	assert.Equal(t, "gate-thirty", log.History[1].Label)
	assert.Equal(t, "2026-02-01", log.History[1].Date)
	assert.Equal(t, "first gate", log.History[1].Note)

	latest := engine.LatestAnchors(log)
	require.Len(t, latest, 1, "a rename must not fork the milestone")
	assert.Equal(t, "gate-thirty", latest[0].Label)
}

// TestAnchorRename_CLI_JSON emits the surviving renamed record — what the
// milestone is now, not what its old name became.
func TestAnchorRename_CLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "rename", "gate-30", "gate-thirty", "--json")
	require.NoError(t, err)

	got := jsonObject(t, out)
	assert.Equal(t, []string{"date", "id", "label", "recorded_at"}, sortedKeys(got))
	assert.Equal(t, "gate-thirty", got["label"])
	assert.Equal(t, "anchor_2026_07_05_a", got["id"])
}

// TestAnchorRename_CLI_AdoptsLegacyAnchor is the day-one path: every anchor in
// an existing ledger predates ids, so a first rename adopts one. End-to-end
// through the binary surface — the old name leaves the counting surface, the
// milestone continues under the new name with its date and note, and exactly
// one retired stub appears under the old name.
func TestAnchorRename_CLI_AdoptsLegacyAnchor(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	seedLegacyAnchor(t, home, "gate-30", "2026-02-01", "first gate")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "rename", "gate-30", "gate-thirty")
	require.NoError(t, err)
	assert.Contains(t, out, "Anchor renamed: gate-30 → gate-thirty — 2026-02-01.")
	assert.Contains(t, out, "anchors_sunset")

	log := readAnchorLog(t, home)
	require.Len(t, log.History, 3, "adoption writes the retirement stub and the surviving record")
	stub, adopted := log.History[1], log.History[2]
	assert.Empty(t, stub.ID, "the stub must stay id-less to retire the synthetic identity")
	assert.Equal(t, engine.AnchorStateSunset, stub.State)
	assert.Empty(t, stub.Note, "the note travels on the surviving record, not the stub")
	assert.Equal(t, "anchor_2026_07_05_a", adopted.ID)
	assert.Equal(t, "gate-thirty", adopted.Label)
	assert.Equal(t, "2026-02-01", adopted.Date)
	assert.Equal(t, "first gate", adopted.Note)

	// The projected surfaces agree with the ledger.
	metricsOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "metrics", "--json")
	require.NoError(t, err)
	var m engine.Metrics
	require.NoError(t, json.Unmarshal([]byte(metricsOut), &m))
	require.Len(t, m.Anchors, 1)
	assert.Equal(t, "gate-thirty", m.Anchors[0].Label)
	require.Len(t, m.AnchorsSunset, 1)
	assert.Equal(t, "gate-30", m.AnchorsSunset[0].Label)
	assert.Equal(t, "legacy:gate-30", m.AnchorsSunset[0].ID)
}

// TestAnchorRename_CLI_RejectsTakenActiveLabel: at most one active anchor per
// label is what lets every verb take a bare label, so the new name must be free.
func TestAnchorRename_CLI_RejectsTakenActiveLabel(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-01-01")
	require.NoError(t, err)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "rename", "gate-30", "sobriety")
	require.ErrorIs(t, err, errAnchorNotRecorded)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out)
	assert.Contains(t, errOut, `an active anchor already uses "sobriety"`)

	assert.Len(t, readAnchorLog(t, home).History, 2, "a rejected rename must not append")
}

// TestAnchorRename_CLI_OntoRetiredLabelSucceeds: a retirement frees the name,
// exactly as it does for `anchor add`. This is the rejection that must not fire.
func TestAnchorRename_CLI_OntoRetiredLabelSucceeds(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "sobriety", "2026-01-01")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "sobriety")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "rename", "gate-30", "sobriety")
	require.NoError(t, err)
	assert.Contains(t, out, "Anchor renamed: gate-30 → sobriety — 2026-02-01.")
}

// TestAnchorRename_CLI_RejectsUnknownLabel names the recorded labels, the same
// remedy `sunset` gives.
func TestAnchorRename_CLI_RejectsUnknownLabel(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "rename", "gate-3", "gate-thirty")
	require.ErrorIs(t, err, errAnchorNotRecorded)
	assert.Contains(t, errOut, `no anchor named "gate-3" — recorded anchors: gate-30`)

	assert.Len(t, readAnchorLog(t, home).History, 1, "a rejected rename must not append")
}

// TestAnchorRename_CLI_RejectsSameLabel: renaming a label to itself is a
// rejection, not a silent no-op append.
func TestAnchorRename_CLI_RejectsSameLabel(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "add", "gate-30", "2026-02-01")
	require.NoError(t, err)

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "rename", "gate-30", "gate-30")
	require.ErrorIs(t, err, errAnchorNotRecorded)
	assert.Contains(t, errOut, "is already its name")

	assert.Len(t, readAnchorLog(t, home).History, 1, "a rejected rename must not append")
}

// TestAnchorRename_CLI_ArgCount pins the two-argument contract: unlike sunset,
// rename takes no trailing reason, so a third word is a usage error.
func TestAnchorRename_CLI_ArgCount(t *testing.T) {
	for _, args := range [][]string{
		{"anchor", "rename", "gate-30"},
		{"anchor", "rename", "gate-30", "gate-thirty", "extra"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			isolatedHome(t)
			_, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
			require.Error(t, err)
			assert.Equal(t, ExitUsage, exitCodeForError(err))
		})
	}
}

// TestAnchorSunset_CLI_BootError: a Ledger that cannot be scaffolded surfaces
// the boot error rather than a false retirement.
func TestAnchorSunset_CLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-30")
	require.Error(t, err)
}

// TestAnchorRename_CLI_BootError: likewise for rename.
func TestAnchorRename_CLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "rename", "gate-30", "gate-thirty")
	require.Error(t, err)
}

// TestAnchorSunset_CLI_ReadErrorIsNotARejection: an unreadable store is a real
// failure, not a deterministic refusal. It must surface as itself rather than
// being flattened into the "nothing was recorded" exit, or a caller would read
// a broken Ledger as a well-formed no.
func TestAnchorSunset_CLI_ReadErrorIsNotARejection(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	seedLegacyAnchor(t, home, "gate-30", "2026-02-01", "")
	require.NoError(t, os.WriteFile(filepath.Join(home, "engine", "anchors.json"), []byte("not json"), 0o600))

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "sunset", "gate-30")
	require.Error(t, err)
	assert.NotErrorIs(t, err, errAnchorNotRecorded, "a read failure is not a rejection")
}
