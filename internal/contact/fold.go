package contact

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/isoweek"
)

// Cadence states (ADR-0014 §3). A person with no cadence events is none.
const (
	StateNone    = "none"
	StateActive  = "active"
	StatePaused  = "paused"
	StateRetired = "retired"
)

// Contact is the most recent non-voided contact with a person: its logical day
// and how (Via, "" when not given).
type Contact struct {
	Day string
	Via string
}

// PersonState is one person's folded check-in state. Window is the most recent
// window set (kept through a pause or retire for display); Anchor and Closes
// are set only while the cadence is active — the window closes Window.Days
// after Anchor. LastContact is nil until a non-voided contact exists, and is
// kept for people with no cadence at all. SnoozedWeeks holds the ISO week label
// (YYYY-Www) of every non-voided snooze.
type PersonState struct {
	PersonKey    string
	State        string
	Window       Window
	Anchor       string
	Closes       string
	LastContact  *Contact
	SnoozedWeeks map[string]bool
}

// Fold resolves events into per-person state, keyed by person_key. The caller
// passes events whose person keys are already canonical (merge redirects
// followed), so history recorded before a merge folds into the merged person.
// An event that fails [Event.Validate] is skipped, never a panic. Cadence
// events apply in (recorded_at, id) order; a transition the verbs would have
// refused (a pause that is not active, say) is ignored.
//
// The window-start rules (ADR-0014 §3) hold the anchor:
//   - after a first set, or a set after retire: the most recent non-voided
//     contact, whenever it was logged, or the set day when none exists;
//   - after a resume: the later of the most recent non-voided contact and the
//     resume day;
//   - a set on a live cadence changes only the window, keeping the anchor.
func Fold(events []Event) map[string]PersonState {
	valid := validEvents(events)
	voided := voidedBy(valid)
	byPerson := make(map[string][]Event)
	for _, e := range valid {
		byPerson[e.PersonKey] = append(byPerson[e.PersonKey], e)
	}
	out := make(map[string]PersonState, len(byPerson))
	for key, evs := range byPerson {
		out[key] = foldPerson(key, evs, voided)
	}
	return out
}

// foldPerson folds one person's events (already validated, in any order).
func foldPerson(key string, evs []Event, voided map[string]string) PersonState {
	st := PersonState{
		PersonKey:    key,
		State:        StateNone,
		LastContact:  lastContact(evs, voided),
		SnoozedWeeks: snoozedWeeks(evs, voided),
	}
	base, resumed := applyCadence(&st, evs)
	if st.State == StateActive {
		st.Anchor = anchorFor(base, resumed, st.LastContact)
		st.Closes = addDays(st.Anchor, st.Window.Days)
	}
	return st
}

// lastContact returns the most recent non-voided contact, or nil. Two contacts
// on the same day resolve to the later write.
func lastContact(evs []Event, voided map[string]string) *Contact {
	var last *Event
	for i := range evs {
		e := &evs[i]
		if e.Type != TypeContact {
			continue
		}
		if _, gone := voided[e.ID]; gone {
			continue
		}
		if last == nil || e.LogicalDate > last.LogicalDate ||
			(e.LogicalDate == last.LogicalDate && compareEvents(*e, *last) > 0) {
			last = e
		}
	}
	if last == nil {
		return nil
	}
	return &Contact{Day: last.LogicalDate, Via: last.Via}
}

// snoozedWeeks returns the ISO week label of every non-voided snooze; never nil.
func snoozedWeeks(evs []Event, voided map[string]string) map[string]bool {
	weeks := map[string]bool{}
	for _, e := range evs {
		if e.Type != TypeSnooze {
			continue
		}
		if _, gone := voided[e.ID]; gone {
			continue
		}
		if week, ok := weekOf(e.LogicalDate); ok {
			weeks[week] = true
		}
	}
	return weeks
}

// applyCadence replays the cadence events in (recorded_at, id) order onto st,
// setting its State and Window. It returns the day the current regime started
// and whether that start was a resume (otherwise a first set, or a set after
// retire). A transition the verbs would have refused is ignored.
func applyCadence(st *PersonState, evs []Event) (base string, resumed bool) {
	var cadence []Event
	for _, e := range evs {
		if e.Type == TypeCadence {
			cadence = append(cadence, e)
		}
	}
	slices.SortStableFunc(cadence, compareEvents)
	for _, c := range cadence {
		switch c.Action {
		case ActionSet:
			if st.State == StateNone || st.State == StateRetired {
				st.State = StateActive
				base, resumed = c.LogicalDate, false
			}
			st.Window = Window{Text: c.Window, Days: c.WindowDays}
		case ActionPause:
			if st.State == StateActive {
				st.State = StatePaused
			}
		case ActionResume:
			if st.State == StatePaused {
				st.State = StateActive
				base, resumed = c.LogicalDate, true
			}
		case ActionRetire:
			if st.State == StateActive || st.State == StatePaused {
				st.State = StateRetired
			}
		}
	}
	return base, resumed
}

// anchorFor applies the window-start rules (ADR-0014 §3) to an active regime
// that started on base: after a set, the last contact wins whenever it falls,
// else base; after a resume, the later of the last contact and base.
func anchorFor(base string, resumed bool, last *Contact) string {
	switch {
	case last == nil:
		return base
	case !resumed:
		return last.Day
	case last.Day > base:
		return last.Day
	default:
		return base
	}
}

// Transition refusals (error-states.md Ct-7, Ct-8, Ct-9). A refused transition
// is a [*TransitionError] that unwraps to one of these.
var (
	// ErrNotActive refuses a pause on a cadence that is not active.
	ErrNotActive = errors.New("contact: cadence is not active")
	// ErrNotPaused refuses a resume on a cadence that is not paused.
	ErrNotPaused = errors.New("contact: cadence is not paused")
	// ErrNoLiveCadence refuses a retire with no live cadence.
	ErrNoLiveCadence = errors.New("contact: no live cadence")
)

// TransitionError is a refused cadence transition: Action could not apply from
// State. [TransitionError.Message] renders the user-facing copy for a person.
type TransitionError struct {
	Action string
	State  string
}

// Error renders the refusal for an unnamed person.
func (e *TransitionError) Error() string { return e.Message("this person") }

// Unwrap returns the sentinel for the refused action.
func (e *TransitionError) Unwrap() error {
	switch e.Action {
	case ActionPause:
		return ErrNotActive
	case ActionResume:
		return ErrNotPaused
	default:
		return ErrNoLiveCadence
	}
}

// Message renders the refusal naming the person and the state, without the
// trailing "Nothing was saved." the caller appends.
func (e *TransitionError) Message(name string) string {
	if e.State == StateNone || e.State == "" || e.Action == ActionRetire {
		return name + " has no check-in cadence to " + e.Action + "."
	}
	switch {
	case e.Action == ActionResume && e.State == StateActive:
		return name + "'s check-ins aren't paused — nothing to resume."
	case e.State == StatePaused:
		return name + "'s check-ins are already paused — nothing to " + e.Action + "."
	default:
		return name + "'s check-ins are " + e.State + " — nothing to " + e.Action + "."
	}
}

// ValidateTransition reports whether a cadence action may apply to a person in
// state (ADR-0014 §3): set always applies (it starts, changes or restarts a
// window); pause needs an active cadence; resume needs a paused one; retire
// needs a live (active or paused) one. A refusal is a [*TransitionError].
func ValidateTransition(state PersonState, action string) error {
	s := state.State
	if s == "" {
		s = StateNone
	}
	switch action {
	case ActionSet:
		return nil
	case ActionPause:
		if s == StateActive {
			return nil
		}
	case ActionResume:
		if s == StatePaused {
			return nil
		}
	case ActionRetire:
		if s == StateActive || s == StatePaused {
			return nil
		}
	default:
		return fmt.Errorf("contact: unknown cadence action %q", action)
	}
	return &TransitionError{Action: action, State: s}
}

// Void refusals (error-states.md Ct-11). Unknown and already-voided refusals
// carry the ids in their message and unwrap to these sentinels; the other two
// are the fixed copy itself. The caller appends "Nothing was saved."
var (
	// ErrVoidUnknown refuses a void naming no known contact event.
	ErrVoidUnknown = errors.New("no such contact event")
	// ErrVoidAlreadyVoided refuses a second void of the same event.
	ErrVoidAlreadyVoided = errors.New("contact event already voided")
	// ErrVoidOfVoid refuses voiding a void.
	ErrVoidOfVoid = errors.New("a void can't itself be voided")
	// ErrVoidCadence refuses voiding a cadence event: the cadence verbs correct
	// it instead.
	ErrVoidCadence = errors.New("a cadence event is never voided — change it with lucid contact every, pause, resume or retire")
)

// refusal is a user-facing refusal message that still matches its sentinel
// under errors.Is.
type refusal struct {
	kind error
	msg  string
}

func (r *refusal) Error() string { return r.msg }
func (r *refusal) Unwrap() error { return r.kind }

// ValidateVoid reports whether targetID may be voided given every recorded
// event (ADR-0014 §5): it must name a known contact or snooze that no earlier
// void already corrects. Malformed events are ignored, so a damaged line reads
// as unknown.
func ValidateVoid(events []Event, targetID string) error {
	valid := validEvents(events)
	var target *Event
	for i := range valid {
		if valid[i].ID != "" && valid[i].ID == targetID {
			target = &valid[i]
			break
		}
	}
	if target == nil {
		return &refusal{kind: ErrVoidUnknown, msg: fmt.Sprintf("no contact event %q", targetID)}
	}
	switch target.Type {
	case TypeVoid:
		return ErrVoidOfVoid
	case TypeCadence:
		return ErrVoidCadence
	}
	if by, ok := voidedBy(valid)[targetID]; ok {
		return &refusal{kind: ErrVoidAlreadyVoided, msg: fmt.Sprintf("%q is already voided (by %q)", targetID, by)}
	}
	return nil
}

// validEvents returns the events that pass [Event.Validate], in input order.
func validEvents(events []Event) []Event {
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if e.Validate() == nil {
			out = append(out, e)
		}
	}
	return out
}

// voidedBy maps each voided contact or snooze id to the id of the earliest
// void that corrects it. A void naming a cadence event, a void, or an unknown
// id has no effect.
func voidedBy(valid []Event) map[string]string {
	types := make(map[string]string, len(valid))
	var voids []Event
	for _, e := range valid {
		if e.ID != "" {
			types[e.ID] = e.Type
		}
		if e.Type == TypeVoid {
			voids = append(voids, e)
		}
	}
	slices.SortStableFunc(voids, compareEvents)
	out := make(map[string]string, len(voids))
	for _, v := range voids {
		target, _ := v.Voids()
		if t := types[target]; t != TypeContact && t != TypeSnooze {
			continue
		}
		if _, done := out[target]; !done {
			out[target] = v.ID
		}
	}
	return out
}

// compareEvents orders events by recorded_at as an instant (so a DST offset
// change cannot reorder them), then by id. Validated events always parse.
func compareEvents(a, b Event) int {
	ta, errA := time.Parse(time.RFC3339, a.RecordedAt)
	tb, errB := time.Parse(time.RFC3339, b.RecordedAt)
	if errA == nil && errB == nil {
		if c := ta.Compare(tb); c != 0 {
			return c
		}
	} else if c := strings.Compare(a.RecordedAt, b.RecordedAt); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// civilDate parses a YYYY-MM-DD logical day as a UTC civil date, so day
// arithmetic never meets a DST transition.
func civilDate(day string) (time.Time, bool) {
	t, err := time.Parse(dateLayout, day)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// addDays returns day plus n civil days, or "" for an unreadable day.
func addDays(day string, n int) string {
	t, ok := civilDate(day)
	if !ok {
		return ""
	}
	return t.AddDate(0, 0, n).Format(dateLayout)
}

// weekOf returns the ISO week label (YYYY-Www) of a logical day.
func weekOf(day string) (string, bool) {
	t, ok := civilDate(day)
	if !ok {
		return "", false
	}
	return isoweek.Label(t), true
}
