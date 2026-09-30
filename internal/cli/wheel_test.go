package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// wheelClock is a deterministic mid-month instant; the current logical month is
// 2026-09.
func wheelClock() time.Time { return time.Date(2026, 9, 6, 17, 12, 40, 0, time.UTC) }

// wheelRatingArgs returns the eight rating flags with synthetic values.
func wheelRatingArgs() []string {
	return []string{
		"--health", "6", "--relationships", "7", "--career", "5", "--finances", "4",
		"--growth", "7", "--fun", "4", "--environment", "6", "--contribution", "5",
	}
}

// wheelAddArgs builds `wheel add <ratings> <extra…>`.
func wheelAddArgs(extra ...string) []string {
	return append(append([]string{"wheel", "add"}, wheelRatingArgs()...), extra...)
}

// readWheelMonth reads a month's entry straight from the isolated Ledger.
func readWheelMonth(t *testing.T, home, month string) (observations.WheelEntry, bool) {
	t.Helper()
	e, found, err := storage.New(home).ReadWheel(month)
	require.NoError(t, err)
	return e, found
}

// assertNoWheel asserts nothing was written to the wheel tree.
func assertNoWheel(t *testing.T, home string) {
	t.Helper()
	all, err := storage.New(home).ReadWheelAll()
	require.NoError(t, err)
	assert.Empty(t, all, "a refused add writes nothing")
}

// syntheticWheelDoc is a valid --input document — invented test data.
const syntheticWheelDoc = `{
  "month": "2026-08",
  "pillars": {
    "health":        { "score": 6, "note": "synthetic walk note", "suggested": 5 },
    "relationships": { "score": 7 },
    "career":        { "score": 5, "suggested": 6 },
    "finances":      { "score": 4, "note": "synthetic repair note" },
    "growth":        { "score": 7 },
    "fun":           { "score": 4 },
    "environment":   { "score": 6 },
    "contribution":  { "score": 5 }
  },
  "vision_reviewed": true,
  "vision_reflection": "Synthetic reflection."
}`

// TestWheelAddCLI_HumanAck: `wheel add` records the month and prints the ack —
// the month, the receipt, and the eight stored ratings, one `label: N` line
// each, never a suggestion (wheel.md §7.1).
func TestWheelAddCLI_HumanAck(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs(
		"--note", "health=synthetic walk note", "--suggested", "health=2", "--suggested", "fun=9",
	)...)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, strings.Join([]string{
		"Recorded the 2026-09 wheel as wheel_2026_09_001.",
		"health: 6", "relationships: 7", "career/work: 5", "finances: 4",
		"personal growth: 7", "fun/recreation: 4", "environment: 6", "contribution: 5",
	}, "\n")+"\n", out)

	e, found := readWheelMonth(t, home, "2026-09")
	require.True(t, found)
	snap := e.History[0]
	assert.Equal(t, "synthetic walk note", snap.Pillars["health"].Note)
	assert.Equal(t, 6, snap.Pillars["health"].Score)
	require.NotNil(t, snap.Pillars["health"].Suggested)
	assert.Equal(t, 2, *snap.Pillars["health"].Suggested, "the suggestion is stored in its own field")
	assert.Equal(t, 9, *snap.Pillars["fun"].Suggested)
	assert.Equal(t, 4, snap.Pillars["fun"].Score)
}

// TestWheelSameMonthLatestWins_CLI: a second add for a month appends and says so
// in the ack; --json reports amended and the stored self-ratings only.
func TestWheelSameMonthLatestWins_CLI(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs()...)
	require.NoError(t, err)

	args := []string{
		"wheel", "add", "--health", "8", "--relationships", "7", "--career", "5", "--finances", "4",
		"--growth", "7", "--fun", "4", "--environment", "6", "--contribution", "5", "--suggested", "career=1",
	}
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded the 2026-09 wheel as wheel_2026_09_002 (replaces wheel_2026_09_001 when read; both kept).")
	assert.Contains(t, out, "health: 8")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, append(args, "--json")...)
	require.NoError(t, err)
	var view struct {
		ReceiptID string         `json:"receipt_id"`
		EntryID   string         `json:"entry_id"`
		Month     string         `json:"month"`
		Amended   bool           `json:"amended"`
		Scores    map[string]int `json:"scores"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &view), out)
	assert.Equal(t, "wheel_2026_09_003", view.ReceiptID)
	assert.Equal(t, "wheel_2026-09", view.EntryID)
	assert.Equal(t, "2026-09", view.Month)
	assert.True(t, view.Amended)
	assert.Equal(t, map[string]int{
		"health": 8, "relationships": 7, "career": 5, "finances": 4,
		"growth": 7, "fun": 4, "environment": 6, "contribution": 5,
	}, view.Scores, "scores are the self-ratings; career's suggestion of 1 never appears")

	e, found := readWheelMonth(t, home, "2026-09")
	require.True(t, found)
	assert.Len(t, e.History, 3, "every add kept its receipt")
}

// TestWheelSelfRatingRequired_CLI: every rating flag is required — a missing
// one is a usage error (exit 2) naming each missing pillar, and nothing is
// written (wheel.md §3).
func TestWheelSelfRatingRequired_CLI(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "wheel", "add", "--health", "6", "--relationships", "7")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assert.Contains(t, stderr,
		`required flag(s) "career", "finances", "growth", "fun", "environment", "contribution" not set`)
	assert.Contains(t, stderr, "nothing was saved")

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "wheel", "add")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assertNoWheel(t, home)
}

// TestWheelScaleRejects_CLI: a rating that is not a plain whole number 1–10 is a
// usage error naming the pillar and the range, and nothing is written.
func TestWheelScaleRejects_CLI(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	for _, bad := range []string{"2.5", "11", "0", "six", "-1", "6.0", ""} {
		args := []string{
			"wheel", "add", "--health", "6", "--relationships", "7", "--career", "5", "--finances", "4",
			"--growth", "7", "--fun", bad, "--environment", "6", "--contribution", "5",
		}
		_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
		require.Errorf(t, err, "--fun %q", bad)
		assert.Equalf(t, ExitUsage, exitCodeForError(err), "--fun %q", bad)
		assert.Containsf(t, stderr, `for "--fun" flag: fun/recreation needs a whole number from 1 to 10`, "--fun %q", bad)
	}
	assertNoWheel(t, home)
}

// TestWheelAddCLI_PairFlagErrors: --note and --suggested take <pillar>=<value>;
// a pair with no `=`, an unknown pillar, a pillar named twice, an empty note,
// or an off-scale suggestion is a usage error and nothing is written.
func TestWheelAddCLI_PairFlagErrors(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	cases := map[string][]string{
		"want <pillar>=<value>":                    {"--note", "health"},
		"where <pillar> is one of health":          {"--note", "wealth=rich"},
		"health is named twice":                    {"--note", "health=a", "--note", "health=b"},
		"the note is empty":                        {"--note", "health=  "},
		"a suggestion needs a whole number from 1": {"--suggested", "fun=11"},
		`for "--suggested" flag: career is named`:  {"--suggested", "career=2", "--suggested", "career=3"},
	}
	for want, extra := range cases {
		_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs(extra...)...)
		require.Errorf(t, err, "%v", extra)
		assert.Equalf(t, ExitUsage, exitCodeForError(err), "%v", extra)
		assert.Containsf(t, stderr, want, "%v", extra)
		assert.Containsf(t, stderr, "nothing was saved", "%v", extra)
	}
	assertNoWheel(t, home)
}

// TestWheelAddCLI_VisionReflection: the reflection comes inline or from a file
// (or stdin), is stored verbatim, and implies --vision-reviewed; giving both
// forms, or an empty file, is refused (wheel.md §6, §7.1).
func TestWheelAddCLI_VisionReflection(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	_, _, err := runRootWithStdin(t, "Synthetic: still true; pace shifted.\n",
		wheelAddArgs("--month", "2026-07", "--vision-reflection-file", "-")...)
	require.NoError(t, err)
	e, _ := readWheelMonth(t, home, "2026-07")
	assert.True(t, e.History[0].VisionReviewed, "a reflection implies the re-read")
	assert.Equal(t, "Synthetic: still true; pace shifted.", e.History[0].VisionReflection)

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs("--month", "2026-08", "--vision-reviewed")...)
	require.NoError(t, err)
	e, _ = readWheelMonth(t, home, "2026-08")
	assert.True(t, e.History[0].VisionReviewed)
	assert.Empty(t, e.History[0].VisionReflection)

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs(
		"--vision-reflection", "x", "--vision-reflection-file", "-",
	)...)
	require.Error(t, err)
	assert.Contains(t, stderr, "not both")

	empty := filepath.Join(t.TempDir(), "empty.txt")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs("--vision-reflection-file", empty)...)
	require.Error(t, err)
	assert.Contains(t, stderr, "file is empty")

	_, found := readWheelMonth(t, home, "2026-09")
	assert.False(t, found, "the refused adds wrote nothing")
}

// TestWheelAddCLI_Month: --month names the month (a leading @ tolerated); a
// future or malformed month is refused and nothing is written.
func TestWheelAddCLI_Month(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs("--month", "@2026-05")...)
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded the 2026-05 wheel as wheel_2026_05_001.")

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs("--month", "2026-10")...)
	require.Error(t, err)
	assert.Contains(t, stderr, "that month has not happened yet")

	_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs("--month", "Sept")...)
	require.Error(t, err)
	assert.Contains(t, stderr, "(want YYYY-MM)")

	all, err := storage.New(home).ReadWheelAll()
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

// TestWheelAddCLI_Input: --input reads the whole wheel from a strict JSON
// document — a file or stdin — storing ratings, notes, suggestions (separately),
// and the vision fields exactly as given (wheel.md §7.1).
func TestWheelAddCLI_Input(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	path := filepath.Join(t.TempDir(), "wheel.json")
	require.NoError(t, os.WriteFile(path, []byte(syntheticWheelDoc), 0o600))
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "wheel", "add", "--input", path, "--json")
	require.NoError(t, err)
	var view struct {
		ReceiptID string `json:"receipt_id"`
		Month     string `json:"month"`
		Amended   bool   `json:"amended"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &view), out)
	assert.Equal(t, "wheel_2026_08_001", view.ReceiptID)
	assert.Equal(t, "2026-08", view.Month)
	assert.False(t, view.Amended)

	e, found := readWheelMonth(t, home, "2026-08")
	require.True(t, found)
	snap := e.History[0]
	assert.Equal(t, 6, snap.Pillars["health"].Score)
	assert.Equal(t, "synthetic walk note", snap.Pillars["health"].Note)
	require.NotNil(t, snap.Pillars["health"].Suggested)
	assert.Equal(t, 5, *snap.Pillars["health"].Suggested)
	assert.Nil(t, snap.Pillars["growth"].Suggested)
	assert.True(t, snap.VisionReviewed)
	assert.Equal(t, "Synthetic reflection.", snap.VisionReflection)

	// stdin, with the month defaulted and a null suggestion treated as absent.
	doc := strings.Replace(syntheticWheelDoc, `"month": "2026-08",`, "", 1)
	doc = strings.Replace(doc, `"suggested": 6`, `"suggested": null`, 1)
	out, _, err = runRootWithStdin(t, doc, "wheel", "add", "--input", "-")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded the 2026-09 wheel as wheel_2026_09_001.")
	e, _ = readWheelMonth(t, home, "2026-09")
	assert.Nil(t, e.History[0].Pillars["career"].Suggested)
}

// TestWheelAddCLI_InputStrict: the --input document is strict — an unknown key,
// trailing data, a quoted or decimal score, an off-scale suggestion, a missing
// score, an unknown pillar, or a document mixed with other add flags is refused
// and nothing is written.
func TestWheelAddCLI_InputStrict(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	cases := map[string]string{
		"is not a valid wheel document": strings.Replace(syntheticWheelDoc, `"vision_reviewed"`, `"visionReviewed"`, 1),
		"more than one JSON document":   syntheticWheelDoc + "\n{}",
		"health needs a whole number from 1 to 10 (got \"6\")": strings.Replace(
			syntheticWheelDoc, `"score": 6, "note"`, `"score": "6", "note"`, 1,
		),
		"personal growth needs a whole number from 1 to 10 (got 7.0)": strings.Replace(
			syntheticWheelDoc, `"growth":        { "score": 7 }`, `"growth": { "score": 7.0 }`, 1,
		),
		"the suggestion for career/work needs a whole number from 1 to 10 (got 0)": strings.Replace(
			syntheticWheelDoc, `"suggested": 6`, `"suggested": 0`, 1,
		),
		"missing: fun/recreation": strings.Replace(
			syntheticWheelDoc, `"fun":           { "score": 4 },`, `"fun": { "note": "synthetic" },`, 1,
		),
		`unknown pillar "wealth"`: strings.Replace(
			syntheticWheelDoc, `"fun":           { "score": 4 },`, `"fun": { "score": 4 }, "wealth": { "score": 3 },`, 1,
		),
		"not a valid wheel document: invalid character": "{not json",
	}
	for want, doc := range cases {
		_, stderr, err := runRootWithStdin(t, doc, "wheel", "add", "--input", "-")
		require.Errorf(t, err, want)
		assert.Containsf(t, stderr, want, "stderr: %s", stderr)
		assert.Containsf(t, stderr, "nothing was saved", want)
	}

	_, stderr, err := runRootWithStdin(t, syntheticWheelDoc, "wheel", "add", "--input", "-", "--health", "5", "--month", "2026-08")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assert.Contains(t, stderr, "so --health, --month cannot be given with it")

	_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "wheel", "add", "--input", filepath.Join(t.TempDir(), "absent.json"))
	require.Error(t, err)
	assert.Contains(t, stderr, "--input")

	assertNoWheel(t, home)
}

// TestWheelAddCLI_NoModel: the structured add is a complete terminal path with
// no companion and no model (architecture P9) — the verb builds no provider and
// records the wheel on a fresh Ledger.
func TestWheelAddCLI_NoModel(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs()...)
	require.NoError(t, err)
	_, found := readWheelMonth(t, home, "2026-09")
	assert.True(t, found)
}

// TestWheelAddCLI_RejectsArgs: add takes no positional arguments.
func TestWheelAddCLI_RejectsArgs(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, append(wheelAddArgs(), "extra")...)
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}
