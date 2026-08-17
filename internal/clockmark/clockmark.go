// Package clockmark parses and represents an "HH:MM" 24-hour local clock
// mark — the single, strict rule shared by every Lucid path that reads a bell,
// close-out, rollover, tripwire, drain-window, slot, or report time from config
// or a chain (data-model.md: local civil times; usage/commands.md: the evening
// marks).
//
// Before this package the rule was copy-pasted in eight places that quietly
// disagreed on malformed input: some trimmed inner whitespace and accepted
// "12 : 30", some did not, so a mark that one path accepted another rejected.
// [Parse] adopts the strict end of those rules (see its doc), which is
// behavior-identical for every real mark like "07:30" and only tightens
// rejection of malformed input — and makes a mark that passes config validation
// build a valid cron everywhere.
//
// It is pure: no filesystem, no model, no obligation, and it never reads the
// clock — [Mark.At] resolves a mark against a caller-supplied instant. It
// depends only on fmt, strconv, strings, and time (the last solely for the
// calendar arithmetic in [Mark.At]). Callers keep their own public wrappers,
// return shapes, and error wording (mirroring internal/keyderive); this package
// owns only the one parsing rule and the renderings derived from it —
// [Mark.String], [Mark.Minutes], [Mark.Cron], [Mark.At], and the
// minutes-first [CronOfMinutes].
package clockmark

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Mark is a validated time of day: an hour in [0,23] and a minute in [0,59].
// The zero value is 00:00 (midnight), itself a valid mark. It carries no date
// and no location — it is a mark on the clock, resolved against a day by the
// caller.
type Mark struct {
	hour   int
	minute int
}

// Parse reads a strict "HH:MM" 24-hour clock mark. Outer whitespace is trimmed,
// the value is split on the first colon, and both fields must be in-range
// integers: the hour 0–23 and the minute 0–59, with no inner whitespace. So
// "07:30", "7:5" (→ 07:05), and " 07:30 " parse; "12 : 30" (inner whitespace),
// "24:00", "12:60", "12", "12:30:00", "", and non-numeric fields are rejected.
// A leading "+" on a field is accepted (strconv.Atoi's rule, as in every former
// copy); a leading "-" is caught by the range check.
//
// This is the strict end of the rules the eight former copies used. It is
// behavior-identical for every real mark and only tightens rejection of
// malformed input (notably inner whitespace, which config and schedstatus
// formerly accepted), so a mark that passes config validation is now guaranteed
// to build a valid cron in the daemons.
func Parse(s string) (Mark, error) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return Mark{}, fmt.Errorf("clockmark: %q is not HH:MM", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return Mark{}, fmt.Errorf("clockmark: invalid hour in %q (want 00-23)", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return Mark{}, fmt.Errorf("clockmark: invalid minute in %q (want 00-59)", s)
	}
	return Mark{hour: h, minute: m}, nil
}

// Hour returns the hour component, 0–23.
func (m Mark) Hour() int { return m.hour }

// Minute returns the minute component, 0–59.
func (m Mark) Minute() int { return m.minute }

// Minutes returns the mark as minutes since local midnight, 0–1439.
func (m Mark) Minutes() int { return m.hour*60 + m.minute }

// String renders the mark back to its canonical zero-padded "HH:MM" form, so
// Parse(m.String()) round-trips to m.
func (m Mark) String() string { return fmt.Sprintf("%02d:%02d", m.hour, m.minute) }

// Cron renders the mark as a daily five-field cron expression
// ("minute hour * * *"): 21:30 -> "30 21 * * *". It delegates to
// [CronOfMinutes] so a mark and a minutes-derived fire time render identically.
func (m Mark) Cron() string { return CronOfMinutes(m.Minutes()) }

// At returns the wall-clock instant of the mark on now's calendar date, in
// now's location: the mark's hour and minute placed on now's year/month/day
// (seconds and nanoseconds zeroed). It reads nothing from now but its date and
// location, so a fire on any day resolves the same civil time. It is the shared
// body of every daemon's per-day mark-to-instant resolution — the reference a
// missed-fire window compares against — so a mark config accepted resolves the
// identical instant everywhere.
func (m Mark) At(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), m.hour, m.minute, 0, 0, now.Location())
}

// CronOfMinutes renders minutes since local midnight (0–1439) as a daily
// five-field cron expression: 1290 -> "30 21 * * *". It is the shared body
// behind [Mark.Cron] and the daemons' backstop/fallback arithmetic, which
// derives a fire mark in minutes (a grace past another mark, wrapped at
// midnight) and needs the same rendering without first rebuilding a Mark.
func CronOfMinutes(total int) string {
	return fmt.Sprintf("%d %d * * *", total%60, total/60)
}
