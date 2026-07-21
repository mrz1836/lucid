package workout

// This file owns the workout message's **layout** — the same deterministic split
// the companion draws (internal/companion/render.go): Lucid renders the whole
// sectioned scaffold, and the model (in compose.go) only phrases the prose around
// an already-decided recommendation. Because the layout is code, not prose, the
// readability contract is unit-testable and a model can never restructure the
// message, drop the safety line, or talk the user into a fourth "just push
// through it" door: it never owns the structure. See
// docs/mvp/workout-module.md §"The message scaffold" and §"Safety copy".

import (
	"fmt"
	"strings"
	"time"
)

// Scaffold literals. A chat surface renders markdown tables as raw text, so the
// layout is built from bullets and light horizontal dividers, exactly like the
// companion. The date format is Go's reference layout for "Monday, Jul 20".
const (
	workoutDivider = "― ― ―"
	workoutBullet  = "•"
	workoutDateFmt = "Monday, Jan 2"
)

// Header + section emojis. Kept as package constants so the renderer and the
// golden test name the identical bytes (the emoji carry variation selectors; a
// shared constant removes any copy-mismatch risk between render and test).
const (
	emojiHeader    = "🏋️"
	emojiOfferings = "🎯"
	emojiProgress  = "📈"
	emojiReason    = "💡"
	emojiSafety    = "🛟"
)

// The three offering labels — exactly three doors, always: the recommended plan,
// the easier variant, and the back-off/safety option. The count is fixed by the
// module spec (§"Three offerings"); a message never renders two or four.
const (
	labelRecommended = "Recommended"
	labelEasier      = "Easier"
	labelBackOff     = "Back off"
)

// safetyLine is the deterministic not-medical-advice line every rendered message
// carries — authored boundary copy (docs/mvp/workout-module.md §"Safety copy"),
// the same stance as observations.md §9. It is a fixed constant, not program
// config, so the line is present even when the program omits its own safety copy
// and even when the model is down. The guard test asserts it is always rendered.
const safetyLine = "This is not medical advice — for concerning pain or injury, consult a professional."

// Render turns an already-decided Recommendation and its Trend into the final
// Discord message. It is pure and byte-stable: the same inputs always render the
// identical bytes, which is what makes the readability contract testable. The
// region order is fixed — header, the three offerings, the progress panel, the
// one-line reason, then the safety line — with each non-empty region joined by a
// blank-line-padded divider. An empty region (a blank reason) is dropped so the
// message never carries a dangling divider.
func Render(rec Recommendation, tr Trend, now time.Time) string {
	regions := []string{
		renderHeader(now),
		renderOfferings(rec),
		renderProgress(tr),
		renderReason(rec.Reason),
		renderSafety(),
	}
	groups := make([]string, 0, len(regions))
	for _, r := range regions {
		if strings.TrimSpace(r) != "" {
			groups = append(groups, r)
		}
	}
	return strings.Join(groups, "\n\n"+workoutDivider+"\n\n")
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
	b.WriteString("\n")
	b.WriteString(offeringLine(labelEasier, cardOffering(rec.Fallback)))
	b.WriteString("\n")
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

// renderReason renders the deterministic one-line reason under its header. An
// empty reason drops the whole region (Render omits it).
func renderReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	return fmt.Sprintf("%s **Why** — %s", emojiReason, reason)
}

// renderSafety renders the deterministic safety line under its header. It is
// always present — the one region Render can never drop.
func renderSafety() string {
	return fmt.Sprintf("%s %s", emojiSafety, safetyLine)
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
