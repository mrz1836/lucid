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

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "--day", "2026-07-06", "dfx", "3", "ran it")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not happened yet")
	assert.Equal(t, 0, engineDayCount(t, home))
}

// TestCloseoutCLI_DayFlagUnreadable refuses a typo rather than backfilling some
// other day.
func TestCloseoutCLI_DayFlagUnreadable(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "--day", "@yesterdya", "dfx", "3", "ran it")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
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
