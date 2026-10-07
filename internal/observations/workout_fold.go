package observations

import (
	"cmp"
	"slices"
	"time"
)

// RefRedate marks a workout correction that moves the session to another day
// (observations.md §2, "Workout re-dates carry refs.redate"; mvp/data-model.md
// §"Workout amendments"). It is a boolean modifier on a refs.corrects event,
// not a link: a correction carrying `redate: true` also carries the session's
// new occurred_at, precision, and logical_date, and the fold takes the
// session's date trio only from such corrections. Like RefCorrects it is
// exported so the amend write path (internal/router) stamps the very key this
// fold reads — one source of truth for the wire format.
const RefRedate = "redate"

// FoldWorkoutAmendments collapses logged workout sessions and their
// corrections into the effective current sessions (mvp/data-model.md
// §"Workout amendments", fold-on-read). A correction is a KindWorkout event
// whose refs.corrects names a base session; the fold overlays only the payload
// fields each correction carries (last-write-wins per field, a list field
// carrying its full replacement list), removes any field named in
// refs.cleared, and — for a correction marked refs.redate — moves the
// session's date trio (occurred_at, precision, occurred_at_end, logical_date)
// to the correction's own. A correction that is not marked redate never
// touches the dates, so a later field edit keeps an earlier re-date.
//
// A session's corrections apply in recorded_at order with the event id as the
// deterministic tie-break — never by id alone: a re-date files under the new
// logical day, so its id carries that day and a later correction can sort
// lexically before an earlier one.
//
// Corrections are dropped from the output (they are history, not sessions),
// and a correction whose base session is absent from the input is dropped too,
// so a session counts once however often it is amended. Every non-KindWorkout
// event passes through untouched, so the fold is safe on a mixed slice. The
// fold is pure — each base is folded onto a copy, so the input is never
// mutated — and input order is preserved: a folded session keeps its base
// position even when a re-date moves its logical day.
func FoldWorkoutAmendments(events []Event) []Event {
	byTarget := groupWorkoutAmendments(events)

	out := make([]Event, 0, len(events))
	for _, e := range events {
		// Drop corrections — their effect lives on the folded session.
		if _, ok := workoutCorrects(e); ok {
			continue
		}
		// Non-workout events (and sessions without corrections) pass through
		// untouched.
		if e.Kind != KindWorkout {
			out = append(out, e)
			continue
		}
		amendments := byTarget[e.ID]
		if len(amendments) == 0 {
			out = append(out, e)
			continue
		}
		folded := cloneForFold(e)
		for _, a := range amendments {
			applyWorkoutAmendment(&folded, a)
		}
		out = append(out, folded)
	}
	return out
}

// groupWorkoutAmendments buckets every workout correction in events by the
// base session its refs.corrects names, each bucket in fold order
// (compareWorkoutAmendments).
func groupWorkoutAmendments(events []Event) map[string][]Event {
	byTarget := map[string][]Event{}
	for _, e := range events {
		if target, ok := workoutCorrects(e); ok {
			byTarget[target] = append(byTarget[target], e)
		}
	}
	for target := range byTarget {
		slices.SortStableFunc(byTarget[target], compareWorkoutAmendments)
	}
	return byTarget
}

// compareWorkoutAmendments orders two corrections of one session
// chronologically: by parsed recorded_at instant (the write path stamps it at
// nanosecond precision), then by event id. A correction whose recorded_at does
// not parse sorts before every parseable one — it never outranks a well-formed
// later correction — and the id settles every remaining tie, so the order is
// total and deterministic.
func compareWorkoutAmendments(a, b Event) int {
	at, aok := parseRecordedAt(a.RecordedAt)
	bt, bok := parseRecordedAt(b.RecordedAt)
	switch {
	case aok && bok:
		if c := at.Compare(bt); c != 0 {
			return c
		}
	case aok != bok:
		if aok {
			return 1
		}
		return -1
	}
	return cmp.Compare(a.ID, b.ID)
}

// parseRecordedAt parses an RFC3339 recorded_at stamp, with or without
// fractional seconds.
func parseRecordedAt(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// workoutCorrects reports the base session id a KindWorkout correction
// corrects, and whether the event is such a correction. A non-workout event,
// or a workout event with no non-empty string refs.corrects, is not a
// correction.
func workoutCorrects(e Event) (string, bool) {
	if e.Kind != KindWorkout || e.Refs == nil {
		return "", false
	}
	s, ok := e.Refs[RefCorrects].(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// isWorkoutRedate reports whether a correction carries the refs.redate marker
// (a JSON boolean true; any other value is not a re-date).
func isWorkoutRedate(e Event) bool {
	if e.Refs == nil {
		return false
	}
	v, ok := e.Refs[RefRedate].(bool)
	return ok && v
}

// applyWorkoutAmendment overlays one correction onto an (already-cloned) base
// session: it sets every payload key the correction carries, removes every
// payload field named in refs.cleared (kept for parity with memory; the
// workout write path does not emit it), and, for a redate-marked correction,
// moves the session's date trio. The base's own refs are never touched — a
// folded session must not read as a correction.
func applyWorkoutAmendment(base *Event, amend Event) {
	for k, v := range amend.Payload {
		base.Payload[k] = v
	}
	for _, field := range clearedFields(amend.Refs) {
		delete(base.Payload, field)
	}
	if isWorkoutRedate(amend) {
		base.OccurredAt = amend.OccurredAt
		base.OccurredAtPrecision = amend.OccurredAtPrecision
		base.OccurredAtEnd = nil
		if amend.OccurredAtEnd != nil {
			end := *amend.OccurredAtEnd
			base.OccurredAtEnd = &end
		}
		base.LogicalDate = amend.LogicalDate
	}
}
