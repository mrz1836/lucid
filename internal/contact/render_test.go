package contact

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shameWords is the alphabetic half of the ethos deny-list (ADR-0014 §10): no
// contact surface may carry lateness, scoring or shame. Entries match as whole
// words (an optional plural "s" included), case-insensitively, so "template"
// or "latest" never trip them. This literal list lives only in tests.
var shameWords = regexp.MustCompile(`(?i)\b(overdue|late|behind|missed|failed|neglect|streak|score|forgot)s?\b`)

// shameHits returns every deny-list entry found in s: the whole-word entries,
// plus "%" and the multi-word phrases matched as case-insensitive substrings.
func shameHits(s string) []string {
	hits := shameWords.FindAllString(s, -1)
	lower := strings.ToLower(s)
	for _, p := range []string{"%", "days ago", "days past", "should have"} {
		if strings.Contains(lower, p) {
			hits = append(hits, p)
		}
	}
	return hits
}

// TestContactCopy_DenyListMatcher keeps the deny-list matcher honest: it
// catches each entry and lets ordinary words that merely contain one through.
func TestContactCopy_DenyListMatcher(t *testing.T) {
	for _, bad := range []string{
		"Sam Rivera is 3 days OVERDUE", "running late", "falling behind", "you missed Alex Kim",
		"failed", "neglected? no: neglect", "a 4-week streak", "streaks", "score: 9", "50%",
		"2 days ago", "4 days past the window", "you should have called", "forgot again",
	} {
		assert.NotEmpty(t, shameHits(bad), bad)
	}
	for _, ok := range []string{
		"template", "related", "latest", "translate", "plate", "Still open from before",
		"Nobody's due this week.", "no pressure; anyone here rolls into Monday's list.",
	} {
		assert.Empty(t, shameHits(ok), ok)
	}
}

// TestRenderDueLine_ExactCopyNoLatenessCount pins the person line (ADR-0014
// §6): cadence and last contact or "not logged yet", the year only for another
// year, and never anything about how long ago the window closed.
func TestRenderDueLine_ExactCopyNoLatenessCount(t *testing.T) {
	sam := DueRow{
		PersonKey: keySam, DisplayName: "Sam Rivera", Cadence: "4w", CadenceDays: 28,
		Human: "every 4 weeks", LastContact: "2026-09-28", WindowCloses: "2026-10-26",
	}
	assert.Equal(t, "Sam Rivera (every 4 weeks), last caught up Sep 28", RenderDueLine(sam, docToday))

	alex := DueRow{PersonKey: keyAlex, DisplayName: "Alex Kim", Cadence: "10d", CadenceDays: 10, WindowCloses: "2026-10-12"}
	assert.Equal(t, "Alex Kim (every 10 days), not logged yet", RenderDueLine(alex, docToday),
		"Human is recomputed from the cadence when a row carries none")

	jordan := DueRow{
		PersonKey: keyJordan, DisplayName: "Jordan Lee", Cadence: "2w", CadenceDays: 14,
		Human: "every 2 weeks", LastContact: "2025-12-30", WindowCloses: "2026-01-13",
	}
	assert.Equal(t, "Jordan Lee (every 2 weeks), last caught up Dec 30, 2025", RenderDueLine(jordan, "2026-03-04"))

	// Alex's window closed 23 days before today; the line says nothing of it.
	line := RenderDueLine(alex, docToday)
	for _, leak := range []string{"23", "Oct 12", "2026-10-12", "closed", "since"} {
		assert.NotContains(t, line, leak)
	}
}

// TestRenderDueText_GroupsAndEmptyStates pins the human due view: the usage
// doc's week exactly, and each empty state.
func TestRenderDueText_GroupsAndEmptyStates(t *testing.T) {
	states, people := docWeek(t)
	assert.Equal(t, `Week of Nov 2 – Nov 8

Due this week
- Sam Rivera (every 4 weeks), last caught up Oct 8

Still open from before
- Alex Kim (every 2 weeks), not logged yet

Coming up next week
- Jordan Lee (every 3 weeks), last caught up Oct 20

Birthdays this week
- Jordan Lee — Wed Nov 4`, RenderDueText(Due(states, people, docToday, false)))

	t.Run("no cadences at all", func(t *testing.T) {
		assert.Equal(t, "Week of Nov 2 – Nov 8\n\n"+
			"No check-in cadences yet — set one with lucid contact every <name> 4w.",
			RenderDueText(Due(nil, nil, docToday, false)))
	})

	t.Run("no cadences, birthdays still listed", func(t *testing.T) {
		people := index(info(keyJordan, "Jordan Lee", "1990-11-04"), info(keySam, "Sam Rivera", "1990-11-06"))
		assert.Equal(t, "Week of Nov 2 – Nov 8\n\n"+
			"No check-in cadences yet — set one with lucid contact every <name> 4w.\n\n"+
			"Birthdays this week\n- Jordan Lee — Wed Nov 4\n- Sam Rivera — Fri Nov 6",
			RenderDueText(Due(nil, people, docToday, false)))
	})

	t.Run("cadences, nobody in the groups", func(t *testing.T) {
		b := newBook(t)
		b.person(keyCasey).every(docToday, "4w")
		people := index(info(keyCasey, "Casey Park", "1990-11-07"))
		assert.Equal(t, "Week of Nov 2 – Nov 8\n\nNobody's due this week.\n\n"+
			"Birthdays this week\n- Casey Park — Sat Nov 7",
			RenderDueText(Due(b.fold(), people, docToday, false)))
	})

	t.Run("a single section, the rest left out", func(t *testing.T) {
		b := newBook(t)
		b.person(keyAlex).every("2026-10-14", "2w")
		v := Due(b.fold(), index(info(keyAlex, "Alex Kim", "")), docToday, false)
		assert.Equal(t, "Week of Nov 2 – Nov 8\n\nStill open from before\n- Alex Kim (every 2 weeks), not logged yet",
			RenderDueText(v))
	})

	t.Run("a week across two months", func(t *testing.T) {
		assert.True(t, strings.HasPrefix(RenderDueText(Due(nil, nil, "2026-10-28", false)), "Week of Oct 26 – Nov 1\n\n"))
		assert.True(t, strings.HasPrefix(RenderDueText(Due(nil, nil, "2029-12-31", false)), "Week of Dec 31 – Jan 6\n\n"))
	})
}

// TestRenderWrapText_TwoListsExact pins the Sunday wrap (ADR-0014 §9): the
// opening, then only "Due this week" and "Still open from before" — never
// "Coming up next week" or birthdays — or the single empty line.
func TestRenderWrapText_TwoListsExact(t *testing.T) {
	states, people := docWeek(t)
	want := `Still open this week — no pressure; anyone here rolls into Monday's list.

Due this week
- Sam Rivera (every 4 weeks), last caught up Oct 8

Still open from before
- Alex Kim (every 2 weeks), not logged yet`
	assert.Equal(t, want, RenderWrapText(Due(states, people, docToday, true)))
	assert.Equal(t, want, RenderWrapText(Due(states, people, docToday, false)),
		"a full view renders the same wrap: coming up and birthdays never appear")

	assert.Equal(t, "Nobody's still open this week.", RenderWrapText(Due(nil, nil, docToday, true)))

	b := newBook(t)
	b.person(keyJordan).every("2026-10-26", "2w") // closes Nov 9: coming up only
	assert.Equal(t, "Nobody's still open this week.",
		RenderWrapText(Due(b.fold(), index(info(keyJordan, "Jordan Lee", "1990-11-04")), docToday, false)))

	only := newBook(t)
	only.person(keySam).every("2026-10-08", "4w") // closes Nov 5: due only
	assert.Equal(t, "Still open this week — no pressure; anyone here rolls into Monday's list.\n\n"+
		"Due this week\n- Sam Rivera (every 4 weeks), not logged yet",
		RenderWrapText(Due(only.fold(), index(info(keySam, "Sam Rivera", "")), docToday, true)))
}

// personLineStates is one folded state per person-line form: active, paused,
// retired, contact without a cadence, a cadence with nothing logged, and the
// silent case — no cadence and no contact that still counts.
func personLineStates() map[string]PersonState {
	w := Window{Text: "4w", Days: 28}
	last := &Contact{Day: "2026-09-28", Via: ViaCall}
	return map[string]PersonState{
		"active":      {State: StateActive, Window: w, LastContact: last},
		"paused":      {State: StatePaused, Window: w, LastContact: last},
		"retired":     {State: StateRetired, Window: w, LastContact: last},
		"no cadence":  {State: StateNone, LastContact: last},
		"not logged":  {State: StateActive, Window: Window{Text: "10d", Days: 10}},
		"nothing yet": {State: StateNone},
	}
}

// TestRenderPersonLine_ExactCopy pins the check-in line `lucid person` shows
// (ADR-0014 §6): the cadence or its state and the last contact as a date, and
// no line at all when there is nothing to say.
func TestRenderPersonLine_ExactCopy(t *testing.T) {
	want := map[string]string{
		"active":     "Check-ins: every 4 weeks — last caught up 2026-09-28",
		"paused":     "Check-ins: paused (every 4 weeks) — last caught up 2026-09-28",
		"retired":    "Check-ins: retired — last caught up 2026-09-28",
		"no cadence": "Last caught up 2026-09-28 (no check-in cadence)",
		"not logged": "Check-ins: every 10 days — not logged yet",
	}
	for label, st := range personLineStates() {
		line, ok := RenderPersonLine(st)
		if label == "nothing yet" {
			assert.False(t, ok, label)
			assert.Empty(t, line, label)
			continue
		}
		assert.True(t, ok, label)
		assert.Equal(t, want[label], line, label)
	}
}

// lastEvent is the most recent event a ledger wrote.
func lastEvent(l *ledger) Event { return l.events[len(l.events)-1] }

// ack is one rendered acknowledgement and the copy it must equal.
type ack struct {
	label, got, want string
}

// ackFixtures writes one of every kind of event and renders its ack against
// the folded state that includes it.
func ackFixtures(t *testing.T) []ack {
	t.Helper()
	const day = "2026-10-29"
	b := newBook(t)
	var out []ack
	render := func(label string, l *ledger, name, today, want string) {
		e := lastEvent(l)
		out = append(out, ack{label, RenderAck(e, name, b.fold()[l.key], today), strings.ReplaceAll(want, "<id>", e.ID)})
	}

	sam := b.person(keySam)
	sam.contact(day, day, ViaCall)
	render("contact", sam, "Sam Rivera", day, "Logged contact with Sam Rivera on 2026-10-29 (contact_2026_10_29_a).")
	sam.every(day, "4w")
	render("every", sam, "Sam Rivera", day, "Sam Rivera: every 4 weeks — next due by Nov 26 (contact_2026_10_29_b).")
	sam.snooze(day)
	render("snooze", sam, "Sam Rivera", day, "Snoozed Sam Rivera for this week (<id>).")
	sam.void(sam.events[0], day)
	render("void", sam, "Sam Rivera", day, "Voided contact_2026_10_29_a for Sam Rivera (<id>).")

	alex := b.person(keyAlex)
	alex.contact("2026-09-01", day, "")
	alex.every(day, "2w") // anchored on Sep 1: closed Sep 15
	render("every, open now", alex, "Alex Kim", day, "Alex Kim: every 2 weeks — open now; they'll be on Monday's list (<id>).")

	jordan := b.person(keyJordan)
	jordan.every("2026-10-01", "2w")
	jordan.pause("2026-10-20")
	render("pause", jordan, "Jordan Lee", "2026-10-20", "Jordan Lee: check-ins paused (<id>).")
	jordan.every(day, "3w")
	render("every while paused", jordan, "Jordan Lee", day, "Jordan Lee: every 3 weeks, still paused (<id>).")
	jordan.resume("2026-10-30")
	render("resume", jordan, "Jordan Lee", "2026-10-30", "Jordan Lee: check-ins resumed, every 3 weeks — next due by Nov 20 (<id>).")

	casey := b.person(keyCasey)
	casey.every("2026-12-20", "4w")
	render("every, next year", casey, "Casey Park", "2026-12-20", "Casey Park: every 4 weeks — next due by Jan 17, 2027 (<id>).")
	casey.retire("2026-12-21")
	render("retire", casey, "Casey Park", "2026-12-21", "Casey Park: check-ins retired (<id>).")
	return out
}

// TestRenderAck_InventoryOnly pins every write's acknowledgement: what was
// recorded, the receipt id, and for a cadence when the window next closes.
func TestRenderAck_InventoryOnly(t *testing.T) {
	for _, a := range ackFixtures(t) {
		assert.Equal(t, a.want, a.got, a.label)
	}
}

// refusalCopy renders every refusal the package exports: the input refusals,
// each transition refusal, each snooze refusal and each void refusal.
func refusalCopy(t *testing.T) []string {
	t.Helper()
	out := []string{}
	for _, err := range []error{
		ErrInvalidWindow, ErrInvalidVia, ErrNoteNotOneLine,
		ErrNotActive, ErrNotPaused, ErrNoLiveCadence,
		ErrVoidUnknown, ErrVoidAlreadyVoided, ErrVoidOfVoid, ErrVoidCadence,
		ErrSnoozeNotActive, ErrSnoozedThisWeek,
	} {
		out = append(out, err.Error())
	}
	for _, action := range []string{ActionPause, ActionResume, ActionRetire} {
		for _, state := range []string{StateNone, StateActive, StatePaused, StateRetired} {
			err := ValidateTransition(PersonState{State: state}, action)
			var te *TransitionError
			if errors.As(err, &te) {
				out = append(out, te.Error(), te.Message("Sam Rivera"))
			}
		}
	}
	for _, se := range []*SnoozeError{{}, {Already: true}, {Already: true, ID: "contact_2026_11_03_a"}} {
		out = append(out, se.Error(), se.Message("Sam Rivera"))
	}

	b := newBook(t)
	sam := b.person(keySam)
	set := sam.every("2026-10-29", "4w")
	c := sam.contact("2026-10-29", "2026-10-29", "")
	v := sam.void(c, "2026-10-29")
	for _, target := range []string{"contact_2026_01_01_a", c.ID, v.ID, set.ID} {
		err := ValidateVoid(b.events(), target)
		require.Error(t, err, target)
		out = append(out, err.Error())
	}
	return out
}

// TestContactCopy_NoShameLanguage renders every contact surface — person lines,
// the due text and the wrap over full and empty fixtures, birthday lines, the
// fixed constants, every ack and every refusal, and the person-view check-in
// line — and asserts none carries the
// deny-list. The Monday post joins this test when it lands.
func TestContactCopy_NoShameLanguage(t *testing.T) {
	states, people := docWeek(t)
	full := Due(states, people, docToday, false)
	views := []DueView{
		full,
		Due(states, people, docToday, true),
		Due(nil, nil, docToday, false),
		Due(nil, nil, docToday, true),
		Due(states, people, "2026-12-07", false), // weeks on: everyone carried over
	}

	surfaces := []string{
		HeadingDueThisWeek, HeadingStillOpen, HeadingComingUp, HeadingBirthdays,
		NoCadencesYet, NobodyDue, WrapOpening, NobodyStillOpen, NotLoggedYet,
	}
	for _, v := range views {
		surfaces = append(surfaces, RenderDueText(v), RenderWrapText(v))
		for _, rows := range [][]DueRow{v.DueThisWeek, v.StillOpen, v.ComingUp} {
			for _, r := range rows {
				surfaces = append(surfaces, RenderDueLine(r, v.Day))
			}
		}
		for _, bd := range v.Birthdays {
			surfaces = append(surfaces, RenderBirthdayLine(bd))
		}
		b, err := json.Marshal(v)
		require.NoError(t, err)
		surfaces = append(surfaces, string(b))
	}
	for _, a := range ackFixtures(t) {
		surfaces = append(surfaces, a.got)
	}
	surfaces = append(surfaces, refusalCopy(t)...)
	for _, st := range personLineStates() {
		if line, ok := RenderPersonLine(st); ok {
			surfaces = append(surfaces, line)
		}
	}

	require.Greater(t, len(surfaces), 40, "every surface is rendered")
	for _, s := range surfaces {
		require.NotEmpty(t, s)
		assert.Empty(t, shameHits(s), "shame language in contact copy: %q", s)
	}
}
