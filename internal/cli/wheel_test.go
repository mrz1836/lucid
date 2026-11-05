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
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// wheelClock is a deterministic mid-month instant; the current logical month is
// 2026-09.
func wheelClock() time.Time { return time.Date(2026, 9, 6, 17, 12, 40, 0, time.UTC) }

// wheelRatingArgs returns the nine rating flags of the current pillar set with
// synthetic values.
func wheelRatingArgs() []string {
	return []string{
		"--health", "6", "--romance", "6", "--friends", "7", "--career", "5", "--finances", "4",
		"--growth", "7", "--fun", "4", "--environment", "6", "--contribution", "5",
	}
}

// wheelAddArgs builds `wheel add <ratings> <extra…>`.
func wheelAddArgs(extra ...string) []string {
	return append(append([]string{"wheel", "add"}, wheelRatingArgs()...), extra...)
}

// wheelScorePtr returns a pointer to a rating — one null-aligned trend entry.
func wheelScorePtr(n int) *int { return &n }

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
    "romance":       { "score": 6 },
    "friends":       { "score": 7 },
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
// the month, the receipt, and the nine stored ratings, one `label: N` line
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
		"health: 6", "romance/partner: 6", "friends & family: 7", "career/work: 5", "finances: 4",
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
		"wheel", "add", "--health", "8", "--romance", "6", "--friends", "7", "--career", "5", "--finances", "4",
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
		"health": 8, "romance": 6, "friends": 7, "career": 5, "finances": 4,
		"growth": 7, "fun": 4, "environment": 6, "contribution": 5,
	}, view.Scores, "scores are the self-ratings; career's suggestion of 1 never appears")

	e, found := readWheelMonth(t, home, "2026-09")
	require.True(t, found)
	assert.Len(t, e.History, 3, "every add kept its receipt")
}

// TestWheelSelfRatingRequired_CLI: every rating flag of the current pillar set
// is required — a missing one, romance and friends included, is a usage error
// (exit 2) naming each missing pillar, and nothing is written (wheel.md §3).
func TestWheelSelfRatingRequired_CLI(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "wheel", "add", "--health", "6", "--romance", "7")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assert.Contains(t, stderr,
		`required flag(s) "friends", "career", "finances", "growth", "fun", "environment", "contribution" not set`)
	assert.Contains(t, stderr, "nothing was saved")
	assertUsageErrorOnce(t, stderr, "lucid wheel add")

	// Each new spoke is required on its own: the other eight never stand in.
	for flag, want := range map[string]string{
		"--romance": `required flag(s) "romance" not set`,
		"--friends": `required flag(s) "friends" not set`,
	} {
		args := []string{"wheel", "add"}
		ratings := wheelRatingArgs()
		for i := 0; i < len(ratings); i += 2 {
			if ratings[i] != flag {
				args = append(args, ratings[i], ratings[i+1])
			}
		}
		_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, args...)
		require.Errorf(t, err, "without %s", flag)
		assert.Equalf(t, ExitUsage, exitCodeForError(err), "without %s", flag)
		assert.Containsf(t, stderr, want, "without %s", flag)
		assert.Containsf(t, stderr, "nothing was saved", "without %s", flag)
		assertUsageErrorOnce(t, stderr, "lucid wheel add")
	}

	_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "wheel", "add")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assertUsageErrorOnce(t, stderr, "lucid wheel add")
	assertNoWheel(t, home)
}

// TestWheelScaleRejects_CLI: a rating that is not a plain whole number 1–10 is a
// usage error naming the pillar and the range, and nothing is written.
func TestWheelScaleRejects_CLI(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	for _, bad := range []string{"2.5", "11", "0", "six", "-1", "6.0", ""} {
		args := []string{
			"wheel", "add", "--health", "6", "--romance", "6", "--friends", "7", "--career", "5", "--finances", "4",
			"--growth", "7", "--fun", bad, "--environment", "6", "--contribution", "5",
		}
		_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
		require.Errorf(t, err, "--fun %q", bad)
		assert.Equalf(t, ExitUsage, exitCodeForError(err), "--fun %q", bad)
		assert.Containsf(t, stderr, `for "--fun" flag: fun/recreation needs a whole number from 1 to 10`, "--fun %q", bad)
		assertUsageErrorOnce(t, stderr, "lucid wheel add")
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
		assertUsageErrorOnce(t, stderr, "lucid wheel add")
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
	assertUsageErrorOnce(t, stderr, "lucid wheel add")

	_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "wheel", "add", "--input", filepath.Join(t.TempDir(), "absent.json"))
	require.Error(t, err)
	assert.Contains(t, stderr, "--input")

	assertNoWheel(t, home)
}

// wheelSplitMessage is the one refusal every add path gives a retired
// relationships pillar (wheel.md §2, §7.1).
const wheelSplitMessage = "relationships was split into romance and friends in pillar set 2; rate both; nothing was saved"

// wheelMonthFileBytes returns a month file's raw bytes, or nil when the month
// has no file — the "nothing was written" check for a refused add.
func wheelMonthFileBytes(t *testing.T, home, month string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "registries", "wheel", "wheel_"+month+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return b
}

// wheelOldSevenArgs returns the rating flags of the seven pillars pillar set 2
// kept from set 1 — the old eight minus relationships — with synthetic values.
func wheelOldSevenArgs() []string {
	return []string{
		"--health", "6", "--career", "5", "--finances", "4", "--growth", "7",
		"--fun", "4", "--environment", "6", "--contribution", "5",
	}
}

// TestWheelAddCLI_NinePillars: the nine rating flags record the month — the ack
// echoes nine `label: N` lines in pillar set 2's canonical order, and --json
// scores carry exactly the nine keys (wheel.md §7.1, §7.4).
func TestWheelAddCLI_NinePillars(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	args := []string{
		"wheel", "add", "--health", "7", "--romance", "3", "--friends", "9", "--career", "6", "--finances", "5",
		"--growth", "8", "--fun", "2", "--environment", "4", "--contribution", "6", "--month", "2026-07",
	}
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Empty(t, stderr)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	require.Len(t, lines, 10, out)
	assert.Equal(t, "Recorded the 2026-07 wheel as wheel_2026_07_001.", lines[0])
	assert.Equal(t, []string{
		"health: 7", "romance/partner: 3", "friends & family: 9", "career/work: 6", "finances: 5",
		"personal growth: 8", "fun/recreation: 2", "environment: 4", "contribution: 6",
	}, lines[1:], "nine label lines in canonical order")
	labels := make([]string, 0, 9)
	for _, p := range observations.WheelPillars() {
		labels = append(labels, p.Label)
	}
	for i, line := range lines[1:] {
		assert.Truef(t, strings.HasPrefix(line, labels[i]+": "), "line %d %q follows the set table's order", i, line)
	}

	e, found := readWheelMonth(t, home, "2026-07")
	require.True(t, found)
	assert.Equal(t, observations.CurrentWheelPillarSet, e.History[0].EffectivePillarSet())
	assert.Len(t, e.History[0].Pillars, 9)
	assert.NotContains(t, e.History[0].Pillars, observations.PillarRelationships)

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, append(args, "--json")...)
	require.NoError(t, err)
	var view struct {
		ReceiptID string         `json:"receipt_id"`
		Scores    map[string]int `json:"scores"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &view), out)
	assert.Equal(t, "wheel_2026_07_002", view.ReceiptID)
	assert.Equal(t, map[string]int{
		"health": 7, "romance": 3, "friends": 9, "career": 6, "finances": 5,
		"growth": 8, "fun": 2, "environment": 4, "contribution": 6,
	}, view.Scores)
}

// TestWheelAddCLI_RelationshipsFlagRefused: the retired relationships pillar is
// refused on every flag path — the hidden --relationships flag (beside the old
// seven or the new nine), a --note pair and a --suggested pair naming it — as a
// usage error (exit 2) carrying the split message, never as an unknown pillar or
// a missing romance/friends, and nothing is written: no month file on a fresh
// Ledger, and an existing month's bytes unchanged.
func TestWheelAddCLI_RelationshipsFlagRefused(t *testing.T) {
	cases := map[string][][]string{
		"flag": {
			append(append([]string{"wheel", "add"}, wheelOldSevenArgs()...), "--relationships", "6"),
			wheelAddArgs("--relationships", "6"),
			{"wheel", "add", "--relationships", "6"},
		},
		"note": {
			wheelAddArgs("--note", "relationships=synthetic blended note"),
			wheelAddArgs("--note", "health=synthetic walk note", "--note", " relationships =x"),
		},
		"suggested": {
			wheelAddArgs("--suggested", "relationships=5"),
		},
	}
	for name, argSets := range cases {
		t.Run(name, func(t *testing.T) {
			home := isolatedHome(t)
			withClock(t, wheelClock())

			refused := func(args []string) {
				t.Helper()
				out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
				require.Errorf(t, err, "%v", args)
				assert.Equalf(t, ExitUsage, exitCodeForError(err), "%v", args)
				assert.Emptyf(t, out, "%v", args)
				assert.Containsf(t, stderr, wheelSplitMessage, "%v", args)
				assert.NotContainsf(t, stderr, "unknown", "%v", args)
				assert.NotContainsf(t, stderr, "required flag", "%v", args)
				assertUsageErrorOnce(t, stderr, "lucid wheel add")
			}

			for _, args := range argSets {
				refused(args)
			}
			assertNoWheel(t, home)
			assert.Nil(t, wheelMonthFileBytes(t, home, "2026-09"), "no month file on a fresh Ledger")

			_, _, err := runRoot(t, BuildInfo{Version: "dev"}, wheelAddArgs()...)
			require.NoError(t, err)
			before := wheelMonthFileBytes(t, home, "2026-09")
			require.NotNil(t, before)
			for _, args := range argSets {
				refused(args)
			}
			assert.Equal(t, before, wheelMonthFileBytes(t, home, "2026-09"), "an existing month's bytes are unchanged")
		})
	}

	// Beside --input the hidden flag is one more add flag the document excludes.
	home := isolatedHome(t)
	withClock(t, wheelClock())
	_, stderr, err := runRootWithStdin(t, syntheticWheelDoc, "wheel", "add", "--input", "-", "--relationships", "6")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assert.Contains(t, stderr, "so --relationships cannot be given with it; nothing was saved")
	assertUsageErrorOnce(t, stderr, "lucid wheel add")
	assertNoWheel(t, home)
}

// TestWheelAddCLI_InputRelationshipsRefused: a relationships key under the
// --input document's pillars — beside the nine, or in the old eight's place — is
// refused with the split message, never as an unknown pillar, and nothing is
// written (wheel.md §7.1).
func TestWheelAddCLI_InputRelationshipsRefused(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	docs := map[string]string{
		"beside the nine": strings.Replace(syntheticWheelDoc,
			`"romance":       { "score": 6 },`,
			`"romance":       { "score": 6 }, "relationships": { "score": 6, "note": "synthetic" },`, 1),
		"the old eight": strings.Replace(syntheticWheelDoc,
			`"romance":       { "score": 6 },
    "friends":       { "score": 7 },`,
			`"relationships": { "score": 6 },`, 1),
	}
	for name, doc := range docs {
		require.Containsf(t, doc, `"relationships"`, name)
		out, stderr, err := runRootWithStdin(t, doc, "wheel", "add", "--input", "-")
		require.Errorf(t, err, name)
		assert.Emptyf(t, out, name)
		assert.Containsf(t, stderr, "wheel: "+wheelSplitMessage, "%s: %s", name, stderr)
		assert.NotContainsf(t, stderr, "unknown pillar", name)
		assert.NotContainsf(t, stderr, "missing", name)
	}
	assertNoWheel(t, home)
	assert.Nil(t, wheelMonthFileBytes(t, home, "2026-08"))
}

// TestWheelAddCLI_InputPillarSetRefused: the binary always records the current
// pillar set, so a top-level pillar_set key in the --input document is an
// unknown key — refused, and nothing is written (wheel.md §7.1).
func TestWheelAddCLI_InputPillarSetRefused(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	for _, set := range []string{"1", "2"} {
		doc := strings.Replace(syntheticWheelDoc, `"month": "2026-08",`, `"month": "2026-08", "pillar_set": `+set+`,`, 1)
		require.Contains(t, doc, `"pillar_set"`)
		_, stderr, err := runRootWithStdin(t, doc, "wheel", "add", "--input", "-")
		require.Errorf(t, err, "pillar_set %s", set)
		assert.Containsf(t, stderr, `is not a valid wheel document: json: unknown field "pillar_set"`, "stderr: %s", stderr)
		assert.Containsf(t, stderr, "nothing was saved", "pillar_set %s", set)
	}
	assertNoWheel(t, home)
}

// TestWheelAddCLI_HelpListsNinePillars: `wheel add --help` lists the nine rating
// flags — --romance and --friends among them — says "nine", and never lists the
// hidden, retired --relationships; `wheel --help` says nine too.
func TestWheelAddCLI_HelpListsNinePillars(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "wheel", "add", "--help")
	require.NoError(t, err)
	for _, key := range observations.WheelPillarKeys() {
		assert.Containsf(t, out, "--"+key+" ", "help lists --%s", key)
	}
	assert.Contains(t, out, "--romance")
	assert.Contains(t, out, "--friends")
	assert.Contains(t, out, "Your own 1–10 rating for romance/partner (required)")
	assert.Contains(t, out, "Your own 1–10 rating for friends & family (required)")
	assert.Contains(t, out, "all nine pillars")
	assert.NotContains(t, out, "--relationships")
	assert.NotContains(t, out, "relationships")
	assert.NotContains(t, out, "eight")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "wheel", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "nine life pillars")
	assert.NotContains(t, out, "eight")
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

// runWheel runs a wheel command on the isolated Ledger and fails on error.
func runWheel(t *testing.T, args ...string) string {
	t.Helper()
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoError(t, err, stderr)
	return out
}

// seedWheelMonths records two synthetic months through the CLI: August, then
// September with notes, suggestions, and a vision reflection.
func seedWheelMonths(t *testing.T) {
	t.Helper()
	runWheel(t, "wheel", "add", "--month", "2026-08",
		"--health", "5", "--romance", "7", "--friends", "7", "--career", "6", "--finances", "6",
		"--growth", "7", "--fun", "4", "--environment", "6", "--contribution", "5")
	runWheel(t, wheelAddArgs(
		"--note", "health=synthetic walk note", "--suggested", "health=2", "--suggested", "fun=9",
		"--vision-reflection", "Synthetic reflection.",
	)...)
}

// TestWheelShowCLI_Human: `wheel show` prints the Discord-safe trend — the
// header naming the prior month, one `label: N (±d) <sparkline>` line per
// pillar, the callouts, notes, and vision fields — and never a suggestion or a
// markdown table (wheel.md §7.2).
func TestWheelShowCLI_Human(t *testing.T) {
	isolatedHome(t)
	withClock(t, wheelClock())
	seedWheelMonths(t)

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "wheel", "show")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Equal(t, strings.Join([]string{
		"wheel: 2026-09 (vs 2026-08)",
		"health: 6 (+1) ▄▅",
		"romance/partner: 6 (-1) ▆▅",
		"friends & family: 7 (0) ▆▆",
		"career/work: 5 (-1) ▅▄",
		"finances: 4 (-2) ▅▃",
		"personal growth: 7 (0) ▆▆",
		"fun/recreation: 4 (0) ▃▃",
		"environment: 6 (0) ▅▅",
		"contribution: 5 (0) ▄▄",
		"lowest: finances (4), fun/recreation (4)",
		"biggest drop: finances (-2)",
		"notes:",
		"- health: synthetic walk note",
		"vision reviewed: yes",
		"vision reflection: Synthetic reflection.",
	}, "\n")+"\n", out)
	assert.NotContains(t, out, "|", "never a markdown table")
	assert.NotContains(t, strings.ToLower(out), "suggest", "a suggestion never reaches human output")

	prior := runWheel(t, "wheel", "show", "--month", "2026-08")
	assert.True(t, strings.HasPrefix(prior, "wheel: 2026-08 (no prior month)\nhealth: 5 ▄\n"), prior)
	assert.Contains(t, prior, "biggest drop: no prior month\n")
}

// TestWheelShowCLI_JSON: `wheel show --json` emits the structured view — the
// stored ratings verbatim, deltas, trends, callouts — and the calibration
// block is the only place the stored suggestions appear (wheel.md §5, §7.2).
func TestWheelShowCLI_JSON(t *testing.T) {
	isolatedHome(t)
	withClock(t, wheelClock())
	seedWheelMonths(t)

	var view struct {
		Month      string   `json:"month"`
		PriorMonth string   `json:"prior_month"`
		ReceiptID  string   `json:"receipt_id"`
		Months     []string `json:"months"`
		Pillars    []struct {
			Pillar string `json:"pillar"`
			Score  int    `json:"score"`
			Delta  *int   `json:"delta"`
			Trend  []int  `json:"trend"`
		} `json:"pillars"`
		BiggestDrop []map[string]any `json:"biggest_drop"`
		Calibration struct {
			Suggested map[string]int `json:"suggested"`
		} `json:"calibration"`
	}
	require.NoError(t, json.Unmarshal([]byte(runWheel(t, "wheel", "show", "--json")), &view))
	assert.Equal(t, "2026-09", view.Month)
	assert.Equal(t, "2026-08", view.PriorMonth)
	assert.Equal(t, "wheel_2026_09_001", view.ReceiptID)
	assert.Equal(t, []string{"2026-08", "2026-09"}, view.Months)
	require.Len(t, view.Pillars, 9)
	assert.Equal(t, "health", view.Pillars[0].Pillar)
	assert.Equal(t, 6, view.Pillars[0].Score, "the score is the self-rating, not the suggestion")
	require.NotNil(t, view.Pillars[0].Delta)
	assert.Equal(t, 1, *view.Pillars[0].Delta)
	assert.Equal(t, []int{5, 6}, view.Pillars[0].Trend)
	assert.Equal(t, "fun", view.Pillars[6].Pillar)
	assert.Equal(t, 4, view.Pillars[6].Score, "fun keeps its own 4 beside a suggestion of 9")
	assert.Len(t, view.BiggestDrop, 1)
	assert.Equal(t, map[string]int{"health": 2, "fun": 9}, view.Calibration.Suggested)
}

// TestWheelShowCLI_EmptyHistory: with no wheel recorded, show says so and
// exits 0; --json emits the empty view with every array [] (wheel.md §7.2).
func TestWheelShowCLI_EmptyHistory(t *testing.T) {
	isolatedHome(t)
	assert.Equal(t, "no prior month — no wheel recorded yet\n", runWheel(t, "wheel", "show"))

	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(runWheel(t, "wheel", "show", "--json")), &view))
	assert.Empty(t, view["month"])
	for _, key := range []string{"months", "pillars", "lowest", "biggest_drop"} {
		assert.Equal(t, []any{}, view[key], "%s is [] not null", key)
	}
}

// TestWheelShowCLI_MonthErrors: a malformed --month, or a month with no wheel,
// is a clean error on stderr with a non-zero exit; show takes no positional
// arguments.
func TestWheelShowCLI_MonthErrors(t *testing.T) {
	isolatedHome(t)
	withClock(t, wheelClock())
	runWheel(t, wheelAddArgs()...)

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "wheel", "show", "--month", "2026-07")
	require.Error(t, err)
	assert.Contains(t, stderr, "no wheel is recorded for 2026-07")
	assert.Equal(t, ExitErr, exitCodeForError(err))

	_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "wheel", "show", "--month", "Sept")
	require.Error(t, err)
	assert.Contains(t, stderr, "want YYYY-MM")

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "wheel", "show", "extra")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestWheelListCLI_EmptyHistory: with no wheel recorded, list prints `no prior
// month` and exits 0 — never an invented row; --json emits {"months": []}
// (wheel.md §7.3).
func TestWheelListCLI_EmptyHistory(t *testing.T) {
	isolatedHome(t)
	assert.Equal(t, "no prior month — no wheel recorded yet\n", runWheel(t, "wheel", "list"))
	assert.JSONEq(t, `{"months": []}`, runWheel(t, "wheel", "list", "--json"))

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "wheel", "list", "extra")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestWheelListCLI_Months: list names each recorded month, most recent first,
// with its latest receipt, the snapshot count when amended, and the vision
// flag; --json carries the same rows (wheel.md §7.3).
func TestWheelListCLI_Months(t *testing.T) {
	isolatedHome(t)
	withClock(t, wheelClock())
	seedWheelMonths(t)
	runWheel(t, wheelAddArgs("--vision-reviewed")...) // amend September

	assert.Equal(t,
		"2026-09: wheel_2026_09_002 (2 snapshots) · vision reviewed\n2026-08: wheel_2026_08_001\n",
		runWheel(t, "wheel", "list"))

	var view struct {
		Months []struct {
			Month          string `json:"month"`
			EntryID        string `json:"entry_id"`
			ReceiptID      string `json:"receipt_id"`
			Snapshots      int    `json:"snapshots"`
			RecordedAt     string `json:"recorded_at"`
			VisionReviewed bool   `json:"vision_reviewed"`
		} `json:"months"`
	}
	require.NoError(t, json.Unmarshal([]byte(runWheel(t, "wheel", "list", "--json")), &view))
	require.Len(t, view.Months, 2)
	assert.Equal(t, "2026-09", view.Months[0].Month)
	assert.Equal(t, "wheel_2026-09", view.Months[0].EntryID)
	assert.Equal(t, "wheel_2026_09_002", view.Months[0].ReceiptID)
	assert.Equal(t, 2, view.Months[0].Snapshots)
	assert.Equal(t, wheelClock().Format(time.RFC3339), view.Months[0].RecordedAt)
	assert.True(t, view.Months[0].VisionReviewed)
	assert.Equal(t, "2026-08", view.Months[1].Month)
	assert.False(t, view.Months[1].VisionReviewed)
}

// TestWheelListCLI_NoModel: add, show, and list complete as a terminal path
// with no companion and no model (architecture P9), and the reads leave the
// Ledger byte-for-byte as they found it.
func TestWheelListCLI_NoModel(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())
	runWheel(t, wheelAddArgs()...)
	path := filepath.Join(home, "registries", "wheel", "wheel_2026-09.json")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	assert.Contains(t, runWheel(t, "wheel", "show"), "wheel: 2026-09 (no prior month)\n")
	assert.Equal(t, "2026-09: wheel_2026_09_001\n", runWheel(t, "wheel", "list"))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "show and list write nothing")
}

// TestWheelRoundTrip_AddListShow_CLI: three months recorded through both add
// paths — flags and a --input document — plus a same-month amend read back
// through list and show exactly as written: the latest snapshot wins, every
// receipt survives, each stored rating renders verbatim, and a past month
// shows its own notes and vision fields (wheel.md §4, §7).
func TestWheelRoundTrip_AddListShow_CLI(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())

	runWheel(t, append([]string{"wheel", "add", "--month", "2026-07"}, wheelRatingArgs()...)...)
	path := filepath.Join(t.TempDir(), "wheel.json")
	require.NoError(t, os.WriteFile(path, []byte(syntheticWheelDoc), 0o600))
	runWheel(t, "wheel", "add", "--input", path)
	runWheel(t, wheelAddArgs()...)

	var ack struct {
		ReceiptID string         `json:"receipt_id"`
		Month     string         `json:"month"`
		Amended   bool           `json:"amended"`
		Scores    map[string]int `json:"scores"`
	}
	amend := []string{
		"wheel", "add", "--json", "--health", "8", "--romance", "7", "--friends", "7", "--career", "5", "--finances", "3",
		"--growth", "7", "--fun", "5", "--environment", "6", "--contribution", "5",
	}
	require.NoError(t, json.Unmarshal([]byte(runWheel(t, amend...)), &ack))
	assert.Equal(t, "wheel_2026_09_002", ack.ReceiptID)
	assert.Equal(t, "2026-09", ack.Month)
	assert.True(t, ack.Amended)
	assert.Equal(t, 8, ack.Scores["health"])

	var list router.WheelListView
	require.NoError(t, json.Unmarshal([]byte(runWheel(t, "wheel", "list", "--json")), &list))
	require.Len(t, list.Months, 3)
	assert.Equal(t, []string{"2026-09", "2026-08", "2026-07"},
		[]string{list.Months[0].Month, list.Months[1].Month, list.Months[2].Month})
	assert.Equal(t, "wheel_2026_09_002", list.Months[0].ReceiptID)
	assert.Equal(t, 2, list.Months[0].Snapshots)
	assert.True(t, list.Months[1].VisionReviewed)

	var show router.WheelShowView
	require.NoError(t, json.Unmarshal([]byte(runWheel(t, "wheel", "show", "--json")), &show))
	assert.Equal(t, "2026-09", show.Month)
	assert.Equal(t, "2026-08", show.PriorMonth)
	assert.Equal(t, "wheel_2026_09_002", show.ReceiptID)
	assert.Equal(t, []string{"2026-07", "2026-08", "2026-09"}, show.Months)
	want := map[string]struct{ score, delta int }{
		"health": {8, 2}, "romance": {7, 1}, "friends": {7, 0}, "career": {5, 0}, "finances": {3, -1},
		"growth": {7, 0}, "fun": {5, 1}, "environment": {6, 0}, "contribution": {5, 0},
	}
	require.Len(t, show.Pillars, 9)
	assert.Equal(t, 2, show.PillarSet)
	assert.Nil(t, show.PillarsChanged, "every month here is recorded in pillar set 2")
	for _, p := range show.Pillars {
		w := want[p.Pillar]
		assert.Equal(t, w.score, p.Score, "%s renders the latest stored rating", p.Pillar)
		require.NotNil(t, p.Delta, p.Pillar)
		assert.Equal(t, w.delta, *p.Delta, "%s delta vs 2026-08", p.Pillar)
	}
	assert.Equal(t, []*int{wheelScorePtr(6), wheelScorePtr(6), wheelScorePtr(8)}, show.Pillars[0].Trend)
	assert.Equal(t, []router.WheelPillarScore{{Pillar: "finances", Score: 3}}, show.Lowest)

	var aug router.WheelShowView
	require.NoError(t, json.Unmarshal([]byte(runWheel(t, "wheel", "show", "--month", "2026-08", "--json")), &aug))
	assert.Equal(t, "2026-07", aug.PriorMonth)
	assert.True(t, aug.VisionReviewed)
	assert.Equal(t, "Synthetic reflection.", aug.VisionReflection)
	assert.Equal(t, "synthetic walk note", aug.Pillars[0].Note)
	assert.Equal(t, map[string]int{"health": 5, "career": 6}, aug.Calibration.Suggested)

	e, found := readWheelMonth(t, home, "2026-09")
	require.True(t, found)
	require.Len(t, e.History, 2, "the amend appended; the first snapshot is untouched")
	assert.Equal(t, 6, e.History[0].Pillars["health"].Score)
	assert.Equal(t, "wheel_2026_09_001", e.History[0].ID)
}

// TestWheelListCLI_ReadError: an unreadable month file is a clean error on
// stderr with a non-zero exit, never a partial list.
func TestWheelListCLI_ReadError(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())
	runWheel(t, wheelAddArgs()...)
	require.NoError(t, os.WriteFile(filepath.Join(home, "registries", "wheel", "wheel_2026-09.json"), []byte("{bad"), 0o600))

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "wheel", "list")
	require.Error(t, err)
	assert.Empty(t, out)
	assert.Contains(t, stderr, "could not read the wheel")
	assert.Equal(t, ExitErr, exitCodeForError(err))
}

// writeRawWheelFile writes one month file straight into the isolated Ledger,
// bypassing the add path, so the fixture keeps its pillar set whatever set new
// writes record in: set 1 with no pillar_set under schema 1 (exactly as every
// month before pillar set 2 was stored), any other set stamped under schema 2.
// Ratings follow the set's canonical order — invented test data.
func writeRawWheelFile(
	t *testing.T, home, month string, set int, scores []int, edit func(map[string]observations.PillarScore),
) {
	t.Helper()
	keys, ok := observations.WheelPillarKeysOf(set)
	require.True(t, ok, "pillar set %d", set)
	require.Len(t, scores, len(keys))
	pillars := make(map[string]observations.PillarScore, len(keys))
	for i, key := range keys {
		pillars[key] = observations.PillarScore{Score: scores[i]}
	}
	if edit != nil {
		edit(pillars)
	}
	schema, stamped := 1, 0
	if set != 1 {
		schema, stamped = 2, set
	}
	at := wheelClock().Format(time.RFC3339)
	entry := observations.WheelEntry{
		Key: observations.WheelEntryKey(month), Kind: observations.RegistryWheel, Schema: schema, Month: month,
		History: []observations.WheelSnapshot{{
			ID: observations.WheelReceiptID(month, 1), At: at, Type: observations.WheelEventSnapshot,
			PillarSet: stamped, Pillars: pillars,
		}},
		CreatedAt: at, UpdatedAt: at,
	}
	b, err := json.Marshal(entry)
	require.NoError(t, err)
	dir := filepath.Join(home, "registries", "wheel")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wheel_"+month+".json"), b, 0o600))
}

// seedWheelPillarSetBoundary writes a set-1 August (with a note and a
// suggestion on the retired relationships pillar) and a set-2 September — the
// first month after the split — with a friends note and a health suggestion.
func seedWheelPillarSetBoundary(t *testing.T, home string) {
	t.Helper()
	writeRawWheelFile(t, home, "2026-08", 1, []int{5, 7, 6, 6, 6, 4, 6, 5}, func(p map[string]observations.PillarScore) {
		suggested := 4
		p[observations.PillarRelationships] = observations.PillarScore{
			Score: 7, Note: "synthetic blended spoke note", Suggested: &suggested,
		}
	})
	writeRawWheelFile(t, home, "2026-09", 2, []int{6, 2, 7, 5, 3, 7, 4, 6, 5}, func(p map[string]observations.PillarScore) {
		suggested := 7
		p[observations.PillarHealth] = observations.PillarScore{Score: 6, Suggested: &suggested}
		p[observations.PillarFriends] = observations.PillarScore{Score: 7, Note: "synthetic dinner note"}
	})
}

// TestWheelShowCLI_PillarSetBoundary_Human: `wheel show` on a set-2 month
// against a set-1 prior prints the change line right after the header, `(new)`
// on the two new pillars, real deltas on the seven unchanged ones, and no
// retired relationships line or note — Discord-safe, with no suggestion
// (wheel.md §7.2 "Across a pillar-set change").
func TestWheelShowCLI_PillarSetBoundary_Human(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())
	seedWheelPillarSetBoundary(t, home)

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "wheel", "show")
	require.NoError(t, err, stderr)
	assert.Empty(t, stderr)
	assert.Equal(t, strings.Join([]string{
		"wheel: 2026-09 (vs 2026-08)",
		"pillars changed since 2026-08: relationships → romance/partner + friends & family",
		"health: 6 (+1) ▄▅",
		"romance/partner: 2 (new) ▂",
		"friends & family: 7 (new) ▆",
		"career/work: 5 (-1) ▅▄",
		"finances: 3 (-3) ▅▃",
		"personal growth: 7 (+1) ▅▆",
		"fun/recreation: 4 (0) ▃▃",
		"environment: 6 (0) ▅▅",
		"contribution: 5 (0) ▄▄",
		"lowest: romance/partner (2)",
		"biggest drop: finances (-3)",
		"notes:",
		"- friends & family: synthetic dinner note",
		"vision reviewed: no",
	}, "\n")+"\n", out)

	assert.Equal(t, 2, strings.Count(out, "(new)"), "exactly the two new pillars read (new)")
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		assert.NotContains(t, line, "|", "never a markdown table: %q", line)
		assert.False(t, strings.HasPrefix(line, "relationships:"), "no retired pillar line: %q", line)
		if strings.HasPrefix(line, "romance/partner:") || strings.HasPrefix(line, "friends & family:") {
			assert.NotContains(t, line, "(0)", "a new pillar never reads a zero delta: %q", line)
			assert.NotContains(t, line, "(+", "a new pillar never reads a computed delta: %q", line)
			assert.NotContains(t, line, "(-", "a new pillar never reads a computed delta: %q", line)
		}
	}
	assert.NotContains(t, out, "blended", "the retired pillar's note is not carried")
	assert.NotContains(t, strings.ToLower(out), "suggest", "a suggestion never reaches human output")
}

// TestWheelShowCLI_PillarSetBoundary_JSON: `wheel show --json` across the
// boundary carries the shown month's pillar_set and the pillars_changed block,
// a null-aligned trend with a null delta on each new pillar, only the shown
// month's stored suggestions in calibration, and every array present
// (wheel.md §7.2).
func TestWheelShowCLI_PillarSetBoundary_JSON(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, wheelClock())
	seedWheelPillarSetBoundary(t, home)
	out := runWheel(t, "wheel", "show", "--json")

	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &raw), out)
	for _, key := range []string{"months", "pillars", "lowest", "biggest_drop"} {
		assert.IsType(t, []any{}, raw[key], "%s is present and an array, never null", key)
	}
	assert.Equal(t, map[string]any{
		"since": "2026-08", "from": []any{"relationships"}, "to": []any{"romance", "friends"},
	}, raw["pillars_changed"])

	var view struct {
		PillarSet      int `json:"pillar_set"`
		PillarsChanged *struct {
			Since string   `json:"since"`
			From  []string `json:"from"`
			To    []string `json:"to"`
		} `json:"pillars_changed"`
		Months  []string `json:"months"`
		Pillars []struct {
			Pillar    string `json:"pillar"`
			Label     string `json:"label"`
			Score     int    `json:"score"`
			Delta     *int   `json:"delta"`
			Trend     []*int `json:"trend"`
			Sparkline string `json:"sparkline"`
		} `json:"pillars"`
		BiggestDrop []router.WheelPillarDelta `json:"biggest_drop"`
		Calibration struct {
			Suggested map[string]int `json:"suggested"`
		} `json:"calibration"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &view), out)
	assert.Equal(t, 2, view.PillarSet)
	require.NotNil(t, view.PillarsChanged)
	assert.Equal(t, "2026-08", view.PillarsChanged.Since)
	assert.Equal(t, []string{"2026-08", "2026-09"}, view.Months)
	require.Len(t, view.Pillars, 9)

	romance := view.Pillars[1]
	assert.Equal(t, "romance", romance.Pillar)
	assert.Equal(t, "romance/partner", romance.Label)
	assert.Nil(t, romance.Delta, "a new pillar's delta is null")
	require.Len(t, romance.Trend, 2)
	assert.Nil(t, romance.Trend[0], "null where the month lacks the pillar")
	require.NotNil(t, romance.Trend[1])
	assert.Equal(t, 2, *romance.Trend[1])
	assert.Equal(t, "▂", romance.Sparkline, "one block: only the month that rated it")
	assert.Equal(t, "friends", view.Pillars[2].Pillar)
	assert.Nil(t, view.Pillars[2].Delta)

	for _, p := range view.Pillars {
		assert.Len(t, p.Trend, len(view.Months), "%s trend is aligned to months", p.Pillar)
		assert.NotEqual(t, "relationships", p.Pillar, "the retired pillar is not shown")
	}
	assert.Equal(t, []router.WheelPillarDelta{{Pillar: "finances", Delta: -3}}, view.BiggestDrop)
	assert.Equal(t, map[string]int{"health": 7}, view.Calibration.Suggested,
		"only the shown month's stored suggestions — never the retired pillar's")
	rawRomance, ok := raw["pillars"].([]any)[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{nil, float64(2)}, rawRomance["trend"], "the trend's gap is a literal JSON null")
	assert.Contains(t, rawRomance, "delta")
	assert.Nil(t, rawRomance["delta"])
}

// TestWheelShowCLI_Schema1Golden_AdditiveKeys: over the schema-1 golden Ledger
// (no pillar_set anywhere), `wheel show --json` carries the two additive keys
// as pillar_set 1 and pillars_changed null — for the latest month and for
// --month on the first one — and the reads rewrite no stored byte.
func TestWheelShowCLI_Schema1Golden_AdditiveKeys(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, schema1GoldenNow())
	paths := seedSchema1GoldenLedger(t, home)

	for _, args := range [][]string{
		{"wheel", "show", "--json"},
		{"wheel", "show", "--month", "2026-07", "--json"},
	} {
		var view map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(runWheel(t, args...)), &view))
		assert.JSONEq(t, "1", string(view["pillar_set"]), "%v: a month with no pillar_set reads as set 1", args)
		changed, present := view["pillars_changed"]
		assert.True(t, present, "%v: pillars_changed is always present", args)
		assert.JSONEq(t, "null", string(changed), "%v: the set did not change", args)
	}

	for i, want := range []string{schema1GoldenJuly, schema1GoldenAugust} {
		got, err := os.ReadFile(paths[i])
		require.NoError(t, err)
		assert.Equal(t, want, string(got), "a read never rewrites a stored schema-1 file")
	}
}
