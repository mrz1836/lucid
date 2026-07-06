// Package isoweek is the deterministic ISO-8601 week helper Lucid uses to id
// and bound weekly reflection records (data-model.md §"Weekly reflections";
// claude-code-workflow.md §"Deterministic scripts before clever agents"). No
// LLM ever does timezone or week math — this package is the single source of
// truth for the `reflection_YYYY_wWW` id, the `YYYY-Www` label, and the
// Monday-00:00:00 → Sunday-23:59:59 bounds of the week that contains an instant.
//
// It is pure and dependency-free: every function is a total, side-effect-free
// map from a time to strings or times, so a record's id, label, and window
// always agree and reruns are byte-identical.
package isoweek

import (
	"fmt"
	"time"
)

// idPrefix is the reflection-record id stem (data-model.md §"Naming
// conventions": `reflection_2026_w18`).
const idPrefix = "reflection_"

// ID returns the reflection-record id for the ISO week containing t:
// `reflection_YYYY_wWW`, with the week zero-padded to two digits. The year and
// week come from [time.Time.ISOWeek], so a late-December or early-January date
// carries its ISO year (which may differ from the calendar year) — the same
// convention data-model.md documents.
func ID(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%s%04d_w%02d", idPrefix, year, week)
}

// Label returns the ISO-8601 week label for t: `YYYY-Www` (e.g. `2026-W18`),
// the value written to a reflection record's `iso_week` frontmatter field.
func Label(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}

// Bounds returns the start and end of the ISO week that contains t, in t's own
// location: start is Monday at 00:00:00 and end is the following Sunday at
// 23:59:59 (one second before the next week's Monday), matching the
// `window_start`/`window_end` pair in a reflection record (data-model.md
// §"Weekly reflections"). The bounds are computed from calendar-date
// arithmetic, so they are correct across month and year boundaries regardless
// of how ISOWeek numbers the week.
func Bounds(t time.Time) (start, end time.Time) {
	// Go's Weekday runs Sunday=0..Saturday=6; the ISO week starts on Monday, so
	// treat Sunday as day 7 and count days back to the week's Monday.
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	start = day.AddDate(0, 0, -(weekday - 1))
	end = start.AddDate(0, 0, 7).Add(-time.Second)
	return start, end
}

// Same reports whether a and b fall in the same ISO week — the test the
// reflection writer uses to decide whether a new `/reflect` pass appends to an
// existing week file or opens a fresh one. It compares ids, so it is correct
// across year boundaries where naive week-number comparison is not.
func Same(a, b time.Time) bool {
	return ID(a) == ID(b)
}
