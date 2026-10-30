package contact

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The remaining synthetic people (event_test.go holds Sam, Alex and Jordan).
const (
	keyCasey = "person_c-park"
	keyRiley = "person_r-moss"
)

// docToday is the Wednesday the usage doc's synthetic week is drawn on.
const docToday = "2026-11-04"

// book is a multi-person ledger: every person's events mint ids from one
// shared per-day slot map, the way the storage adapter mints them from one
// shard per day.
type book struct {
	t      *testing.T
	slots  map[string]int
	people []*ledger
}

func newBook(t *testing.T) *book {
	t.Helper()
	return &book{t: t, slots: map[string]int{}}
}

// person returns a ledger for key that writes into this book.
func (b *book) person(key string) *ledger {
	l := &ledger{t: b.t, key: key, slots: b.slots}
	b.people = append(b.people, l)
	return l
}

// events returns every person's events, in write order per person.
func (b *book) events() []Event {
	var out []Event
	for _, l := range b.people {
		out = append(out, l.events...)
	}
	return out
}

func (b *book) fold() map[string]PersonState { return Fold(b.events()) }

// info builds a live person record.
func info(key, name, dob string) PersonInfo {
	return PersonInfo{Key: key, DisplayName: name, Dob: dob}
}

// index keys PersonInfo by key.
func index(people ...PersonInfo) map[string]PersonInfo {
	out := make(map[string]PersonInfo, len(people))
	for _, p := range people {
		out[p.Key] = p
	}
	return out
}

// docWeek builds the synthetic week from docs/usage/contact.md, seen from
// Wednesday 2026-11-04:
//   - Alex Kim, every 2 weeks, set Oct 14, nothing logged: closed Oct 28 — still open;
//   - Sam Rivera, every 4 weeks, last caught up Oct 8: closes Nov 5 — due this week;
//   - Jordan Lee, every 3 weeks, last caught up Oct 20: closes Nov 10 — coming up,
//     and a birthday on Wed Nov 4;
//   - Casey Park, every 4 weeks, last caught up Oct 30: closes Nov 27 — later;
//   - Riley Moss, every 2 weeks, last caught up Oct 16: closed Oct 30 — snoozed this week.
func docWeek(t *testing.T) (map[string]PersonState, map[string]PersonInfo) {
	t.Helper()
	b := newBook(t)
	alex := b.person(keyAlex)
	alex.every("2026-10-14", "2w")
	sam := b.person(keySam)
	sam.contact("2026-10-08", "2026-10-08", ViaCall)
	sam.every("2026-10-10", "4w")
	jordan := b.person(keyJordan)
	jordan.every("2026-10-01", "3w")
	jordan.contact("2026-10-20", "2026-10-20", ViaText)
	casey := b.person(keyCasey)
	casey.every("2026-10-01", "4w")
	casey.contact("2026-10-30", "2026-10-30", ViaInPerson)
	riley := b.person(keyRiley)
	riley.every("2026-10-01", "2w")
	riley.contact("2026-10-16", "2026-10-16", ViaVideo)
	riley.snooze("2026-11-03")
	people := index(
		info(keyAlex, "Alex Kim", ""),
		info(keySam, "Sam Rivera", ""),
		info(keyJordan, "Jordan Lee", "1990-11-04"),
		info(keyCasey, "Casey Park", ""),
		info(keyRiley, "Riley Moss", ""),
	)
	return b.fold(), people
}

// keysOf lists a group's person keys in order.
func keysOf(rows []DueRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.PersonKey)
	}
	return out
}

// grouped reports which group a person landed in: "due", "open", "coming" or
// "" when absent.
func grouped(v DueView, key string) string {
	for name, rows := range map[string][]DueRow{"due": v.DueThisWeek, "open": v.StillOpen, "coming": v.ComingUp} {
		for _, r := range rows {
			if r.PersonKey == key {
				return name
			}
		}
	}
	return ""
}

// activeState is an active weekly cadence whose window closes on closes.
func activeState(t *testing.T, key, closes string) PersonState {
	t.Helper()
	w := mustWindow(t, "1w")
	return PersonState{
		PersonKey:    key,
		State:        StateActive,
		Window:       w,
		Anchor:       addDays(closes, -w.Days),
		Closes:       closes,
		SnoozedWeeks: map[string]bool{},
	}
}

// TestDue_ThreeGroups pins the three groups on the usage doc's synthetic week:
// each person lands in exactly the documented group, a later window and a
// snoozed person are absent, and a contact-only person is never grouped.
func TestDue_ThreeGroups(t *testing.T) {
	states, people := docWeek(t)
	v := Due(states, people, docToday, false)

	assert.Equal(t, docToday, v.Day)
	assert.Equal(t, "2026-W45", v.Week)
	assert.Equal(t, "2026-11-02", v.WeekStart)
	assert.Equal(t, "2026-11-08", v.WeekEnd)
	assert.False(t, v.Wrap)

	assert.Equal(t, []string{keySam}, keysOf(v.DueThisWeek))
	assert.Equal(t, []string{keyAlex}, keysOf(v.StillOpen))
	assert.Equal(t, []string{keyJordan}, keysOf(v.ComingUp))
	assert.Empty(t, grouped(v, keyCasey), "a window closing after next Sunday is not shown")
	assert.Empty(t, grouped(v, keyRiley), "a person snoozed this week is not shown")
	assert.Equal(t, 5, v.ActiveCadences)

	sam := v.DueThisWeek[0]
	assert.Equal(t, DueRow{
		PersonKey: keySam, DisplayName: "Sam Rivera", Cadence: "4w", CadenceDays: 28,
		Human: "every 4 weeks", LastContact: "2026-10-08", WindowCloses: "2026-11-05",
	}, sam)
	alex := v.StillOpen[0]
	assert.Empty(t, alex.LastContact, "nothing logged yet")
	assert.Equal(t, "2026-10-28", alex.WindowCloses)

	require.Len(t, v.Birthdays, 1)
	assert.Equal(t, BirthdayRow{PersonKey: keyJordan, DisplayName: "Jordan Lee", Date: "2026-11-04"}, v.Birthdays[0])

	// A person with contacts and no cadence has history but no due effect.
	b := newBook(t)
	b.person(keySam).contact("2026-09-01", "2026-09-01", "")
	none := Due(b.fold(), index(info(keySam, "Sam Rivera", "")), docToday, false)
	assert.Empty(t, grouped(none, keySam))
	assert.Zero(t, none.ActiveCadences)
}

// TestDue_WeekBoundaries pins the Monday–Sunday edges, seen from Wednesday
// 2026-11-04 and from that week's own Monday and Sunday: a close on this
// Monday or Sunday is due this week, next Monday and next Sunday are coming
// up, the previous Sunday is still open, and the Monday after next is absent.
func TestDue_WeekBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		closes string
		want   string
	}{
		{"previous Sunday is still open", "2026-11-01", "open"},
		{"this Monday is due this week", "2026-11-02", "due"},
		{"exactly this Sunday is due this week", "2026-11-08", "due"},
		{"next Monday is coming up", "2026-11-09", "coming"},
		{"next Sunday is coming up", "2026-11-15", "coming"},
		{"after next Sunday is absent", "2026-11-16", ""},
	}
	people := index(info(keySam, "Sam Rivera", ""))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			states := map[string]PersonState{keySam: activeState(t, keySam, tc.closes)}
			for _, today := range []string{"2026-11-02", docToday, "2026-11-08"} {
				v := Due(states, people, today, false)
				assert.Equal(t, "2026-11-02", v.WeekStart, today)
				assert.Equal(t, "2026-11-08", v.WeekEnd, today)
				assert.Equal(t, tc.want, grouped(v, keySam), "today %s", today)
			}
		})
	}

	// The wrap keeps the same two lists and never shows coming up.
	states := map[string]PersonState{keySam: activeState(t, keySam, "2026-11-09")}
	assert.Empty(t, grouped(Due(states, people, docToday, true), keySam))
}

// TestDue_PausedRetiredSnoozedAbsent pins who is grouped: only active,
// unsnoozed people. A pause, a retire or a snooze never hides a birthday.
func TestDue_PausedRetiredSnoozedAbsent(t *testing.T) {
	b := newBook(t)
	b.person(keySam).every("2026-10-29", "1w") // closes Nov 5: due
	alex := b.person(keyAlex)
	alex.every("2026-10-29", "1w")
	alex.pause("2026-10-30")
	jordan := b.person(keyJordan)
	jordan.every("2026-10-20", "1w")
	jordan.retire("2026-10-25")
	casey := b.person(keyCasey)
	casey.every("2026-10-29", "1w")
	casey.snooze("2026-11-03")
	b.person(keyRiley).contact("2026-11-01", "2026-11-01", "")

	people := index(
		info(keySam, "Sam Rivera", ""),
		info(keyAlex, "Alex Kim", ""),
		info(keyJordan, "Jordan Lee", "1985-11-06"),
		info(keyCasey, "Casey Park", "1992-11-07"),
		info(keyRiley, "Riley Moss", ""),
	)
	v := Due(b.fold(), people, docToday, false)

	assert.Equal(t, []string{keySam}, keysOf(v.DueThisWeek))
	assert.Empty(t, v.StillOpen)
	assert.Empty(t, v.ComingUp)
	for _, key := range []string{keyAlex, keyJordan, keyCasey, keyRiley} {
		assert.Empty(t, grouped(v, key), key)
	}
	assert.Equal(t, 2, v.ActiveCadences, "active cadences count snoozed people, not paused or retired ones")
	assert.Equal(t, []BirthdayRow{
		{PersonKey: keyJordan, DisplayName: "Jordan Lee", Date: "2026-11-06"},
		{PersonKey: keyCasey, DisplayName: "Casey Park", Date: "2026-11-07"},
	}, v.Birthdays)
}

// TestDue_OffLimitsExcluded pins the off-limits rule: an off-limits person with
// an active cadence and a birthday this week appears in no group, no birthday
// and no count — in the full view and the wrap alike.
func TestDue_OffLimitsExcluded(t *testing.T) {
	b := newBook(t)
	b.person(keySam).every("2026-10-29", "1w")
	b.person(keyAlex).every("2026-10-29", "1w")
	states := b.fold()

	sam := info(keySam, "Sam Rivera", "1990-11-04")
	sam.OffLimits = true
	people := index(sam, info(keyAlex, "Alex Kim", ""))

	for _, wrap := range []bool{false, true} {
		v := Due(states, people, docToday, wrap)
		assert.Empty(t, grouped(v, keySam), "wrap=%v", wrap)
		assert.Equal(t, []string{keyAlex}, keysOf(v.DueThisWeek), "wrap=%v", wrap)
		assert.Empty(t, v.Birthdays, "wrap=%v", wrap)
		assert.Equal(t, 1, v.ActiveCadences, "wrap=%v", wrap)
	}

	// A state with no live record (no PersonInfo) is never shown either.
	delete(people, keyAlex)
	assert.Empty(t, Due(states, people, docToday, false).DueThisWeek)
}

// TestDue_SnoozeHidesCurrentWeekOnly pins the one-week snooze: written on a
// Tuesday it hides the person for that whole Monday–Sunday week; the next
// Monday they are back, with the window and anchor untouched. A voided snooze
// stops hiding them at once.
func TestDue_SnoozeHidesCurrentWeekOnly(t *testing.T) {
	b := newBook(t)
	sam := b.person(keySam)
	sam.contact("2026-10-08", "2026-10-08", "")
	sam.every("2026-10-10", "4w") // closes Thu Nov 5
	before := b.fold()[keySam]
	snooze := sam.snooze("2026-11-03") // Tuesday
	states := b.fold()
	people := index(info(keySam, "Sam Rivera", ""))

	assert.Equal(t, before.Anchor, states[keySam].Anchor, "a snooze leaves the anchor alone")
	assert.Equal(t, before.Closes, states[keySam].Closes, "a snooze leaves the window alone")

	for _, today := range []string{"2026-11-02", "2026-11-03", docToday, "2026-11-05", "2026-11-08"} {
		v := Due(states, people, today, false)
		assert.Empty(t, grouped(v, keySam), "snoozed all week, %s", today)
		assert.Empty(t, grouped(Due(states, people, today, true), keySam), "snoozed in the wrap, %s", today)
	}

	next := Due(states, people, "2026-11-09", false)
	require.Equal(t, []string{keySam}, keysOf(next.StillOpen), "back the next Monday")
	assert.Equal(t, "2026-11-05", next.StillOpen[0].WindowCloses, "with the same close date")

	sam.void(snooze, docToday)
	assert.Equal(t, "due", grouped(Due(b.fold(), people, docToday, false), keySam), "a voided snooze no longer hides")
}

// TestDue_UncheckedRollsForwardToStillOpen pins the carry-over: someone due on
// Monday d who is not reached shows under "still open" on Monday d+7 and keeps
// rolling forward, with no special handling; a contact clears them.
func TestDue_UncheckedRollsForwardToStillOpen(t *testing.T) {
	b := newBook(t)
	sam := b.person(keySam)
	sam.every("2026-10-26", "2w") // closes Mon Nov 9
	people := index(info(keySam, "Sam Rivera", ""))

	assert.Equal(t, "due", grouped(Due(b.fold(), people, "2026-11-09", false), keySam))
	assert.Equal(t, "open", grouped(Due(b.fold(), people, "2026-11-16", false), keySam))
	assert.Equal(t, "open", grouped(Due(b.fold(), people, "2026-11-23", false), keySam))

	sam.contact("2026-11-18", "2026-11-18", "") // closes Wed Dec 2
	assert.Empty(t, grouped(Due(b.fold(), people, "2026-11-18", false), keySam), "cleared by the contact")
	assert.Equal(t, "coming", grouped(Due(b.fold(), people, "2026-11-23", false), keySam))
	assert.Equal(t, "due", grouped(Due(b.fold(), people, "2026-11-30", false), keySam))
}

// TestDue_OrderCloseThenNameThenKey pins each group's order: close date, then
// case-folded display name, then person key — stable across map iteration.
func TestDue_OrderCloseThenNameThenKey(t *testing.T) {
	const (
		keyLowerAlex = "person_z-kim"
		keyJordanA   = "person_j-lee-a"
		keyJordanB   = "person_j-lee-b"
	)
	states := map[string]PersonState{
		keyRiley:     activeState(t, keyRiley, "2026-11-03"),
		keySam:       activeState(t, keySam, "2026-11-04"),
		keyCasey:     activeState(t, keyCasey, "2026-11-05"),
		keyLowerAlex: activeState(t, keyLowerAlex, "2026-11-05"),
		keyJordanB:   activeState(t, keyJordanB, "2026-11-05"),
		keyJordanA:   activeState(t, keyJordanA, "2026-11-05"),
	}
	people := index(
		info(keyRiley, "Riley Moss", ""),
		info(keySam, "Sam Rivera", ""),
		info(keyCasey, "Casey Park", ""),
		info(keyLowerAlex, "alex kim", ""),
		info(keyJordanB, "Jordan Lee", ""),
		info(keyJordanA, "Jordan Lee", ""),
	)
	want := []string{keyRiley, keySam, keyLowerAlex, keyCasey, keyJordanA, keyJordanB}
	first, err := json.Marshal(Due(states, people, docToday, false))
	require.NoError(t, err)
	for range 20 {
		v := Due(states, people, docToday, false)
		assert.Equal(t, want, keysOf(v.DueThisWeek))
		again, err := json.Marshal(v)
		require.NoError(t, err)
		assert.Equal(t, string(first), string(again), "byte-deterministic")
	}
}

// TestDue_DSTWeekStable pins that a US DST change cannot move a day: across
// both 2026 change weeks, every local instant from Monday to Sunday reads the
// same week and the same groups.
func TestDue_DSTWeekStable(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tz database unavailable: %v", err)
	}
	weeks := []struct {
		name           string
		monday, sunday string
	}{
		{"spring forward", "2026-03-02", "2026-03-08"},
		{"fall back", "2026-10-26", "2026-11-01"},
	}
	for _, wk := range weeks {
		t.Run(wk.name, func(t *testing.T) {
			states := map[string]PersonState{
				keySam:    activeState(t, keySam, wk.sunday),                 // the change Sunday: due
				keyAlex:   activeState(t, keyAlex, addDays(wk.sunday, 1)),    // next Monday: coming up
				keyJordan: activeState(t, keyJordan, addDays(wk.monday, -1)), // previous Sunday: still open
			}
			people := index(info(keySam, "Sam Rivera", ""), info(keyAlex, "Alex Kim", ""), info(keyJordan, "Jordan Lee", ""))
			var ref []byte
			for d := 0; d < 7; d++ {
				civil, ok := civilDate(addDays(wk.monday, d))
				require.True(t, ok)
				for _, hour := range []int{5, 12, 23} {
					at := time.Date(civil.Year(), civil.Month(), civil.Day(), hour, 30, 0, 0, loc)
					day := LogicalDay(at)
					require.Equal(t, civil.Format(dateLayout), day, "logical day of %s", at)
					v := Due(states, people, day, false)
					assert.Equal(t, wk.monday, v.WeekStart)
					assert.Equal(t, wk.sunday, v.WeekEnd)
					assert.Equal(t, "due", grouped(v, keySam))
					assert.Equal(t, "coming", grouped(v, keyAlex))
					assert.Equal(t, "open", grouped(v, keyJordan))
					v.Day = ""
					b, err := json.Marshal(v)
					require.NoError(t, err)
					if ref == nil {
						ref = b
					}
					assert.Equal(t, string(ref), string(b), "the same view all week")
				}
			}
		})
	}
}

// TestDueView_JSONContract pins the documented --json shape (ADR-0014 §6):
// key order, last_contact null when nothing is logged, arrays never null, the
// wrap dropping coming_up and birthdays, and a byte-stable round trip.
func TestDueView_JSONContract(t *testing.T) {
	states, people := docWeek(t)

	// Byte-exact compares throughout: key order is part of the contract.
	full, err := json.Marshal(Due(states, people, docToday, false))
	require.NoError(t, err)
	wantFull := `{"day":"2026-11-04","week":"2026-W45","week_start":"2026-11-02","week_end":"2026-11-08","wrap":false,` +
		`"due_this_week":[{"person_key":"person_s-rivera","display_name":"Sam Rivera","cadence":"4w","cadence_days":28,"last_contact":"2026-10-08","window_closes":"2026-11-05"}],` +
		`"still_open":[{"person_key":"person_a-kim","display_name":"Alex Kim","cadence":"2w","cadence_days":14,"last_contact":null,"window_closes":"2026-10-28"}],` +
		`"coming_up":[{"person_key":"person_j-lee","display_name":"Jordan Lee","cadence":"3w","cadence_days":21,"last_contact":"2026-10-20","window_closes":"2026-11-10"}],` +
		`"birthdays":[{"person_key":"person_j-lee","display_name":"Jordan Lee","date":"2026-11-04"}]}`
	assert.Equal(t, wantFull, string(full))

	wrap, err := json.Marshal(Due(states, people, docToday, true))
	require.NoError(t, err)
	wantWrap := `{"day":"2026-11-04","week":"2026-W45","week_start":"2026-11-02","week_end":"2026-11-08","wrap":true,` +
		`"due_this_week":[{"person_key":"person_s-rivera","display_name":"Sam Rivera","cadence":"4w","cadence_days":28,"last_contact":"2026-10-08","window_closes":"2026-11-05"}],` +
		`"still_open":[{"person_key":"person_a-kim","display_name":"Alex Kim","cadence":"2w","cadence_days":14,"last_contact":null,"window_closes":"2026-10-28"}]}`
	assert.Equal(t, wantWrap, string(wrap))

	empty, err := json.Marshal(Due(nil, nil, docToday, false))
	require.NoError(t, err)
	wantEmpty := `{"day":"2026-11-04","week":"2026-W45","week_start":"2026-11-02","week_end":"2026-11-08","wrap":false,` +
		`"due_this_week":[],"still_open":[],"coming_up":[],"birthdays":[]}`
	assert.Equal(t, wantEmpty, string(empty))

	// A zero view still shows empty arrays, never null.
	zero, err := json.Marshal(DueView{Day: docToday})
	require.NoError(t, err)
	assert.Contains(t, string(zero), `"due_this_week":[],"still_open":[],"coming_up":[],"birthdays":[]`)

	var back DueView
	require.NoError(t, json.Unmarshal(full, &back))
	assert.Equal(t, "every 4 weeks", back.DueThisWeek[0].Human, "a decoded row renders like the original")
	again, err := json.Marshal(back)
	require.NoError(t, err)
	assert.Equal(t, string(full), string(again), "round trip is byte-stable")

	// An unreadable day is an empty view, never a panic.
	bad := Due(states, people, "not-a-day", false)
	assert.Empty(t, bad.Week)
	assert.Empty(t, bad.DueThisWeek)
	assert.NotNil(t, bad.Birthdays)
}

// TestValidateSnooze_ActiveOncePerWeek pins the Ct-10 refusals: a snooze needs
// an active cadence and is refused a second time in the same ISO week, but is
// allowed again the next week.
func TestValidateSnooze_ActiveOncePerWeek(t *testing.T) {
	b := newBook(t)
	sam := b.person(keySam)
	require.ErrorIs(t, ValidateSnooze(b.fold()[keySam], docToday), ErrSnoozeNotActive, "no cadence")

	sam.every("2026-10-26", "2w")
	require.NoError(t, ValidateSnooze(b.fold()[keySam], docToday))
	sam.snooze("2026-11-03")

	err := ValidateSnooze(b.fold()[keySam], docToday)
	require.ErrorIs(t, err, ErrSnoozedThisWeek)
	var se *SnoozeError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, "Sam Rivera is already snoozed this week.", se.Message("Sam Rivera"))
	se.ID = "contact_2026_11_03_a"
	assert.Equal(t, "Sam Rivera is already snoozed this week (contact_2026_11_03_a).", se.Message("Sam Rivera"),
		"a caller that knows the snooze names its receipt (error-states Ct-10)")
	require.NoError(t, ValidateSnooze(b.fold()[keySam], "2026-11-09"), "next week is a new week")

	sam.pause(docToday)
	err = ValidateSnooze(b.fold()[keySam], "2026-11-09")
	require.ErrorIs(t, err, ErrSnoozeNotActive, "a paused cadence")
	require.ErrorAs(t, err, &se)
	assert.Equal(t, "Sam Rivera has no active check-in cadence to snooze.", se.Message("Sam Rivera"))
}
