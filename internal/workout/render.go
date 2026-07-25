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

// renderProgress renders the read-only trend panel — the streak (from the Engine
// chain), the frequency direction, the skipped-day count, and the recent body
// response. It is a compact glance, never a grade; the body-response line is
// omitted when there is nothing logged.
func renderProgress(tr Trend) string {
	var b strings.Builder
	b.WriteString(emojiProgress)
	b.WriteString(" **Progress**")
	b.WriteString("\n" + bulletLine(streakLine(tr.Streak)))
	b.WriteString("\n" + bulletLine(frequencyLine(tr)))
	b.WriteString("\n" + bulletLine(skippedLine(tr)))
	if body := bodyResponseLine(tr.BodyResponse); body != "" {
		b.WriteString("\n" + bulletLine(body))
	}
	return b.String()
}

// streakLine frames the Engine chain's streak — the build during the early ramp,
// the count once it holds — never a hollow "0-day streak".
func streakLine(streak int) string {
	if streak <= 0 {
		return "Building — no active streak yet"
	}
	return fmt.Sprintf("%d-day streak", streak)
}

// frequencyLine renders the frequency read: an arrow and the direction word, then
// this week's session count against the prior week's.
func frequencyLine(tr Trend) string {
	return fmt.Sprintf("Frequency %s %s · %d this week vs %d the week before",
		directionArrow(tr.Direction), tr.Direction, tr.ThisWeek, tr.PriorWeek)
}

// directionArrow maps a frequency direction to its glyph.
func directionArrow(direction string) string {
	switch direction {
	case DirectionUp:
		return "↗"
	case DirectionDown:
		return "↘"
	default:
		return "→"
	}
}

// skippedLine renders the skipped-day count as neutral inventory — a count over
// the window, never a shame line.
func skippedLine(tr Trend) string {
	return fmt.Sprintf("%d of the last %d days had no logged session", tr.SkippedDays, tr.WindowDays)
}

// bodyResponseLine renders the recent body response — each part with whatever it
// reported (soreness, pain, or both). Returns "" when nothing was logged, so the
// panel drops the line entirely rather than showing an empty label.
func bodyResponseLine(signals []BodySignal) string {
	parts := make([]string, 0, len(signals))
	for _, s := range signals {
		var bits []string
		if s.Soreness != nil {
			bits = append(bits, fmt.Sprintf("soreness %d", *s.Soreness))
		}
		if s.Pain != nil {
			bits = append(bits, fmt.Sprintf("pain %d", *s.Pain))
		}
		if len(bits) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", s.Part, strings.Join(bits, "/")))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Body: " + strings.Join(parts, " · ")
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

// anchorItemLine renders one anchor item — its name, its current-week target
// when the program gives one, and its mode in parentheses ("accumulate" marks a
// movement done in small sets through the day). An unnamed item renders "" so
// the line never carries a bare number.
func anchorItemLine(item AnchorLine) string {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return ""
	}
	if item.Target > 0 {
		name += fmt.Sprintf(" %d", item.Target)
	}
	if mode := strings.TrimSpace(item.Mode); mode != "" {
		name += fmt.Sprintf(" (%s)", mode)
	}
	return name
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
