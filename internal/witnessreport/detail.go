package witnessreport

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/router"
)

// This file owns the opt-in full anchor detail (witness_report.anchor_detail:
// full): the one deliberate exception to the package's by-construction privacy
// (ADR-0013 §7). The section is the week's anchor events with their notes,
// verbatim. It is rendered here, by code; it is never part of the compose body
// or the report digest, so the model's input is byte-identical with the detail
// on and off, and it never reaches the witness-safe scan, so a note is never
// withheld or reworded. Nothing is dropped for length: RenderEmbeds chunks the
// section across numbered fields and, past one embed's budget, across numbered
// continuation messages, and joining the parts in order reproduces every event
// and note exactly as RenderMarkdown prints them.

// AnchorDetailReader is the one note-reaching seam: the week's anchor events,
// notes included. It is exactly router.Router.AnchorDetail. The composition
// root wires it only when witness_report.anchor_detail is full; in summary mode
// it is nil, so no reader that can reach a note exists at all.
type AnchorDetailReader interface {
	AnchorDetail(now time.Time) ([]router.AnchorDetailEvent, error)
}

// Discord's embed budgets (the documented limits the friend-facing surface
// must respect). Lengths are counted in UTF-16 code units — what Discord's
// client counts — which is never fewer than the characters, so a part within
// these budgets is within them however a character is counted.
const (
	maxFieldValueLen = 1024
	maxEmbedFields   = 25
	maxEmbedLen      = 6000
)

// fieldAnchorDetail is the section header the embed fields, the continuation
// titles and the markdown section share.
const fieldAnchorDetail = "⚓ Anchor detail"

// continuedSuffix marks a field whose value continues the previous field's
// line mid-text (a note too long for one field), so a reader — and the
// reassembly the tests prove — knows to join the two without a line break.
const continuedSuffix = " (continued)"

// continuationTitleReserve is the longest continuation title the packer
// budgets for before the part count is known; the real title, set once every
// part is packed, is never longer, so a packed part never outgrows its budget.
var continuationTitleReserve = continuationTitle(99999, 99999) //nolint:gochecknoglobals // a derived constant: the budget reserve for a continuation title

// continuationTitle names continuation message part of total — the main
// report card is part 1.
func continuationTitle(part, total int) string {
	return fmt.Sprintf("%s · part %d of %d", fieldAnchorDetail, part, total)
}

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

// detailChunk is one field's worth of the section: whole bullet lines joined
// by line breaks, or — for a line longer than a field — a slice of it. cont
// marks a chunk that continues the previous chunk's line mid-text.
type detailChunk struct {
	text string
	cont bool
}

// anchorDetailFields packs the section into numbered embed fields, each value
// within the field budget. Whole lines share a field while they fit; a line
// longer than a field is split at character boundaries, never changing a
// byte, and its later slices go in fields marked continued. Concatenating the
// field values — a line break before an unmarked field, nothing before a
// continued one — reproduces anchorDetailMarkdown exactly.
func anchorDetailFields(events []router.AnchorDetailEvent) []notify.EmbedField {
	var chunks []detailChunk
	for _, line := range anchorDetailLines(events) {
		for i, seg := range splitByLen("• "+line, maxFieldValueLen) {
			last := len(chunks) - 1
			if i == 0 && last >= 0 && discordLen(chunks[last].text)+1+discordLen(seg) <= maxFieldValueLen {
				chunks[last].text += "\n" + seg
				continue
			}
			chunks = append(chunks, detailChunk{text: seg, cont: i > 0})
		}
	}

	fields := make([]notify.EmbedField, 0, len(chunks))
	for i, c := range chunks {
		name := fieldAnchorDetail
		if i > 0 {
			name = fmt.Sprintf("%s · %d", fieldAnchorDetail, i+1)
		}
		if c.cont {
			name += continuedSuffix
		}
		fields = append(fields, notify.EmbedField{Name: name, Value: c.text})
	}
	return fields
}

// RenderEmbeds renders the report as the ordered batch of embeds a delivery
// sends, one per message. Without anchor detail it is exactly
// [RenderEmbed] — one card. With full detail, the section's fields follow the
// card's own while the card stays within Discord's field-count and total
// budgets; whatever does not fit continues in numbered continuation embeds
// (`⚓ Anchor detail · part 2 of 3`) carrying the card's sidebar color. Nothing
// is ever dropped or summarized. It is pure and byte-stable.
func RenderEmbeds(r Report) []notify.Embed {
	card := RenderEmbed(r)
	if len(r.AnchorDetail) == 0 {
		return []notify.Embed{card}
	}
	embeds := []notify.Embed{card}
	for _, f := range anchorDetailFields(r.AnchorDetail) {
		if !embedFits(embeds[len(embeds)-1], f) {
			embeds = append(embeds, notify.Embed{Title: continuationTitleReserve, Color: card.Color})
		}
		last := &embeds[len(embeds)-1]
		last.Fields = append(last.Fields, f)
	}
	for i := 1; i < len(embeds); i++ {
		embeds[i].Title = continuationTitle(i+1, len(embeds))
	}
	return embeds
}

// embedFits reports whether one more field keeps an embed within Discord's
// field-count and total-character budgets.
func embedFits(e notify.Embed, f notify.EmbedField) bool {
	return len(e.Fields) < maxEmbedFields &&
		embedLen(e)+discordLen(f.Name)+discordLen(f.Value) <= maxEmbedLen
}

// embedLen is an embed's total against Discord's budget: the title, the
// description, the footer, and every field's name and value.
func embedLen(e notify.Embed) int {
	n := discordLen(e.Title) + discordLen(e.Description) + discordLen(e.Footer)
	for _, f := range e.Fields {
		n += discordLen(f.Name) + discordLen(f.Value)
	}
	return n
}

// discordLen is s's length in UTF-16 code units: a character outside the
// Basic Multilingual Plane (most emoji) counts two.
func discordLen(s string) int {
	n := 0
	for _, r := range s {
		n += runeUnits(r)
	}
	return n
}

// runeUnits is one character's UTF-16 width.
func runeUnits(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// splitByLen cuts s into consecutive slices of at most limit UTF-16 code
// units, only ever at a character boundary, so the slices concatenate back to
// s byte for byte. A string within the limit comes back whole.
func splitByLen(s string, limit int) []string {
	var out []string
	start, n := 0, 0
	for i, r := range s {
		w := runeUnits(r)
		if n+w > limit && i > start {
			out = append(out, s[start:i])
			start, n = i, 0
		}
		n += w
	}
	return append(out, s[start:])
}
