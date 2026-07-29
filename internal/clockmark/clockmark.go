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
// It is pure: no filesystem, no clock, no model, no obligation — only fmt,
// strconv, and strings. Callers keep their own public wrappers, return shapes,
// and error wording (mirroring internal/keyderive); this package owns only the
// one parsing rule and the renderings derived from it.
package clockmark

import (
	"fmt"
	"strconv"
	"strings"
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
// ("minute hour * * *"): 21:30 -> "30 21 * * *".
func (m Mark) Cron() string { return fmt.Sprintf("%d %d * * *", m.minute, m.hour) }
