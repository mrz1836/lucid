package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// shellHostileNote is free text an unquoted shell line would cut short: the
// file flags exist so it reaches the store whole.
const shellHostileNote = "cake & candles; then home"

// writeNoteFile writes the shell-hostile note, with the trailing newline an
// editor leaves, to a file under the test's temp dir and returns its path.
func writeNoteFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(shellHostileNote+"\n"), 0o600))
	return path
}

// readAnchorEvents reads the anchor event store from an isolated home.
func readAnchorEvents(t *testing.T, home string) engine.AnchorEventLog {
	t.Helper()
	log, err := storage.New(home).ReadAnchorEvents()
	require.NoError(t, err)
	return log
}

// runAnchor runs one anchor verb and requires it to succeed.
func runAnchor(t *testing.T, args ...string) string {
	t.Helper()
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, append([]string{"anchor"}, args...)...)
	require.NoError(t, err, "stderr: %s", stderr)
	return out
}

// requireUsageError asserts an anchor invocation is a usage error (exit 2)
// that names reason and wrote nothing to either anchor file.
func requireUsageError(t *testing.T, home, reason string, args ...string) {
	t.Helper()
	before := readAnchorLog(t, home)
	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, append([]string{"anchor"}, args...)...)
	require.Error(t, err)
	var ue *usageError
	require.ErrorAs(t, err, &ue, "%v should be a usage error", args)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assert.Contains(t, stderr, reason)
	assert.Contains(t, stderr, "Nothing was saved.")
	assert.Equal(t, before, readAnchorLog(t, home), "a usage error writes nothing")
}

// TestAnchorVerbsJSONAndFileFlags pins the --json shapes of add --kind, kind,
// slip, use and void, proves every free-text field travels whole through its
// file flag (an unquoted & or ; never cuts it short), and types the
// command-line rejections as usage errors.
func TestAnchorVerbsJSONAndFileFlags(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	t.Run("add --kind", func(t *testing.T) {
		note := writeNoteFile(t, "add-note.txt")
		got := jsonObject(t, runAnchor(t, "add", "sobriety", "2026-01-01", "--kind", "quit", "--note-file", note, "--json"))
		assert.Equal(t,
			[]string{"date", "event", "id", "kind", "label", "note", "receipt_id", "recorded_at"}, sortedKeys(got))
		assert.Equal(t, "quit", got["kind"])
		assert.Equal(t, "anchorev_2026_07_05_a", got["receipt_id"])
		assert.Equal(t, shellHostileNote, got["note"], "the definition's note arrives whole")
		event, ok := got["event"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []string{"anchor_id", "day", "id", "kind", "recorded_at", "type"}, sortedKeys(event))
		assert.Equal(t, got["id"], event["anchor_id"])

		limit := jsonObject(t, runAnchor(t, "add", "late-night-snacks", "2026-01-01",
			"--kind", "limit", "--allowance", "4", "--window", "90", "--json"))
		event, ok = limit["event"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 4, event["allowance"], 0)
		assert.InDelta(t, 90, event["window_days"], 0)

		// A plain add's shape is unchanged.
		plain := jsonObject(t, runAnchor(t, "add", "first-gate", "2026-03-01", "--json"))
		assert.Equal(t, []string{"date", "id", "label", "recorded_at"}, sortedKeys(plain))
	})

	t.Run("kind", func(t *testing.T) {
		note := writeNoteFile(t, "kind-note.txt")
		got := jsonObject(t, runAnchor(t, "kind", "first-gate", "habit", "--window", "14", "--note-file", note, "--json"))
		assert.Equal(t, []string{"event", "label", "logical_date", "receipt_id"}, sortedKeys(got),
			"no compat when nothing was re-dated")
		event, ok := got["event"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, shellHostileNote, event["note"])
		assert.InDelta(t, 14, event["window_days"], 0)
		assert.Equal(t, "first-gate", got["label"])
	})

	var slipID string
	t.Run("slip", func(t *testing.T) {
		note := writeNoteFile(t, "slip-note.txt")
		got := jsonObject(t, runAnchor(t, "slip", "sobriety", "--day", "2026-03-01", "--note-file", note, "--json"))
		assert.Equal(t, []string{"compat", "event", "label", "logical_date", "receipt_id"}, sortedKeys(got))
		assert.Equal(t, "2026-03-01", got["logical_date"], "logical_date is the event's day")
		compat, ok := got["compat"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []string{"date", "id", "label", "note", "recorded_at"}, sortedKeys(compat),
			"the re-date mirrors the definition, its note included")
		assert.Equal(t, "2026-03-01", compat["date"])
		event, ok := got["event"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, shellHostileNote, event["note"])
		slipID, _ = got["receipt_id"].(string)
		assert.Equal(t, event["id"], slipID)
	})

	t.Run("use, with the note on stdin", func(t *testing.T) {
		out, stderr, err := runRootWithStdin(t, shellHostileNote+"\n",
			"anchor", "use", "late-night-snacks", "--planned", "--note-file", "-", "--json")
		require.NoError(t, err, stderr)
		got := jsonObject(t, out)
		assert.Equal(t, []string{"event", "label", "logical_date", "receipt_id"}, sortedKeys(got))
		event, ok := got["event"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "planned", event["intent"])
		assert.Equal(t, shellHostileNote, event["note"])
	})

	t.Run("void", func(t *testing.T) {
		reason := writeNoteFile(t, "void-reason.txt")
		got := jsonObject(t, runAnchor(t, "void", slipID, "--reason-file", reason, "--json"))
		assert.Equal(t, []string{"compat", "event", "label", "logical_date", "receipt_id"}, sortedKeys(got),
			"voiding the re-dating slip restores the definition")
		event, ok := got["event"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, slipID, event["target"])
		assert.Equal(t, shellHostileNote, event["note"])
		compat, ok := got["compat"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "2026-01-01", compat["date"])
	})

	t.Run("every note is stored verbatim", func(t *testing.T) {
		var notes []string
		for _, e := range readAnchorEvents(t, home).Events {
			if e.Note != "" {
				notes = append(notes, e.Note)
			}
		}
		assert.Equal(t, []string{shellHostileNote, shellHostileNote, shellHostileNote, shellHostileNote}, notes)
	})

	t.Run("the prose ack names the receipt", func(t *testing.T) {
		out := runAnchor(t, "slip", "sobriety")
		// Receipts so far: three kind events (a plain add writes none), a slip, a
		// use and a void — so this slip is the seventh.
		assert.Contains(t, out, "Logged a slip on sobriety for 2026-07-05 as `anchorev_2026_07_05_g`")
	})

	t.Run("command-line rejections are usage errors", func(t *testing.T) {
		requireUsageError(t, home, "say whether this use was planned or a slip", "use", "late-night-snacks")
		requireUsageError(t, home, "say whether this use was planned or a slip",
			"use", "late-night-snacks", "--planned", "--slip")
		requireUsageError(t, home, "is not an anchor kind", "kind", "sobriety", "streak")
		requireUsageError(t, home, "is not an anchor kind", "add", "x", "2026-01-01", "--kind", "streak")
		requireUsageError(t, home, "go with --kind", "add", "x", "2026-01-01", "--allowance", "4", "--window", "90")
		requireUsageError(t, home, "a limit needs both", "kind", "sobriety", "limit", "--allowance", "4")
		requireUsageError(t, home, "--allowance is a positive whole number",
			"kind", "sobriety", "limit", "--allowance", "0", "--window", "90")
		requireUsageError(t, home, "only a limit or a habit takes it", "kind", "sobriety", "quit", "--window", "30")
	})

	t.Run("store rejections exit 1 with the reason on stderr", func(t *testing.T) {
		_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "use", "sobriety", "--slip")
		require.ErrorIs(t, err, errAnchorNotRecorded)
		assert.Equal(t, ExitErr, exitCodeForError(err))
		assert.Contains(t, stderr, `"sobriety" is a quit, and a use belongs to a limit`)

		_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "void", "anchorev_2026_01_01_z")
		require.ErrorIs(t, err, errAnchorNotRecorded)
		assert.Contains(t, stderr, `no anchor event "anchorev_2026_01_01_z"`)
	})
}

// TestAnchorEventDayRollover: an event written after midnight and before the
// rollover lands on the evening's logical day — the day metrics counts
// against — and its receipt is minted from that day; @yesterday is the day
// before it, and a day after it has not happened yet.
func TestAnchorEventDayRollover(t *testing.T) {
	home := isolatedHome(t)
	afterMidnight := time.Date(2026, time.July, 6, 0, 30, 0, 0, time.UTC) // before the 04:00 rollover
	withClock(t, afterMidnight)

	add := jsonObject(t, runAnchor(t, "add", "sobriety", "2026-01-01", "--kind", "quit", "--json"))
	assert.Equal(t, "anchorev_2026_07_05_a", add["receipt_id"])
	runAnchor(t, "add", "late-night-snacks", "2026-01-01", "--kind", "limit", "--allowance", "4", "--window", "90")

	slip := jsonObject(t, runAnchor(t, "slip", "sobriety", "--json"))
	assert.Equal(t, "2026-07-05", slip["logical_date"], "a slip at 00:30 belongs to the evening before")
	assert.Equal(t, "anchorev_2026_07_05_c", slip["receipt_id"])

	use := jsonObject(t, runAnchor(t, "use", "late-night-snacks", "--slip", "--day", "@yesterday", "--json"))
	assert.Equal(t, "2026-07-04", use["logical_date"], "@yesterday is the day before the logical day")

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "anchor", "slip", "sobriety", "--day", "2026-07-06")
	require.ErrorIs(t, err, errAnchorNotRecorded)
	assert.Contains(t, stderr, "that day has not happened yet")

	_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "anchor", "slip", "sobriety", "--day", "2026-07")
	require.ErrorIs(t, err, errAnchorNotRecorded)
	assert.Contains(t, stderr, "an anchor event lands on a real day")

	withClock(t, time.Date(2026, time.July, 6, 5, 0, 0, 0, time.UTC)) // past the rollover
	later := jsonObject(t, runAnchor(t, "slip", "sobriety", "--json"))
	assert.Equal(t, "2026-07-06", later["logical_date"])
	assert.Equal(t, "anchorev_2026_07_06_a", later["receipt_id"])

	days := map[string]string{}
	for _, e := range readAnchorEvents(t, home).Events {
		days[e.ID] = e.Day
	}
	assert.Equal(t, "2026-07-05", days["anchorev_2026_07_05_c"])
	assert.Equal(t, "2026-07-06", days["anchorev_2026_07_06_a"])
}
