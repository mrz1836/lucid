package contact

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseWindow_DaysAndWeeksEquivalent pins the Nd / Nw grammar (ADR-0014 §3):
// 28d and 4w are the same 28-day window, the canonical typed form is kept for
// display, and Human reads naturally for one and for many.
func TestParseWindow_DaysAndWeeksEquivalent(t *testing.T) {
	days, err := ParseWindow("28d")
	require.NoError(t, err)
	weeks, err := ParseWindow("4w")
	require.NoError(t, err)

	assert.Equal(t, 28, days.Days)
	assert.Equal(t, 28, weeks.Days)
	assert.Equal(t, days.Days, weeks.Days, "28d and 4w are the same window")
	assert.Equal(t, "28d", days.Text, "the form as typed is kept for display")
	assert.Equal(t, "4w", weeks.Text)

	assert.Equal(t, "every 4 weeks", weeks.Human())
	assert.Equal(t, "every 28 days", days.Human())

	cases := map[string]string{
		"1d":  "every day",
		"1w":  "every week",
		"10d": "every 10 days",
		"2w":  "every 2 weeks",
	}
	for in, want := range cases {
		w, err := ParseWindow(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, w.Human(), in)
	}

	// Surrounding space and the unit's case are forgiven; the number is
	// canonicalized.
	for _, in := range []string{" 4W ", "04w", "4w"} {
		w, err := ParseWindow(in)
		require.NoError(t, err, in)
		assert.Equal(t, Window{Text: "4w", Days: 28}, w, in)
	}
}

// TestParseWindow_RejectsInvalidWithGrammar pins Ct-3: zero, negative,
// fractional, unit-less and unreadable windows are refused, and the refusal
// carries the accepted grammar.
func TestParseWindow_RejectsInvalidWithGrammar(t *testing.T) {
	bad := []string{
		"0d", "-2w", "1.5w", "4", "w", "4x", "", "four weeks",
		"0w", "+4w", "4 w", "4wd", "d4", " ", "100000w", "99999999999999999999d",
	}
	for _, in := range bad {
		w, err := ParseWindow(in)
		require.Errorf(t, err, "%q must be refused", in)
		require.ErrorIs(t, err, ErrInvalidWindow, in)
		assert.Contains(t, err.Error(), "Nd or Nw", in)
		assert.Contains(t, err.Error(), "e.g. 28d or 4w", in)
		assert.Equal(t, Window{}, w, in)
	}
	assert.Equal(t, "a window is Nd or Nw — a whole number ≥ 1 of days or weeks, e.g. 28d or 4w", ErrInvalidWindow.Error())
}

// TestWindowHuman_FallsBackToDays keeps display total: a window whose text no
// longer parses (a hand-edited line) still renders from its day count.
func TestWindowHuman_FallsBackToDays(t *testing.T) {
	assert.Equal(t, "every 9 days", Window{Days: 9}.Human())
	assert.Equal(t, "every day", Window{Text: "bogus", Days: 1}.Human())
}
