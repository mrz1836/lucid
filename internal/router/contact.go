package router

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/contact"
	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/storage"
)

// ErrContactRejected classifies a contact write refused for its input outside
// the grammar the core owns — a blank void id, an unknown cadence action. The
// grammar refusals (window, --via, note) carry the core's own sentinels.
var ErrContactRejected = errors.New("contact write rejected")

// nothingSaved closes every contact refusal (error-states.md Ct-3..Ct-11).
const nothingSaved = " Nothing was saved."

// ContactRejectedError is a deterministic contact refusal (error-states.md
// Ct-1..Ct-11, Ct-6 aside): a fixed, model-free Reason that is already a
// complete user-facing sentence ending with the nothing-was-saved clause, and
// nothing appended. Cause classifies it — a contact core sentinel
// ([contact.ErrInvalidWindow], [contact.ErrNotActive], …), a subject
// resolution failure (a [*PersonRejectedError], so errors.Is still matches
// [ErrPersonUnknownSubject] and [ErrPersonAmbiguousSubject]), or
// [ErrContactRejected] — so a caller matches the class with errors.Is while the
// CLI prints Reason verbatim. It mirrors [AnchorRejectedError]. A `--day`
// refusal is the shared [DayRejectedError] instead.
type ContactRejectedError struct {
	Reason string
	Cause  error
}

// Error returns the fixed reason, a complete user-facing sentence.
func (e *ContactRejectedError) Error() string { return e.Reason }

// Unwrap exposes the classifying cause to errors.Is and errors.As.
func (e *ContactRejectedError) Unwrap() error { return e.Cause }

// rejectContact builds a [ContactRejectedError] from a cause and its reason.
func rejectContact(cause error, reason string) error {
	return &ContactRejectedError{Reason: reason, Cause: cause}
}

// ContactLogRequest is one `lucid contact <subject>` turn (ADR-0014 §4): the
// raw subject, an optional --via from the closed set, an optional one-line
// contact note, the strict-tier --day value, and now (injected so backdating
// and ids are deterministic in tests).
type ContactLogRequest struct {
	Subject string
	Via     string
	Note    string
	DayArg  string
	Now     time.Time
}

// ContactCadenceRequest is one cadence write (ADR-0014 §3): Action is
// [contact.ActionSet] (`every`, which carries Window), [contact.ActionPause],
// [contact.ActionResume] or [contact.ActionRetire]. A cadence write stamps the
// current logical day; it takes no --day.
type ContactCadenceRequest struct {
	Subject string
	Action  string
	Window  string
	Now     time.Time
}

// ContactSnoozeRequest is one `lucid contact snooze <subject>` turn (ADR-0014
// §5). It covers the ISO week of the current logical day; it takes no --day.
type ContactSnoozeRequest struct {
	Subject string
	Now     time.Time
}

// ContactVoidRequest is one `lucid contact void <id>` turn (ADR-0014 §5): the
// receipt id of the contact or snooze to correct.
type ContactVoidRequest struct {
	ID  string
	Now time.Time
}

// ContactWriteResult reports every contact write: the appended event (its id
// is the receipt), the canonical person it belongs to, their display name, and
// the inventory-only ack — what was recorded, never an evaluation.
type ContactWriteResult struct {
	Event       contact.Event
	PersonKey   string
	DisplayName string
	Ack         string
}

// ContactLog records that the user and a person were in touch on a logical
// day (ADR-0014 §4). It is capture tier: deterministic, agent-free and
// immediate. Contact either way counts and nothing about the conversation is
// asked. A --via outside the closed set or a note of more than one line is
// refused (Ct-4, Ct-5); a backdating --day runs on the strict tier, so an
// unreadable token or a future day is a [DayRejectedError] (Ct-6); and an
// unknown subject is refused with a pointer to `lucid person create` — logging
// contact never creates a person (Ct-1). A refusal writes nothing.
// `recorded_at` is the real write time; only `logical_date` follows --day.
// Contact with someone who has no cadence is recorded all the same: it is
// history, with no due effect.
func (r *Router) ContactLog(req ContactLogRequest) (ContactWriteResult, error) {
	via := strings.TrimSpace(req.Via)
	if !contact.ValidVia(via) {
		return ContactWriteResult{}, rejectContact(contact.ErrInvalidVia, contact.ErrInvalidVia.Error()+"."+nothingSaved)
	}
	note := strings.TrimSpace(req.Note)
	if strings.ContainsAny(note, "\r\n") {
		return ContactWriteResult{}, rejectContact(contact.ErrNoteNotOneLine, contact.ErrNoteNotOneLine.Error()+"."+nothingSaved)
	}
	now := whenOr(req.Now)
	// Strict-tier --day: the one shared capture grammar, so the accepted forms,
	// the 04:00 rollover and the future ceiling match every other dated capture.
	when, err := resolveCaptureWhen(req.DayArg, now)
	if err != nil {
		return ContactWriteResult{}, err
	}
	if err = r.prepareContacts(); err != nil {
		return ContactWriteResult{}, err
	}
	rec, err := r.contactSubject(req.Subject)
	if err != nil {
		return ContactWriteResult{}, err
	}
	stored, err := r.store.AppendContactEvent(
		contact.NewContact(rec.PersonKey, when.LogicalDate, now.Format(time.RFC3339), via, note),
	)
	if err != nil {
		return ContactWriteResult{}, fmt.Errorf("could not log the contact; nothing was saved: %w", err)
	}
	return contactResult(stored, rec, contact.PersonState{}, contact.LogicalDay(now)), nil
}

// ContactCadence sets, changes, pauses, resumes or retires a person's check-in
// cadence (ADR-0014 §3) by appending one cadence event — history is never
// rewritten. A window outside the Nd / Nw grammar is refused with the grammar
// (Ct-3); a transition from the wrong state — pausing a cadence that is not
// active, resuming one that is not paused, retiring with no live cadence — is
// refused naming the person and the state (Ct-7..Ct-9). A refusal writes
// nothing. The ack names the window and, for a live cadence, when it next
// closes; where the window starts follows the window-start rules the fold
// owns ([contact.Fold]).
func (r *Router) ContactCadence(req ContactCadenceRequest) (ContactWriteResult, error) {
	var w contact.Window
	switch req.Action {
	case contact.ActionSet:
		parsed, err := contact.ParseWindow(req.Window)
		if err != nil {
			return ContactWriteResult{}, rejectContact(err, err.Error()+"."+nothingSaved)
		}
		w = parsed
	case contact.ActionPause, contact.ActionResume, contact.ActionRetire:
	default:
		return ContactWriteResult{}, rejectContact(ErrContactRejected,
			fmt.Sprintf("%q is not a cadence change — use every, pause, resume or retire.%s", req.Action, nothingSaved))
	}
	if err := r.prepareContacts(); err != nil {
		return ContactWriteResult{}, err
	}
	rec, err := r.contactSubject(req.Subject)
	if err != nil {
		return ContactWriteResult{}, err
	}
	events, states, err := r.foldContacts()
	if err != nil {
		return ContactWriteResult{}, err
	}
	if err = contact.ValidateTransition(states[rec.PersonKey], req.Action); err != nil {
		var refused *contact.TransitionError
		if errors.As(err, &refused) {
			return ContactWriteResult{}, rejectContact(err, refused.Message(rec.DisplayName)+nothingSaved)
		}
		return ContactWriteResult{}, err
	}
	now := whenOr(req.Now)
	today := contact.LogicalDay(now)
	stored, err := r.store.AppendContactEvent(
		contact.NewCadence(rec.PersonKey, today, now.Format(time.RFC3339), req.Action, w),
	)
	if err != nil {
		return ContactWriteResult{}, fmt.Errorf("could not change the check-in cadence; nothing was saved: %w", err)
	}
	after := contact.Fold(append(events, stored))[rec.PersonKey]
	return contactResult(stored, rec, after, today), nil
}

// ContactSnooze keeps a person off the due lists for the ISO week of the
// current logical day (ADR-0014 §5), leaving their window and anchor
// untouched. It needs an active cadence and is refused a second time in the
// same week, naming the snooze already recorded (Ct-10). A refusal writes
// nothing. It is capture tier, like the bare contact form.
func (r *Router) ContactSnooze(req ContactSnoozeRequest) (ContactWriteResult, error) {
	if err := r.prepareContacts(); err != nil {
		return ContactWriteResult{}, err
	}
	rec, err := r.contactSubject(req.Subject)
	if err != nil {
		return ContactWriteResult{}, err
	}
	events, states, err := r.foldContacts()
	if err != nil {
		return ContactWriteResult{}, err
	}
	now := whenOr(req.Now)
	today := contact.LogicalDay(now)
	if err = contact.ValidateSnooze(states[rec.PersonKey], today); err != nil {
		var refused *contact.SnoozeError
		if errors.As(err, &refused) {
			if refused.Already {
				refused.ID = snoozeInWeek(events, rec.PersonKey, today)
			}
			return ContactWriteResult{}, rejectContact(err, refused.Message(rec.DisplayName)+nothingSaved)
		}
		return ContactWriteResult{}, err
	}
	stored, err := r.store.AppendContactEvent(
		contact.NewSnooze(rec.PersonKey, today, now.Format(time.RFC3339)),
	)
	if err != nil {
		return ContactWriteResult{}, fmt.Errorf("could not snooze; nothing was saved: %w", err)
	}
	after := contact.Fold(append(events, stored))[rec.PersonKey]
	return contactResult(stored, rec, after, today), nil
}

// ContactVoid corrects a mistaken contact or snooze by its receipt id
// (ADR-0014 §5): it appends a void that names the target, so the target stops
// counting — a voided contact no longer moves the window, a voided snooze no
// longer hides the person — while both records stay in the history. An
// unknown id, an already-voided event, a void and a cadence event are refused
// (Ct-11); a cadence is corrected with the cadence verbs, never voided. A
// refusal writes nothing. The void stamps the current logical day and belongs
// to the target's person, followed through any merge since.
func (r *Router) ContactVoid(req ContactVoidRequest) (ContactWriteResult, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return ContactWriteResult{}, rejectContact(ErrContactRejected,
			"a contact event id is required to void — the receipt id a contact or snooze returned."+nothingSaved)
	}
	if err := r.prepareContacts(); err != nil {
		return ContactWriteResult{}, err
	}
	idx, err := r.contactIndex()
	if err != nil {
		return ContactWriteResult{}, err
	}
	events, err := r.readContactEvents(idx)
	if err != nil {
		return ContactWriteResult{}, err
	}
	if err = contact.ValidateVoid(events, id); err != nil {
		return ContactWriteResult{}, rejectContact(err, err.Error()+"."+nothingSaved)
	}
	var target contact.Event
	for _, e := range events {
		if e.ID == id {
			target = e
			break
		}
	}
	rec := storage.PersonRecord{PersonKey: target.PersonKey, DisplayName: idx.name(target.PersonKey)}
	now := whenOr(req.Now)
	today := contact.LogicalDay(now)
	stored, err := r.store.AppendContactEvent(
		contact.NewVoid(id, rec.PersonKey, today, now.Format(time.RFC3339)),
	)
	if err != nil {
		return ContactWriteResult{}, fmt.Errorf("could not void %s; nothing was saved: %w", id, err)
	}
	after := contact.Fold(append(events, stored))[rec.PersonKey]
	return contactResult(stored, rec, after, today), nil
}

// ContactDue builds the due view for the logical day of now (ADR-0014 §6): the
// three groups over the ISO Monday–Sunday week, plus the week's birthdays, or
// under wrap the Sunday wrap's two lists alone (§9). It is computed fresh on
// every call — there is no stored snapshot — and it is the one read the CLI's
// `contact due` and the weekly node share. Events fold through merge
// redirects, only live canonical records are grouped, and off-limits people
// appear nowhere in it. It never calls a model and appends nothing; the
// idempotent contacts/ scaffold may run.
func (r *Router) ContactDue(now time.Time, wrap bool) (contact.DueView, error) {
	if err := r.prepareContacts(); err != nil {
		return contact.DueView{}, err
	}
	idx, err := r.contactIndex()
	if err != nil {
		return contact.DueView{}, err
	}
	events, err := r.readContactEvents(idx)
	if err != nil {
		return contact.DueView{}, err
	}
	return contact.Due(contact.Fold(events), idx.info, contact.LogicalDay(whenOr(now)), wrap), nil
}

// PersonContact is the check-in summary a person view carries (ADR-0014 §6):
// the cadence State ([contact.StateActive], [contact.StatePaused],
// [contact.StateRetired], or [contact.StateNone] for contact logged with no
// cadence), the most recent Window set (zero when none ever was), the last
// non-voided contact day ("" when none counts), and the rendered Line.
type PersonContact struct {
	State       string
	Window      contact.Window
	LastContact string
	Line        string
}

// personContact folds the contact stream for one canonical person and returns
// their check-in summary, or nil when there is nothing to show — no contact
// events, or none that still counts — so such a person's view renders exactly
// as before (S-22). It is a read: it neither scaffolds nor appends.
func (r *Router) personContact(personKey string) (*PersonContact, error) {
	_, states, err := r.foldContacts()
	if err != nil {
		return nil, fmt.Errorf("person: read check-ins: %w", err)
	}
	st, ok := states[personKey]
	if !ok {
		return nil, nil //nolint:nilnil // no contact events: the view adds nothing
	}
	line, ok := contact.RenderPersonLine(st)
	if !ok {
		return nil, nil //nolint:nilnil // nothing that still counts: the view adds nothing
	}
	pc := &PersonContact{State: st.State, Window: st.Window, Line: line}
	if st.LastContact != nil {
		pc.LastContact = st.LastContact.Day
	}
	return pc, nil
}

// contactResult assembles a write's result and its ack.
func contactResult(stored contact.Event, rec storage.PersonRecord, after contact.PersonState, today string) ContactWriteResult {
	return ContactWriteResult{
		Event:       stored,
		PersonKey:   rec.PersonKey,
		DisplayName: rec.DisplayName,
		Ack:         contact.RenderAck(stored, rec.DisplayName, after, today),
	}
}

// contactSubject resolves a contact verb's subject through the shared person
// resolver (aliases, merge redirects, §P-4 / §P-5), so the verbs never grow
// their own lookup. An unknown subject is re-worded to carry the remedy —
// logging contact never creates a person (Ct-1); an ambiguous one keeps the
// resolver's copy naming the candidate keys (Ct-2). Either way the refusal is
// a [ContactRejectedError] that still matches the person sentinels.
func (r *Router) contactSubject(raw string) (storage.PersonRecord, error) {
	rec, err := r.resolvePersonSubject(raw)
	if err == nil {
		return rec, nil
	}
	var refused *PersonRejectedError
	if !errors.As(err, &refused) {
		return storage.PersonRecord{}, err
	}
	reason := refused.Reason
	if errors.Is(err, ErrPersonUnknownSubject) {
		reason = fmt.Sprintf("no one matches %q — add them first with lucid person create <name>; nothing was saved.",
			strings.TrimSpace(raw))
	}
	return storage.PersonRecord{}, rejectContact(err, reason)
}

// prepareContacts scaffolds the contacts tree idempotently, wrapping any
// failure with the shared message the contact verbs report (mirrors
// [Router.prepareReps]).
func (r *Router) prepareContacts() error {
	if err := r.store.ScaffoldContacts(); err != nil {
		return fmt.Errorf("could not prepare the contacts tree: %w", err)
	}
	return nil
}

// contactPeople is one read of the people tree, as the contact verbs need it:
// the canonical key for every key on disk, and what the due view needs to
// know about each live canonical person.
type contactPeople struct {
	canon map[string]string
	info  map[string]contact.PersonInfo
}

// canonical returns the canonical person key for key: a tombstone resolves to
// its target, and a live key — or one with no record at all — to itself
// (the [storage.Adapter.ResolvePersonRedirect] rule).
func (p contactPeople) canonical(key string) string {
	if c, ok := p.canon[key]; ok {
		return c
	}
	return key
}

// name returns a live person's display name, or the key itself when no live
// record holds it.
func (p contactPeople) name(key string) string {
	if info, ok := p.info[key]; ok && info.DisplayName != "" {
		return info.DisplayName
	}
	return key
}

// contactIndex reads the people tree once: every key's canonical key, the
// display name and date of birth of each live canonical record, and the
// off-limits registry canonicalized onto them — a person is off-limits when
// their own key or any key merged into them is, so a merge never un-redacts
// anyone. Redirects are single-hop: a merge flattens every chain.
func (r *Router) contactIndex() (contactPeople, error) {
	keys, err := r.store.ListPeopleKeys()
	if err != nil {
		return contactPeople{}, fmt.Errorf("could not read the people: %w", err)
	}
	idx := contactPeople{
		canon: make(map[string]string, len(keys)),
		info:  make(map[string]contact.PersonInfo, len(keys)),
	}
	for _, key := range keys {
		rec, found, rerr := r.store.ReadPerson(key)
		if rerr != nil {
			return contactPeople{}, fmt.Errorf("could not read the person %q: %w", key, rerr)
		}
		if !found {
			continue
		}
		if rec.IsTombstone() {
			idx.canon[key] = rec.RedirectTo
			continue
		}
		idx.canon[key] = key
		info := contact.PersonInfo{Key: key, DisplayName: rec.DisplayName}
		if rec.Dob != nil {
			info.Dob = *rec.Dob
		}
		idx.info[key] = info
	}
	offLimits, err := r.store.ReadOffLimitsPersonKeys()
	if err != nil {
		return contactPeople{}, fmt.Errorf("could not read the off-limits registry: %w", err)
	}
	for _, key := range offLimits {
		canon := idx.canonical(key)
		if info, ok := idx.info[canon]; ok {
			info.OffLimits = true
			idx.info[canon] = info
		}
	}
	return idx, nil
}

// readContactEvents reads the whole contact stream with every person key
// mapped to its canonical key, so history recorded before a merge belongs to
// the merged person (ADR-0014 §2).
func (r *Router) readContactEvents(idx contactPeople) ([]contact.Event, error) {
	events, err := r.store.ReadContactEvents()
	if err != nil {
		return nil, fmt.Errorf("could not read the contact events: %w", err)
	}
	for i := range events {
		events[i].PersonKey = idx.canonical(events[i].PersonKey)
	}
	return events, nil
}

// foldContacts reads the people and the contact stream, canonicalizes every
// event's person key, and folds the result ([contact.Fold]). It returns the
// canonical events too, so a write can fold its own new event in without a
// second read.
func (r *Router) foldContacts() ([]contact.Event, map[string]contact.PersonState, error) {
	idx, err := r.contactIndex()
	if err != nil {
		return nil, nil, err
	}
	events, err := r.readContactEvents(idx)
	if err != nil {
		return nil, nil, err
	}
	return events, contact.Fold(events), nil
}

// snoozeInWeek returns the id of the person's first snooze that falls in the
// ISO week of day and that no void corrects, or "" when there is none — the
// receipt a second snooze's refusal points at.
func snoozeInWeek(events []contact.Event, personKey, day string) string {
	week, ok := isoWeekOf(day)
	if !ok {
		return ""
	}
	voided := make(map[string]bool)
	for _, e := range events {
		if target, ok := e.Voids(); ok && e.Type == contact.TypeVoid {
			voided[target] = true
		}
	}
	for _, e := range events {
		if e.Type != contact.TypeSnooze || e.PersonKey != personKey || voided[e.ID] {
			continue
		}
		if w, ok := isoWeekOf(e.LogicalDate); ok && w == week {
			return e.ID
		}
	}
	return ""
}

// isoWeekOf returns the ISO week label (YYYY-Www) of a YYYY-MM-DD logical day.
func isoWeekOf(day string) (string, bool) {
	t, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return "", false
	}
	return isoweek.Label(t), true
}
