package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseBackfillDate covers the explicit YYYY-MM-DD backfill target parse:
// a well-formed date resolves in the host zone, anything else is the start of
// the compact form (ok=false) and the caller falls through to the router.
func TestParseBackfillDate(t *testing.T) {
	tests := []struct {
		name   string
		tok    string
		wantOK bool
	}{
		{"iso date", "2026-07-05", true},
		{"yesterday keyword", "yesterday", false},
		{"compact form head", "dfx", false},
		{"empty", "", false},
		{"malformed date", "2026-13-40", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseBackfillDate(tt.tok)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.NotNil(t, got)
				want, perr := time.ParseInLocation("2006-01-02", tt.tok, time.Now().Location())
				require.NoError(t, perr)
				assert.True(t, got.Equal(want))
			} else {
				assert.Nil(t, got)
			}
		})
	}
}

// engineDayCount counts written day records under the isolated home.
func engineDayCount(t *testing.T, home string) int {
	t.Helper()
	var n int
	_ = filepath.WalkDir(filepath.Join(home, "engine", "days"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(d.Name()) == ".json" {
			n++
		}
		return nil
	})
	return n
}

func TestCloseoutCLI_CompactWrites(t *testing.T) {
	home := isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "dfx", "3/wrist", "Long", "day", "but", "the", "chain", "ran.")
	require.NoError(t, err)
	assert.Contains(t, out, "streak 1.")
	assert.Equal(t, 1, engineDayCount(t, home))

	// A raw journal landed too.
	var raw int
	_ = filepath.WalkDir(filepath.Join(home, "raw"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(d.Name()) == ".md" {
			raw++
		}
		return nil
	})
	assert.Equal(t, 1, raw)
}

func TestCloseoutCLI_Skip(t *testing.T) {
	home := isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "skip")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded a miss")
	assert.Equal(t, 1, engineDayCount(t, home))
}

func TestCloseoutCLI_Today(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "today", "ddd", "4", "solid")
	require.NoError(t, err)
	assert.Contains(t, out, "streak")
}

func TestCloseoutCLI_Backfill(t *testing.T) {
	home := isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "backfill", "yesterday", "ddd", "4", "the chain ran")
	require.NoError(t, err)
	assert.Contains(t, out, "Backfilled")
	assert.Equal(t, 1, engineDayCount(t, home))
}

func TestCloseoutCLI_NoArgsIsError(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
}

func TestCloseoutCLI_BadCompactIsError(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "zz", "3", "bad chars")
	require.Error(t, err)
}

func TestCloseoutCLI_RegisteredInSpine(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "closeout" {
			found = true
			assert.NotContains(t, c.Short, "not implemented")
		}
	}
	assert.True(t, found, "closeout must be registered")
}

// dayRecordBytes reads one engine day record verbatim from an isolated home.
func dayRecordBytes(t *testing.T, home, dayID string) []byte {
	t.Helper()
	parts := strings.Split(dayID, "_")
	require.Len(t, parts, 4)
	b, err := os.ReadFile(filepath.Join(home, "engine", "days", parts[1], parts[2], dayID+".json"))
	require.NoError(t, err)
	return b
}

// TestCloseoutCLI_DayFlagMatchesPositionalBackfill is the alias's whole claim:
// `--day @yesterday` and `backfill yesterday` produce the same record, because
// the flag routes onto the same router path rather than a second one.
func TestCloseoutCLI_DayFlagMatchesPositionalBackfill(t *testing.T) {
	withClock(t, afternoon())

	flagHome := isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"closeout", "--day", "@yesterday", "dfx", "3/wrist", "ran", "it,", "forgot", "to", "record")
	require.NoError(t, err)

	positionalHome := isolatedHome(t)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"},
		"closeout", "backfill", "yesterday", "dfx", "3/wrist", "ran", "it,", "forgot", "to", "record")
	require.NoError(t, err)

	assert.Equal(t,
		string(dayRecordBytes(t, positionalHome, "day_2026_07_04")),
		string(dayRecordBytes(t, flagHome, "day_2026_07_04")),
	)
}

// TestCloseoutCLI_DayFlagStampsBackfilled: the alias lands on the backfill
// path, so the record carries its provenance.
func TestCloseoutCLI_DayFlagStampsBackfilled(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"closeout", "--day", "2026-07-03", "dfx", "3", "ran it")
	require.NoError(t, err)
	assert.Contains(t, out, "Backfilled 2026-07-03")
	assert.Contains(t, string(dayRecordBytes(t, home, "day_2026_07_03")), "\"backfilled\": true")
}

// TestCloseoutCLI_DayFlagRejectsToday: the window rule is the router's, and the
// alias inherits it — a backfill target is always a day that has ended.
func TestCloseoutCLI_DayFlagRejectsToday(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "--day", "2026-07-05", "dfx", "3", "ran it")
	require.NoError(t, err)
	assert.Contains(t, out, "outside the backfill window")
	assert.Equal(t, 0, engineDayCount(t, home))
}

// TestCloseoutCLI_DayFlagRejectsFuture: the strict tier refuses a day that has
// not happened yet before the router is reached.
func TestCloseoutCLI_DayFlagRejectsFuture(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "--day", "2026-07-06", "dfx", "3", "ran it")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not happened yet")
	assert.Contains(t, errOut, "has not happened yet", "the reason reaches the user, not just the exit code")
	assert.Equal(t, 0, engineDayCount(t, home))
}

// TestCloseoutCLI_DayFlagUnreadable refuses a typo rather than backfilling some
// other day.
func TestCloseoutCLI_DayFlagUnreadable(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "--day", "@yesterdya", "dfx", "3", "ran it")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
	assert.Contains(t, errOut, "could not read the day", "the reason reaches the user, not just the exit code")
	assert.Equal(t, 0, engineDayCount(t, home))
}

// TestCloseoutCLI_DayFlagWithSecondTargetIsUsageError: two targets, one intent.
// Each rejected combination writes nothing.
func TestCloseoutCLI_DayFlagWithSecondTargetIsUsageError(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"positional yesterday", []string{"backfill", "yesterday", "dfx", "3", "ran it"}, "both name a target"},
		{"positional date", []string{"backfill", "2026-07-03", "dfx", "3", "ran it"}, "both name a target"},
		{"skip", []string{"skip"}, "cannot be combined with `skip`"},
		{"today", []string{"today", "dfx", "3", "ran it"}, "cannot be combined with `today`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := isolatedHome(t)
			withClock(t, afternoon())

			args := append([]string{"closeout", "--day", "@yesterday"}, tt.args...)
			_, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Equal(t, 0, engineDayCount(t, home))
		})
	}
}

// TestCloseoutCLI_DayFlagAfterBareBackfillWord: the sub-word names the path the
// flag already routes onto and no day of its own, so it is redundant rather
// than contradictory.
func TestCloseoutCLI_DayFlagAfterBareBackfillWord(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"closeout", "--day", "@yesterday", "backfill", "dfx", "3", "ran it")
	require.NoError(t, err)
	assert.Contains(t, out, "Backfilled 2026-07-04")
	assert.Equal(t, 1, engineDayCount(t, home))
}

// TestCloseoutCLI_PositionalFormsUnchanged: the alias is additive — every
// positional form still behaves exactly as it did.
func TestCloseoutCLI_PositionalFormsUnchanged(t *testing.T) {
	withClock(t, afternoon())

	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "backfill", "2026-07-02", "dfx", "3", "ran it")
	require.NoError(t, err)
	assert.Contains(t, out, "Backfilled 2026-07-02")

	skipHome := isolatedHome(t)
	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "closeout", "skip")
	require.NoError(t, err)
	assert.NotEmpty(t, out)
	assert.Equal(t, 1, engineDayCount(t, skipHome))

	todayHome := isolatedHome(t)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "closeout", "today", "dfx", "3", "ran it")
	require.NoError(t, err)
	assert.Equal(t, 1, engineDayCount(t, todayHome))
	assert.Contains(t, string(dayRecordBytes(t, todayHome, "day_2026_07_05")), "\"backfilled\": false")
}

// TestResolveCloseoutDay covers the flag's own resolution: a relative token
// forwards the `yesterday` intent for the router to resolve against the
// rollover, an explicit or partial date is passed as the target, and the
// strict tier's two refusals name what was wrong.
func TestResolveCloseoutDay(t *testing.T) {
	now := time.Date(2026, 7, 5, 14, 0, 0, 0, time.UTC)

	target, yesterday, err := resolveCloseoutDay("@yesterday", now)
	require.NoError(t, err)
	assert.True(t, yesterday, "the router owns the rollover-aware resolution, not the CLI")
	assert.Nil(t, target)

	target, yesterday, err = resolveCloseoutDay("2026-07-01", now)
	require.NoError(t, err)
	assert.False(t, yesterday)
	require.NotNil(t, target)
	assert.Equal(t, "2026-07-01", target.Format("2006-01-02"))

	target, _, err = resolveCloseoutDay("2026-06", now)
	require.NoError(t, err)
	require.NotNil(t, target)
	assert.Equal(t, "2026-06-01", target.Format("2006-01-02"), "a partial date snaps to the period's first day")

	_, _, err = resolveCloseoutDay("2026-07-06", now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not happened yet")

	_, _, err = resolveCloseoutDay("spring 2015", now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
}

// rawEntryBodies concatenates every raw entry written under an isolated home —
// the journal lines a close-out captures. A substring assertion against it
// proves the file-supplied journal reached raw/ without going through the
// shell.
func rawEntryBodies(t *testing.T, home string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(filepath.Join(home, "raw"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(d.Name()) == ".md" {
			content, rerr := os.ReadFile(path)
			require.NoError(t, rerr)
			b.Write(content)
		}
		return nil
	})
	return b.String()
}

// writeJournalFile writes journal text to a temp file and returns its path —
// the off-command-line source under test.
func writeJournalFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.txt")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	return path
}

// TestCloseoutCLI_JournalFileWrites: --journal-file supplies the journal off
// the command line, and its exact text lands in the raw entry. The payload
// carries an ampersand — the character that split the original invocation — to
// prove it now survives as data.
func TestCloseoutCLI_JournalFileWrites(t *testing.T) {
	home := isolatedHome(t)
	jf := writeJournalFile(t, "tried another new thing today with r&b yoga\n")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "dfx", "3/wrist", "--journal-file", jf)
	require.NoError(t, err)
	assert.Contains(t, out, "streak")
	assert.Contains(t, rawEntryBodies(t, home), "tried another new thing today with r&b yoga")
}

// TestCloseoutCLI_JournalFileBackfill: the flag threads through the --day
// backfill form too, not just a bare close-out.
func TestCloseoutCLI_JournalFileBackfill(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	jf := writeJournalFile(t, "backfilled with a | pipe & an ampersand")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"closeout", "--day", "@yesterday", "dfx", "3", "--journal-file", jf)
	require.NoError(t, err)
	assert.Contains(t, out, "Backfilled")
	assert.Contains(t, rawEntryBodies(t, home), "backfilled with a | pipe & an ampersand")
}

// TestCloseoutCLI_JournalFileConflictsWithPositional: a positional journal
// alongside --journal-file is two sources for one field, so the CLI refuses
// rather than silently picking one.
func TestCloseoutCLI_JournalFileConflictsWithPositional(t *testing.T) {
	isolatedHome(t)
	jf := writeJournalFile(t, "from the file")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"closeout", "dfx", "3", "positional", "journal", "words", "--journal-file", jf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestCloseoutCLI_JournalFileRejectsSkip: a recorded miss carries no journal,
// so pairing --journal-file with `skip` is a contradiction the CLI refuses.
func TestCloseoutCLI_JournalFileRejectsSkip(t *testing.T) {
	home := isolatedHome(t)
	jf := writeJournalFile(t, "should never be written")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "skip", "--journal-file", jf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skip")
	assert.Equal(t, 0, engineDayCount(t, home))
}

// TestCloseoutCLI_JournalFileMissing: an unreadable --journal-file path is a
// clean error that writes nothing.
func TestCloseoutCLI_JournalFileMissing(t *testing.T) {
	home := isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"closeout", "dfx", "3", "--journal-file", filepath.Join(t.TempDir(), "nope.txt"))
	require.Error(t, err)
	assert.Equal(t, 0, engineDayCount(t, home))
}

// TestCloseoutCLI_BootError proves closeout surfaces a boot failure (an
// unscaffoldable home) before it would parse or write anything — mirrors the
// other *_BootError guards on the spine.
func TestCloseoutCLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "dfx", "3/wrist", "the chain ran")
	require.Error(t, err)
}

// TestCloseoutAmend is the AC-4 CLI surface: `closeout amend --day --journal-file`
// corrects a sealed journal off the command line, both flags are required, and
// the immutable boundary (a skip record has no journal to amend) is refused.
func TestCloseoutAmend(t *testing.T) {
	t.Run("happy path supersedes the sealed journal", func(t *testing.T) {
		home := isolatedHome(t)
		withClock(t, afternoon())

		// Seal a real close-out on a prior day with a truncated stub.
		_, _, err := runRoot(t, BuildInfo{Version: "dev"},
			"closeout", "--day", "2026-07-03", "dfx", "3/wrist", "tried another new thing today with r")
		require.NoError(t, err)

		// Amend it with the full narrative — carrying the ampersand that split the
		// original invocation, now supplied off the command line.
		jf := writeJournalFile(t, "tried another new thing today with r&b yoga\n")
		out, _, err := runRoot(t, BuildInfo{Version: "dev"},
			"closeout", "amend", "--day", "2026-07-03", "--journal-file", jf)
		require.NoError(t, err)
		assert.Contains(t, out, "Amended")
		assert.Contains(t, out, "2026-07-03")

		// The corrected text reached raw/, and the original stub is still there.
		bodies := rawEntryBodies(t, home)
		assert.Contains(t, bodies, "tried another new thing today with r&b yoga")
		assert.Contains(t, bodies, "tried another new thing today with r\n",
			"the original truncated stub stays traceable in history")

		// No new day record was created — the amend touched only the display.
		assert.Equal(t, 1, engineDayCount(t, home))
	})

	t.Run("missing --day is a usage error", func(t *testing.T) {
		isolatedHome(t)
		jf := writeJournalFile(t, "corrected text")
		_, _, err := runRoot(t, BuildInfo{Version: "dev"},
			"closeout", "amend", "--journal-file", jf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both required")
	})

	t.Run("missing --journal-file is a usage error", func(t *testing.T) {
		isolatedHome(t)
		_, _, err := runRoot(t, BuildInfo{Version: "dev"},
			"closeout", "amend", "--day", "2026-07-03")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both required")
	})

	t.Run("refuses to amend a skip record", func(t *testing.T) {
		isolatedHome(t)
		// After the evening bell, a bare skip lands on the base logical day
		// (2026-07-05) rather than back-attributing to last night.
		withClock(t, time.Date(2026, 7, 5, 22, 0, 0, 0, time.UTC))

		// A recorded miss carries no journal, so there is nothing to supersede.
		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "skip")
		require.NoError(t, err)

		jf := writeJournalFile(t, "should be refused")
		_, errOut, err := runRoot(t, BuildInfo{Version: "dev"},
			"closeout", "amend", "--day", "2026-07-05", "--journal-file", jf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "skip record")
		assert.Contains(t, errOut, "skip record",
			"the router's reason reaches the user, not just the exit code")
	})

	t.Run("refuses trailing positional words", func(t *testing.T) {
		isolatedHome(t)
		jf := writeJournalFile(t, "corrected text")
		_, _, err := runRoot(t, BuildInfo{Version: "dev"},
			"closeout", "amend", "extra", "words", "--day", "2026-07-03", "--journal-file", jf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no positional words")
	})
}
