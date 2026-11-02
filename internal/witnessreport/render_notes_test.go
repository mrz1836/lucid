package witnessreport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/router"
)

// shortWeekNotes is a synthetic, everyday notes body — an anonymous user's
// week, no names — of the size a week usually carries: it fits on the card.
const shortWeekNotes = "A heavy stretch at work this week; he still closed out every night, even the late ones.\n\n" +
	"What went well: the new morning routine held on four of five workdays, and a friend's call on Thursday clearly lifted him.\n\n" +
	"What he's carrying: a deadline at the end of the month and not much slack in the evenings.\n\n" +
	"What would help: a short message midweek, or an invitation to something outdoors on the weekend. " +
	"Ask him how the new routine is landing."

// weekDayNotes is a synthetic day-by-day read of an invented week, one
// paragraph a day, each its own line.
func weekDayNotes() string {
	days := []string{
		"Monday — a long day at work and a late train home. He still closed out the night on time and kept the evening walk short but real.",
		"Tuesday — the quietest day of the week. An early start, a proper lunch away from the desk, and the phone out of the bedroom early.",
		"Wednesday — the day it slipped. A meeting ran into the evening, dinner was late, and the night routine never happened. He logged the miss honestly the next morning.",
		"Thursday — a friend's call in the evening, the first in a while. It clearly lifted him; the rest of the night went to plan.",
		"Friday — tired, and said so. He skipped the late plans he had half agreed to and went to bed early instead, which was the right trade.",
		"Saturday — outdoors most of the day. A long walk in the morning, errands after, and an easy evening at home.",
		"Sunday — the weekly review, done properly: what held, what slipped, and one change for the week ahead.",
	}
	return strings.Join(days, "\n\n")
}

// weekSummaryNotes closes a synthetic read with what went well, what he is
// carrying, and what a friend could do this week.
const weekSummaryNotes = "What went well: six of seven nights closed out, the morning routine held on the workdays, and he kept the weekly review even after a rough Wednesday.\n\n" +
	"What he's carrying: a deadline at the end of the month, a schedule with very little slack, and the worry that one late meeting can undo a good week.\n\n" +
	"What would help: a short message midweek, an invitation to something outdoors on the weekend, and asking him how the new routine is landing rather than how the numbers look."

// longReflection is one synthetic paragraph written as a single line longer
// than a Discord field, so its overflow continues in a field marked
// continued.
func longReflection() string {
	sentences := []string{
		"Looking at the whole week rather than any single day, the pattern is clearer than it was a month ago.",
		"The hard days are the ones where work runs late and the evening gets compressed, and the routine is the first thing to go when that happens.",
		"What has changed is that a missed night no longer turns into a missed week: the next morning he notices, writes it down, and starts again without making a story out of it.",
		"That recovery is the real progress, more than any streak.",
		"The open question is how to protect the evenings when the end-of-month deadline lands, because the calendar will not get lighter on its own.",
		"A friend asking about that directly, and offering a walk or a meal on one of those evenings, would probably help more than encouragement about the numbers.",
		"He tends to say he is fine when asked in general terms, so a specific question works better than a general one.",
		"He also does better with a plan made a day ahead than with a last-minute invitation, so suggesting a time early in the week is kinder than texting the same evening.",
		"None of this is a crisis; it is an ordinary busy stretch, handled better than the last one, and the honest numbers show both the wobble and the recovery.",
		"The thing to watch is whether the routine survives two late nights in a row, which has not happened yet this month and would be the real test.",
		"If it does, that is worth saying out loud to him, because he is quicker to notice the misses than the saves.",
	}
	return strings.Join(sentences, " ")
}

// smallWinsNotes is a synthetic list of the week's small wins, one per line.
const smallWinsNotes = "Small wins this week:\n" +
	"- six nights closed out on time\n" +
	"- lunch away from the desk on three days\n" +
	"- the phone out of the bedroom every night but one\n" +
	"- one long walk outdoors on the weekend\n" +
	"- the weekly review done on Sunday, not skipped\n" +
	"- a call with a friend, the first in a while"

// lookingAheadNotes is a synthetic paragraph about the week ahead.
const lookingAheadNotes = "Looking ahead: the end-of-month deadline lands next week, so the evenings will be tight. " +
	"The plan is a fixed stop time on the two heaviest days, a walk before work instead of after, " +
	"and the weekly review moved to Saturday morning so Sunday stays free."

// loggingNotes is a synthetic paragraph squaring a thinly logged week with
// the read above it.
const loggingNotes = "A note on the numbers: only two days were logged this week, which reads worse than the week felt. " +
	"The logging slipped on the busy days, not the routine itself, and the two are easy to confuse from the outside. " +
	"Getting the daily log back to every day is the one change he picked for next week, and a friendly nudge on " +
	"Wednesday or Thursday, when it usually slips, would help it stick."

// notesWithDetailNotes is the body the notes-plus-anchor-detail golden
// carries: the day-by-day read, the single-line reflection, and the closing
// summary — long enough to spill off a card carrying a long warm read.
func notesWithDetailNotes() string {
	return weekDayNotes() + "\n\n" + longReflection() + "\n\n" + weekSummaryNotes
}

// longWeekNotes is the near-cap body the long-notes golden carries: the day
// read, the single-line reflection, the small wins, the week ahead, a note on
// the thin logging, and the closing summary.
func longWeekNotes() string {
	return weekDayNotes() + "\n\n" + longReflection() + "\n\n" + smallWinsNotes + "\n\n" +
		lookingAheadNotes + "\n\n" + loggingNotes + "\n\n" + weekSummaryNotes
}

// goldenDetailEvents is a synthetic full anchor detail for the report week
// (2026-07-07 → 2026-07-13), long enough, with the notes ahead of it, to
// continue past a second message.
func goldenDetailEvents() []router.AnchorDetailEvent {
	habit := func(label, typ, day, note string) router.AnchorDetailEvent {
		return router.AnchorDetailEvent{
			ID: "anchorev_" + strings.ReplaceAll(day, "-", "_") + "_" + label, Label: label,
			AnchorKind: engine.AnchorKindHabit, Type: typ, Day: day, Note: note,
		}
	}
	longNote := strings.TrimSpace(strings.Repeat("Late meeting again, so the evening was short; still went out for ten minutes. ", 14))
	confirm := habit("morning-walk", engine.AnchorEventConfirm, "2026-07-13", "")
	confirm.From = "2026-07-11"
	return []router.AnchorDetailEvent{
		habit("evening-stretch", engine.AnchorEventMiss, "2026-07-09", longNote),
		habit("evening-stretch", engine.AnchorEventMiss, "2026-07-10", "tired; skipped it and went to bed early"),
		habit("morning-walk", engine.AnchorEventMiss, "2026-07-08", "rain all morning"),
		habit("morning-walk", engine.AnchorEventMiss, "2026-07-09", longNote),
		habit("morning-walk", engine.AnchorEventMiss, "2026-07-10", longNote),
		habit("morning-walk", engine.AnchorEventMiss, "2026-07-11", longNote),
		habit("morning-walk", engine.AnchorEventMiss, "2026-07-12", longNote),
		confirm,
	}
}

// embedsJSON marshals a rendered batch to indented JSON — each embed in its
// Discord wire form — so a golden captures the exact bytes that would leave
// the machine, one message per array element.
func embedsJSON(t *testing.T, embeds []notify.Embed) string {
	t.Helper()
	b, err := json.MarshalIndent(embeds, "", "  ")
	require.NoError(t, err)
	return string(b)
}

// TestRender_WeekNotesGolden pins the embed batch and the markdown of a week
// carrying week notes to committed goldens: everyday notes that fit on the
// card; notes ahead of full anchor detail behind a long warm read, spilling
// into a continuation that carries both sections and on into one that carries
// only the anchor detail; and near-cap notes whose single long paragraph
// continues across fields and whose tail continues in a second message. In
// every case each section's fields reassemble to the markdown's text exactly.
func TestRender_WeekNotesGolden(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		golden   string
		prose    bool
		notes    string
		detail   []router.AnchorDetailEvent
		messages int
	}{
		{"notes_week", "strong-week", "notes-week", false, shortWeekNotes, nil, 1},
		{"notes_with_anchor_detail", "miss-heavy-week", "notes-detail-week", true, notesWithDetailNotes(), goldenDetailEvents(), 3},
		{"notes_long", "quiet-week", "notes-long", true, longWeekNotes(), nil, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, _ := buildFromFixture(t, tc.fixture+".json")
			if tc.prose {
				base.Narrative = longWarmRead()
				base.Faults = "A couple of hard days midweek, logged honestly and not explained away."
				base.Progress = "The routine recovered the next morning every time it slipped."
			}
			base.AnchorDetail = tc.detail
			r := withNotes(base, includedBody(t, tc.notes))

			embeds := RenderEmbeds(r)
			md := RenderMarkdown(r)
			checkGolden(t, tc.golden+".embeds.golden", embedsJSON(t, embeds))
			checkGolden(t, tc.golden+".md", md)

			require.Len(t, embeds, tc.messages)
			assert.Equal(t, r.Notes, reassembleSection(embeds, fieldWeekNotes), "the notes reassemble exactly")
			assert.Contains(t, md, "**"+fieldWeekNotes+"**\n"+r.Notes+"\n\n")
			if len(r.AnchorDetail) > 0 {
				assert.Equal(t, anchorDetailMarkdown(r.AnchorDetail), reassembleSection(embeds, fieldAnchorDetail))
			}
		})
	}
}
