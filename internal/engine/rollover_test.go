package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// at builds a UTC instant for the rollover fixtures — the host clock is
// trusted, so a fixed zone keeps the logical-day math deterministic.
//
//nolint:unparam // y is kept explicit for fixture readability even though the cases share 2026
func at(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

// defaultClocks returns the default-profile clocks (bell 19:00, rollover
// 04:00, tripwire 06:00).
func defaultClocks(t *testing.T) Clocks {
	t.Helper()
	c, err := DefaultChain().ClocksFor(DefaultProfile)
	require.NoError(t, err)
	return c
}

// TestResolveLogicalDay_Fixtures is the four binding rollover cases from
// engine-module.md §Phase 8 acceptance.
func TestResolveLogicalDay_Fixtures(t *testing.T) {
	c := defaultClocks(t)
	tests := []struct {
		name         string
		now          time.Time
		prevRecorded bool
		forceToday   bool
		want         string
	}{
		{"23:50 → today", at(2026, 7, 5, 23, 50), false, false, "2026-07-05"},
		{"03:50 → yesterday", at(2026, 7, 6, 3, 50), false, false, "2026-07-05"},
		{"04:12 unrecorded, bell not rung → yesterday", at(2026, 7, 6, 4, 12), false, false, "2026-07-05"},
		{"04:12 yesterday completed → today", at(2026, 7, 6, 4, 12), true, false, "2026-07-06"},
		{"04:12 forceToday → today", at(2026, 7, 6, 4, 12), false, true, "2026-07-06"},
		{"exactly at rollover 04:00 → today", at(2026, 7, 6, 4, 0), true, false, "2026-07-06"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := c.ResolveLogicalDay(tc.now, tc.prevRecorded, tc.forceToday)
			assert.Equal(t, tc.want, DateString(got))
		})
	}
}

// TestBaseLogicalDate covers the primary rollover rule directly.
func TestBaseLogicalDate(t *testing.T) {
	c := defaultClocks(t)
	assert.Equal(t, "2026-07-05", DateString(c.BaseLogicalDate(at(2026, 7, 5, 22, 0))))
	assert.Equal(t, "2026-07-05", DateString(c.BaseLogicalDate(at(2026, 7, 6, 3, 59))))
	assert.Equal(t, "2026-07-06", DateString(c.BaseLogicalDate(at(2026, 7, 6, 4, 0))))
}

// TestResolveLogicalDay_EveningPastBell confirms a normal 22:00 close-out
// stays on today regardless of the previous day's record (the bell has
// rung, so the night-shift branch never applies).
func TestResolveLogicalDay_EveningPastBell(t *testing.T) {
	c := defaultClocks(t)
	assert.Equal(t, "2026-07-05", DateString(c.ResolveLogicalDay(at(2026, 7, 5, 22, 0), false, false)))
}

func TestParseHM(t *testing.T) {
	cases := map[string]int{"00:00": 0, "04:00": 240, "21:30": 1290, "23:59": 1439}
	for in, want := range cases {
		got, err := parseHM(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	for _, bad := range []string{"", "24:00", "12", "12:60", "aa:bb", "-1:00", "12:xx"} {
		_, err := parseHM(bad)
		assert.Errorf(t, err, "expected %q to be rejected", bad)
	}
}

func TestDateHelpers(t *testing.T) {
	d := at(2026, 7, 6, 15, 4)
	assert.Equal(t, "2026-07-06", DateString(DateOf(d)))
	assert.Equal(t, "2026-07-05", DateString(AddDays(DateOf(d), -1)))
	assert.Equal(t, "day_2026_07_06", DayID(DateOf(d)))
	assert.Equal(t, 1, DaysBetween(at(2026, 7, 5, 0, 0), at(2026, 7, 6, 23, 0)))
	assert.Equal(t, 7, DaysBetween(at(2026, 7, 1, 12, 0), at(2026, 7, 8, 1, 0)))
	assert.Equal(t, 0, DaysBetween(at(2026, 7, 6, 1, 0), at(2026, 7, 6, 23, 0)))
}

// TestClocksFor covers the default and named-profile paths plus the
// undefined-profile rejection.
func TestClocksFor(t *testing.T) {
	chain := DefaultChain()
	def, err := chain.ClocksFor(DefaultProfile)
	require.NoError(t, err)
	assert.Equal(t, Clocks{BellMin: 1140, RolloverMin: 240, TripwireMin: 360}, def)

	nights, err := chain.ClocksFor("nights")
	require.NoError(t, err)
	assert.Equal(t, Clocks{BellMin: 510, RolloverMin: 720, TripwireMin: 1020}, nights)

	_, err = chain.ClocksFor("does-not-exist")
	assert.Error(t, err)
}

// TestClocksFor_MalformedClock guards the parse-failure branches.
func TestClocksFor_MalformedClock(t *testing.T) {
	chain := DefaultChain()
	chain.BellTime = "9pm"
	_, err := chain.ClocksFor(DefaultProfile)
	require.Error(t, err)

	chain = DefaultChain()
	chain.Rollover = "bad"
	_, err = chain.ClocksFor(DefaultProfile)
	require.Error(t, err)

	chain = DefaultChain()
	chain.Escalation.TripwireTime = "nope"
	_, err = chain.ClocksFor(DefaultProfile)
	require.Error(t, err)
}

func TestHasProfileAndLinkKeys(t *testing.T) {
	chain := DefaultChain()
	assert.True(t, chain.HasProfile(DefaultProfile))
	assert.True(t, chain.HasProfile("nights"))
	assert.False(t, chain.HasProfile("weekends"))
	assert.Equal(t, []string{"journal", "dock", "read"}, chain.LinkKeys())
}
