package contact

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// Schema is the event schema version (ADR-0014 §2). Versioned per record family
// exactly as the rep and focus envelopes are: new needs go in a new optional
// field under a bumped schema, and readers tolerate an unknown field.
const Schema = 1

// SourceContact is the provenance every contact event carries: written by a
// `lucid contact` verb.
const SourceContact = "contact"

// IDPrefix is the event-id namespace: contact_YYYY_MM_DD_<slot>.
const IDPrefix = "contact_"

// Event types (ADR-0014 §2).
const (
	// TypeCadence is a cadence lifecycle change: set (or change) a window,
	// pause, resume, or retire.
	TypeCadence = "cadence"
	// TypeContact records that the user and the person were in touch on a day.
	TypeContact = "contact"
	// TypeSnooze keeps the person off the due lists for one ISO week.
	TypeSnooze = "snooze"
	// TypeVoid corrects a mistaken contact or snooze by reference.
	TypeVoid = "void"
)

// Cadence actions (ADR-0014 §3). A `set` carries the window; the others carry
// nothing beyond the envelope.
const (
	ActionSet    = "set"
	ActionPause  = "pause"
	ActionResume = "resume"
	ActionRetire = "retire"
)

// Via values (ADR-0014 §4): how the user and the person were in touch. Optional,
// and a closed set — anything else is refused.
const (
	ViaCall     = "call"
	ViaText     = "text"
	ViaVideo    = "video"
	ViaInPerson = "in-person"
)

// ViaValues returns the closed --via set in its documented order. It returns a
// fresh slice, so callers may not alter the set.
func ViaValues() []string {
	return []string{ViaCall, ViaText, ViaVideo, ViaInPerson}
}

// ValidVia reports whether v is empty (unset) or one of [ViaValues].
func ValidVia(v string) bool {
	if v == "" {
		return true
	}
	for _, ok := range ViaValues() {
		if v == ok {
			return true
		}
	}
	return false
}

// Fixed input refusals (error-states.md Ct-4, Ct-5). The caller appends
// "Nothing was saved."
var (
	// ErrInvalidVia is a --via value outside the closed set.
	ErrInvalidVia = errors.New("--via is one of call, text, video or in-person")
	// ErrNoteNotOneLine is a contact note that spans more than one line.
	ErrNoteNotOneLine = errors.New("a contact note is one line")
)

// dateLayout is the logical_date string form (YYYY-MM-DD, local civil date),
// matching the rep, focus and observation layers' join-key form.
const dateLayout = "2006-01-02"

// Refs carries an event's references. Only a void uses it: Voids names the
// contact or snooze the void corrects.
type Refs struct {
	Voids string `json:"voids,omitempty"`
}

// Event is one contact event, appended verbatim as one JSONL line to its
// per-logical-day shard (ADR-0014 §2). Field order matches the documented
// schema so a marshaled event reads like the spec. Type-specific fields are
// omitted when empty, except that a contact always carries via and note ("" when
// unset), so an optional field never changes a contact's shape. There is no
// direction, initiator, content, score or streak field.
type Event struct {
	ID          string `json:"id"`
	Schema      int    `json:"schema"`
	Type        string `json:"type"`
	PersonKey   string `json:"person_key"`
	LogicalDate string `json:"logical_date"`
	RecordedAt  string `json:"recorded_at"`
	Source      string `json:"source"`
	Action      string `json:"action,omitempty"`
	Window      string `json:"window,omitempty"`
	WindowDays  int    `json:"window_days,omitempty"`
	Via         string `json:"via,omitempty"`
	Note        string `json:"note,omitempty"`
	Refs        *Refs  `json:"refs,omitempty"`
}

// NewCadence builds a cadence event for personKey. A set carries the window
// (as typed, plus its length in days); pause, resume and retire carry none.
// The storage adapter assigns the id.
func NewCadence(personKey, logicalDate, recordedAt, action string, w Window) Event {
	e := envelope(TypeCadence, personKey, logicalDate, recordedAt)
	e.Action = action
	if action == ActionSet {
		e.Window = w.Text
		e.WindowDays = w.Days
	}
	return e
}

// NewContact builds a contact event: the person and the logical day, with an
// optional via and an optional one-line note. Nothing else is asked.
func NewContact(personKey, logicalDate, recordedAt, via, note string) Event {
	e := envelope(TypeContact, personKey, logicalDate, recordedAt)
	e.Via = via
	e.Note = note
	return e
}

// NewSnooze builds a snooze event; it covers the ISO week of logicalDate.
func NewSnooze(personKey, logicalDate, recordedAt string) Event {
	return envelope(TypeSnooze, personKey, logicalDate, recordedAt)
}

// NewVoid builds a void event that corrects targetID, a contact or snooze
// belonging to personKey.
func NewVoid(targetID, personKey, logicalDate, recordedAt string) Event {
	e := envelope(TypeVoid, personKey, logicalDate, recordedAt)
	e.Refs = &Refs{Voids: targetID}
	return e
}

// envelope fills the fields every event carries.
func envelope(typ, personKey, logicalDate, recordedAt string) Event {
	return Event{
		Schema:      Schema,
		Type:        typ,
		PersonKey:   personKey,
		LogicalDate: logicalDate,
		RecordedAt:  recordedAt,
		Source:      SourceContact,
	}
}

// plainEvent is Event without its methods, so MarshalJSON can delegate to the
// default encoder without recursing.
type plainEvent Event

// contactWire is the on-disk shape of a contact: the same field order as Event,
// with via and note always emitted.
type contactWire struct {
	ID          string `json:"id"`
	Schema      int    `json:"schema"`
	Type        string `json:"type"`
	PersonKey   string `json:"person_key"`
	LogicalDate string `json:"logical_date"`
	RecordedAt  string `json:"recorded_at"`
	Source      string `json:"source"`
	Action      string `json:"action,omitempty"`
	Window      string `json:"window,omitempty"`
	WindowDays  int    `json:"window_days,omitempty"`
	Via         string `json:"via"`
	Note        string `json:"note"`
	Refs        *Refs  `json:"refs,omitempty"`
}

// MarshalJSON renders the event in its documented per-type shape, so a receipt
// that embeds an event (`--json`) and the stored line always agree.
func (e Event) MarshalJSON() ([]byte, error) {
	if e.Type != TypeContact {
		return json.Marshal(plainEvent(e))
	}
	return json.Marshal(contactWire(e))
}

// MarshalLine renders the event as exactly one compact JSON line (no trailing
// newline). The storage adapter adds the terminator and fsyncs. The same event
// always marshals to the same bytes.
func (e Event) MarshalLine() ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("contact: marshal event: %w", err)
	}
	return b, nil
}

// UnmarshalEventLine parses one JSONL line into an Event. A malformed line is a
// decode error the reader turns into a skip, never a crash.
func UnmarshalEventLine(line []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return Event{}, fmt.Errorf("contact: parse event line: %w", err)
	}
	return e, nil
}

// Voids returns the id this event voids and whether it names one.
func (e Event) Voids() (string, bool) {
	if e.Refs == nil || e.Refs.Voids == "" {
		return "", false
	}
	return e.Refs.Voids, true
}

// Validate reports the first structural problem with an event before it is
// appended, and lets the fold skip a hand-damaged line. The id is optional (the
// storage adapter assigns it), but when present it must be a well-formed
// contact id for the event's own logical date.
func (e Event) Validate() error {
	if e.Schema != Schema {
		return fmt.Errorf("contact: unsupported schema %d (want %d)", e.Schema, Schema)
	}
	switch e.Type {
	case TypeCadence, TypeContact, TypeSnooze, TypeVoid:
	default:
		return fmt.Errorf("contact: unknown event type %q", e.Type)
	}
	if strings.TrimSpace(e.PersonKey) == "" {
		return errors.New("contact: person_key is required")
	}
	if _, err := time.Parse(dateLayout, e.LogicalDate); err != nil {
		return fmt.Errorf("contact: logical_date must be YYYY-MM-DD, got %q", e.LogicalDate)
	}
	if _, err := time.Parse(time.RFC3339, e.RecordedAt); err != nil {
		return fmt.Errorf("contact: recorded_at must be RFC3339, got %q", e.RecordedAt)
	}
	if e.Source == "" {
		return errors.New("contact: source is required")
	}
	if e.ID != "" {
		if d, ok := Date(e.ID); !ok || d != e.LogicalDate {
			return fmt.Errorf("contact: id %q is not a contact id for %s", e.ID, e.LogicalDate)
		}
	}
	switch e.Type {
	case TypeCadence:
		return e.validateCadence()
	case TypeContact:
		if !ValidVia(e.Via) {
			return ErrInvalidVia
		}
		if strings.ContainsAny(e.Note, "\r\n") {
			return ErrNoteNotOneLine
		}
	case TypeVoid:
		target, ok := e.Voids()
		if !ok {
			return errors.New("contact: a void needs refs.voids naming the event it corrects")
		}
		if _, ok := Date(target); !ok {
			return fmt.Errorf("contact: refs.voids %q is not a contact event id", target)
		}
	}
	return nil
}

// validateCadence checks a cadence event's action, and a set's window.
func (e Event) validateCadence() error {
	switch e.Action {
	case ActionSet:
		w, err := ParseWindow(e.Window)
		if err != nil {
			return fmt.Errorf("contact: cadence window %q: %w", e.Window, err)
		}
		if w.Days != e.WindowDays {
			return fmt.Errorf("contact: cadence window %q is %d days, not %d", e.Window, w.Days, e.WindowDays)
		}
	case ActionPause, ActionResume, ActionRetire:
	default:
		return fmt.Errorf("contact: unknown cadence action %q", e.Action)
	}
	return nil
}

// ID renders the event id for a logical date and a zero-based per-day slot:
// contact_YYYY_MM_DD_<slot>, the slot in the anchor-event grammar
// ([engine.SlotLabel]: a, b, … z, aa, …). A backdated event's id encodes its
// logical date, not the recording time. A negative slot is treated as 0.
func ID(logicalDate string, slot int) string {
	if slot < 0 {
		slot = 0
	}
	return IDPrefix + strings.ReplaceAll(logicalDate, "-", "_") + "_" + engine.SlotLabel(slot)
}

// Date extracts the YYYY-MM-DD logical date an event id encodes — the read-side
// inverse of [ID]. It reports ok=false for anything that is not a well-formed
// contact id: a missing prefix, a date that is not a real civil date, or a slot
// that is not one or more lowercase letters. The date is returned in dashed form
// so it feeds the day-shard readers directly.
func Date(id string) (date string, ok bool) {
	rest, cut := strings.CutPrefix(id, IDPrefix)
	if !cut {
		return "", false
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 4 || parts[3] == "" {
		return "", false
	}
	for _, r := range parts[3] {
		if r < 'a' || r > 'z' {
			return "", false
		}
	}
	d := parts[0] + "-" + parts[1] + "-" + parts[2]
	if t, err := time.Parse(dateLayout, d); err != nil || t.Format(dateLayout) != d {
		return "", false
	}
	return d, true
}

// LogicalDay returns the logical-day key for an instant — the 04:00-rollover
// civil day the rep, focus, observation and Engine surfaces already use, so
// "today" means the same boundary everywhere in the Ledger.
func LogicalDay(now time.Time) string {
	return observations.DateString(observations.LogicalBaseDate(now, observations.DefaultRolloverMin))
}
