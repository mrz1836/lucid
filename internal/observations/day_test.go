package observations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lateNight is a fixed pre-rollover "now": 2026-07-02 02:00, which still
// belongs to the logical day 2026-07-01 under the 04:00 boundary. It is the
// clock that separates a rollover-aware `yesterday` (2026-06-30) from a
// calendar-literal one (2026-07-01).
var lateNight = at(2026, 7, 2, 2, 0) //nolint:gochecknoglobals // deterministic test-fixture clock

// TestResolveDay_Grammar covers every form the shared grammar accepts on a
// deliberate date flag (usage/commands.md §"Backdating with --day"): the
// relative word, explicit dates, a full timestamp, a bare clock time, a clock
// range, a trailing time, and the two partial dates.
func TestResolveDay_Grammar(t *testing.T) {
	partial := DayOptions{AllowPartial: true}

	cases := []struct {
		name    string
		arg     string
		opts    DayOptions
		occ     time.Time
		prec    string
		day     string
		gran    string
		typed   string
		hasEnd  bool
		relativ bool
	}{
		{
			name: "empty is now at exact precision",
			arg:  "", opts: partial,
			occ: now, prec: PrecisionExact, day: "2026-07-02", gran: GranularityDay,
		},
		{
			name: "@yesterday is approximate on the prior logical day",
			arg:  "@yesterday", opts: partial,
			occ: at(2026, 7, 1, 0, 0), prec: PrecisionApproximate, day: "2026-07-01",
			gran: GranularityDay, typed: "yesterday", relativ: true,
		},
		{
			name: "a bare yesterday needs no @",
			arg:  "yesterday", opts: partial,
			occ: at(2026, 7, 1, 0, 0), prec: PrecisionApproximate, day: "2026-07-01",
			gran: GranularityDay, typed: "yesterday", relativ: true,
		},
		{
			name: "a trailing time promotes yesterday to exact",
			arg:  "@yesterday 19:30", opts: partial,
			occ: at(2026, 7, 1, 19, 30), prec: PrecisionExact, day: "2026-07-01",
			gran: GranularityDay, typed: "yesterday", relativ: true,
		},
		{
			name: "an explicit date is approximate midnight",
			arg:  "2026-07-01", opts: partial,
			occ: at(2026, 7, 1, 0, 0), prec: PrecisionApproximate, day: "2026-07-01",
			gran: GranularityDay, typed: "2026-07-01",
		},
		{
			name: "a trailing time promotes an explicit date to exact",
			arg:  "2026-07-01 19:30", opts: partial,
			occ: at(2026, 7, 1, 19, 30), prec: PrecisionExact, day: "2026-07-01",
			gran: GranularityDay, typed: "2026-07-01",
		},
		{
			name: "a full timestamp inside the token is exact",
			arg:  "@2026-07-01T19:30", opts: partial,
			occ: at(2026, 7, 1, 19, 30), prec: PrecisionExact, day: "2026-07-01",
			gran: GranularityDay, typed: "2026-07-01T19:30",
		},
		{
			name: "a bare clock time is today at that time",
			arg:  "@18:30", opts: partial,
			occ: at(2026, 7, 2, 18, 30), prec: PrecisionExact, day: "2026-07-02",
			gran: GranularityDay, typed: "18:30",
		},
		{
			name: "a clock range yields range precision with an end",
			arg:  "@09:00-12:30", opts: partial,
			occ: at(2026, 7, 2, 9, 0), prec: PrecisionRange, day: "2026-07-02",
			gran: GranularityDay, typed: "09:00-12:30", hasEnd: true,
		},
		{
			name: "a bare year snaps to January 1st, approximate",
			arg:  "2014", opts: partial,
			occ: at(2014, 1, 1, 0, 0), prec: PrecisionApproximate, day: "2014-01-01",
			gran: GranularityYear, typed: "2014",
		},
		{
			name: "a year-month snaps to the 1st, approximate",
			arg:  "2014-09", opts: partial,
			occ: at(2014, 9, 1, 0, 0), prec: PrecisionApproximate, day: "2014-09-01",
			gran: GranularityMonth, typed: "2014-09",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ResolveDay(tc.arg, now, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, tc.occ, res.OccurredAt)
			assert.Equal(t, tc.prec, res.Precision)
			assert.Equal(t, tc.day, res.LogicalDate)
			assert.Equal(t, tc.gran, res.Granularity)
			assert.Equal(t, tc.typed, res.Typed)
			assert.Equal(t, tc.relativ, res.Relative)
			if tc.hasEnd {
				require.NotNil(t, res.End)
				assert.Equal(t, at(2026, 7, 2, 12, 30), *res.End)
			} else {
				assert.Nil(t, res.End)
			}
		})
	}
}

// TestResolveDay_TrailingTimeIsTokenized is the regression guard for the bug
// that made the whole optional-time grammar unreachable through a flag: the
// resolver used to hand its argument to the token grammar as one string, so
// `--day "@yesterday 19:30"` matched no branch and silently fell back to now
// at exact precision — the caller believed it had backdated and had not.
func TestResolveDay_TrailingTimeIsTokenized(t *testing.T) {
	res, err := ResolveDay("@yesterday 19:30", now, DayOptions{AllowPartial: true})
	require.NoError(t, err)
	assert.Equal(t, at(2026, 7, 1, 19, 30), res.OccurredAt, "the trailing time must land on the backdated day")
	assert.Equal(t, PrecisionExact, res.Precision, "a supplied time promotes the record to exact")
	assert.NotEqual(t, now, res.OccurredAt, "it must never silently resolve to now")
}

// TestResolveDay_Rollover proves the rule that applies on both tiers: the
// relative word respects the 04:00 rollover, and an explicit date — full or
// partial — is taken literally at any hour.
func TestResolveDay_Rollover(t *testing.T) {
	partial := DayOptions{AllowPartial: true}

	// 02:00 still belongs to the logical day 2026-07-01, so @yesterday is the
	// day before that — not the previous calendar day.
	res, err := ResolveDay("@yesterday", lateNight, partial)
	require.NoError(t, err)
	assert.Equal(t, at(2026, 6, 30, 0, 0), res.OccurredAt)
	assert.Equal(t, "2026-06-30", res.LogicalDate)
	assert.True(t, res.Relative)

	// After the rollover the same word names the plain previous day.
	res, err = ResolveDay("@yesterday", at(2026, 7, 2, 10, 0), partial)
	require.NoError(t, err)
	assert.Equal(t, at(2026, 7, 1, 0, 0), res.OccurredAt)
	assert.Equal(t, "2026-07-01", res.LogicalDate)

	// An explicit date never shifts, at either hour.
	for _, clock := range []time.Time{lateNight, at(2026, 7, 2, 10, 0)} {
		for _, arg := range []string{"2026-06-20", "2014-09", "2014"} {
			shifted, resolveErr := ResolveDay(arg, clock, partial)
			require.NoError(t, resolveErr)
			assert.False(t, shifted.Relative, "%q is explicit, not relative", arg)
			assert.Equal(t, DateString(DateOf(shifted.OccurredAt)), shifted.LogicalDate,
				"%q must land on its own literal day at %s", arg, clock.Format(time.Kitchen))
		}
	}
}

// TestResolveDay_StrictTier proves the deliberate-flag contract: an unreadable
// value, a future day, or a partial date the command cannot use is a clean
// error, so the caller writes nothing.
func TestResolveDay_StrictTier(t *testing.T) {
	partial := DayOptions{AllowPartial: true}

	t.Run("unreadable values", func(t *testing.T) {
		for _, arg := range []string{
			"@yesterdya", "spring 2015", "2014-13", "2014-00", "not-a-date", "@", "2014-9", "20x4-09", "@09:00-nope",
		} {
			_, err := ResolveDay(arg, now, partial)
			require.Error(t, err, "%q must not be read as a date", arg)
			require.ErrorIs(t, err, ErrDayUnparseable, "%q", arg)
			assert.Contains(t, err.Error(), AcceptedDayForms, "the error must name the accepted forms")
		}
	})

	t.Run("the year floor keeps a stray token from becoming year 2", func(t *testing.T) {
		// "0002" is four digits, so the width test alone would read it as a
		// year. The 1000 floor rejects that, leaving it to the clock branch —
		// where it has always meant 00:02 — rather than inventing antiquity.
		res, err := ResolveDay("0002", now, partial)
		require.NoError(t, err)
		assert.Equal(t, at(2026, 7, 2, 0, 2), res.OccurredAt)
		assert.Equal(t, GranularityDay, res.Granularity)
	})

	t.Run("a trailing token the grammar cannot use is refused", func(t *testing.T) {
		_, err := ResolveDay("2026-07-01 lunchtime", now, partial)
		require.Error(t, err, "an unread trailing token must never be dropped silently")
		assert.ErrorIs(t, err, ErrDayUnparseable)
	})

	t.Run("future days", func(t *testing.T) {
		for _, arg := range []string{"2026-07-03", "2027", "2027-05"} {
			_, err := ResolveDay(arg, now, partial)
			require.Error(t, err, "%q has not happened yet", arg)
			assert.ErrorIs(t, err, ErrDayFuture, "%q", arg)
		}
	})

	t.Run("the future ceiling is opt-out", func(t *testing.T) {
		res, err := ResolveDay("2027-01-01", now, DayOptions{AllowFuture: true})
		require.NoError(t, err, "a forward commitment is legal where the command allows it")
		assert.Equal(t, at(2027, 1, 1, 0, 0), res.OccurredAt)
	})

	t.Run("the ceiling reads the resolved instant, not the typed string", func(t *testing.T) {
		// The current year snaps to its January 1st, which is in the past, so it
		// is accepted even though the year itself runs into the future.
		res, err := ResolveDay("2026", now, partial)
		require.NoError(t, err)
		assert.Equal(t, "2026-01-01", res.LogicalDate)
		assert.Equal(t, "2026", res.Typed, "the typed string is preserved for a caller that stores it")
	})

	t.Run("partial dates where a specific day is required", func(t *testing.T) {
		for _, arg := range []string{"2014", "2014-09"} {
			_, err := ResolveDay(arg, now, DayOptions{})
			require.Error(t, err, "%q names a period, not a day", arg)
			require.ErrorIs(t, err, ErrDayPartial, "%q", arg)
			assert.NotErrorIs(t, err, ErrDayUnparseable,
				"a partial date is well-formed — it must not be reported as unreadable")
		}
	})

	t.Run("a zero now falls back to the wall clock", func(t *testing.T) {
		res, err := ResolveDay("@yesterday", time.Time{}, partial)
		require.NoError(t, err, "an uninjected clock must still resolve")
		assert.True(t, res.Relative)
		assert.Equal(t, PrecisionApproximate, res.Precision)
	})

	t.Run("sentinels are distinct", func(t *testing.T) {
		require.NotErrorIs(t, ErrDayFuture, ErrDayUnparseable)
		require.NotErrorIs(t, ErrDayPartial, ErrDayUnparseable)
	})
}

// TestParseDayToken_BareYearCollision pins the one place the two tiers read the
// same token differently, and why. parseClockHM accepts a colon-less HHMM, so
// every year in 1900-1959 and 2000-2059 is also a valid wall-clock time. On a
// deliberate date flag the year wins; in dictated prose the clock time wins.
// Collapsing the two branch orders into one silently breaks whichever side
// loses, and nothing else in the suite would catch it.
func TestParseDayToken_BareYearCollision(t *testing.T) {
	occ, prec, end, gran, relative, consumed, ok := parseDayToken("2014", "19:30", now, true)
	require.True(t, ok)
	assert.Equal(t, at(2014, 1, 1, 0, 0), occ, "a flag reads a bare four-digit token as a year")
	assert.Equal(t, PrecisionApproximate, prec)
	assert.Equal(t, GranularityYear, gran)
	assert.Nil(t, end)
	assert.False(t, relative, "an explicit period is not the relative word")
	assert.False(t, consumed, "a period has no time of day, so it never absorbs one")

	occ, prec, end, gran, relative, consumed, ok = parseDayToken("2014", "19:30", now, false)
	require.True(t, ok)
	assert.Equal(t, at(2026, 7, 2, 20, 14), occ, "prose reads the same token as the dictated time 20:14")
	assert.Equal(t, PrecisionExact, prec)
	assert.Equal(t, GranularityDay, gran)
	assert.Nil(t, end)
	assert.False(t, relative)
	assert.False(t, consumed)

	// The whole reason the prose ordering is preserved: a dictated capture.
	spoken := parse(KindIntake, "food", "eggs", "@2014")
	assert.Equal(t, at(2026, 7, 2, 20, 14), spoken.OccurredAt)
	assert.Equal(t, PrecisionExact, spoken.Precision)
}

// TestResolveBackdate_NeverBlocks proves the permissive tier: every value the
// strict tier rejects resolves to now at exact precision instead of an error,
// so capture is never blocked (product-principles.md P10).
func TestResolveBackdate_NeverBlocks(t *testing.T) {
	for _, arg := range []string{"@yesterdya", "spring 2015", "2014-13", "not-a-date", "2026-07-01 lunchtime"} {
		occ, prec, end := ResolveBackdate(arg, now)
		assert.Equal(t, now, occ, "%q must fall back to now", arg)
		assert.Equal(t, PrecisionExact, prec, "%q", arg)
		assert.Nil(t, end, "%q", arg)
	}

	// A future value is kept rather than refused — the permissive path has no
	// ceiling to enforce.
	occ, prec, _ := ResolveBackdate("2027-01-01", now)
	assert.Equal(t, at(2027, 1, 1, 0, 0), occ)
	assert.Equal(t, PrecisionApproximate, prec)

	// It shares the grammar, so the trailing time works here too.
	occ, prec, _ = ResolveBackdate("@yesterday 19:30", now)
	assert.Equal(t, at(2026, 7, 1, 19, 30), occ)
	assert.Equal(t, PrecisionExact, prec)

	// And the rollover rule applies on this tier as well.
	occ, _, _ = ResolveBackdate("@yesterday", lateNight)
	assert.Equal(t, at(2026, 6, 30, 0, 0), occ, "the relative word rolls over on both tiers")
}
