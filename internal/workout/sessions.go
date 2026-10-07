package workout

// This file owns the sessions echo on the on-demand `--json` projection: the
// logged sessions the decision read, each as its current folded state, so a
// harness can read a corrected value (`lucid workout amend`) straight back
// rather than infer it from the pick or the trend. It renders nothing and is
// never stored.

import (
	"cmp"
	"slices"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// SessionView is one logged session as the `workout --json` projection echoes
// it: the session's id and its current fields with every correction folded in
// (latest correction winning per field, a re-date having moved occurred_at and
// logical_date). Session fields the session never recorded are omitted rather
// than rendered as a hollow zero — RPE and duration are pointers because 0 is
// a real reading — and the two dates are always present.
type SessionView struct {
	ID          string   `json:"id"`
	Type        string   `json:"type,omitempty"`
	RPE         *int     `json:"rpe,omitempty"`
	DurationMin *int     `json:"duration_min,omitempty"`
	BodyParts   []string `json:"body_parts,omitempty"`
	Movements   []string `json:"movements,omitempty"`
	Note        string   `json:"note,omitempty"`
	OccurredAt  string   `json:"occurred_at"`
	LogicalDate string   `json:"logical_date"`
}

// sessionAt pairs a view with its resolved instant, so the echo sorts on the
// same parsed time the recovery guardrail reads rather than on a string.
type sessionAt struct {
	view SessionView
	at   time.Time
}

// BuildSessions projects the already-folded workout events into the sessions
// echo: every logged session whose (folded) logical day falls in the trend's
// window ending today — the same band the trend counts — newest first, with the
// id breaking a tie. Corrections must already be folded on (the composer's
// read choke point does it); an anchor-only capture is omitted, since it closes
// the day but is not a session, so every id echoed is one `workout amend`
// accepts. It is pure, and always returns a non-nil slice so an empty window
// projects as `[]`.
func BuildSessions(workouts []observations.Event, now time.Time, loc *time.Location) []SessionView {
	if loc == nil {
		loc = time.UTC
	}
	today := observations.LogicalBaseDate(now.In(loc), observations.DefaultRolloverMin)

	found := make([]sessionAt, 0, len(workouts))
	for _, ev := range workouts {
		if ev.Kind != observations.KindWorkout || observations.IsWorkoutAnchorOnly(ev) {
			continue
		}
		day, ok := eventLogicalDay(ev, loc)
		if !ok {
			continue
		}
		if ds := engine.DaysSince(day, today); ds < 0 || ds >= defaultTrendWindowDays {
			continue
		}
		found = append(found, sessionAt{view: sessionView(ev, day), at: eventTime(ev, loc)})
	}
	slices.SortStableFunc(found, func(a, b sessionAt) int {
		if c := b.at.Compare(a.at); c != 0 {
			return c
		}
		return cmp.Compare(b.view.ID, a.view.ID)
	})

	out := make([]SessionView, 0, len(found))
	for _, s := range found {
		out = append(out, s.view)
	}
	return out
}

// sessionView reads one folded workout event's session fields, tolerating the
// typed values a fresh capture carries and the JSON-decoded ones a Ledger
// round-trip yields. The type and note are echoed exactly as stored — a note is
// kept verbatim on the record, so the echo never re-trims it. day is the
// event's resolved logical day.
func sessionView(ev observations.Event, day time.Time) SessionView {
	v := SessionView{
		ID:          ev.ID,
		BodyParts:   payloadStrings(ev.Payload, "body_parts"),
		Movements:   payloadStrings(ev.Payload, "movements"),
		OccurredAt:  ev.OccurredAt,
		LogicalDate: day.Format(dateLayout),
	}
	v.Type, _ = ev.Payload["type"].(string)
	v.Note, _ = ev.Payload["note"].(string)
	if n, ok := payloadInt(ev.Payload, "rpe"); ok {
		v.RPE = &n
	}
	if n, ok := payloadInt(ev.Payload, "duration_min"); ok {
		v.DurationMin = &n
	}
	return v
}
