package contact

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ledger builds one synthetic person's event history, minting ids per logical
// day the way the storage adapter does.
type ledger struct {
	t      *testing.T
	key    string
	events []Event
	slots  map[string]int
}

func newLedger(t *testing.T, key string) *ledger {
	t.Helper()
	return &ledger{t: t, key: key, slots: map[string]int{}}
}

// at is a recorded_at on a write day at hour h, in a fixed -04:00 offset.
func at(day string, h int) string { return fmt.Sprintf("%sT%02d:00:00-04:00", day, h) }

func (l *ledger) add(e Event) Event {
	l.t.Helper()
	e.ID = ID(e.LogicalDate, l.slots[e.LogicalDate])
	l.slots[e.LogicalDate]++
	require.NoError(l.t, e.Validate())
	l.events = append(l.events, e)
	return e
}

func (l *ledger) every(day, window string) Event {
	l.t.Helper()
	return l.add(NewCadence(l.key, day, at(day, 9), ActionSet, mustWindow(l.t, window)))
}

func (l *ledger) pause(day string) {
	l.add(NewCadence(l.key, day, at(day, 10), ActionPause, Window{}))
}

func (l *ledger) resume(day string) {
	l.add(NewCadence(l.key, day, at(day, 11), ActionResume, Window{}))
}

func (l *ledger) retire(day string) {
	l.add(NewCadence(l.key, day, at(day, 12), ActionRetire, Window{}))
}

// contact logs a contact for logical day `day`, written on day `written` (a
// backdated contact has written after day).
func (l *ledger) contact(day, written, via string) Event {
	return l.add(NewContact(l.key, day, at(written, 13), via, ""))
}

func (l *ledger) snooze(day string) Event {
	return l.add(NewSnooze(l.key, day, at(day, 14)))
}

func (l *ledger) void(target Event, day string) Event {
	return l.add(NewVoid(target.ID, l.key, day, at(day, 18)))
}

func (l *ledger) state() PersonState { return Fold(l.events)[l.key] }

// assertWindow checks an active state's anchor and close date.
func assertWindow(t *testing.T, st PersonState, anchor, closes string) {
	t.Helper()
	assert.Equal(t, StateActive, st.State)
	assert.Equal(t, anchor, st.Anchor, "anchor")
	assert.Equal(t, closes, st.Closes, "window closes")
}

// TestFoldAnchor_WindowStartRules pins every window-start rule settled in
// clarify (ADR-0014 §3), one named subtest per rule.
func TestFoldAnchor_WindowStartRules(t *testing.T) {
	t.Run("first set with no contact anchors on the set day", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.every("2026-10-01", "4w")
		st := l.state()
		assertWindow(t, st, "2026-10-01", "2026-10-29")
		assert.Equal(t, Window{Text: "4w", Days: 28}, st.Window)
		assert.Nil(t, st.LastContact)
	})

	t.Run("first set after an older contact anchors on that contact", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.contact("2026-08-01", "2026-08-01", ViaCall)
		l.every("2026-10-01", "4w")
		// Closes before the set day: honestly still open from before.
		assertWindow(t, l.state(), "2026-08-01", "2026-08-29")
	})

	t.Run("a backdated contact logged after the set moves the anchor to its day", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.every("2026-10-01", "4w")
		l.contact("2026-09-20", "2026-10-02", "")
		assertWindow(t, l.state(), "2026-09-20", "2026-10-18")
	})

	t.Run("a later contact moves the anchor forward", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.every("2026-10-01", "4w")
		l.contact("2026-10-15", "2026-10-15", ViaText)
		assertWindow(t, l.state(), "2026-10-15", "2026-11-12")
	})

	t.Run("resume anchors on the resume day when the last contact is earlier", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.contact("2026-09-20", "2026-09-20", "")
		l.every("2026-10-01", "4w")
		l.pause("2026-10-05")
		l.resume("2026-10-20")
		assertWindow(t, l.state(), "2026-10-20", "2026-11-17")
	})

	t.Run("resume then a later contact anchors on the contact", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.every("2026-10-01", "4w")
		l.pause("2026-10-05")
		l.resume("2026-10-20")
		l.contact("2026-10-25", "2026-10-25", "")
		assertWindow(t, l.state(), "2026-10-25", "2026-11-22")
	})

	t.Run("resume then a backdated contact before the resume day keeps the resume day", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.every("2026-10-01", "4w")
		l.pause("2026-10-05")
		l.resume("2026-10-20")
		l.contact("2026-10-10", "2026-10-21", "")
		st := l.state()
		assertWindow(t, st, "2026-10-20", "2026-11-17")
		require.NotNil(t, st.LastContact)
		assert.Equal(t, "2026-10-10", st.LastContact.Day)
	})

	t.Run("every again on an active cadence keeps the anchor", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.every("2026-10-01", "4w")
		l.contact("2026-10-10", "2026-10-10", "")
		l.every("2026-10-15", "2w")
		st := l.state()
		assertWindow(t, st, "2026-10-10", "2026-10-24")
		assert.Equal(t, Window{Text: "2w", Days: 14}, st.Window)
	})

	t.Run("every after a resume keeps the resume anchor", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.every("2026-10-01", "4w")
		l.pause("2026-10-05")
		l.resume("2026-10-20")
		l.every("2026-10-22", "1w")
		assertWindow(t, l.state(), "2026-10-20", "2026-10-27")
	})

	t.Run("every while paused changes the window and stays paused", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.every("2026-10-01", "4w")
		l.pause("2026-10-05")
		l.every("2026-10-07", "3w")
		st := l.state()
		assert.Equal(t, StatePaused, st.State)
		assert.Equal(t, Window{Text: "3w", Days: 21}, st.Window)
		assert.Empty(t, st.Anchor, "a paused cadence has no open window")
		assert.Empty(t, st.Closes)

		l.resume("2026-10-20")
		assertWindow(t, l.state(), "2026-10-20", "2026-11-10")
	})

	t.Run("retire then every restarts with the first-set rule", func(t *testing.T) {
		withOld := newLedger(t, keySam)
		withOld.contact("2026-06-01", "2026-06-01", ViaVideo)
		withOld.every("2026-07-01", "4w")
		withOld.pause("2026-07-10")
		withOld.resume("2026-07-20")
		withOld.retire("2026-08-01")
		st := withOld.state()
		assert.Equal(t, StateRetired, st.State)
		assert.Empty(t, st.Closes)
		withOld.every("2026-10-01", "2w")
		assertWindow(t, withOld.state(), "2026-06-01", "2026-06-15")

		fresh := newLedger(t, keyAlex)
		fresh.every("2026-07-01", "4w")
		fresh.pause("2026-07-10")
		fresh.resume("2026-07-20")
		fresh.retire("2026-08-01")
		fresh.every("2026-10-01", "2w")
		assertWindow(t, fresh.state(), "2026-10-01", "2026-10-15")
	})

	t.Run("a voided contact is ignored and the anchor falls back", func(t *testing.T) {
		l := newLedger(t, keySam)
		l.every("2026-10-01", "4w")
		c := l.contact("2026-10-15", "2026-10-15", "")
		l.void(c, "2026-10-16")
		st := l.state()
		assertWindow(t, st, "2026-10-01", "2026-10-29")
		assert.Nil(t, st.LastContact)

		two := newLedger(t, keyAlex)
		two.every("2026-10-01", "4w")
		two.contact("2026-10-05", "2026-10-05", ViaCall)
		later := two.contact("2026-10-15", "2026-10-15", ViaText)
		two.void(later, "2026-10-16")
		st = two.state()
		assertWindow(t, st, "2026-10-05", "2026-11-02")
		assert.Equal(t, &Contact{Day: "2026-10-05", Via: ViaCall}, st.LastContact)
	})
}

// TestFold_StateTransitions walks the whole lifecycle and pins which
// transitions ValidateTransition refuses, with the Ct-7/8/9 copy.
func TestFold_StateTransitions(t *testing.T) {
	l := newLedger(t, keyJordan)
	assert.Empty(t, Fold(l.events), "no events, no state")

	steps := []struct {
		write func()
		want  string
	}{
		{func() { l.every("2026-09-01", "3w") }, StateActive},
		{func() { l.pause("2026-09-10") }, StatePaused},
		{func() { l.resume("2026-09-20") }, StateActive},
		{func() { l.retire("2026-10-01") }, StateRetired},
		{func() { l.every("2026-10-10", "3w") }, StateActive},
	}
	for i, s := range steps {
		s.write()
		assert.Equalf(t, s.want, l.state().State, "after step %d", i+1)
	}

	st := func(s string) PersonState { return PersonState{State: s} }
	allowed := []struct{ state, action string }{
		{StateNone, ActionSet},
		{StateActive, ActionSet},
		{StatePaused, ActionSet},
		{StateRetired, ActionSet},
		{StateActive, ActionPause},
		{StatePaused, ActionResume},
		{StateActive, ActionRetire},
		{StatePaused, ActionRetire},
	}
	for _, a := range allowed {
		require.NoErrorf(t, ValidateTransition(st(a.state), a.action), "%s from %s", a.action, a.state)
	}
	refused := []struct {
		state, action string
		sentinel      error
	}{
		{StateNone, ActionPause, ErrNotActive},
		{StatePaused, ActionPause, ErrNotActive},
		{StateRetired, ActionPause, ErrNotActive},
		{StateNone, ActionResume, ErrNotPaused},
		{StateActive, ActionResume, ErrNotPaused},
		{StateRetired, ActionResume, ErrNotPaused},
		{StateNone, ActionRetire, ErrNoLiveCadence},
		{StateRetired, ActionRetire, ErrNoLiveCadence},
		{"", ActionPause, ErrNotActive},
	}
	for _, r := range refused {
		err := ValidateTransition(st(r.state), r.action)
		require.Errorf(t, err, "%s from %q must be refused", r.action, r.state)
		require.ErrorIs(t, err, r.sentinel)
		var te *TransitionError
		require.ErrorAs(t, err, &te)
	}
	require.Error(t, ValidateTransition(st(StateActive), "snooze"), "an unknown action is refused")

	msg := func(s, action string) string {
		var te *TransitionError
		require.ErrorAs(t, ValidateTransition(st(s), action), &te)
		return te.Message("Jordan Lee")
	}
	assert.Equal(t, "Jordan Lee's check-ins are already paused — nothing to pause.", msg(StatePaused, ActionPause))
	assert.Equal(t, "Jordan Lee has no check-in cadence to pause.", msg(StateNone, ActionPause))
	assert.Equal(t, "Jordan Lee's check-ins are retired — nothing to pause.", msg(StateRetired, ActionPause))
	assert.Equal(t, "Jordan Lee's check-ins aren't paused — nothing to resume.", msg(StateActive, ActionResume))
	assert.Equal(t, "Jordan Lee has no check-in cadence to resume.", msg(StateNone, ActionResume))
	assert.Equal(t, "Jordan Lee has no check-in cadence to retire.", msg(StateNone, ActionRetire))
	assert.Equal(t, "Jordan Lee has no check-in cadence to retire.", msg(StateRetired, ActionRetire))
	assert.Equal(t, "this person's check-ins aren't paused — nothing to resume.",
		ValidateTransition(st(StateActive), ActionResume).Error())

	// A transition the verbs would have refused (a hand-written line) is
	// ignored by the fold rather than corrupting the state.
	odd := newLedger(t, keyAlex)
	odd.pause("2026-09-01")
	odd.retire("2026-09-02")
	assert.Equal(t, StateNone, odd.state().State)
	odd.every("2026-09-03", "2w")
	odd.resume("2026-09-04")
	assertWindow(t, odd.state(), "2026-09-03", "2026-09-17")
}

// TestFoldVoid_ContactAndSnooze pins void semantics: a voided snooze stops
// hiding the person, a voided contact stops counting, and ValidateVoid refuses
// an unknown id, an already-voided event, a void and a cadence event.
func TestFoldVoid_ContactAndSnooze(t *testing.T) {
	l := newLedger(t, keyAlex)
	set := l.every("2026-10-19", "2w")
	s := l.snooze("2026-10-27")
	require.Equal(t, "contact_2026_10_27_a", s.ID)

	st := l.state()
	assert.Equal(t, map[string]bool{"2026-W44": true}, st.SnoozedWeeks)
	assertWindow(t, st, "2026-10-19", "2026-11-02") // a snooze leaves the window alone

	require.NoError(t, ValidateVoid(l.events, s.ID))
	v := l.void(s, "2026-10-28")
	require.Equal(t, "contact_2026_10_28_a", v.ID)
	st = l.state()
	assert.NotNil(t, st.SnoozedWeeks)
	assert.Empty(t, st.SnoozedWeeks, "a voided snooze no longer hides the person")
	assertWindow(t, st, "2026-10-19", "2026-11-02")

	c := l.contact("2026-10-26", "2026-10-26", ViaInPerson)
	assertWindow(t, l.state(), "2026-10-26", "2026-11-09")
	require.NoError(t, ValidateVoid(l.events, c.ID))
	l.void(c, "2026-10-28")
	st = l.state()
	assertWindow(t, st, "2026-10-19", "2026-11-02")
	assert.Nil(t, st.LastContact, "a voided contact no longer counts")

	// Refusals, each with its fixed copy.
	err := ValidateVoid(l.events, "contact_2026_10_29_z")
	require.ErrorIs(t, err, ErrVoidUnknown)
	require.EqualError(t, err, `no contact event "contact_2026_10_29_z"`)

	err = ValidateVoid(l.events, s.ID)
	require.ErrorIs(t, err, ErrVoidAlreadyVoided)
	require.EqualError(t, err, `"contact_2026_10_27_a" is already voided (by "contact_2026_10_28_a")`)

	err = ValidateVoid(l.events, v.ID)
	require.ErrorIs(t, err, ErrVoidOfVoid)
	require.EqualError(t, err, "a void can't itself be voided")

	err = ValidateVoid(l.events, set.ID)
	require.ErrorIs(t, err, ErrVoidCadence)
	require.EqualError(t, err, "a cadence event is never voided — change it with lucid contact every, pause, resume or retire")

	// The earliest void is the one named, even if a second slipped onto disk.
	l.void(s, "2026-10-29")
	err = ValidateVoid(l.events, s.ID)
	require.EqualError(t, err, `"contact_2026_10_27_a" is already voided (by "contact_2026_10_28_a")`)

	// A hand-written void naming a cadence event has no effect on the fold.
	l.void(set, "2026-10-29")
	assertWindow(t, l.state(), "2026-10-19", "2026-11-02")

	// A malformed line is not a known event.
	bad := NewContact(keyAlex, "2026-10-29", at("2026-10-29", 8), "", "")
	bad.ID, bad.Schema = "contact_2026_10_29_y", 2
	err = ValidateVoid(append(l.events, bad), bad.ID)
	require.ErrorIs(t, err, ErrVoidUnknown)
}

// TestFold_LastContactIncludesNoCadencePeople pins that contact is history even
// without a cadence: the person folds to none with their last contact set, and
// no window.
func TestFold_LastContactIncludesNoCadencePeople(t *testing.T) {
	l := newLedger(t, keyJordan)
	l.contact("2026-10-01", "2026-10-01", ViaCall)
	l.contact("2026-10-20", "2026-10-20", ViaText)
	l.contact("2026-10-12", "2026-10-21", "")

	st := l.state()
	assert.Equal(t, StateNone, st.State)
	assert.Equal(t, &Contact{Day: "2026-10-20", Via: ViaText}, st.LastContact)
	assert.Empty(t, st.Anchor)
	assert.Empty(t, st.Closes)
	assert.Equal(t, Window{}, st.Window)

	// Two contacts on the same day: the later write wins.
	l.contact("2026-10-20", "2026-10-22", ViaInPerson)
	assert.Equal(t, &Contact{Day: "2026-10-20", Via: ViaInPerson}, l.state().LastContact)
}

// TestFold_SkipsInvalidEventsAndKeepsPeopleApart pins that a malformed event is
// skipped, never a panic, and that people never share state.
func TestFold_SkipsInvalidEventsAndKeepsPeopleApart(t *testing.T) {
	sam := newLedger(t, keySam)
	sam.every("2026-10-01", "4w")
	alex := newLedger(t, keyAlex)
	alex.contact("2026-10-05", "2026-10-05", "")

	broken := NewCadence(keyJordan, "2026-10-01", at("2026-10-01", 9), ActionSet, Window{Text: "4", Days: 4})
	odd := NewContact(keyJordan, "2026-10-01", at("2026-10-01", 9), "email", "")

	all := append(append(append([]Event{}, sam.events...), alex.events...), broken, odd)
	states := Fold(all)
	require.Len(t, states, 2)
	assertWindow(t, states[keySam], "2026-10-01", "2026-10-29")
	assert.Equal(t, StateNone, states[keyAlex].State)
	assert.Equal(t, keyAlex, states[keyAlex].PersonKey)
	assert.Nil(t, states[keySam].LastContact)
	assert.Empty(t, Fold(nil))
}

// TestFold_OrdersCadenceByInstantAcrossDSTOffsets pins that cadence events
// order by the instant they were recorded, not their text: across the autumn
// offset change a pause at 01:30-04:00 precedes a resume at 01:10-05:00.
func TestFold_OrdersCadenceByInstantAcrossDSTOffsets(t *testing.T) {
	l := newLedger(t, keySam)
	l.every("2026-10-25", "2w")
	l.add(NewCadence(keySam, "2026-10-31", "2026-11-01T01:30:00-04:00", ActionPause, Window{}))
	l.add(NewCadence(keySam, "2026-10-31", "2026-11-01T01:10:00-05:00", ActionResume, Window{}))
	assertWindow(t, l.state(), "2026-10-31", "2026-11-14")
}
