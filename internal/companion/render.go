package companion

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// This file owns the companion's message *layout*. The split is deliberate:
// Lucid renders the whole sectioned scaffold deterministically — the header, the
// status panel, the context sections, the freshness labels, and the ordering —
// while the model fills only two short slots dropped into fixed places (an
// interpretation and one or two next actions). Because the layout is
// code, not prose, the readability contract is unit-testable and a model can
// never produce a wall of numbers or restate a raw metric: it never owns the
// structure. See docs/usage/companion.md §"The message scaffold".

// Scaffold literals. These are the fixed structural tokens the layout is built
// from — a chat surface renders markdown tables as raw text, so the scaffold
// uses bullets and key/value lines. Both windows omit horizontal divider chrome
// so the delivered card stays compact on a phone.
const (
	bulletMark = "•"
	// dateHeaderFmt renders the header day ("Monday, Jul 20").
	dateHeaderFmt = "Monday, Jan 2"
	// logicalDateFmt parses an event's stored logical date ("2026-07-19") for
	// the staleness comparison. It matches engine.DateString's on-disk form.
	logicalDateFmt = "2006-01-02"
	// staleAfterDays is the freshness threshold: a section whose most-recent
	// event is strictly older than this many civil days carries a `stale` flag.
	staleAfterDays = 2
)

// The two model-slot delimiter labels. The model returns exactly these two
// labeled slots, each introduced by its delimiter on its own line; everything
// else in the message is Lucid's. Kept as package constants so the renderer and
// the compose prompt name the identical tokens.
const (
	interpDelim  = "%%INTERPRETATION%%"
	actionsDelim = "%%ACTIONS%%"
)

// Header emojis and labels per window.
const (
	emojiMorning = "☀️"
	emojiNight   = "🌙"
	labelMorning = "Morning"
	labelNight   = "Night"
)

// Section is one context group in the scaffold — a `{Emoji} **{Label}** ·
// {Meta}` header over `•` bullet lines. Meta carries the "as logged <date>"
// freshness stamp (and a `stale` flag when the group's newest event is stale).
// A Section with no Lines is omitted from the rendered message.
type Section struct {
	Emoji string
	Label string
	Meta  string
	Lines []string
}

// Briefing is the fully-resolved, deterministic value the renderer turns into
// one companion message. Everything here is decided by Lucid code except the two
// model slots — Interpretation and Actions — which the model fills. Panel is the
// compact status panel (see buildStatusPanel); Sections are the enrichment
// groups; Verdict is the Engine's deterministic miss-day line, appended
// byte-for-byte as the final group when present.
type Briefing struct {
	Mode           Mode
	Date           time.Time
	Panel          []string
	Sections       []Section
	Interpretation string
	Actions        []string
	Verdict        string
}

// Render turns a Briefing into the final Discord message. It is pure and
// byte-stable: the same Briefing always renders the identical bytes, which is
// what makes the readability contract testable. Major groups are joined by
// blank lines only. Empty groups are dropped, so a message with no enrichment or
// no model slots still reads cleanly (no dangling structural chrome).
//
// The region order differs by window. Morning is forward-looking — the status
// panel is the hero, then the day's context, then the read.
// Night is a close-out ritual — the day's read-back (the context sections)
// leads, then the numbers, then the single close-out action. Night deliberately
// suppresses the interpretation slot so it does not read
// like a second morning memo. The Engine verdict, when present, is always the
// last group in both.
func Render(b Briefing) string {
	header := renderHeader(b)
	panel := strings.Join(b.Panel, "\n")
	sections := renderSections(b.Sections)

	interp := ""
	if strings.TrimSpace(b.Interpretation) != "" {
		interp = interpHeader(b.Mode) + "\n" + strings.TrimSpace(b.Interpretation)
	}

	next := ""
	if len(b.Actions) > 0 {
		next = renderActions(b.Mode, b.Actions)
	}

	verdict := strings.TrimSpace(b.Verdict)

	var order []string
	separator := "\n\n"
	if b.Mode == ModeNight {
		// Close-out ordering: read-back (sections) leads, the numbers follow,
		// and the user's single close-out action ends the ritual. The model's
		// interpretation slot is intentionally not rendered at night.
		order = []string{header, sections, panel, next, verdict}
	} else {
		// Forward-looking ordering: the status panel is the hero. Morning renders actions only as a routine cue, never as a generic
		// invented Next section.
		order = []string{header, panel, sections, interp, next, verdict}
	}

	groups := make([]string, 0, len(order))
	for _, g := range order {
		if strings.TrimSpace(g) != "" {
			groups = append(groups, g)
		}
	}
	return strings.Join(groups, separator)
}

// renderHeader renders the window header — `{emoji} **{Label}** · {Weekday,
// Mon D}`.
func renderHeader(b Briefing) string {
	emoji, label := emojiMorning, labelMorning
	if b.Mode == ModeNight {
		emoji, label = emojiNight, labelNight
	}
	return fmt.Sprintf("%s **%s** · %s", emoji, label, b.Date.Format(dateHeaderFmt))
}

// renderSections renders the context groups, one blank line apart. A section
// with no bullet lines is omitted — the graceful-omit rule keeps an empty
// signal group (nothing logged in the window) from leaving a stray header.
// Returns "" when no section has data, so Render drops the whole group.
func renderSections(sections []Section) string {
	blocks := make([]string, 0, len(sections))
	for _, s := range sections {
		if len(s.Lines) == 0 {
			continue
		}
		blocks = append(blocks, renderSection(s))
	}
	return strings.Join(blocks, "\n\n")
}

// renderSection renders one context group: a `{emoji} **{Label}** · {meta}`
// header over `•` bullet lines. The emoji and the ` · {meta}` are omitted when
// absent so a section without a freshness stamp still renders cleanly.
func renderSection(s Section) string {
	var b strings.Builder
	if strings.TrimSpace(s.Emoji) != "" {
		b.WriteString(s.Emoji)
		b.WriteString(" ")
	}
	b.WriteString("**")
	b.WriteString(s.Label)
	b.WriteString("**")
	if strings.TrimSpace(s.Meta) != "" {
		b.WriteString(" · ")
		b.WriteString(s.Meta)
	}
	for _, ln := range s.Lines {
		b.WriteString("\n")
		b.WriteString(bulletMark)
		b.WriteString(" ")
		b.WriteString(ln)
	}
	return b.String()
}

// renderActions renders the next-move slot — the mode's action header over `•`
// bullet lines, one per concrete action.
func renderActions(mode Mode, actions []string) string {
	var b strings.Builder
	b.WriteString(nextHeader(mode))
	for _, a := range actions {
		b.WriteString("\n")
		b.WriteString(bulletMark)
		b.WriteString(" ")
		b.WriteString(strings.TrimSpace(a))
	}
	return b.String()
}

// interpHeader is the interpretation-slot header. Night currently suppresses
// the interpretation slot in Render, but this remains defined for parser/render
// contract completeness.
func interpHeader(mode Mode) string {
	if mode == ModeNight {
		return "🌙 **The read**"
	}
	return "🧭 **The read**"
}

// nextHeader is the next-move-slot header, framed for the window: the morning
// "next" points forward; the night "close-out" ends the day.
func nextHeader(mode Mode) string {
	if mode == ModeNight {
		return "🌒 **Close-out**"
	}
	return "🌅 **Morning routine**"
}

// buildStatusPanel renders the compact status panel from the engine
// projections — the small, meaningful hero block that replaces the old
// wall-of-numbers dump. It is at most four lines: the streak+adherence line and
// the error-budget line always, then the consecutive-miss and standing-storm
// ambient lines only when they hold. Every number is copied from the projection;
// the days-to-gate co-number rides the budget line. The longer gate rollups and
// the per-anchor days-since lines the full metrics surface carries are
// deliberately left out — the panel is a status glance, not the metrics dump.
func buildStatusPanel(m engine.Metrics, st engine.Status) []string {
	lines := []string{
		panelChainLine(m),
		panelBudgetLine(m.ErrorBudget, st.DaysToNextGate),
	}
	if st.ConsecutiveMisses > 0 {
		lines = append(lines, fmt.Sprintf("⚠️ Consecutive misses · %d", st.ConsecutiveMisses))
	}
	if st.StormState == engine.StormStandingState {
		lines = append(lines, "🌩️ Storm standing — the stake is stayed")
	}
	return lines
}

// panelChainLine renders the streak+adherence hero line. During the early ramp
// (no decided day yet) it frames the build rather than a hollow 0%; once days
// are decided it names the streak and the decided-day adherence.
func panelChainLine(m engine.Metrics) string {
	a := m.Adherence
	if a.Decided == 0 {
		return fmt.Sprintf("⛓️ Building · %d completed of %d accounted — no decided day yet",
			a.Completed, a.DaysAccounted)
	}
	return fmt.Sprintf("⛓️ %d-day streak · %s adherence (%d/%d decided)",
		m.CurrentStreak, panelPercent(a.Adherence), a.Completed, a.Decided)
}

// panelBudgetLine renders the isolated-miss error budget with its burn, the
// over-budget note when it holds, and the days-to-next-gate co-number when the
// projection carries one.
func panelBudgetLine(b engine.ErrorBudget, daysToGate *int) string {
	line := fmt.Sprintf("📊 Error budget · %d/%d isolated misses left", b.Remaining, b.Budget)
	if b.Exceeded {
		line += " (over — gates hold)"
	}
	if daysToGate != nil {
		line += fmt.Sprintf(" · %dd to gate", *daysToGate)
	}
	return line
}

// panelPercent renders a 0..1 rate as a whole percent, rounded to nearest —
// mirroring the router's status/metrics percent rendering so the panel and the
// scripted surfaces read the same number.
func panelPercent(rate float64) string {
	return fmt.Sprintf("%d%%", int(rate*100+0.5))
}

// freshnessStamp renders a section's freshness meta from its most-recent event:
// "as logged <YYYY-MM-DD>", the logical date of the event, plus a `· stale`
// flag when that date is strictly older than staleAfterDays civil days before
// now. Every Ledger-derived section carries this stamp so nothing unverified is
// silently mixed into today's message; the chain's live numbers need no stamp
// because they are always current.
func freshnessStamp(ev observations.Event, now time.Time) string {
	stamp := "as logged " + ev.LogicalDate
	if isStale(ev.LogicalDate, now) {
		stamp += " · stale"
	}
	return stamp
}

// isStale reports whether a stored logical date is strictly older than the
// staleness threshold relative to now. An unparseable date is never flagged
// stale — a malformed stamp degrades to "fresh" rather than crying wolf.
func isStale(logicalDate string, now time.Time) bool {
	d, err := time.Parse(logicalDateFmt, logicalDate)
	if err != nil {
		return false
	}
	return civilDaysBetween(d, now) > staleAfterDays
}

// civilDaysBetween returns whole civil days from `from` to `to`, re-anchoring
// both instants to UTC civil midnight first so a DST transition can never make
// the count drift (the same discipline as engine.DaysSince).
func civilDaysBetween(from, to time.Time) int {
	fy, fm, fd := from.Date()
	ty, tm, td := to.Date()
	f := time.Date(fy, fm, fd, 0, 0, 0, 0, time.UTC)
	t := time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC)
	return int(t.Sub(f).Hours() / 24)
}

// parseSlots splits a model reply into its two slots. It scans for the
// %%INTERPRETATION%% and %%ACTIONS%% delimiter lines: the text between the
// interpretation delimiter and the actions delimiter (or the end) is the
// interpretation; the lines after the actions delimiter, stripped of their
// bullet markers, are the actions.
//
// The parser is deliberately tolerant. If the %%INTERPRETATION%% delimiter is
// absent, it returns the whole trimmed reply as the interpretation with no
// actions and ok=false — a reply that is plain prose is still a valid scaffold,
// not a failure. A stray %%ACTIONS%% delimiter appearing before the
// interpretation one is ignored.
func parseSlots(resp string) (interpretation string, actions []string, ok bool) {
	lines := strings.Split(resp, "\n")
	interpStart, actionsStart := -1, -1
	for i, ln := range lines {
		switch strings.TrimSpace(ln) {
		case interpDelim:
			if interpStart < 0 {
				interpStart = i
			}
		case actionsDelim:
			if actionsStart < 0 {
				actionsStart = i
			}
		}
	}

	if interpStart < 0 {
		return strings.TrimSpace(resp), nil, false
	}

	interpEnd := len(lines)
	if actionsStart > interpStart {
		interpEnd = actionsStart
	}
	interpretation = strings.TrimSpace(strings.Join(lines[interpStart+1:interpEnd], "\n"))
	if actionsStart > interpStart {
		actions = parseActionLines(lines[actionsStart+1:])
	}
	return interpretation, actions, true
}

// parseActionLines turns the raw lines of the actions slot into clean action
// strings: each non-empty line has any leading bullet marker (-, *, •) and
// surrounding whitespace stripped. Blank lines are dropped.
func parseActionLines(lines []string) []string {
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		t = strings.TrimLeft(t, "-*•")
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
