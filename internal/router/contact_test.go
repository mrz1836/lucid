package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/contact"
	"github.com/mrz1836/lucid/internal/storage"
)

// ctAt returns 10:00 on a 2026 civil day at a fixed -04:00 offset. The fixed
// offset keeps the logical day off the machine's zone, and 10:00 is well past
// the 04:00 rollover, so the logical day is the civil day. 2026-10-29 is a
// Thursday in ISO week 2026-W44 (Mon Oct 26 – Sun Nov 1).
func ctAt(month time.Month, day int) time.Time {
	return time.Date(2026, month, day, 10, 0, 0, 0, time.FixedZone("EDT", -4*3600))
}

// newContactRouter returns a booted router over a fresh Ledger and its home.
// Every person in these tests is synthetic.
func newContactRouter(t *testing.T) (*Router, *storage.Adapter, string) {
	t.Helper()
	return newBootedRouter(t)
}

// createPerson registers a synthetic person and returns their key.
func createPerson(t *testing.T, r *Router, name string) string {
	t.Helper()
	res, err := r.PersonCreate(PersonCreateRequest{Name: name, Now: ctAt(time.October, 1)})
	require.NoError(t, err)
	return res.Record.PersonKey
}

// contactTree returns every file under contacts/ keyed by its path relative
// to the tree, so a refusal can be proven to have written nothing.
func contactTree(t *testing.T, home string) map[string][]byte {
	t.Helper()
	root := filepath.Join(home, "contacts")
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = b
		return nil
	})
	require.NoError(t, err)
	return out
}

// requireTreeGrewOnly asserts every file in before is a byte prefix of the
// same file now — nothing earlier was rewritten.
func requireTreeGrewOnly(t *testing.T, before, after map[string][]byte) {
	t.Helper()
	for name, b := range before {
		require.Contains(t, after, name)
		require.True(t, bytes.HasPrefix(after[name], b), "%s: earlier lines stay byte-identical", name)
	}
}

// requireContactRefusal asserts err is a contact refusal of the given class
// with the exact copy.
func requireContactRefusal(t *testing.T, err, class error, reason string) {
	t.Helper()
	require.Error(t, err)
	require.ErrorIs(t, err, class)
	var refused *ContactRejectedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, reason, refused.Reason)
	assert.Equal(t, reason, err.Error())
}

// contactStates folds the live store the way the verbs do.
func contactStates(t *testing.T, r *Router) map[string]contact.PersonState {
	t.Helper()
	_, states, err := r.foldContacts()
	require.NoError(t, err)
	return states
}

// logContact records a contact and returns its stored event.
func logContact(t *testing.T, r *Router, req ContactLogRequest) contact.Event {
	t.Helper()
	res, err := r.ContactLog(req)
	require.NoError(t, err)
	return res.Event
}

// cadence applies a cadence change and returns its result.
func cadence(t *testing.T, r *Router, subject, action, window string, now time.Time) ContactWriteResult {
	t.Helper()
	res, err := r.ContactCadence(ContactCadenceRequest{Subject: subject, Action: action, Window: window, Now: now})
	require.NoError(t, err)
	return res
}

// TestContactLog_ReturnsReceiptAndPersists: a contact is one capture-tier
// append — the receipt id is minted for the logical day, the ack names the
// person, the day and the receipt, the note is trimmed, and the event reads
// back verbatim from its day shard.
func TestContactLog_ReturnsReceiptAndPersists(t *testing.T) {
	r, a, home := newContactRouter(t)
	key := createPerson(t, r, "Sam Rivera")
	now := ctAt(time.October, 29)

	res, err := r.ContactLog(ContactLogRequest{
		Subject: "Sam Rivera", Via: contact.ViaCall, Note: "  coffee after the game  ", Now: now,
	})
	require.NoError(t, err)
	assert.Equal(t, "contact_2026_10_29_a", res.Event.ID)
	assert.Equal(t, contact.TypeContact, res.Event.Type)
	assert.Equal(t, key, res.Event.PersonKey)
	assert.Equal(t, key, res.PersonKey)
	assert.Equal(t, "Sam Rivera", res.DisplayName)
	assert.Equal(t, "2026-10-29", res.Event.LogicalDate)
	assert.Equal(t, now.Format(time.RFC3339), res.Event.RecordedAt)
	assert.Equal(t, contact.ViaCall, res.Event.Via)
	assert.Equal(t, "coffee after the game", res.Event.Note)
	assert.Equal(t, "Logged contact with Sam Rivera on 2026-10-29 (contact_2026_10_29_a).", res.Ack)

	got, ok, err := a.ReadContactEventByID(res.Event.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, res.Event, got)

	// The subject resolves case-insensitively, and via and note are optional.
	plain := logContact(t, r, ContactLogRequest{Subject: "sam rivera", Now: now})
	assert.Equal(t, "contact_2026_10_29_b", plain.ID)
	assert.Empty(t, plain.Via)
	assert.Empty(t, plain.Note)

	shard := contactTree(t, home)[filepath.Join("2026", "10", "contact_2026_10_29.jsonl")]
	assert.Equal(t, 2, bytes.Count(shard, []byte("\n")), "one line per write")
	assert.Contains(t, string(shard), `"via":"","note":""`, "a contact always carries via and note")
}

// TestContactLog_ViaAndNoteValidated: --via is a closed set and a note is one
// line (Ct-4, Ct-5); a refusal writes nothing, and every accepted value lands.
func TestContactLog_ViaAndNoteValidated(t *testing.T) {
	r, _, home := newContactRouter(t)
	createPerson(t, r, "Sam Rivera")
	now := ctAt(time.October, 29)
	before := contactTree(t, home)

	for _, via := range []string{"email", "Call", "phone", "in person"} {
		_, err := r.ContactLog(ContactLogRequest{Subject: "Sam Rivera", Via: via, Now: now})
		requireContactRefusal(t, err, contact.ErrInvalidVia, "--via is one of call, text, video or in-person. Nothing was saved.")
	}
	for _, note := range []string{"first line\nsecond line", "one\r\ntwo", "carriage\rreturn"} {
		_, err := r.ContactLog(ContactLogRequest{Subject: "Sam Rivera", Note: note, Now: now})
		requireContactRefusal(t, err, contact.ErrNoteNotOneLine, "a contact note is one line. Nothing was saved.")
	}
	assert.Equal(t, before, contactTree(t, home), "a refused contact writes nothing")

	for i, via := range contact.ViaValues() {
		e := logContact(t, r, ContactLogRequest{Subject: "Sam Rivera", Via: via, Note: "\n  said hello  \n", Now: now})
		assert.Equal(t, contact.ID("2026-10-29", i), e.ID)
		assert.Equal(t, via, e.Via)
		assert.Equal(t, "said hello", e.Note, "surrounding whitespace is trimmed, not refused")
	}
}

// TestContactLog_DayStrictRejectWritesNothing: --day runs on the strict tier
// (Ct-6) — a future or unreadable day is a DayRejectedError and nothing is
// written — while a past day, including @yesterday, files the contact under
// that day with its own receipt and the real write time.
func TestContactLog_DayStrictRejectWritesNothing(t *testing.T) {
	r, _, home := newContactRouter(t)
	createPerson(t, r, "Sam Rivera")
	now := ctAt(time.October, 29)
	before := contactTree(t, home)

	for _, tc := range []struct{ day, want string }{
		{"2026-11-05", "has not happened yet"},
		{"tomorrow-ish", "could not read the day"},
		{"2026-13-40", "could not read the day"},
	} {
		_, err := r.ContactLog(ContactLogRequest{Subject: "Sam Rivera", DayArg: tc.day, Now: now})
		var refused *DayRejectedError
		require.ErrorAs(t, err, &refused, tc.day)
		assert.Contains(t, refused.Reason, tc.want, tc.day)
		assert.Contains(t, refused.Reason, "nothing was saved", tc.day)
	}
	assert.Equal(t, before, contactTree(t, home), "a refused day writes nothing")

	yesterday := logContact(t, r, ContactLogRequest{Subject: "Sam Rivera", DayArg: "@yesterday", Now: now})
	assert.Equal(t, "contact_2026_10_28_a", yesterday.ID)
	assert.Equal(t, "2026-10-28", yesterday.LogicalDate)
	assert.Equal(t, now.Format(time.RFC3339), yesterday.RecordedAt, "recorded_at stays the real write time")

	back := logContact(t, r, ContactLogRequest{Subject: "Sam Rivera", DayArg: "2026-10-12", Now: now})
	assert.Equal(t, "contact_2026_10_12_a", back.ID)
	today := logContact(t, r, ContactLogRequest{Subject: "Sam Rivera", Now: now})
	assert.Equal(t, "contact_2026_10_29_a", today.ID, "backdated writes leave today's sequence alone")
}

// TestContactCadence_LifecycleAppendOnlyWithReceipts walks set → change →
// pause (and a window change while paused) → resume → retire → set again.
// Every step appends one cadence event with a receipt and an inventory-only
// ack, no earlier line is ever rewritten, and each refused transition (Ct-7,
// Ct-8, Ct-9) writes nothing.
func TestContactCadence_LifecycleAppendOnlyWithReceipts(t *testing.T) {
	r, _, home := newContactRouter(t)
	key := createPerson(t, r, "Sam Rivera")
	oct29, nov5, nov12 := ctAt(time.October, 29), ctAt(time.November, 5), ctAt(time.November, 12)

	refuse := func(action, window string, now time.Time, class error, reason string) {
		t.Helper()
		before := contactTree(t, home)
		_, err := r.ContactCadence(ContactCadenceRequest{Subject: "Sam Rivera", Action: action, Window: window, Now: now})
		requireContactRefusal(t, err, class, reason)
		assert.Equal(t, before, contactTree(t, home), "a refused %s writes nothing", action)
	}
	step := func(action, window string, now time.Time, wantID, wantAck string) {
		t.Helper()
		before := contactTree(t, home)
		res := cadence(t, r, "Sam Rivera", action, window, now)
		assert.Equal(t, wantID, res.Event.ID)
		assert.Equal(t, contact.TypeCadence, res.Event.Type)
		assert.Equal(t, action, res.Event.Action)
		assert.Equal(t, key, res.PersonKey)
		assert.Equal(t, wantAck, res.Ack)
		requireTreeGrewOnly(t, before, contactTree(t, home))
	}

	refuse(contact.ActionPause, "", oct29, contact.ErrNotActive,
		"Sam Rivera has no check-in cadence to pause. Nothing was saved.")
	refuse(contact.ActionResume, "", oct29, contact.ErrNotPaused,
		"Sam Rivera has no check-in cadence to resume. Nothing was saved.")
	refuse(contact.ActionRetire, "", oct29, contact.ErrNoLiveCadence,
		"Sam Rivera has no check-in cadence to retire. Nothing was saved.")

	step(contact.ActionSet, "4w", oct29, "contact_2026_10_29_a",
		"Sam Rivera: every 4 weeks — next due by Nov 26 (contact_2026_10_29_a).")
	step(contact.ActionSet, "3w", oct29, "contact_2026_10_29_b",
		"Sam Rivera: every 3 weeks — next due by Nov 19 (contact_2026_10_29_b).")
	refuse(contact.ActionResume, "", oct29, contact.ErrNotPaused,
		"Sam Rivera's check-ins aren't paused — nothing to resume. Nothing was saved.")
	step(contact.ActionPause, "", oct29, "contact_2026_10_29_c",
		"Sam Rivera: check-ins paused (contact_2026_10_29_c).")
	refuse(contact.ActionPause, "", oct29, contact.ErrNotActive,
		"Sam Rivera's check-ins are already paused — nothing to pause. Nothing was saved.")
	step(contact.ActionSet, "2w", oct29, "contact_2026_10_29_d",
		"Sam Rivera: every 2 weeks, still paused (contact_2026_10_29_d).")
	assert.Equal(t, contact.StatePaused, contactStates(t, r)[key].State, "a window change keeps a pause")

	// Resume anchors on the later of the last contact (none) and the resume day.
	step(contact.ActionResume, "", nov5, "contact_2026_11_05_a",
		"Sam Rivera: check-ins resumed, every 2 weeks — next due by Nov 19 (contact_2026_11_05_a).")
	step(contact.ActionRetire, "", nov12, "contact_2026_11_12_a",
		"Sam Rivera: check-ins retired (contact_2026_11_12_a).")
	refuse(contact.ActionRetire, "", nov12, contact.ErrNoLiveCadence,
		"Sam Rivera has no check-in cadence to retire. Nothing was saved.")
	refuse(contact.ActionPause, "", nov12, contact.ErrNotActive,
		"Sam Rivera's check-ins are retired — nothing to pause. Nothing was saved.")

	retired := contactStates(t, r)[key]
	assert.Equal(t, contact.StateRetired, retired.State)
	assert.Equal(t, "2w", retired.Window.Text, "the last window is kept for display")

	// A set after retire starts a fresh window on the set day.
	step(contact.ActionSet, "28d", nov12, "contact_2026_11_12_b",
		"Sam Rivera: every 28 days — next due by Dec 10 (contact_2026_11_12_b).")

	all, err := r.Store().ReadContactEvents()
	require.NoError(t, err)
	actions := make([]string, 0, len(all))
	for _, e := range all {
		require.Equal(t, contact.TypeCadence, e.Type)
		actions = append(actions, e.Action+":"+e.Window)
	}
	assert.Equal(t, []string{"set:4w", "set:3w", "pause:", "set:2w", "resume:", "retire:", "set:28d"}, actions,
		"every change is its own appended event; history is kept")
}

// TestContactEvery_InvalidWindowWritesNothing: a window outside the Nd / Nw
// grammar is refused with the grammar (Ct-3), as is an action that is not a
// cadence change, and nothing is written.
func TestContactEvery_InvalidWindowWritesNothing(t *testing.T) {
	r, _, home := newContactRouter(t)
	createPerson(t, r, "Jordan Lee")
	now := ctAt(time.October, 29)
	before := contactTree(t, home)

	for _, window := range []string{"0d", "-2w", "1.5w", "4", "w", "4x", "", "four weeks"} {
		_, err := r.ContactCadence(ContactCadenceRequest{Subject: "Jordan Lee", Action: contact.ActionSet, Window: window, Now: now})
		requireContactRefusal(t, err, contact.ErrInvalidWindow,
			"a window is Nd or Nw — a whole number ≥ 1 of days or weeks, e.g. 28d or 4w. Nothing was saved.")
	}
	_, err := r.ContactCadence(ContactCadenceRequest{Subject: "Jordan Lee", Action: "snooze", Now: now})
	require.ErrorIs(t, err, ErrContactRejected)
	assert.Contains(t, err.Error(), "Nothing was saved.")

	// The window is checked before the subject: a bad window for an unknown
	// subject still carries the grammar.
	_, err = r.ContactCadence(ContactCadenceRequest{Subject: "Riley Moss", Action: contact.ActionSet, Window: "4", Now: now})
	require.ErrorIs(t, err, contact.ErrInvalidWindow)

	assert.Equal(t, before, contactTree(t, home), "an invalid window writes nothing")
}

// TestContactSnooze_RequiresActiveCadenceAndOncePerWeek: a snooze needs an
// active cadence and is refused a second time in the same ISO week, naming the
// snooze already recorded (Ct-10); it leaves the window and anchor untouched;
// a voided snooze frees the week; and the next week is a new week.
func TestContactSnooze_RequiresActiveCadenceAndOncePerWeek(t *testing.T) {
	r, _, home := newContactRouter(t)
	key := createPerson(t, r, "Alex Kim")
	tue, wed, nextMon := ctAt(time.November, 3), ctAt(time.November, 4), ctAt(time.November, 9)

	before := contactTree(t, home)
	_, err := r.ContactSnooze(ContactSnoozeRequest{Subject: "Alex Kim", Now: tue})
	requireContactRefusal(t, err, contact.ErrSnoozeNotActive, "Alex Kim has no active check-in cadence to snooze. Nothing was saved.")
	assert.Equal(t, before, contactTree(t, home))

	cadence(t, r, "Alex Kim", contact.ActionSet, "2w", ctAt(time.October, 29))
	open := contactStates(t, r)[key]

	res, err := r.ContactSnooze(ContactSnoozeRequest{Subject: "Alex Kim", Now: tue})
	require.NoError(t, err)
	assert.Equal(t, "contact_2026_11_03_a", res.Event.ID)
	assert.Equal(t, contact.TypeSnooze, res.Event.Type)
	assert.Equal(t, "Snoozed Alex Kim for this week (contact_2026_11_03_a).", res.Ack)

	snoozed := contactStates(t, r)[key]
	assert.True(t, snoozed.SnoozedWeeks["2026-W45"])
	assert.Equal(t, open.Anchor, snoozed.Anchor, "a snooze leaves the anchor alone")
	assert.Equal(t, open.Closes, snoozed.Closes, "and the window")

	before = contactTree(t, home)
	_, err = r.ContactSnooze(ContactSnoozeRequest{Subject: "Alex Kim", Now: wed})
	requireContactRefusal(t, err, contact.ErrSnoozedThisWeek,
		"Alex Kim is already snoozed this week (contact_2026_11_03_a). Nothing was saved.")
	assert.Equal(t, before, contactTree(t, home))

	// Voiding the snooze frees the week.
	_, err = r.ContactVoid(ContactVoidRequest{ID: "contact_2026_11_03_a", Now: wed})
	require.NoError(t, err)
	again, err := r.ContactSnooze(ContactSnoozeRequest{Subject: "Alex Kim", Now: wed})
	require.NoError(t, err)
	assert.Equal(t, "contact_2026_11_04_b", again.Event.ID)
	_, err = r.ContactSnooze(ContactSnoozeRequest{Subject: "Alex Kim", Now: wed})
	requireContactRefusal(t, err, contact.ErrSnoozedThisWeek,
		"Alex Kim is already snoozed this week (contact_2026_11_04_b). Nothing was saved.")

	next, err := r.ContactSnooze(ContactSnoozeRequest{Subject: "Alex Kim", Now: nextMon})
	require.NoError(t, err, "next Monday is a new week")
	assert.Equal(t, "contact_2026_11_09_a", next.Event.ID)

	cadence(t, r, "Alex Kim", contact.ActionPause, "", nextMon)
	_, err = r.ContactSnooze(ContactSnoozeRequest{Subject: "Alex Kim", Now: ctAt(time.November, 16)})
	requireContactRefusal(t, err, contact.ErrSnoozeNotActive, "Alex Kim has no active check-in cadence to snooze. Nothing was saved.")
}

// TestContactVoid_ContactAndSnooze: a void corrects a contact (it stops
// counting and the anchor falls back) or a snooze (it stops hiding the
// person), keeps both records, and refuses an unknown id, an already-voided
// event, a void and a cadence event (Ct-11) without writing anything.
func TestContactVoid_ContactAndSnooze(t *testing.T) {
	r, a, home := newContactRouter(t)
	key := createPerson(t, r, "Sam Rivera")
	set := cadence(t, r, "Sam Rivera", contact.ActionSet, "4w", ctAt(time.October, 1)).Event
	now := ctAt(time.October, 29)

	c := logContact(t, r, ContactLogRequest{Subject: "Sam Rivera", DayArg: "2026-10-20", Now: now})
	require.Equal(t, "contact_2026_10_20_a", c.ID)
	st := contactStates(t, r)[key]
	assert.Equal(t, "2026-10-20", st.Anchor, "the backdated contact moved the anchor")
	assert.Equal(t, "2026-11-17", st.Closes)

	voidC, err := r.ContactVoid(ContactVoidRequest{ID: "  " + c.ID + "  ", Now: now})
	require.NoError(t, err)
	assert.Equal(t, "contact_2026_10_29_a", voidC.Event.ID)
	assert.Equal(t, contact.TypeVoid, voidC.Event.Type)
	assert.Equal(t, key, voidC.PersonKey)
	target, ok := voidC.Event.Voids()
	require.True(t, ok)
	assert.Equal(t, c.ID, target)
	assert.Equal(t, "Voided contact_2026_10_20_a for Sam Rivera (contact_2026_10_29_a).", voidC.Ack)

	st = contactStates(t, r)[key]
	assert.Nil(t, st.LastContact, "a voided contact no longer counts")
	assert.Equal(t, "2026-10-01", st.Anchor, "the anchor falls back to the set day")
	assert.Equal(t, "2026-10-29", st.Closes)
	kept, ok, err := a.ReadContactEventByID(c.ID)
	require.NoError(t, err)
	require.True(t, ok, "the voided contact stays in the history")
	assert.Equal(t, c, kept)

	// A snooze hides Sam this week; voiding it brings them back.
	snooze, err := r.ContactSnooze(ContactSnoozeRequest{Subject: "Sam Rivera", Now: now})
	require.NoError(t, err)
	idx, err := r.contactIndex()
	require.NoError(t, err)
	assert.Empty(t, contact.Due(contactStates(t, r), idx.info, "2026-10-29", false).DueThisWeek, "snoozed this week")

	voidS, err := r.ContactVoid(ContactVoidRequest{ID: snooze.Event.ID, Now: now})
	require.NoError(t, err)
	assert.Equal(t, "Voided contact_2026_10_29_b for Sam Rivera (contact_2026_10_29_c).", voidS.Ack)
	assert.False(t, contactStates(t, r)[key].SnoozedWeeks["2026-W44"], "a voided snooze no longer hides the person")
	due := contact.Due(contactStates(t, r), idx.info, "2026-10-29", false)
	require.Len(t, due.DueThisWeek, 1)
	assert.Equal(t, key, due.DueThisWeek[0].PersonKey)

	before := contactTree(t, home)
	for _, tc := range []struct {
		id     string
		class  error
		reason string
	}{
		{"contact_2026_10_29_z", contact.ErrVoidUnknown, `no contact event "contact_2026_10_29_z". Nothing was saved.`},
		{"rep_2026_10_29_001", contact.ErrVoidUnknown, `no contact event "rep_2026_10_29_001". Nothing was saved.`},
		{c.ID, contact.ErrVoidAlreadyVoided, `"contact_2026_10_20_a" is already voided (by "contact_2026_10_29_a"). Nothing was saved.`},
		{voidC.Event.ID, contact.ErrVoidOfVoid, "a void can't itself be voided. Nothing was saved."},
		{set.ID, contact.ErrVoidCadence, "a cadence event is never voided — change it with lucid contact every, pause, resume or retire. Nothing was saved."},
	} {
		_, verr := r.ContactVoid(ContactVoidRequest{ID: tc.id, Now: now})
		requireContactRefusal(t, verr, tc.class, tc.reason)
	}
	_, err = r.ContactVoid(ContactVoidRequest{ID: "   ", Now: now})
	require.ErrorIs(t, err, ErrContactRejected, "a blank id is an input refusal")
	assert.Equal(t, before, contactTree(t, home), "a refused void writes nothing")
}

// TestContact_NoCadenceRecordsHistoryNoDueEffect: contact with someone who has
// no cadence is recorded — it folds into their last contact — but they never
// appear in the due view.
func TestContact_NoCadenceRecordsHistoryNoDueEffect(t *testing.T) {
	r, _, _ := newContactRouter(t)
	key := createPerson(t, r, "Casey Park")
	now := ctAt(time.October, 29)

	e := logContact(t, r, ContactLogRequest{Subject: "Casey Park", Via: contact.ViaText, DayArg: "@yesterday", Now: now})
	assert.Equal(t, "contact_2026_10_28_a", e.ID)

	st := contactStates(t, r)[key]
	assert.Equal(t, contact.StateNone, st.State)
	require.NotNil(t, st.LastContact)
	assert.Equal(t, contact.Contact{Day: "2026-10-28", Via: contact.ViaText}, *st.LastContact)
	assert.Empty(t, st.Closes, "no cadence, no window")

	idx, err := r.contactIndex()
	require.NoError(t, err)
	for _, day := range []string{"2026-10-29", "2026-11-26", "2027-03-01"} {
		v := contact.Due(contactStates(t, r), idx.info, day, false)
		assert.Empty(t, v.DueThisWeek, day)
		assert.Empty(t, v.StillOpen, day)
		assert.Empty(t, v.ComingUp, day)
		assert.Zero(t, v.ActiveCadences, day)
	}
}

// TestContact_UnknownSubjectRefusedPointsAtPersonCreate: every contact verb
// refuses a subject that names no one (Ct-1) with the person create remedy,
// still matching the §P-4 sentinel — and never creates a person or writes an
// event.
func TestContact_UnknownSubjectRefusedPointsAtPersonCreate(t *testing.T) {
	r, a, home := newContactRouter(t)
	now := ctAt(time.October, 29)
	const want = `no one matches "Riley Moss" — add them first with lucid person create <name>; nothing was saved.`

	calls := map[string]func() error{
		"contact": func() error {
			_, err := r.ContactLog(ContactLogRequest{Subject: "Riley Moss", Via: contact.ViaCall, Now: now})
			return err
		},
		"every": func() error {
			_, err := r.ContactCadence(ContactCadenceRequest{Subject: "Riley Moss", Action: contact.ActionSet, Window: "4w", Now: now})
			return err
		},
		"pause": func() error {
			_, err := r.ContactCadence(ContactCadenceRequest{Subject: " Riley Moss ", Action: contact.ActionPause, Now: now})
			return err
		},
		"snooze": func() error {
			_, err := r.ContactSnooze(ContactSnoozeRequest{Subject: "Riley Moss", Now: now})
			return err
		},
	}
	for verb, call := range calls {
		err := call()
		requireContactRefusal(t, err, ErrPersonUnknownSubject, want)
		assert.Contains(t, err.Error(), "lucid person create", verb)
	}

	keys, err := a.ListPeopleKeys()
	require.NoError(t, err)
	assert.Empty(t, keys, "logging contact never creates a person")
	assert.Empty(t, contactTree(t, home), "and writes no event")

	_, err = r.ContactLog(ContactLogRequest{Subject: "   ", Now: now})
	require.ErrorIs(t, err, ErrPersonRejected, "a blank subject is an input refusal")
	var refused *ContactRejectedError
	require.ErrorAs(t, err, &refused)
}

// TestContact_AmbiguousSubjectRefusedWithCandidates: a subject matching two
// live records is refused naming both keys (Ct-2) and nothing is written; the
// exact key still reaches either one.
func TestContact_AmbiguousSubjectRefusedWithCandidates(t *testing.T) {
	r, _, home := newContactRouter(t)
	seen := ctAt(time.October, 1)
	writePersonFile(t, home, "person_s-river", "Sam Rivera", []string{"Sam"}, seen)
	writePersonFile(t, home, "person_s-river-2", "Sam Rivera", []string{"Sam"}, seen)
	now := ctAt(time.October, 29)

	for _, subject := range []string{"Sam Rivera", "sam"} {
		_, err := r.ContactLog(ContactLogRequest{Subject: subject, Now: now})
		require.ErrorIs(t, err, ErrPersonAmbiguousSubject, subject)
		var refused *ContactRejectedError
		require.ErrorAs(t, err, &refused)
		assert.Contains(t, refused.Reason, "person_s-river / person_s-river-2", subject)
		assert.Contains(t, refused.Reason, "nothing was saved", subject)
	}
	_, err := r.ContactSnooze(ContactSnoozeRequest{Subject: "Sam", Now: now})
	require.ErrorIs(t, err, ErrPersonAmbiguousSubject)
	assert.Empty(t, contactTree(t, home), "an ambiguous subject writes nothing")

	e := logContact(t, r, ContactLogRequest{Subject: "person_s-river-2", Now: now})
	assert.Equal(t, "person_s-river-2", e.PersonKey, "an exact key disambiguates")
}

// TestContact_ResolvesAliasAndRedirect: an alias form and a tombstone key both
// resolve to the canonical person, and the event is stored under the
// canonical key.
func TestContact_ResolvesAliasAndRedirect(t *testing.T) {
	r, _, home := newContactRouter(t)
	key := createPerson(t, r, "Jordan Lee")
	_, err := r.PersonAlias(PersonAliasRequest{Subject: "Jordan Lee", Form: "Jordan"})
	require.NoError(t, err)
	writeTombstone(t, home, "person_j-old", "Jordan Lee", key, ctAt(time.October, 1))
	now := ctAt(time.October, 29)

	byAlias := logContact(t, r, ContactLogRequest{Subject: "jordan", Now: now})
	assert.Equal(t, key, byAlias.PersonKey, "an alias form resolves")
	byTombstone, err := r.ContactCadence(ContactCadenceRequest{Subject: "person_j-old", Action: contact.ActionSet, Window: "3w", Now: now})
	require.NoError(t, err)
	assert.Equal(t, key, byTombstone.Event.PersonKey, "a tombstone key resolves forward")
	assert.Equal(t, "Jordan Lee", byTombstone.DisplayName)

	st := contactStates(t, r)[key]
	assert.Equal(t, contact.StateActive, st.State)
	assert.Equal(t, "2026-10-29", st.Anchor)
	require.NotNil(t, st.LastContact)
}

// TestContact_MergeKeepsHistoryWithMergedPerson: a cadence, a contact and a
// snooze recorded on a duplicate record follow it into the person it is merged
// into — nothing is rewritten; the fold follows the redirect — and the
// duplicate's old key keeps resolving there.
func TestContact_MergeKeepsHistoryWithMergedPerson(t *testing.T) {
	r, a, home := newContactRouter(t)
	seen := ctAt(time.October, 1)
	const keep, dup = "person_s-river", "person_s-river-2"
	writePersonFile(t, home, keep, "Sam Rivera", []string{}, seen)
	writePersonFile(t, home, dup, "Sam Rivera", []string{}, seen)
	now := ctAt(time.October, 29)

	cadence(t, r, dup, contact.ActionSet, "2w", now)
	logContact(t, r, ContactLogRequest{Subject: dup, Via: contact.ViaVideo, DayArg: "2026-10-27", Now: now})
	_, err := r.ContactSnooze(ContactSnoozeRequest{Subject: dup, Now: now})
	require.NoError(t, err)
	before := contactTree(t, home)

	_, err = r.PersonMerge(PersonMergeRequest{Source: dup, Target: keep})
	require.NoError(t, err)
	assert.Equal(t, before, contactTree(t, home), "a merge rewrites no contact event")

	states := contactStates(t, r)
	assert.NotContains(t, states, dup, "the duplicate no longer folds on its own")
	st := states[keep]
	assert.Equal(t, contact.StateActive, st.State, "the cadence followed the merge")
	assert.Equal(t, "2w", st.Window.Text)
	require.NotNil(t, st.LastContact, "the contact followed the merge")
	assert.Equal(t, contact.Contact{Day: "2026-10-27", Via: contact.ViaVideo}, *st.LastContact)
	assert.Equal(t, "2026-10-27", st.Anchor)
	assert.Equal(t, "2026-11-10", st.Closes)
	assert.True(t, st.SnoozedWeeks["2026-W44"], "the snooze followed the merge")

	// The old key resolves to the merged person, and the week's snooze still
	// counts against a second one.
	_, err = r.ContactSnooze(ContactSnoozeRequest{Subject: dup, Now: now})
	requireContactRefusal(t, err, contact.ErrSnoozedThisWeek,
		"Sam Rivera is already snoozed this week (contact_2026_10_29_b). Nothing was saved.")
	e := logContact(t, r, ContactLogRequest{Subject: dup, Now: now})
	assert.Equal(t, keep, e.PersonKey)

	raw, err := a.ReadContactEvents()
	require.NoError(t, err)
	assert.Equal(t, dup, raw[0].PersonKey, "stored events keep the key they were written under")
}

// TestContactIndex_CanonicalOffLimitsAndDob: the people index maps tombstones
// to their target, carries display name and dob, and canonicalizes the
// off-limits registry so a key merged away still redacts the merged person.
func TestContactIndex_CanonicalOffLimitsAndDob(t *testing.T) {
	r, a, home := newContactRouter(t)
	dob := "1990-11-04"
	res, err := r.PersonCreate(PersonCreateRequest{Name: "Riley Moss", Dob: &dob, Now: ctAt(time.October, 1)})
	require.NoError(t, err)
	riley := res.Record.PersonKey
	alex := createPerson(t, r, "Alex Kim")
	writeTombstone(t, home, "person_a-old", "Alex Kim", alex, ctAt(time.October, 1))
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{"person_a-old"}))

	idx, err := r.contactIndex()
	require.NoError(t, err)
	assert.Equal(t, alex, idx.canonical("person_a-old"))
	assert.Equal(t, alex, idx.canonical(alex))
	assert.Equal(t, "person_x-unknown", idx.canonical("person_x-unknown"), "a key with no record is its own")
	assert.NotContains(t, idx.info, "person_a-old", "a tombstone is not a live person")
	assert.True(t, idx.info[alex].OffLimits, "off-limits follows the redirect")
	assert.Equal(t, contact.PersonInfo{Key: riley, DisplayName: "Riley Moss", Dob: dob}, idx.info[riley])
	assert.Equal(t, "Riley Moss", idx.name(riley))
	assert.Equal(t, "person_x-unknown", idx.name("person_x-unknown"))

	// Off-limits people stay writable: it is the user's own record.
	_, err = r.ContactCadence(ContactCadenceRequest{Subject: "Alex Kim", Action: contact.ActionSet, Window: "4w", Now: ctAt(time.October, 29)})
	require.NoError(t, err)
	_, err = r.ContactLog(ContactLogRequest{Subject: "Alex Kim", Now: ctAt(time.October, 29)})
	require.NoError(t, err)
}

// TestContactAcks_InventoryOnly: every write's ack reports what was recorded
// and never evaluates — no lateness, no score, no shame word.
func TestContactAcks_InventoryOnly(t *testing.T) {
	r, _, _ := newContactRouter(t)
	createPerson(t, r, "Sam Rivera")
	oct29 := ctAt(time.October, 29)

	acks := make([]string, 0, 7)
	acks = append(acks, cadence(t, r, "Sam Rivera", contact.ActionSet, "1w", ctAt(time.September, 1)).Ack)
	acks = append(acks, cadence(t, r, "Sam Rivera", contact.ActionSet, "2w", oct29).Ack)
	c := logContact(t, r, ContactLogRequest{Subject: "Sam Rivera", Now: oct29})
	snooze, err := r.ContactSnooze(ContactSnoozeRequest{Subject: "Sam Rivera", Now: oct29})
	require.NoError(t, err)
	acks = append(acks, snooze.Ack)
	v, err := r.ContactVoid(ContactVoidRequest{ID: c.ID, Now: oct29})
	require.NoError(t, err)
	acks = append(acks, v.Ack)
	acks = append(acks, cadence(t, r, "Sam Rivera", contact.ActionPause, "", oct29).Ack)
	acks = append(acks, cadence(t, r, "Sam Rivera", contact.ActionResume, "", oct29).Ack)
	acks = append(acks, cadence(t, r, "Sam Rivera", contact.ActionRetire, "", oct29).Ack)

	assert.Equal(t, "Sam Rivera: every 2 weeks — open now; they'll be on Monday's list (contact_2026_10_29_a).", acks[1],
		"a changed window that has already closed says so plainly")
	for _, ack := range acks {
		lower := strings.ToLower(ack)
		for _, word := range []string{"overdue", "late", "behind", "missed", "streak", "score", "%", "days ago"} {
			assert.NotContains(t, lower, word, ack)
		}
		assert.Contains(t, ack, "(contact_", "every ack names its receipt")
	}
}

// dueKeys returns each row's person key, in order.
func dueKeys(rows []contact.DueRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.PersonKey)
	}
	return out
}

// TestContactDue_JSONDeterministicOrder: the due view is byte-identical across
// calls (and across a fresh router over the same Ledger), and each group is
// ordered by close date, then display name, then person key. Today is
// Wednesday 2026-11-04, in ISO week 2026-W45 (Mon Nov 2 – Sun Nov 8).
func TestContactDue_JSONDeterministicOrder(t *testing.T) {
	r, a, home := newContactRouter(t)
	seen := ctAt(time.October, 1)
	writePersonFile(t, home, "person_s-river", "Sam Rivera", []string{}, seen)
	writePersonFile(t, home, "person_s-river-2", "Sam Rivera", []string{}, seen)
	casey := createPerson(t, r, "Casey Park")
	riley := createPerson(t, r, "Riley Moss")
	alex := createPerson(t, r, "Alex Kim")
	jordan := createPerson(t, r, "Jordan Lee")

	oct8 := ctAt(time.October, 8)
	cadence(t, r, "person_s-river-2", contact.ActionSet, "4w", oct8) // closes Thu Nov 5
	cadence(t, r, "person_s-river", contact.ActionSet, "28d", oct8)  // closes Thu Nov 5
	cadence(t, r, "Casey Park", contact.ActionSet, "4w", oct8)       // closes Thu Nov 5
	cadence(t, r, "Riley Moss", contact.ActionSet, "1w", ctAt(time.October, 27))
	cadence(t, r, "Alex Kim", contact.ActionSet, "2w", ctAt(time.October, 14))   // closed Wed Oct 28
	cadence(t, r, "Jordan Lee", contact.ActionSet, "3w", ctAt(time.October, 20)) // closes Tue Nov 10
	logContact(t, r, ContactLogRequest{Subject: "Riley Moss", DayArg: "2026-10-27", Now: ctAt(time.October, 27)})

	now := ctAt(time.November, 4)
	v, err := r.ContactDue(now, false)
	require.NoError(t, err)
	first, err := json.Marshal(v)
	require.NoError(t, err)
	again, err := r.ContactDue(now, false)
	require.NoError(t, err)
	second, err := json.Marshal(again)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "the same Ledger and day render the same bytes")

	fresh := New(a)
	_, err = fresh.Boot()
	require.NoError(t, err)
	third, err := fresh.ContactDue(now, false)
	require.NoError(t, err)
	b3, err := json.Marshal(third)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(b3), "a fresh router over the same Ledger agrees")

	assert.Equal(t, "2026-11-04", v.Day)
	assert.Equal(t, "2026-W45", v.Week)
	assert.Equal(t, []string{riley, casey, "person_s-river", "person_s-river-2"}, dueKeys(v.DueThisWeek),
		"close date first (Nov 3 before Nov 5), then name, then key")
	assert.Equal(t, []string{alex}, dueKeys(v.StillOpen))
	assert.Equal(t, []string{jordan}, dueKeys(v.ComingUp))
	assert.Equal(t, "2026-11-03", v.DueThisWeek[0].WindowCloses)
	assert.Equal(t, "2026-10-27", v.DueThisWeek[0].LastContact)
	assert.Equal(t, "28d", v.DueThisWeek[2].Cadence, "the window as typed is kept")
	assert.Empty(t, v.DueThisWeek[1].LastContact)
	assert.Contains(t, string(first), `"last_contact":null`)

	wrap, err := r.ContactDue(now, true)
	require.NoError(t, err)
	assert.True(t, wrap.Wrap)
	assert.Equal(t, dueKeys(v.DueThisWeek), dueKeys(wrap.DueThisWeek), "the wrap shares the due view's grouping")
	assert.Equal(t, dueKeys(v.StillOpen), dueKeys(wrap.StillOpen))
	assert.Empty(t, wrap.ComingUp)
}

// TestContactDue_OffLimitsExcludedEverywhere: an off-limits person with an
// active cadence and a birthday this week appears in no group, not among the
// birthdays, and not in the wrap — while contact writes for them still
// succeed, since it is the user's own record.
func TestContactDue_OffLimitsExcludedEverywhere(t *testing.T) {
	r, _, _ := newContactRouter(t)
	rileyDob, samDob := "1990-11-04", "1991-11-05"
	created, err := r.PersonCreate(PersonCreateRequest{Name: "Riley Moss", Dob: &rileyDob, Now: ctAt(time.October, 1)})
	require.NoError(t, err)
	riley := created.Record.PersonKey
	_, err = r.PersonCreate(PersonCreateRequest{Name: "Sam Rivera", Dob: &samDob, Now: ctAt(time.October, 1)})
	require.NoError(t, err)
	cadence(t, r, "Riley Moss", contact.ActionSet, "1w", ctAt(time.October, 20)) // closed Oct 27: still open
	cadence(t, r, "Sam Rivera", contact.ActionSet, "4w", ctAt(time.October, 8))  // closes Nov 5: due
	_, err = r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Riley Moss"})
	require.NoError(t, err)

	now := ctAt(time.November, 4)
	_, err = r.ContactLog(ContactLogRequest{Subject: "Riley Moss", DayArg: "@yesterday", Now: now})
	require.NoError(t, err, "an off-limits person stays writable")
	_, err = r.ContactCadence(ContactCadenceRequest{Subject: "Riley Moss", Action: contact.ActionSet, Window: "3d", Now: now})
	require.NoError(t, err)
	st := contactStates(t, r)[riley]
	require.Equal(t, contact.StateActive, st.State)
	require.Equal(t, "2026-11-06", st.Closes, "their cadence would be due this week")

	for _, wrap := range []bool{false, true} {
		v, err := r.ContactDue(now, wrap)
		require.NoError(t, err)
		for _, group := range [][]contact.DueRow{v.DueThisWeek, v.StillOpen, v.ComingUp} {
			assert.NotContains(t, dueKeys(group), riley, "wrap=%v", wrap)
		}
		for _, b := range v.Birthdays {
			assert.NotEqual(t, riley, b.PersonKey, "wrap=%v", wrap)
		}
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		text := contact.RenderDueText(v)
		if wrap {
			text = contact.RenderWrapText(v)
		}
		for _, surface := range []string{string(raw), text} {
			assert.NotContains(t, surface, "Riley", "wrap=%v", wrap)
			assert.NotContains(t, surface, riley, "wrap=%v", wrap)
			assert.Contains(t, surface, "Sam Rivera", "the view is not simply empty (wrap=%v)", wrap)
		}
		if !wrap {
			require.Len(t, v.Birthdays, 1)
			assert.Equal(t, "Sam Rivera", v.Birthdays[0].DisplayName)
		}
	}
}

// TestContactDue_UsesCanonicalDisplayNameAfterRename: a row shows the live
// canonical record's current display name — after a rename, and after a merge,
// where history recorded under the merged-away key folds onto the target.
func TestContactDue_UsesCanonicalDisplayNameAfterRename(t *testing.T) {
	r, _, home := newContactRouter(t)
	riley := createPerson(t, r, "Riley Moss")
	seen := ctAt(time.October, 1)
	const keep, dup = "person_a-kim", "person_j-lee-old"
	writePersonFile(t, home, keep, "Alex Kim", []string{}, seen)
	writePersonFile(t, home, dup, "Jordan Lee", []string{}, seen)
	cadence(t, r, "Riley Moss", contact.ActionSet, "4w", ctAt(time.October, 8))  // closes Nov 5
	cadence(t, r, "Jordan Lee", contact.ActionSet, "2w", ctAt(time.October, 14)) // closed Oct 28

	_, err := r.PersonRename(PersonRenameRequest{Subject: "Riley Moss", NewName: "Casey Park"})
	require.NoError(t, err)
	_, err = r.PersonMerge(PersonMergeRequest{Source: dup, Target: keep})
	require.NoError(t, err)

	v, err := r.ContactDue(ctAt(time.November, 4), false)
	require.NoError(t, err)
	require.Len(t, v.DueThisWeek, 1)
	assert.Equal(t, riley, v.DueThisWeek[0].PersonKey, "a rename keeps the key")
	assert.Equal(t, "Casey Park", v.DueThisWeek[0].DisplayName)
	require.Len(t, v.StillOpen, 1)
	assert.Equal(t, keep, v.StillOpen[0].PersonKey, "the merged-away key folds onto the target")
	assert.Equal(t, "Alex Kim", v.StillOpen[0].DisplayName)
	text := contact.RenderDueText(v)
	assert.NotContains(t, text, "Riley Moss", "the old name is gone from the view")
	assert.NotContains(t, text, "Jordan Lee", "the merged-away name is gone from the view")
}

// personLines returns a person view's text, one line per element.
func personLines(t *testing.T, r *Router, name string) (PersonResult, []string) {
	t.Helper()
	res, err := r.Person(PersonRequest{Name: name})
	require.NoError(t, err)
	require.True(t, res.Matched, name)
	return res, strings.Split(res.Text, "\n")
}

// TestPersonView_ShowsCadenceAndLastContact: `lucid person` adds one check-in
// line after the mention counts — the cadence, or that it is paused or
// retired, and the last contact as a date — plus the Contact summary, in the
// off-limits raw view too. A person with no contact events, or none that still
// counts, renders byte-identically to before (S-22).
func TestPersonView_ShowsCadenceAndLastContact(t *testing.T) {
	r, _, _ := newContactRouter(t)
	createPerson(t, r, "Sam Rivera")
	createPerson(t, r, "Alex Kim")
	createPerson(t, r, "Jordan Lee")
	createPerson(t, r, "Casey Park")
	createPerson(t, r, "Riley Moss")
	untouched := map[string]string{}
	for _, name := range []string{"Jordan Lee", "Casey Park"} {
		res, err := r.Person(PersonRequest{Name: name})
		require.NoError(t, err)
		untouched[name] = res.Text
	}

	oct29 := ctAt(time.October, 29)
	cadence(t, r, "Sam Rivera", contact.ActionSet, "4w", ctAt(time.September, 1))
	logContact(t, r, ContactLogRequest{Subject: "Sam Rivera", Via: contact.ViaCall, DayArg: "2026-09-28", Now: oct29})
	logContact(t, r, ContactLogRequest{Subject: "Alex Kim", DayArg: "2026-09-28", Now: oct29})
	mistaken := logContact(t, r, ContactLogRequest{Subject: "Casey Park", Now: oct29})
	_, err := r.ContactVoid(ContactVoidRequest{ID: mistaken.ID, Now: oct29})
	require.NoError(t, err)

	t.Run("active", func(t *testing.T) {
		res, lines := personLines(t, r, "Sam Rivera")
		require.GreaterOrEqual(t, len(lines), 3)
		assert.Equal(t, "Sam Rivera", lines[0])
		assert.True(t, strings.HasPrefix(lines[1], "Mentioned in "), lines[1])
		assert.Equal(t, "Check-ins: every 4 weeks — last caught up 2026-09-28", lines[2])
		require.NotNil(t, res.Contact)
		assert.Equal(t, PersonContact{
			State:       contact.StateActive,
			Window:      contact.Window{Text: "4w", Days: 28},
			LastContact: "2026-09-28",
			Line:        "Check-ins: every 4 weeks — last caught up 2026-09-28",
		}, *res.Contact)
	})

	t.Run("paused", func(t *testing.T) {
		cadence(t, r, "Sam Rivera", contact.ActionPause, "", oct29)
		res, lines := personLines(t, r, "Sam Rivera")
		assert.Equal(t, "Check-ins: paused (every 4 weeks) — last caught up 2026-09-28", lines[2])
		require.NotNil(t, res.Contact)
		assert.Equal(t, contact.StatePaused, res.Contact.State)
	})

	t.Run("retired", func(t *testing.T) {
		cadence(t, r, "Sam Rivera", contact.ActionRetire, "", oct29)
		res, lines := personLines(t, r, "Sam Rivera")
		assert.Equal(t, "Check-ins: retired — last caught up 2026-09-28", lines[2])
		require.NotNil(t, res.Contact)
		assert.Equal(t, contact.StateRetired, res.Contact.State)
		assert.Equal(t, "4w", res.Contact.Window.Text, "the last window is kept for display")
	})

	t.Run("contact without a cadence", func(t *testing.T) {
		res, lines := personLines(t, r, "Alex Kim")
		assert.Equal(t, "Last caught up 2026-09-28 (no check-in cadence)", lines[2])
		require.NotNil(t, res.Contact)
		assert.Equal(t, contact.StateNone, res.Contact.State)
		assert.Equal(t, contact.Window{}, res.Contact.Window)
		assert.Equal(t, "2026-09-28", res.Contact.LastContact)
	})

	t.Run("cadence with nothing logged", func(t *testing.T) {
		cadence(t, r, "Riley Moss", contact.ActionSet, "10d", oct29)
		res, lines := personLines(t, r, "Riley Moss")
		assert.Equal(t, "Check-ins: every 10 days — not logged yet", lines[2])
		require.NotNil(t, res.Contact)
		assert.Empty(t, res.Contact.LastContact)
	})

	t.Run("off-limits raw view", func(t *testing.T) {
		_, err := r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Riley Moss"})
		require.NoError(t, err)
		res, lines := personLines(t, r, "Riley Moss")
		require.True(t, res.OffLimits)
		assert.True(t, strings.HasPrefix(lines[0], "Riley Moss is off-limits to inference"), lines[0])
		assert.True(t, strings.HasPrefix(lines[1], "Mentioned in "), lines[1])
		assert.Equal(t, "Check-ins: every 10 days — not logged yet", lines[2],
			"the check-in line is the user's own record, not an inference")
		require.NotNil(t, res.Contact)
	})

	t.Run("no contact events, or none that counts, renders as before", func(t *testing.T) {
		for name, before := range untouched {
			res, err := r.Person(PersonRequest{Name: name})
			require.NoError(t, err)
			assert.Equal(t, before, res.Text, name)
			assert.Nil(t, res.Contact, name)
			assert.NotContains(t, res.Text, "Check-ins", name)
			assert.NotContains(t, res.Text, "caught up", name)
		}
	})
}
