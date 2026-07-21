package workout

// This file owns the workout module's **trend / progress projection**. It is a
// read-only fold over the Ledger's workout and body-state events plus the Engine
// metrics — nothing here is ever written back onto an event (the P3 sanctuary
// rule: observations stay inventory, never a score). The streak is the Engine
// chain's, read straight from engine.Metrics; the frequency, skipped-day count,
// and body-response are counted from the two new kinds. The projection computes
// honestly with zero data (an empty trend) and with sparse data, and it makes
// zero model calls and zero disk reads — the caller passes the bounded slices in.
// See docs/mvp/workout-module.md §"The trend / progress projection".

import (
	"cmp"
	"slices"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// defaultTrendWindowDays is the trailing civil-day range BuildTrend projects over
// when the caller names none — four weeks, long enough to read a frequency
// direction without letting a long-ago training block distort the recent picture.
const defaultTrendWindowDays = 28

// trendWeekDays is the length of each of the two trailing windows the frequency
// direction compares: this week (days 0–6) against the week before (days 7–13).
const trendWeekDays = 7

// Frequency-direction labels. The direction is a coarse, deterministic read of
// this week's session count against the prior week's — never a grade, just a
// pointer so the user can see whether the practice is picking up or easing off.
const (
	DirectionUp   = "up"
	DirectionFlat = "flat"
	DirectionDown = "down"
)

// BodySignal is one body part's most-recent soreness/pain reading inside the
// trend window — how the body answered the load, surfaced as inventory. Soreness
// and pain are pointers so an absent field is omitted rather than rendered as a
// hollow zero (a body_state event may carry either, both, or neither). AsOf is
// the reading's logical day.
type BodySignal struct {
	Part     string `json:"part"`
	Soreness *int   `json:"soreness,omitempty"`
	Pain     *int   `json:"pain,omitempty"`
	AsOf     string `json:"as_of,omitempty"`
}

// Trend is the read-only progress projection the surface shows: the Engine
// chain's streak/adherence, the frequency read (this week vs the week before with
// a direction), the skipped-day count over the window, and the recent
// body-response per part. It is computed on demand and never stored.
type Trend struct {
	Streak       int          `json:"streak"`
	Adherence    float64      `json:"adherence"`
	WindowDays   int          `json:"window_days"`
	Sessions     int          `json:"sessions"`
	ThisWeek     int          `json:"this_week"`
	PriorWeek    int          `json:"prior_week"`
	Direction    string       `json:"direction"`
	SkippedDays  int          `json:"skipped_days"`
	BodyResponse []BodySignal `json:"body_response,omitempty"`
}

// TrendInput is everything BuildTrend folds into the projection: the bounded
// recent workout and body-state slices (recency is the caller's concern, exactly
// like RecommendInput), the Engine metrics carrying the chain streak/adherence,
// the current instant, the local location, and an optional window override
// (WindowDays ≤ 0 → the four-week default). Nothing here is mutated.
type TrendInput struct {
	Workouts   []observations.Event
	BodyState  []observations.Event
	Metrics    engine.Metrics
	Now        time.Time
	Loc        *time.Location
	WindowDays int
}

// BuildTrend computes the read-only progress projection. It is pure — zero model
// calls, zero disk I/O — and stores nothing back onto any event (P3 sanctuary).
// The streak and adherence are copied straight from the Engine fold (never
// recomputed as a score on workout events); the session counts and skipped-day
// count are derived from the distinct logical days the workout events fall on;
// the body-response is the most-recent body_state reading per part in range. With
// no events it yields an honest empty trend (zero sessions, the whole window
// skipped, no body signals) rather than a crash or a fabricated number.
func BuildTrend(in TrendInput) Trend {
	loc := in.Loc
	if loc == nil {
		loc = time.UTC
	}
	window := in.WindowDays
	if window <= 0 {
		window = defaultTrendWindowDays
	}
	today := observations.LogicalBaseDate(in.Now.In(loc), observations.DefaultRolloverMin)

	days := workoutDays(in.Workouts, loc)
	thisWeek := daysInBucket(days, today, 0, trendWeekDays-1)
	priorWeek := daysInBucket(days, today, trendWeekDays, 2*trendWeekDays-1)
	sessions := daysInBucket(days, today, 0, window-1)

	return Trend{
		Streak:       in.Metrics.CurrentStreak,
		Adherence:    in.Metrics.Adherence.Adherence,
		WindowDays:   window,
		Sessions:     sessions,
		ThisWeek:     thisWeek,
		PriorWeek:    priorWeek,
		Direction:    frequencyDirection(thisWeek, priorWeek),
		SkippedDays:  max(0, window-sessions),
		BodyResponse: bodyResponse(in.BodyState, today, window, loc),
	}
}

// workoutDays buckets the workout events into the set of distinct logical civil
// days they fall on, keyed by the date string so two sessions on one day count
// once. A non-workout event or one whose day cannot be resolved is skipped.
func workoutDays(events []observations.Event, loc *time.Location) map[string]time.Time {
	out := make(map[string]time.Time, len(events))
	for _, ev := range events {
		if ev.Kind != observations.KindWorkout {
			continue
		}
		day, ok := eventLogicalDay(ev, loc)
		if !ok {
			continue
		}
		out[day.Format(dateLayout)] = day
	}
	return out
}

// daysInBucket counts the distinct workout days whose civil-day distance behind
// today falls in the inclusive [lo, hi] band. A future-dated day (distance < 0)
// is never counted, so a session logged ahead of the clock cannot inflate a
// window. The distance uses engine.DaysSince, which re-anchors both instants to
// UTC civil midnight so a DST transition cannot make the count drift.
func daysInBucket(days map[string]time.Time, today time.Time, lo, hi int) int {
	n := 0
	for _, day := range days {
		ds := engine.DaysSince(day, today)
		if ds >= lo && ds <= hi {
			n++
		}
	}
	return n
}

// frequencyDirection reads this week's session count against the prior week's — a
// coarse, deterministic pointer, never a grade.
func frequencyDirection(thisWeek, priorWeek int) string {
	switch {
	case thisWeek > priorWeek:
		return DirectionUp
	case thisWeek < priorWeek:
		return DirectionDown
	default:
		return DirectionFlat
	}
}

// bodyResponse folds the body-state events into the most-recent soreness/pain
// reading per part inside the window. Recency is the reading's own instant
// (occurred_at, falling back to its logical day); on a tie the later event in the
// slice wins, so the result is deterministic for a given input. The output is
// sorted by part for a stable projection regardless of the map's iteration order.
func bodyResponse(events []observations.Event, today time.Time, window int, loc *time.Location) []BodySignal {
	type reading struct {
		event   observations.Event
		instant time.Time
		day     time.Time
	}
	latest := make(map[string]reading)
	for _, ev := range events {
		if ev.Kind != observations.KindBodyState {
			continue
		}
		part, ok := payloadString(ev.Payload, "body_part")
		if !ok {
			continue
		}
		day, ok := eventLogicalDay(ev, loc)
		if !ok {
			continue
		}
		if ds := engine.DaysSince(day, today); ds < 0 || ds > window-1 {
			continue
		}
		instant := eventTime(ev, loc)
		if instant.IsZero() {
			instant = day
		}
		key := normalize(part)
		if prev, seen := latest[key]; seen && instant.Before(prev.instant) {
			continue
		}
		latest[key] = reading{event: ev, instant: instant, day: day}
	}

	out := make([]BodySignal, 0, len(latest))
	for part, r := range latest {
		sig := BodySignal{Part: part, AsOf: r.day.Format(dateLayout)}
		if v, ok := payloadInt(r.event.Payload, "soreness"); ok {
			sig.Soreness = &v
		}
		if v, ok := payloadInt(r.event.Payload, "pain"); ok {
			sig.Pain = &v
		}
		out = append(out, sig)
	}
	slices.SortFunc(out, func(a, b BodySignal) int { return cmp.Compare(a.Part, b.Part) })
	return out
}

// eventLogicalDay resolves an event's logical civil day: the stored logical_date
// when present and parseable, else the day derived from its occurred_at under the
// rollover rule. An event with neither a usable logical date nor a parseable
// time is skipped (ok=false) rather than counted on a wrong day.
func eventLogicalDay(ev observations.Event, loc *time.Location) (time.Time, bool) {
	if ev.LogicalDate != "" {
		if d, err := time.ParseInLocation(dateLayout, ev.LogicalDate, loc); err == nil {
			return d, true
		}
	}
	at := eventTime(ev, loc)
	if at.IsZero() {
		return time.Time{}, false
	}
	return observations.LogicalBaseDate(at, observations.DefaultRolloverMin), true
}
