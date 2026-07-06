package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// dateLayout is the logical_date string form (data-model.md: local civil
// dates, YYYY-MM-DD).
const dateLayout = "2006-01-02"

// parseHM parses an "HH:MM" clock string into minutes since midnight. It
// is strict: the hour must be 0–23 and the minute 0–59, so a malformed
// chain.json clock is caught at boot rather than silently mis-scheduling.
func parseHM(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid clock %q (want HH:MM)", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("invalid hour in clock %q", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid minute in clock %q", s)
	}
	return h*60 + m, nil
}

// minutesOfDay returns t's time-of-day as minutes since local midnight.
func minutesOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }

// DateOf truncates t to local civil midnight — the canonical anchor for a
// logical day. All logical-day arithmetic happens on these midnight
// values in the host's own location (data-model.md: trust the host clock).
func DateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// AddDays returns the civil date n days from d (n may be negative).
func AddDays(d time.Time, n int) time.Time { return d.AddDate(0, 0, n) }

// DateString renders a civil date as YYYY-MM-DD.
func DateString(d time.Time) string { return d.Format(dateLayout) }

// DayID renders the day-record id for a civil date (day_YYYY_MM_DD).
func DayID(d time.Time) string {
	return fmt.Sprintf("day_%04d_%02d_%02d", d.Year(), int(d.Month()), d.Day())
}

// DaysBetween returns the whole-day span from a to b (b − a), both
// truncated to civil midnight. It is used for the backfill window check.
func DaysBetween(a, b time.Time) int {
	a, b = DateOf(a), DateOf(b)
	return int(b.Sub(a).Hours() / 24)
}

// BaseLogicalDate applies the primary rollover rule (engine-module.md
// §"/closeout sequence" step 1): before the rollover mark the day belongs
// to yesterday; at or after it, to today. The router uses it to name the
// previous logical day when testing whether the night-shift back-
// attribution applies.
func (c Clocks) BaseLogicalDate(now time.Time) time.Time { return c.baseLogicalDate(now) }

// baseLogicalDate is the unexported implementation shared by
// [Clocks.BaseLogicalDate], [Clocks.ResolveLogicalDay], and
// [ComputeSwitch].
func (c Clocks) baseLogicalDate(now time.Time) time.Time {
	base := DateOf(now)
	if minutesOfDay(now) < c.RolloverMin {
		return AddDays(base, -1)
	}
	return base
}

// bellRung reports whether the base logical day's evening bell has already
// rung as of now. It is only consulted in the post-rollover night-shift
// branch, where the base day is now's own calendar date, so a same-date
// time-of-day comparison is exact.
func (c Clocks) bellRung(now time.Time) bool {
	return minutesOfDay(now) >= c.BellMin
}

// ResolveLogicalDay resolves which logical day a close-out at now answers
// (engine-module.md §"/closeout sequence" step 1, the binding attribution
// rule: a close-out belongs to the bell that initiated it).
//
//   - now < rollover ⇒ yesterday (the base rule).
//   - now ≥ rollover AND the previous logical day is unrecorded AND
//     today's bell has not yet rung ⇒ the previous logical day (the
//     night-shift 04:12 case still answers last night's bell).
//   - forceToday (`/closeout today`) forces the base logical day, skipping
//     the night-shift back-attribution.
//
// prevRecorded is supplied by the caller: whether a day record already
// exists for the base logical day minus one.
func (c Clocks) ResolveLogicalDay(now time.Time, prevRecorded, forceToday bool) time.Time {
	base := c.baseLogicalDate(now)
	if forceToday {
		return base
	}
	if minutesOfDay(now) >= c.RolloverMin && !prevRecorded && !c.bellRung(now) {
		return AddDays(base, -1)
	}
	return base
}
