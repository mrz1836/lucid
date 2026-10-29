package contact

import "strings"

// The fixed copy of the due view and the wrap (ADR-0014 §6, §9). Every surface
// that shows the lists renders through this file, so one deny-list test covers
// them all. The only status word is "due"; no line ever counts how late anyone
// is.
const (
	// HeadingDueThisWeek heads the people whose window closes this week.
	HeadingDueThisWeek = "Due this week"
	// HeadingStillOpen heads the gentle carry-over: windows that closed before
	// this Monday.
	HeadingStillOpen = "Still open from before"
	// HeadingComingUp heads the windows that close next week (read view only).
	HeadingComingUp = "Coming up next week"
	// HeadingBirthdays heads the birthdays observed this week.
	HeadingBirthdays = "Birthdays this week"
	// NoCadencesYet is the due view with no active cadence at all.
	NoCadencesYet = "No check-in cadences yet — set one with lucid contact every <name> 4w."
	// NobodyDue is the due view with cadences but nobody in the three groups.
	NobodyDue = "Nobody's due this week."
	// WrapOpening opens a wrap with someone still open.
	WrapOpening = "Still open this week — no pressure; anyone here rolls into Monday's list."
	// NobodyStillOpen is the whole wrap when both lists are empty.
	NobodyStillOpen = "Nobody's still open this week."
	// NotLoggedYet stands in for the last contact when none is logged.
	NotLoggedYet = "not logged yet"
)

// Date layouts for people: "Sep 28", "Dec 30, 2025", "Wed Nov 4".
const (
	shortLayout    = "Jan 2"
	yearLayout     = "Jan 2, 2006"
	birthdayLayout = "Mon Jan 2"
)

// RenderDueLine renders one person line (ADR-0014 §6), e.g.
// "Sam Rivera (every 4 weeks), last caught up Sep 28" or
// "Alex Kim (every 10 days), not logged yet". The year is added when the last
// contact falls in a year other than today's. It never states lateness.
func RenderDueLine(row DueRow, today string) string {
	human := row.Human
	if human == "" {
		human = Window{Text: row.Cadence, Days: row.CadenceDays}.Human()
	}
	last := NotLoggedYet
	if row.LastContact != "" {
		last = "last caught up " + shortDate(row.LastContact, today)
	}
	return row.DisplayName + " (" + human + "), " + last
}

// RenderBirthdayLine renders a birthday for the read view: "Jordan Lee — Wed Nov 4".
func RenderBirthdayLine(b BirthdayRow) string {
	return b.DisplayName + " — " + birthdayDate(b.Date)
}

// RenderDueText renders the human due view: the week heading, then the
// "Due this week", "Still open from before" and "Coming up next week" sections
// and the birthdays, each line prefixed "- ", with empty sections left out.
// With nobody in the three groups it says so — [NoCadencesYet] when no
// cadence is active, else [NobodyDue] — and still lists birthdays. There is no
// trailing newline.
func RenderDueText(v DueView) string {
	var parts []string
	if v.WeekStart != "" && v.WeekEnd != "" {
		parts = append(parts, "Week of "+shortDate(v.WeekStart, v.WeekStart)+" – "+shortDate(v.WeekEnd, v.WeekEnd))
	}
	groups := []section{
		{HeadingDueThisWeek, v.DueThisWeek},
		{HeadingStillOpen, v.StillOpen},
		{HeadingComingUp, v.ComingUp},
	}
	listed := renderSections(groups, v.Day)
	switch {
	case len(listed) > 0:
		parts = append(parts, listed...)
	case v.ActiveCadences == 0:
		parts = append(parts, NoCadencesYet)
	default:
		parts = append(parts, NobodyDue)
	}
	if len(v.Birthdays) > 0 {
		lines := make([]string, 0, len(v.Birthdays)+1)
		lines = append(lines, HeadingBirthdays)
		for _, b := range v.Birthdays {
			lines = append(lines, "- "+RenderBirthdayLine(b))
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// RenderWrapText renders the Sunday wrap (ADR-0014 §9): [WrapOpening], then
// the "Due this week" and "Still open from before" lists, empty ones left out.
// "Coming up next week" and birthdays never appear. When both lists are empty
// the whole wrap is [NobodyStillOpen]. There is no trailing newline.
func RenderWrapText(v DueView) string {
	listed := renderSections([]section{
		{HeadingDueThisWeek, v.DueThisWeek},
		{HeadingStillOpen, v.StillOpen},
	}, v.Day)
	if len(listed) == 0 {
		return NobodyStillOpen
	}
	return strings.Join(append([]string{WrapOpening}, listed...), "\n\n")
}

// section is one headed list of person lines.
type section struct {
	heading string
	rows    []DueRow
}

// renderSections renders each non-empty section as its heading followed by
// "- " lines, one string per section.
func renderSections(sections []section, today string) []string {
	var out []string
	for _, s := range sections {
		if len(s.rows) == 0 {
			continue
		}
		lines := make([]string, 0, len(s.rows)+1)
		lines = append(lines, s.heading)
		for _, row := range s.rows {
			lines = append(lines, "- "+RenderDueLine(row, today))
		}
		out = append(out, strings.Join(lines, "\n"))
	}
	return out
}

// RenderPersonLine renders the check-in line `lucid person <name>` shows
// (ADR-0014 §6): "Check-ins: every 4 weeks — last caught up 2026-09-28", the
// paused form "Check-ins: paused (every 4 weeks) — …", the retired form
// "Check-ins: retired — …", or "Last caught up 2026-09-28 (no check-in
// cadence)" for someone with contacts and no cadence. Days are YYYY-MM-DD, as
// the rest of the person view prints them. ok is false when there is nothing
// to say — no cadence and no contact that still counts — so the view adds no
// line.
func RenderPersonLine(st PersonState) (line string, ok bool) {
	last := NotLoggedYet
	if st.LastContact != nil {
		last = "last caught up " + st.LastContact.Day
	}
	switch st.State {
	case StateActive:
		return "Check-ins: " + st.Window.Human() + " — " + last, true
	case StatePaused:
		return "Check-ins: paused (" + st.Window.Human() + ") — " + last, true
	case StateRetired:
		return "Check-ins: retired — " + last, true
	}
	if st.LastContact == nil {
		return "", false
	}
	return "Last caught up " + st.LastContact.Day + " (no check-in cadence)", true
}

// RenderAck renders the inventory-only acknowledgement for a write that was
// just appended (ADR-0014 §4, §5): e is the stored event with its id, name the
// person's display name, after the person's folded state including e, and
// today the logical day of the write. It reports what was recorded and, for a
// cadence, when the window next closes — never an evaluation.
func RenderAck(e Event, name string, after PersonState, today string) string {
	id := " (" + e.ID + ")."
	switch e.Type {
	case TypeContact:
		return "Logged contact with " + name + " on " + e.LogicalDate + id
	case TypeSnooze:
		return "Snoozed " + name + " for this week" + id
	case TypeVoid:
		target, _ := e.Voids()
		return "Voided " + target + " for " + name + id
	}
	switch e.Action {
	case ActionSet:
		human := Window{Text: e.Window, Days: e.WindowDays}.Human()
		if after.State == StatePaused {
			return name + ": " + human + ", still paused" + id
		}
		return name + ": " + human + " — " + nextDue(after, today) + id
	case ActionPause:
		return name + ": check-ins paused" + id
	case ActionResume:
		return name + ": check-ins resumed, " + after.Window.Human() + " — " + nextDue(after, today) + id
	default:
		return name + ": check-ins retired" + id
	}
}

// nextDue renders when an active window closes: "next due by Nov 26", or, when
// it closed before today, "open now; they'll be on Monday's list".
func nextDue(st PersonState, today string) string {
	if st.Closes != "" && st.Closes < today {
		return "open now; they'll be on Monday's list"
	}
	return "next due by " + shortDate(st.Closes, today)
}

// shortDate renders a YYYY-MM-DD day as "Sep 28", adding the year
// ("Dec 30, 2025") when it differs from today's. An unreadable day is returned
// as is.
func shortDate(day, today string) string {
	t, ok := civilDate(day)
	if !ok {
		return day
	}
	if ref, ok := civilDate(today); ok && ref.Year() != t.Year() {
		return t.Format(yearLayout)
	}
	return t.Format(shortLayout)
}

// birthdayDate renders a YYYY-MM-DD day as "Wed Nov 4"; an unreadable day is
// returned as is.
func birthdayDate(day string) string {
	t, ok := civilDate(day)
	if !ok {
		return day
	}
	return t.Format(birthdayLayout)
}
