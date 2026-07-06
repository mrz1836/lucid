package isoweek

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// eastern is a fixed non-UTC location so the tests exercise the "bounds in t's
// own location" contract, matching the local-TZ timestamps under ~/.lucid/.
func eastern(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tz database unavailable: %v", err)
	}
	return loc
}

// TestID covers the reflection-record id, including the zero-padded week and
// the ISO-year rollover where the calendar year and ISO year differ.
func TestID(t *testing.T) {
	loc := eastern(t)
	cases := []struct {
		name string
		when time.Time
		want string
	}{
		{"mid-may-week-19", time.Date(2026, time.May, 5, 19, 43, 0, 0, loc), "reflection_2026_w19"},
		{"single-digit-week-pads", time.Date(2026, time.February, 2, 9, 0, 0, 0, loc), "reflection_2026_w06"},
		// 2027-01-01 is a Friday; ISO week 53 of 2026 runs through it.
		{"iso-year-rollover", time.Date(2027, time.January, 1, 12, 0, 0, 0, loc), "reflection_2026_w53"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ID(c.when))
		})
	}
}

// TestLabel confirms the YYYY-Www frontmatter label mirrors the id's week.
func TestLabel(t *testing.T) {
	loc := eastern(t)
	assert.Equal(t, "2026-W19", Label(time.Date(2026, time.May, 5, 19, 43, 0, 0, loc)))
	assert.Equal(t, "2026-W06", Label(time.Date(2026, time.February, 2, 9, 0, 0, 0, loc)))
}

// TestBounds confirms the week spans Monday 00:00:00 to Sunday 23:59:59 in the
// instant's own location, for every day of the week including the Sunday edge.
func TestBounds(t *testing.T) {
	loc := eastern(t)
	wantStart := time.Date(2026, time.May, 4, 0, 0, 0, 0, loc)   // Monday
	wantEnd := time.Date(2026, time.May, 10, 23, 59, 59, 0, loc) // Sunday
	for _, day := range []int{4, 5, 6, 7, 8, 9, 10} {
		when := time.Date(2026, time.May, day, 13, 27, 4, 0, loc)
		start, end := Bounds(when)
		assert.True(t, start.Equal(wantStart), "day %d start = %s", day, start)
		assert.True(t, end.Equal(wantEnd), "day %d end = %s", day, end)
		assert.Equal(t, time.Monday, start.Weekday())
		assert.Equal(t, time.Sunday, end.Weekday())
		// The window is exactly seven days minus one second.
		assert.Equal(t, 7*24*time.Hour-time.Second, end.Sub(start))
	}
}

// TestBoundsCrossesMonthBoundary confirms a week straddling two months bounds
// correctly (the Monday can sit in the prior month).
func TestBoundsCrossesMonthBoundary(t *testing.T) {
	loc := eastern(t)
	// 2026-06-01 is a Monday; the prior week's Sunday is 2026-05-31.
	start, end := Bounds(time.Date(2026, time.May, 31, 8, 0, 0, 0, loc))
	assert.Equal(t, time.Date(2026, time.May, 25, 0, 0, 0, 0, loc), start)
	assert.Equal(t, time.Date(2026, time.May, 31, 23, 59, 59, 0, loc), end)
}

// TestSame confirms two instants in the same ISO week compare equal and two in
// adjacent weeks (or the same week number in different years) do not.
func TestSame(t *testing.T) {
	loc := eastern(t)
	monday := time.Date(2026, time.May, 4, 0, 0, 0, 0, loc)
	sunday := time.Date(2026, time.May, 10, 23, 59, 59, 0, loc)
	nextMonday := time.Date(2026, time.May, 11, 0, 0, 0, 0, loc)

	assert.True(t, Same(monday, sunday), "Monday and Sunday of one week are the same week")
	assert.False(t, Same(sunday, nextMonday), "adjacent weeks differ")
	// Same week number, different ISO year → not the same week.
	assert.False(t, Same(monday, monday.AddDate(1, 0, 0)))
}
