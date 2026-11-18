package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/keyderive"
)

// Schema is the record-family tag every identity event carries, so a future
// format change is distinguishable on disk (data-model.md §"Identity log").
const Schema = "identity.v1"

// IDPrefix is the event-id namespace: identity_YYYY_MM_DD_<slot>.
const IDPrefix = "identity_"

// Event kinds (data-model.md §"Identity log" §Schema).
const (
	// KindStatus sets a person's status: active or dormant.
	KindStatus = "status"
	// KindClaim makes a bare form resolve to its claimant.
	KindClaim = "claim"
	// KindRelease ends the claimant's claim on a form.
	KindRelease = "release"
	// KindMoveRef moves one raw entry's mention from one person to another.
	KindMoveRef = "move_ref"
	// KindHold records a mention capture could not assign to one person.
	KindHold = "hold"
	// KindPin settles one raw entry's mention of a form onto a person.
	KindPin = "pin"
)

// Statuses a status event sets. No event means active.
const (
	StatusActive  = "active"
	StatusDormant = "dormant"
)

// Hold reasons (the processed held_people[] reason, error-states.md §P-11).
const (
	// HoldExact: more than one active person holds the form exactly, or the
	// only active holder is reachable only through aka[] while the slug path
	// holds only dormant people.
	HoldExact = "exact"
	// HoldFirstName: only dormant people hold the form, and two or more
	// active people have it as their first name.
	HoldFirstName = "first_name"
)

// Pin causes: what settled a mention.
const (
	// CauseFirstName: capture routed the mention by first name.
	CauseFirstName = "first_name"
	// CauseClaim: a claim write re-resolved a held mention.
	CauseClaim = "claim"
	// CauseRelease: a release write re-resolved a held mention.
	CauseRelease = "release"
	// CauseDormant: a dormant write re-resolved a held mention.
	CauseDormant = "dormant"
	// CauseRestore: a restore write re-resolved a held mention.
	CauseRestore = "restore"
	// CauseMoveRef: a move-ref without --from pinned the raw's held mention.
	CauseMoveRef = "move_ref"
)

// Sources: the provenance every event carries.
const (
	// SourceCLI is a curation verb (dormant, claim, release, move-ref).
	SourceCLI = "cli"
	// SourceStructure is capture: a hold or a first-name pin.
	SourceStructure = "structure"
	// SourceMerge is person merge keeping the log valid.
	SourceMerge = "merge"
)

// dateLayout is the YYYY-MM-DD form an id's date encodes.
const dateLayout = "2006-01-02"

// Fixed input refusals. The caller appends "nothing was saved".
var (
	// ErrReasonNotOneLine is a dormant reason that spans more than one line.
	ErrReasonNotOneLine = errors.New("a dormant reason is one line")
	// ErrBlankForm is a claim, release, hold, or pin whose form normalizes to
	// nothing (no letter or digit).
	ErrBlankForm = errors.New("a name form needs at least one letter or digit")
)

// Event is one identity event, appended verbatim as one JSONL line to
// identity/identity.jsonl. Field order matches the documented schema so a
// marshaled event reads like the spec, and every field after source is
// omitted when unset — each kind carries only its own fields.
type Event struct {
	Schema     string   `json:"schema"`
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	At         string   `json:"at"`
	Source     string   `json:"source"`
	RawID      string   `json:"raw_id,omitempty"`
	Form       string   `json:"form,omitempty"`
	PersonKey  string   `json:"person_key,omitempty"`
	Status     string   `json:"status,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Cause      string   `json:"cause,omitempty"`
	Force      bool     `json:"force,omitempty"`
	Displaced  []string `json:"displaced,omitempty"`
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

// NewStatus builds a status event: personKey becomes active or dormant, with
// an optional one-line reason (dormant only, by the CLI's rule). The storage
// adapter assigns the id.
func NewStatus(at, source, personKey, status, reason string) Event {
	e := envelope(KindStatus, at, source)
	e.PersonKey = personKey
	e.Status = status
	e.Reason = reason
	return e
}

// NewClaim builds a claim event: form resolves to personKey. displaced names
// the person_keys a forced claim overrode; it is sorted here, and force is set
// whenever displaced is non-empty.
func NewClaim(at, source, form, personKey string, force bool, displaced []string) Event {
	e := envelope(KindClaim, at, source)
	e.Form = form
	e.PersonKey = personKey
	e.Displaced = sortedCopy(displaced)
	e.Force = force || len(e.Displaced) > 0
	return e
}

// NewRelease builds a release event: personKey's claim on form ends.
func NewRelease(at, source, form, personKey string) Event {
	e := envelope(KindRelease, at, source)
	e.Form = form
	e.PersonKey = personKey
	return e
}

// NewMoveRef builds a move-ref event: rawID's mention moves from one person to
// another on read.
func NewMoveRef(at, source, rawID, from, to string) Event {
	e := envelope(KindMoveRef, at, source)
	e.RawID = rawID
	e.From = from
	e.To = to
	return e
}

// NewHold builds a hold event: rawID's mention of form could mean any of
// candidates (sorted here) for reason (exact or first_name).
func NewHold(at, source, rawID, form, reason string, candidates []string) Event {
	e := envelope(KindHold, at, source)
	e.RawID = rawID
	e.Form = form
	e.Reason = reason
	e.Candidates = sortedCopy(candidates)
	return e
}

// NewPin builds a pin event: rawID's mention of form settles on personKey,
// for cause.
func NewPin(at, source, rawID, form, personKey, cause string) Event {
	e := envelope(KindPin, at, source)
	e.RawID = rawID
	e.Form = form
	e.PersonKey = personKey
	e.Cause = cause
	return e
}

// MarshalLine renders the event as exactly one compact JSON line (no trailing
// newline). The storage adapter adds the terminator and fsyncs. The same event
// always marshals to the same bytes.
func (e Event) MarshalLine() ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("identity: marshal event: %w", err)
	}
	return b, nil
}

// Validate reports the first structural problem with an event before it is
// appended, and lets the fold skip a hand-damaged line. The id is optional
// (the storage adapter assigns it), but when present it must be a well-formed
// identity id for the local date of the event's own at.
func (e Event) Validate() error {
	if e.Schema != Schema {
		return fmt.Errorf("identity: unsupported schema %q (want %q)", e.Schema, Schema)
	}
	at, err := time.Parse(time.RFC3339, e.At)
	if err != nil {
		return fmt.Errorf("identity: at must be RFC3339, got %q", e.At)
	}
	switch e.Source {
	case SourceCLI, SourceStructure, SourceMerge:
	default:
		return fmt.Errorf("identity: unknown source %q", e.Source)
	}
	if e.ID != "" {
		if d, ok := Date(e.ID); !ok || d != at.Format(dateLayout) {
			return fmt.Errorf("identity: id %q is not an identity id for %s", e.ID, at.Format(dateLayout))
		}
	}
	switch e.Kind {
	case KindStatus:
		return e.validateStatus()
	case KindClaim:
		return e.validateClaim()
	case KindRelease:
		return e.validateFormKey()
	case KindMoveRef:
		return e.validateMoveRef()
	case KindHold:
		return e.validateHold()
	case KindPin:
		return e.validatePin()
	default:
		return fmt.Errorf("identity: unknown event kind %q", e.Kind)
	}
}

// validateStatus checks a status event: a person, a known status, and a
// one-line reason.
func (e Event) validateStatus() error {
	if err := requireKey("person_key", e.PersonKey); err != nil {
		return err
	}
	switch e.Status {
	case StatusActive, StatusDormant:
	default:
		return fmt.Errorf("identity: unknown status %q", e.Status)
	}
	if strings.ContainsAny(e.Reason, "\r\n") {
		return ErrReasonNotOneLine
	}
	return nil
}

// validateClaim checks a claim event: the form and claimant, plus a sorted
// displaced list that only a forced claim carries.
func (e Event) validateClaim() error {
	if err := e.validateFormKey(); err != nil {
		return err
	}
	if len(e.Displaced) > 0 && !e.Force {
		return errors.New("identity: only a forced claim names displaced people")
	}
	return validKeyList("displaced", e.Displaced)
}

// validateFormKey checks the form and person_key a claim and a release share.
func (e Event) validateFormKey() error {
	if err := validForm(e.Form); err != nil {
		return err
	}
	return requireKey("person_key", e.PersonKey)
}

// validateMoveRef checks a move-ref: the raw entry and two different people.
func (e Event) validateMoveRef() error {
	if err := requireKey("raw_id", e.RawID); err != nil {
		return err
	}
	if err := requireKey("from", e.From); err != nil {
		return err
	}
	if err := requireKey("to", e.To); err != nil {
		return err
	}
	if e.From == e.To {
		return fmt.Errorf("identity: a move-ref moves between two people, not %q to itself", e.From)
	}
	return nil
}

// validateHold checks a hold: the raw entry, the form, a known reason, and a
// non-empty sorted candidate list.
func (e Event) validateHold() error {
	if err := requireKey("raw_id", e.RawID); err != nil {
		return err
	}
	if err := validForm(e.Form); err != nil {
		return err
	}
	switch e.Reason {
	case HoldExact, HoldFirstName:
	default:
		return fmt.Errorf("identity: unknown hold reason %q", e.Reason)
	}
	if len(e.Candidates) == 0 {
		return errors.New("identity: a hold names at least one candidate")
	}
	return validKeyList("candidates", e.Candidates)
}

// validatePin checks a pin: the raw entry, the form, the person, and a known
// cause.
func (e Event) validatePin() error {
	if err := requireKey("raw_id", e.RawID); err != nil {
		return err
	}
	if err := e.validateFormKey(); err != nil {
		return err
	}
	switch e.Cause {
	case CauseFirstName, CauseClaim, CauseRelease, CauseDormant, CauseRestore, CauseMoveRef:
	default:
		return fmt.Errorf("identity: unknown pin cause %q", e.Cause)
	}
	return nil
}

// UnmarshalEventLine parses one JSONL line into an Event. A malformed line is a
// decode error the reader turns into a skip, never a crash.
func UnmarshalEventLine(line []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return Event{}, fmt.Errorf("identity: parse event line: %w", err)
	}
	return e, nil
}

// ID renders the event id for a local date (YYYY-MM-DD) and a zero-based
// per-day slot: identity_YYYY_MM_DD_<slot>, the slot in the anchor-event
// grammar ([engine.SlotLabel]: a, b, … z, aa, …). A negative slot is treated
// as 0.
func ID(date string, slot int) string {
	if slot < 0 {
		slot = 0
	}
	return IDPrefix + strings.ReplaceAll(date, "-", "_") + "_" + engine.SlotLabel(slot)
}

// Date extracts the YYYY-MM-DD date an event id encodes — the read-side
// inverse of [ID]. It reports ok=false for anything that is not a well-formed
// identity id: a missing prefix, a date that is not a real civil date, or a
// slot that is not one or more lowercase letters.
func Date(id string) (date string, ok bool) {
	date, _, ok = splitID(id)
	return date, ok
}

// Slot returns the zero-based per-day slot an event id carries — the inverse
// of the [engine.SlotLabel] grammar (a→0, z→25, aa→26) — and ok=false for
// anything that is not a well-formed identity id.
func Slot(id string) (int, bool) {
	_, label, ok := splitID(id)
	if !ok {
		return 0, false
	}
	n := 0
	for _, r := range label {
		n = n*26 + int(r-'a') + 1
	}
	return n - 1, true
}

// splitID parses an identity id into its dashed date and its slot label.
func splitID(id string) (date, label string, ok bool) {
	rest, cut := strings.CutPrefix(id, IDPrefix)
	if !cut {
		return "", "", false
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 4 || parts[3] == "" {
		return "", "", false
	}
	for _, r := range parts[3] {
		if r < 'a' || r > 'z' {
			return "", "", false
		}
	}
	d := parts[0] + "-" + parts[1] + "-" + parts[2]
	if t, err := time.Parse(dateLayout, d); err != nil || t.Format(dateLayout) != d {
		return "", "", false
	}
	return d, parts[3], true
}

// NormalizeForm is the comparison form of a name: lowercase letters and digits
// only, the person_key normalization, so "Sam" and "sam." are one form.
func NormalizeForm(form string) string { return keyderive.Normalize(form) }

// envelope fills the fields every event carries.
func envelope(kind, at, source string) Event {
	return Event{Schema: Schema, Kind: kind, At: at, Source: source}
}

// requireKey refuses an empty or whitespace-carrying reference field.
func requireKey(field, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("identity: %s is required", field)
	}
	if strings.ContainsFunc(v, isSpace) {
		return fmt.Errorf("identity: %s %q has whitespace", field, v)
	}
	return nil
}

// validForm refuses a form with no letter or digit, or one that spans lines.
func validForm(form string) error {
	if NormalizeForm(form) == "" {
		return ErrBlankForm
	}
	if strings.ContainsAny(form, "\r\n") {
		return fmt.Errorf("identity: form %q spans more than one line", form)
	}
	return nil
}

// validKeyList refuses a person_key list that is unsorted, repeats a key, or
// carries a blank one.
func validKeyList(field string, keys []string) error {
	for i, k := range keys {
		if err := requireKey(field, k); err != nil {
			return err
		}
		if i > 0 && keys[i-1] >= k {
			return fmt.Errorf("identity: %s must be sorted and unique", field)
		}
	}
	return nil
}

// sortedCopy returns a sorted, de-duplicated copy of keys, or nil when empty,
// so an omitted list never marshals as [].
func sortedCopy(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	out := slices.Clone(keys)
	slices.Sort(out)
	return slices.Compact(out)
}

// isSpace reports a whitespace rune.
func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}
