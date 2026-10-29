package contact

import (
	"errors"
	"strconv"
	"strings"
)

// Window units (ADR-0014 §3): a window is a whole number of days or weeks.
const (
	unitDays  = "d"
	unitWeeks = "w"
)

// maxWindowDays bounds a window to roughly a century, so a mistyped run of
// digits is refused with the grammar instead of overflowing date arithmetic.
const maxWindowDays = 36600

// ErrInvalidWindow is the fixed refusal for any window outside the grammar
// (error-states.md Ct-3). The caller appends "Nothing was saved." — the message
// itself carries the accepted grammar so the retry is obvious.
var ErrInvalidWindow = errors.New("a window is Nd or Nw — a whole number ≥ 1 of days or weeks, e.g. 28d or 4w")

// Window is a check-in cadence window: Text is the canonical form as typed
// (`4w`, `28d`) kept for display, and Days is its length in days. `28d` and
// `4w` are the same 28-day window and differ only in Text.
type Window struct {
	Text string
	Days int
}

// ParseWindow reads the Nd / Nw grammar (ADR-0014 §3): a whole number N ≥ 1
// followed by `d` (days) or `w` (weeks). Surrounding whitespace and the unit's
// case are forgiven; the number is canonicalized (`04w` reads as `4w`). Zero,
// a sign, a fraction, a missing unit, an unknown unit or anything else is
// refused with [ErrInvalidWindow].
func ParseWindow(s string) (Window, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < 2 {
		return Window{}, ErrInvalidWindow
	}
	digits, unit := s[:len(s)-1], s[len(s)-1:]
	if unit != unitDays && unit != unitWeeks {
		return Window{}, ErrInvalidWindow
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return Window{}, ErrInvalidWindow
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 || n > maxWindowDays {
		return Window{}, ErrInvalidWindow
	}
	days := n
	if unit == unitWeeks {
		days = n * 7
	}
	if days > maxWindowDays {
		return Window{}, ErrInvalidWindow
	}
	return Window{Text: strconv.Itoa(n) + unit, Days: days}, nil
}

// Human renders the window for people: "every 4 weeks", "every 28 days",
// "every week", "every day". A window whose Text no longer parses (a
// hand-edited line) falls back to its day count, so display never fails.
func (w Window) Human() string {
	if parsed, err := ParseWindow(w.Text); err == nil {
		n, _ := strconv.Atoi(parsed.Text[:len(parsed.Text)-1])
		if strings.HasSuffix(parsed.Text, unitWeeks) {
			return every(n, "week", "weeks")
		}
		return every(n, "day", "days")
	}
	return every(w.Days, "day", "days")
}

// every renders "every <unit>" for one and "every N <units>" otherwise.
func every(n int, one, many string) string {
	if n == 1 {
		return "every " + one
	}
	return "every " + strconv.Itoa(n) + " " + many
}
