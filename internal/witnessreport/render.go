package witnessreport

import (
	"fmt"
	"strings"

	"github.com/mrz1836/lucid/internal/notify"
)

// This file owns the report's *layout* — the split that keeps the friend-facing
// surface honest. Lucid renders the whole embed deterministically: the title,
// the colored risk sidebar, every number, the watch-outs, the asks, the footer,
// and the verbatim sections (the week notes and any opted-in anchor detail),
// which code renders exactly as they are. The model fills only the prose slots
// (Faults, Progress, Narrative), which drop into fixed fields. Because the
// layout is code, not prose, a model can never produce a wall of numbers or
// restate a metric — it never owns the structure. RenderEmbed, RenderEmbeds and
// RenderMarkdown are pure and byte-stable: the same Report always renders
// identical bytes, which is what makes the render golden-testable.

// The risk-sidebar colors. Green when the week surfaced no watch-out at all;
// amber for a soft signal (a quiet week, or an anchor signal: a limit over its
// allowance, a habit miss run, a slip this week); red for a hard one (this-week
// misses, a spent error budget, or a 30-day adherence dip). The values are
// ordinary Discord embed decimal colors.
const (
	colorClear   = 0x57F287 // green — nothing to flag
	colorCaution = 0xF1C40F // amber — a soft watch-out (quiet week / anchor signal)
	colorRisk    = 0xED4245 // red — a hard signal (misses / over-budget / adherence dip)
)

// Section field labels — the fixed, scannable headers the embed and the
// markdown fallback share, so the two surfaces never drift.
const (
	fieldStreak    = "⛓️ Streak & adherence"
	fieldThisWeek  = "📅 This week"
	fieldProgress  = "📈 Progress"
	fieldFaults    = "🪞 Faults"
	fieldAsks      = "🤝 How friends can help"
	fieldWatchOuts = "⚠️ Watch-outs"
)

// fieldWeekNotes is the week-notes section header (ADR-0015 §6) the embed
// fields, the continuation titles and the markdown section share. The section
// follows the watch-outs (or the asks when there are none) and precedes the
// anchor detail.
const fieldWeekNotes = "📝 Week notes"

// footerNote is the fixed cadence + honesty line every report carries — the
// standing promise that the numbers above are exact, never softened.
const footerNote = "Weekly · Monday after Sunday's reflection · numbers are exact, never softened"

// RenderEmbed turns a Report into the Discord rich embed card the witness
// channel receives — the whole report except its two verbatim sections, the
// included week notes and any opted-in anchor detail, which [RenderEmbeds]
// adds. Every number comes straight from the deterministic
// Report — the model's prose lands only in the description (the warm read) and
// the Faults / Progress fields. Empty prose fields are omitted (Discord rejects
// an empty field value), so a deterministic fallback report still renders a
// clean, complete card: the honest numbers, the asks, and any watch-outs. It is
// pure and byte-stable.
func RenderEmbed(r Report) notify.Embed {
	e := notify.Embed{
		Title:  fmt.Sprintf("Weekly witness report · Week %s", r.ISOWeek),
		Color:  riskColor(r),
		Footer: footerNote,
	}
	if n := strings.TrimSpace(r.Narrative); n != "" {
		e.Description = n
	}

	e.Fields = append(
		e.Fields,
		notify.EmbedField{Name: fieldStreak, Value: streakLine(r), Inline: true},
		notify.EmbedField{Name: fieldThisWeek, Value: thisWeekLine(r), Inline: true},
		notify.EmbedField{Name: fieldFaults, Value: faultsValue(r)},
	)
	if p := strings.TrimSpace(r.Progress); p != "" {
		e.Fields = append(e.Fields, notify.EmbedField{Name: fieldProgress, Value: p})
	}
	e.Fields = append(e.Fields, notify.EmbedField{Name: fieldAsks, Value: bulletList(r.Asks)})
	if len(r.WatchOuts) > 0 {
		e.Fields = append(e.Fields, notify.EmbedField{Name: fieldWatchOuts, Value: bulletList(r.WatchOuts)})
	}
	return e
}

// RenderEmbeds renders the report as the ordered batch of embeds a delivery
// sends, one per message. Without a verbatim section it is exactly
// [RenderEmbed] — one card. With included week notes, full anchor detail, or
// both, the sections' fields follow the card's own — the week notes first, then
// the anchor detail — while the card stays within Discord's field-count and
// total budgets; whatever does not fit continues in numbered continuation
// embeds titled by the sections they carry (`📝 Week notes · part 2 of 3`,
// `⚓ Anchor detail · part 2 of 3`) and carrying the card's sidebar color.
// Nothing is ever dropped, reworded or summarized. It is pure and byte-stable.
func RenderEmbeds(r Report) []notify.Embed {
	return packVerbatim(RenderEmbed(r), verbatimSections(r))
}

// verbatimSections is the report's verbatim sections in render order: the week
// notes when they were included, then the full anchor detail when opted in.
func verbatimSections(r Report) []verbatimSection {
	var sections []verbatimSection
	if notes, ok := includedNotes(r); ok {
		sections = append(sections, verbatimSection{name: fieldWeekNotes, lines: strings.Split(notes, "\n")})
	}
	if len(r.AnchorDetail) > 0 {
		sections = append(sections, anchorDetailSection(r.AnchorDetail))
	}
	return sections
}

// includedNotes is the week-notes body to render, and whether there is one: it
// is set only when the reader included the notes — valid, fresh, non-empty,
// within the cap and packable losslessly (ADR-0015 §4).
func includedNotes(r Report) (string, bool) {
	return r.Notes, r.NotesStatus == NotesIncluded && r.Notes != ""
}

// RenderMarkdown renders the same report as a structured-markdown message — the
// retained fallback path (AC-7) for a surface where the rich embed cannot be
// used. It mirrors the embed's sections one-for-one so the two never drift: the
// title, the optional warm read, the numbers, the faults, the optional progress,
// the asks, any watch-outs, the included week notes (the raw body, not
// bulleted) and the full anchor detail when opted in — the same text
// [RenderEmbeds] spreads across its parts — and the footer. It is pure and
// byte-stable.
func RenderMarkdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Weekly witness report · Week %s**", r.ISOWeek)
	if n := strings.TrimSpace(r.Narrative); n != "" {
		b.WriteString("\n\n")
		b.WriteString(n)
	}
	section := func(name, value string) {
		b.WriteString("\n\n**")
		b.WriteString(name)
		b.WriteString("**\n")
		b.WriteString(value)
	}
	section(fieldStreak, streakLine(r))
	section(fieldThisWeek, thisWeekLine(r))
	section(fieldFaults, faultsValue(r))
	if p := strings.TrimSpace(r.Progress); p != "" {
		section(fieldProgress, p)
	}
	section(fieldAsks, bulletList(r.Asks))
	if len(r.WatchOuts) > 0 {
		section(fieldWatchOuts, bulletList(r.WatchOuts))
	}
	if notes, ok := includedNotes(r); ok {
		section(fieldWeekNotes, notes)
	}
	if len(r.AnchorDetail) > 0 {
		section(fieldAnchorDetail, anchorDetailMarkdown(r.AnchorDetail))
	}
	b.WriteString("\n\n_")
	b.WriteString(footerNote)
	b.WriteString("_\n")
	return b.String()
}

// riskColor picks the sidebar color from the report's real signals: red for a
// hard signal (this-week misses at the watch-out threshold, a spent error
// budget, or a 30-day adherence dip), amber for any softer watch-out (a quiet
// week, or a kind-aware anchor signal — a limit over its allowance, a habit
// miss run, a slip this week), and green when nothing was flagged. It reads
// only deterministic Report fields, so the color always agrees with the
// rendered watch-outs.
func riskColor(r Report) int {
	hard := r.WeekMisses >= weekMissWatchOut ||
		r.ErrorBudget.Exceeded ||
		(r.Adherence.Decided > 0 && r.Adherence.Adherence < adherenceFloor)
	switch {
	case hard:
		return colorRisk
	case len(r.WatchOuts) > 0:
		return colorCaution
	default:
		return colorClear
	}
}

// streakLine renders the streak + 30-day-adherence hero value. During the early
// ramp (no decided day yet) it frames the build rather than a hollow 0%; once a
// day is decided it names the streak, the decided-day adherence, and the longest
// streak. Every number is copied from the deterministic Report.
func streakLine(r Report) string {
	a := r.Adherence
	if a.Decided == 0 {
		return fmt.Sprintf("Building · %d completed of %d accounted — no decided day yet",
			a.Completed, a.DaysAccounted)
	}
	return fmt.Sprintf("%d-day streak · %d%% adherence (%d/%d decided) · longest %d",
		r.Streak, pct(a.Adherence), a.Completed, a.Decided, r.LongestStreak)
}

// thisWeekLine renders the 7-day window value: completed / decided and the
// logged-days co-number, honestly framed when nothing was decided yet.
func thisWeekLine(r Report) string {
	if r.Week.Decided == 0 {
		return fmt.Sprintf("%d logged of 7 — none decided yet", r.Week.DaysAccounted)
	}
	return fmt.Sprintf("%d/%d completed · %d logged of 7",
		r.Week.Completed, r.Week.Decided, r.Week.DaysAccounted)
}

// faultsValue renders the Faults field. The model's constructive framing is used
// when present; on a deterministic fallback (no model prose) it degrades to an
// honest line straight from the numbers — the misses if there were any, or the
// plain fact that the chain held — so the field is never empty.
func faultsValue(r Report) string {
	if f := strings.TrimSpace(r.Faults); f != "" {
		return f
	}
	if r.WeekMisses > 0 {
		return fmt.Sprintf("Missed %d of %d decided days this week.", r.WeekMisses, r.Week.Decided)
	}
	return "No misses this week — the chain held."
}

// bulletList renders items as `• `-prefixed lines, one per line. Discord field
// values and the markdown fallback share it so the two surfaces bullet
// identically. A caller only passes it a non-empty slice.
func bulletList(items []string) string {
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("• ")
		b.WriteString(it)
	}
	return b.String()
}
