package witnessreport

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/router"
)

// This file owns the opt-in full anchor detail (witness_report.anchor_detail:
// full): the first of the two deliberate, opt-in exceptions to the package's
// by-construction privacy (ADR-0013 §7; the second is the week notes,
// ADR-0015). The section is the week's anchor events with their notes,
// verbatim. It is rendered here, by code; it is never part of the compose body
// or the report digest, so the model's input is byte-identical with the detail
// on and off, and it never reaches the witness-safe scan, so a note is never
// withheld or reworded. Nothing is dropped for length: RenderEmbeds chunks the
// section across numbered fields and, past one embed's budget, across numbered
// continuation messages — through the verbatim-section packer it shares with
// the week notes (verbatim.go) — and joining the parts in order reproduces
// every event and note exactly as RenderMarkdown prints them.

// AnchorDetailReader is the one note-reaching seam: the week's anchor events,
// notes included. It is exactly router.Router.AnchorDetail. The composition
// root wires it only when witness_report.anchor_detail is full; in summary mode
// it is nil, so no reader that can reach a note exists at all.
type AnchorDetailReader interface {
	AnchorDetail(now time.Time) ([]router.AnchorDetailEvent, error)
}

// fieldAnchorDetail is the section header the embed fields, the continuation
// titles and the markdown section share.
const fieldAnchorDetail = "⚓ Anchor detail"

// anchorDetailLines renders each event as one line: the anchor's label and
// kind, the day (a confirm's span), what happened (a use with its intent), and
// the note verbatim when there is one. The events arrive already ordered by
// label, day and receipt.
func anchorDetailLines(events []router.AnchorDetailEvent) []string {
	lines := make([]string, 0, len(events))
	for _, e := range events {
		line := fmt.Sprintf("%s (%s) — %s · %s", e.Label, e.AnchorKind, detailWhen(e), detailWhat(e))
		if e.Note != "" {
			line += " · " + e.Note
		}
		lines = append(lines, line)
	}
	return lines
}

// detailWhen is an event's day, or a confirm's attested span.
func detailWhen(e router.AnchorDetailEvent) string {
	if e.Type == engine.AnchorEventConfirm && e.From != "" && e.From != e.Day {
		return e.From + "–" + e.Day
	}
	return e.Day
}

// detailWhat names what an event recorded in plain words.
func detailWhat(e router.AnchorDetailEvent) string {
	switch e.Type {
	case engine.AnchorEventUse:
		return "use, " + e.Intent
	case engine.AnchorEventConfirm:
		return "confirmed"
	case engine.AnchorEventGraduate:
		return "graduated"
	case engine.AnchorEventKind:
		switch e.Kind {
		case engine.AnchorKindLimit:
			return fmt.Sprintf("kind: limit, %d in %s", e.Allowance, plural(e.WindowDays, "day", "days"))
		case engine.AnchorKindHabit:
			if e.WindowDays > 0 {
				return fmt.Sprintf("kind: habit, %d-day window", e.WindowDays)
			}
		}
		return "kind: " + e.Kind
	}
	return e.Type // slip, miss
}

// anchorDetailMarkdown is the section body RenderMarkdown prints: one bullet
// per event. It is the reference text the embed parts reassemble to.
func anchorDetailMarkdown(events []router.AnchorDetailEvent) string {
	return bulletList(anchorDetailLines(events))
}

// anchorDetailSection is the full detail as a verbatim section: one "• "
// bullet line per event, packed greedily — whole bullet lines share a field
// while they fit, and a line longer than a field is split at character
// boundaries into fields marked continued.
func anchorDetailSection(events []router.AnchorDetailEvent) verbatimSection {
	return verbatimSection{name: fieldAnchorDetail, lines: anchorDetailLines(events), bullet: "• "}
}

// anchorDetailFields packs the section into numbered embed fields, each value
// within the field budget. Concatenating the field values — a line break
// before an unmarked field, nothing before a continued one — reproduces
// anchorDetailMarkdown exactly. Week notes ahead of the section in a batch
// never change these fields.
func anchorDetailFields(events []router.AnchorDetailEvent) []notify.EmbedField {
	return anchorDetailSection(events).fields()
}
