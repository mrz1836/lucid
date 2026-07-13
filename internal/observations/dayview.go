package observations

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// DayView is the observation half of the `/day` join (observations.md §7):
// the events logged for one logical day plus any range event that spans it
// (a night's sleep, a multi-day episode) surfaced from adjacent days. The
// storage adapter reads both trees and the router composes the engine record
// and raw entry ids around this; the assembly here is pure and deterministic,
// so the same Ledger renders byte-identical output on every rerun.
type DayView struct {
	Date        string
	Events      []Event
	RangeEvents []Event
}

// AssembleDayView builds the day view for date from the day's own events and
// a candidate set of range events (from the rebuildable range index). Both
// lists are sorted by id, and the range candidates are filtered to those that
// actually span the day — start strictly before it, end on or after it — so a
// same-day range that already lives in the day file is not duplicated.
func AssembleDayView(date string, dayEvents, rangeCandidates []Event, loc *time.Location) DayView {
	dv := DayView{Date: date}
	dv.Events = SortEventsByID(dayEvents)
	var spanning []Event
	for _, e := range rangeCandidates {
		if IsRangeSpanning(e, date, loc) {
			spanning = append(spanning, e)
		}
	}
	dv.RangeEvents = SortEventsByID(spanning)
	return dv
}

// IsRangeSpanning reports whether a range event spans the day `date` from an
// earlier start (observations.md §2: range events are indexed by their start
// day; day views additionally surface any range event spanning the requested
// day). It is false for a non-range event and for a range whose own start day
// is `date` (already in the day file).
func IsRangeSpanning(e Event, date string, loc *time.Location) bool {
	if e.OccurredAtPrecision != PrecisionRange || e.OccurredAtEnd == nil {
		return false
	}
	target, err := ParseDate(date, loc)
	if err != nil {
		return false
	}
	start, err := ParseDate(e.LogicalDate, loc)
	if err != nil {
		return false
	}
	end, ok := endLogicalDate(*e.OccurredAtEnd, loc)
	if !ok {
		return false
	}
	// Strictly after start (its own file already carries it) and on/before end.
	return start.Before(target) && !target.After(end)
}

// Empty reports whether the day view has no observation content — the router
// uses it to decide the honest "No record for <date>." message.
func (dv DayView) Empty() bool {
	return len(dv.Events) == 0 && len(dv.RangeEvents) == 0
}

// Lines renders the observation events as inventory — one deterministic line
// per event, kind and value with no evaluative language (observations.md §0:
// no streak, quota, target, or score; the ack and every surface is inventory,
// never obligation). Payload keys are emitted in sorted order so the render is
// byte-stable.
func (dv DayView) Lines() []string {
	lines := make([]string, 0, len(dv.Events)+len(dv.RangeEvents))
	for _, e := range dv.Events {
		lines = append(lines, eventLine(e, false))
	}
	for _, e := range dv.RangeEvents {
		lines = append(lines, eventLine(e, true))
	}
	return lines
}

// eventLine renders one event as "<kind> <k=v, …> (<id>)". A spanning range
// event is marked so the reader knows it began earlier.
func eventLine(e Event, spanning bool) string {
	parts := make([]string, 0, len(e.Payload))
	for _, k := range sortedKeys(e.Payload) {
		parts = append(parts, fmt.Sprintf("%s=%v", k, e.Payload[k]))
	}
	body := strings.Join(parts, ", ")
	prefix := e.Kind
	if spanning {
		prefix = e.Kind + " (spanning)"
	}
	if body == "" {
		return fmt.Sprintf("%s (%s)", prefix, e.ID)
	}
	return fmt.Sprintf("%s %s (%s)", prefix, body, e.ID)
}

// SortEventsByID returns a copy of events sorted by id — the deterministic
// order every projection surfaces (ids encode the logical date and a
// monotonic seq, so id order is time order within a day).
func SortEventsByID(events []Event) []Event {
	out := append([]Event{}, events...)
	slices.SortStableFunc(out, func(a, b Event) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// endLogicalDate resolves the calendar date of a range event's end timestamp.
func endLogicalDate(endRFC3339 string, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.UTC
	}
	t, err := time.Parse(time.RFC3339, endRFC3339)
	if err != nil {
		return time.Time{}, false
	}
	return DateOf(t.In(loc)), true
}
