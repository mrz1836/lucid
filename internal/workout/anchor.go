package workout

// This file owns the workout module's **daily-anchor projection** — the second
// pure read of the program, alongside the recommender and the trend. It turns the
// loaded program plus `now` into the floor the card shows today: each anchor item
// with the count that applies to the current program week, and the week index
// itself. It is pure — zero model calls, zero disk I/O — and carries no completion
// state and no comparison: the anchor is surfaced, never graded (the P3 sanctuary
// rule, applied to the daily floor). A program with no anchor items yields an
// honest empty projection the renderer drops entirely rather than a hollow region.
// See docs/mvp/workout-module.md §"The daily-anchor projection".

import (
	"strconv"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// anchorWeekDays is the length of one program week — the divisor that turns a
// day-distance from start_date into the 1-indexed week the targets are keyed by.
const anchorWeekDays = 7

// AnchorLine is one item of the daily floor as the card shows it today: the
// movement's name, the count that applies to the current program week, and the
// item's optional mode ("accumulate" marks a movement done in small sets through
// the day). Target is the number the card *displays*, never a bar the system
// scores against.
type AnchorLine struct {
	Name   string `json:"name"`
	Target int    `json:"target,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

// Anchor is the daily-anchor projection: the derived 1-indexed program week and
// one line per anchor item, in the program's own order. Items is nil when the
// program defines no anchor — the renderer drops the region rather than printing
// an empty header.
type Anchor struct {
	Week  int          `json:"week"`
	Items []AnchorLine `json:"items,omitempty"`
}

// BuildAnchor computes the daily-anchor projection for the given instant. It is
// pure — zero model calls, zero disk I/O — and reads only the already-parsed
// program, so the card's floor is unit-testable without a Ledger or a model. The
// week is derived from the program's start_date at the Ledger's rollover boundary
// (see [programWeek]) and each item's target is the one that applies to that week
// (see [effectiveTarget]). A nil location falls back to UTC, matching BuildTrend.
func BuildAnchor(prog Program, now time.Time, loc *time.Location) Anchor {
	if loc == nil {
		loc = time.UTC
	}
	week := programWeek(prog.StartDate, now, loc)
	if len(prog.DailyAnchor.Items) == 0 {
		return Anchor{Week: week}
	}

	lines := make([]AnchorLine, 0, len(prog.DailyAnchor.Items))
	for _, item := range prog.DailyAnchor.Items {
		lines = append(lines, AnchorLine{
			Name:   item.Name,
			Target: effectiveTarget(item, week, prog.DailyAnchor.TargetsByWeek),
			Mode:   item.Mode,
		})
	}
	return Anchor{Week: week, Items: lines}
}

// programWeek derives the 1-indexed program week for now. The day-distance is
// counted from start_date to the logical civil day now falls on
// (observations.LogicalBaseDate under the standard rollover), so the week turns
// over at the same boundary events are filed under rather than at midnight-local.
// An absent or unparseable start_date is week 1, and a now on or before the start
// is week 1 — the index never goes below 1, so a program configured ahead of time
// reads as "week 1" instead of a zero or a negative week.
func programWeek(startDate string, now time.Time, loc *time.Location) int {
	trimmed := strings.TrimSpace(startDate)
	if trimmed == "" {
		return 1
	}
	start, err := time.ParseInLocation(dateLayout, trimmed, loc)
	if err != nil {
		return 1
	}
	today := observations.LogicalBaseDate(now.In(loc), observations.DefaultRolloverMin)
	return max(1, engine.DaysSince(start, today)/anchorWeekDays+1)
}

// effectiveTarget resolves the count that applies to an anchor item in the given
// program week: the value from the highest targets_by_week entry whose week index
// is at or below the current week and that names the item, falling back to the
// item's own base target when no entry names it. Two consequences, both intended:
// an undefined intermediate week inherits the last defined one (a program may
// specify weeks 1 and 3 and skip 2), and a program running past its last defined
// week holds at that last target — a ramp that ends is a plateau, not a reset.
// Entries whose key is not a positive integer are ignored rather than treated as
// week 0, so a malformed key can never shadow a valid one. The walk raises its
// high-water week only on an entry that names the item, so the result never
// depends on the map's iteration order.
func effectiveTarget(item AnchorItem, week int, byWeek map[string]map[string]int) int {
	target, best := item.Target, 0
	for key, targets := range byWeek {
		k, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || k < 1 || k > week || k <= best {
			continue
		}
		value, ok := targets[item.Name]
		if !ok {
			continue
		}
		target, best = value, k
	}
	return target
}
