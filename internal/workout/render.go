package workout

// This file owns the workout message's **layout** — the same deterministic split
// the companion draws (internal/companion/render.go): Lucid renders the whole
// sectioned scaffold, and the model (in compose.go) only phrases the prose around
// an already-decided recommendation. Because the layout is code, not prose, the
// readability contract is unit-testable and a model can never restructure the
// message, talk the user into a fourth "just push
// through it" door: it never owns the structure. See
// docs/mvp/workout-module.md §"The message scaffold".

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Scaffold literals. A chat surface renders markdown tables as raw text, so the
// layout is built from bullets and blank-line spacing, exactly like the
// companion. The date format is Go's reference layout for "Monday, Jul 20".
const (
	workoutBullet  = "•"
	workoutDateFmt = "Monday, Jan 2"
)

// Header + section emojis. Kept as package constants so the renderer and the
// golden test name the identical bytes (the emoji carry variation selectors; a
// shared constant removes any copy-mismatch risk between render and test).
const (
	emojiHeader    = "🏋️"
	emojiOfferings = "🎯"
	emojiAnchor    = "⚓"
	emojiProgress  = "📈"
)

// The three offering labels — exactly three doors, always: the recommended plan,
// the easier variant, and the back-off/safety option. The count is fixed by the
// module spec (§"Three offerings"); a message never renders two or four.
const (
	labelRecommended = "Recommended"
	labelEasier      = "Easier"
	labelBackOff     = "Back off"
)

// labelWatchOuts heads the progress panel's watch-outs — the frame-don't-restate
// signal list, mirroring the weekly witness report's own `⚠️ Watch-outs` field
// (internal/witnessreport). It is a plain header line, not a `• **` offering
// bullet, so the exactly-three-doors invariant is untouched.
const labelWatchOuts = "⚠️ Watch-outs"

// Render turns an already-decided Recommendation, its Trend, and today's Anchor
// into the final Discord message. It is pure and byte-stable: the same inputs
// always render the identical bytes, which is what makes the readability
// contract testable. The region order is fixed — header, the three offerings,
// the daily anchor, then the progress panel — with each non-empty region joined
// by blank lines. An empty region (a program with no anchor items) is dropped so
// the message never carries dangling structural chrome.
//
// There is deliberately **no "Why" region**: a guardrail veto, a recovery
// downshift, or a pain hard stop still changes which card is recommended — and
// still surfaces through the back-off door, Recommendation.Reason, and
// Recommendation.Vetoes in --json — it simply no longer argues its case in prose
// (docs/mvp/workout-module.md §"The message scaffold").
func Render(rec Recommendation, tr Trend, anchor Anchor, now time.Time) string {
	regions := []string{
		renderHeader(now),
		renderOfferings(rec),
		renderAnchor(anchor),
		renderProgress(tr),
	}
	groups := make([]string, 0, len(regions))
	for _, r := range regions {
		if strings.TrimSpace(r) != "" {
			groups = append(groups, r)
		}
	}
	return strings.Join(groups, "\n\n")
}

// renderHeader renders the window header — `{emoji} **Workout** · {Weekday, Mon D}`.
func renderHeader(now time.Time) string {
	return fmt.Sprintf("%s **Workout** · %s", emojiHeader, now.Format(workoutDateFmt))
}

// renderOfferings renders the heart of the message: exactly three doors under one
// header — the recommended plan, the easier variant, and the back-off option.
// The back-off door is the pain-signal SafetyOption when the recommendation
// carries one, and otherwise a plain "a lighter day is fine" rest line, so there
// is always a third, lowest-effort door (§"Three offerings").
func renderOfferings(rec Recommendation) string {
	var b strings.Builder
	b.WriteString(emojiOfferings)
	b.WriteString(" **Today's options**")
	b.WriteString("\n")
	b.WriteString(offeringLine(labelRecommended, cardOffering(rec.Primary)))
	b.WriteString("\n\n")
	b.WriteString(offeringLine(labelEasier, cardOffering(rec.Fallback)))
	b.WriteString("\n\n")
	b.WriteString(offeringLine(labelBackOff, backOffOffering(rec)))
	return b.String()
}

// offeringLine renders one door as a `• **Label** — detail` bullet.
func offeringLine(label, detail string) string {
	return fmt.Sprintf("%s **%s** — %s", workoutBullet, label, detail)
}

// cardOffering renders a card's detail: its title, then its focus and movements
// as ` · `-separated context (each omitted when empty), so a bare recovery card
// still reads as one clean line.
func cardOffering(c Card) string {
	parts := []string{cardTitle(c)}
	if focus := strings.Join(c.Focus, ", "); focus != "" {
		parts = append(parts, focus)
	}
	if movements := strings.Join(c.Movements, ", "); movements != "" {
		parts = append(parts, movements)
	}
	return strings.Join(parts, " · ")
}

// backOffOffering renders the third door. When the recommendation carries a pain
// signal it is the named SafetyOption (its movements as context); otherwise it is
// a plain rest line — the "less is fine" door that is always present.
func backOffOffering(rec Recommendation) string {
	if rec.HardStop == nil {
		return "a lighter day is always fine — gentle mobility, an easy walk, or simply rest"
	}
	hs := rec.HardStop
	detail := cardTitleFallback(hs.Name, "Protect the sore area")
	if movements := strings.Join(hs.Movements, ", "); movements != "" {
		detail += " · " + movements
	}
	return detail
}

// renderProgress renders the read-only progress panel as an **insight** surface,
// not a flat dashboard (R-050): one slim streak/consistency line, then the
// per-part next-day pain-response trend and its load-vs-pain pattern, the
// stateless post-workout checkpoint scaffold (only when a qualifying session
// supplied one), and the concrete watch-outs under a `⚠️ Watch-outs` label. It is
// a compact panel of signal, never a grade. The old flat lines (frequency
// direction, the skipped-day tally, and the single "Body:" line) are gone from
// the card and live only in the `Trend` struct / `--json`
// (docs/mvp/workout-module.md §"The trend / progress projection").
func renderProgress(tr Trend) string {
	var b strings.Builder
	b.WriteString(emojiProgress)
	b.WriteString(" **Progress**")
	b.WriteString("\n" + bulletLine(streakLine(tr.Streak)))
	for _, line := range progressInsightLines(tr.PainResponse, tr.LoadPattern) {
		b.WriteString("\n" + bulletLine(line))
	}
	if line := checkpointLine(tr.Checkpoints); line != "" {
		b.WriteString("\n" + bulletLine(line))
	}
	if len(tr.WatchOuts) > 0 {
		b.WriteString("\n" + labelWatchOuts)
		for _, w := range tr.WatchOuts {
			b.WriteString("\n" + bulletLine(w))
		}
	}
	return b.String()
}

// streakLine frames the workout streak — the build during the early ramp, the
// count once it holds — never a hollow "0-day streak".
func streakLine(streak int) string {
	if streak <= 0 {
		return "Building — no active streak yet"
	}
	return fmt.Sprintf("%d-day streak", streak)
}

// maxBuildingParts caps how many still-building parts the compact summary names
// before it rolls the remainder into a "+N more", so the line stays one glance of
// signal instead of the per-part wall of identical zero lines it replaced.
const maxBuildingParts = 3

// progressInsightLines renders the per-part next-day pain-response trend and its
// load-vs-pain pattern — the panel's real signal, in the sorted part order the
// fold produced. A part with a **direction** takes its own compact line. An
// **insufficient** part takes no per-part line (R-050: signal, not a per-part
// zero dashboard): parts with no paired days yet are dropped entirely, and parts
// that have begun accumulating pairs fold into one compact "still building" line.
// The load-pattern read is matched to its part by name. Both renderProgress and
// compose.go's progressDigest call this, so the rendered card and the
// model-grounding digest can never drift.
func progressInsightLines(painResponse []PartTrend, loadPattern []PartPattern) []string {
	patternByPart := make(map[string]string, len(loadPattern))
	for _, lp := range loadPattern {
		patternByPart[lp.Part] = lp.Pattern
	}
	out := make([]string, 0, len(painResponse))
	building := make([]PartTrend, 0, len(painResponse))
	for _, pt := range painResponse {
		if pt.Insufficient {
			// No paired days yet → no signal, no line. Some pairs but below
			// the threshold → fold into the one summary line below.
			if pt.PairedDays > 0 {
				building = append(building, pt)
			}
			continue
		}
		out = append(out, partResponseLine(pt, patternByPart[pt.Part]))
	}
	if line := buildingSummaryLine(building); line != "" {
		out = append(out, line)
	}
	return out
}

// partResponseLine renders one part's next-day pain-response read: the
// chronological direction followed by the load-vs-pain pattern over the same
// pairs. The part label flows from the fold's own data (never hardcoded); an
// empty part reads as the neutral "that area". Insufficient parts never reach
// here — progressInsightLines drops or folds them first.
func partResponseLine(pt PartTrend, pattern string) string {
	line := fmt.Sprintf("%s — next-day pain/soreness %s", humanizePart(pt.Part), pt.Direction)
	if pattern != "" {
		line += " · " + pattern
	}
	return line
}

// buildingSummaryLine folds every part that has begun accumulating paired days
// but not yet reached minPairedDays into one compact line — each named part with
// its progress toward the threshold (core 2/3), the nearest first, the rest
// rolled into "+N more". It returns "" when nothing is building, so a card with
// no partial parts drops the line entirely rather than printing an empty nudge.
func buildingSummaryLine(building []PartTrend) string {
	if len(building) == 0 {
		return ""
	}
	sort.SliceStable(building, func(i, j int) bool {
		return building[i].PairedDays > building[j].PairedDays
	})
	extra := 0
	if len(building) > maxBuildingParts {
		extra = len(building) - maxBuildingParts
		building = building[:maxBuildingParts]
	}
	parts := make([]string, 0, len(building))
	for _, pt := range building {
		parts = append(parts, fmt.Sprintf("%s %d/%d", humanizePart(pt.Part), pt.PairedDays, minPairedDays))
	}
	summary := strings.Join(parts, ", ")
	if extra > 0 {
		summary += fmt.Sprintf(", +%d more", extra)
	}
	return "Next-day reads still building · " + summary
}

// checkpointLine renders the stateless post-workout checkpoint scaffold as one
// line: the operator's label and subject, then the relative-time marks (right
// after / ~12h / ~24h) computed off the logged session, and the operator's copy
// in parentheses. It tracks no done/pending state and reads no post-session
// body_state — it is a "when to check in" guide. Returns "" when the scaffold is
// absent or carries no checkpoints, so the panel drops the line entirely.
func checkpointLine(cp *CheckpointScaffold) string {
	if cp == nil || len(cp.Checkpoints) == 0 {
		return ""
	}
	marks := make([]string, 0, len(cp.Checkpoints))
	for _, c := range cp.Checkpoints {
		marks = append(marks, checkpointOffsetLabel(c.OffsetHours))
	}
	head := cardTitleFallback(strings.TrimSpace(cp.Label), "Post-session check-in")
	if subject := strings.TrimSpace(cp.Subject); subject != "" {
		head += " · " + subject
	}
	line := fmt.Sprintf("%s — %s", head, strings.Join(marks, " / "))
	if note := strings.TrimSpace(cp.Copy); note != "" {
		line += fmt.Sprintf(" (%s)", note)
	}
	return line
}

// checkpointOffsetLabel renders one checkpoint's offset as a relative-time mark:
// the session moment itself reads "right after", and any later offset reads
// "~Nh" — relative to when the user actually trained, never a fixed calendar day.
func checkpointOffsetLabel(offset int) string {
	if offset <= 0 {
		return "right after"
	}
	return fmt.Sprintf("~%dh", offset)
}

// renderAnchor renders the daily floor as one ` · `-joined line — each item with
// the count that applies to the current program week, then the derived week
// itself, so the ramp is visible as it happens. It is inventory, not a grade: the
// numbers are shown and compared to nothing. A program that defines no anchor
// items renders "" and Render drops the whole region rather than printing an
// empty label.
func renderAnchor(a Anchor) string {
	if len(a.Items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(a.Items))
	for _, item := range a.Items {
		if line := anchorItemLine(item); line != "" {
			parts = append(parts, line)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("%s **Daily Anchor** · %s — week %d", emojiAnchor, strings.Join(parts, " · "), a.Week)
}

// anchorItemLine renders one anchor item — its name, the quantity form the
// program gives it, and its mode in parentheses ("accumulate" marks a movement
// done in small sets through the day, and still trails the resolved form). The
// quantity resolves to one form (see [anchorQuantity]), so a hold-time or
// set-based movement reads cleanly instead of as a dangling number. An unnamed
// item renders "" so the line never carries a bare number.
func anchorItemLine(item AnchorLine) string {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return ""
	}
	if q := anchorQuantity(item); q != "" {
		name += " " + q
	}
	if mode := strings.TrimSpace(item.Mode); mode != "" {
		name += fmt.Sprintf(" (%s)", mode)
	}
	return name
}

// anchorQuantity resolves an anchor item's non-name quantity to exactly one
// form, checked in a fixed order so a hold-time or set-based item never renders
// as a dangling number (docs/mvp/workout-module.md §"The daily-anchor
// projection"): sets×hold → `5x45s`, a standalone hold → `45s`, a target with a
// unit → `5 min`, a bare set count → `2 sets`, and a plain rep count → `50`. An
// item carrying none of these renders "", matching the prior bare-name behavior.
func anchorQuantity(item AnchorLine) string {
	unit := strings.TrimSpace(item.Unit)
	switch {
	case item.Sets > 0 && item.HoldSeconds > 0:
		return fmt.Sprintf("%dx%ds", item.Sets, item.HoldSeconds)
	case item.HoldSeconds > 0:
		return fmt.Sprintf("%ds", item.HoldSeconds)
	case item.Target > 0 && unit != "":
		return fmt.Sprintf("%d %s", item.Target, unit)
	case item.Sets > 0:
		return fmt.Sprintf("%d sets", item.Sets)
	case item.Target > 0:
		return fmt.Sprintf("%d", item.Target)
	default:
		return ""
	}
}

// bulletLine prefixes a panel line with the bullet mark.
func bulletLine(s string) string {
	return workoutBullet + " " + s
}

// cardTitle is a card's display title: its name, then its id, then a neutral
// recovery fallback for a title-less card.
func cardTitle(c Card) string {
	return cardTitleFallback(cardTitleFallback(c.Name, c.ID), "Recovery + mobility")
}

// cardTitleFallback returns primary when it is non-blank, else fallback.
func cardTitleFallback(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}
