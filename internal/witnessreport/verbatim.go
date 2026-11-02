package witnessreport

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mrz1836/lucid/internal/notify"
)

// This file owns the packer for the report's verbatim sections — the week
// notes (ADR-0015 §6) and the opt-in anchor detail (ADR-0013 §7). A verbatim
// section is text that is rendered by code exactly as it is: never reworded,
// summarized, truncated or scanned. Nothing is dropped for length. The packer
// lays an ordered list of sections into numbered embed fields after the report
// card's own, and past one embed's budget into numbered continuation messages,
// each within Discord's limits. Joining a section's field values in order — a
// line break before an unmarked field, nothing before one marked continued —
// reproduces the section's text exactly as RenderMarkdown prints it. It is pure
// and byte-stable.

// Discord's embed budgets (the documented limits the friend-facing surface
// must respect). Lengths are counted in UTF-16 code units — what Discord's
// client counts — which is never fewer than the characters, so a part within
// these budgets is within them however a character is counted.
const (
	maxFieldValueLen = 1024
	maxEmbedFields   = 25
	maxEmbedLen      = 6000
)

// continuedSuffix marks a field whose value continues the previous field's
// line mid-text (a line too long for one field), so a reader — and the
// reassembly the tests prove — knows to join the two without a line break.
const continuedSuffix = " (continued)"

// titleSeparator joins the section names of a continuation title, and a
// section's name to its field number.
const titleSeparator = " · "

// verbatimSection is one verbatim section the packer lays out: the name its
// fields and continuation titles carry, its lines verbatim, and the prefix
// each line is rendered with. A bulleted section (anchor detail, "• ") packs
// whole bullet lines greedily, exactly as anchor detail always has. A raw
// section (week notes, no bullet) is free text — paragraphs, blank lines,
// indentation — and packs through planRawChunks, so whitespace always rides
// with neighboring text and no field value is ever empty or whitespace-only.
type verbatimSection struct {
	name   string
	lines  []string
	bullet string
}

// fields packs the section into numbered embed fields, each value within the
// field budget: the first named after the section, the rest numbered
// (`📝 Week notes · 2`), and any that continues the previous field's line
// mid-text marked continued. A raw section the planner cannot pack losslessly
// yields no fields rather than an invalid one; the reader never includes such
// a body (ADR-0015 §4), so it never reaches a rendered report.
func (s verbatimSection) fields() []notify.EmbedField {
	var chunks []detailChunk
	if s.bullet == "" {
		chunks, _ = planRawChunks(strings.Join(s.lines, "\n"))
	} else {
		chunks = bulletChunks(s.lines, s.bullet)
	}
	fields := make([]notify.EmbedField, 0, len(chunks))
	for i, c := range chunks {
		name := s.name
		if i > 0 {
			name = fmt.Sprintf("%s%s%d", s.name, titleSeparator, i+1)
		}
		if c.cont {
			name += continuedSuffix
		}
		fields = append(fields, notify.EmbedField{Name: name, Value: c.text})
	}
	return fields
}

// detailChunk is one field's worth of a verbatim section: whole lines joined
// by line breaks, or — for a line longer than a field — a slice of it. cont
// marks a chunk that continues the previous chunk's line mid-text.
type detailChunk struct {
	text string
	cont bool
}

// bulletChunks packs bulleted lines greedily: whole bullet lines share a chunk
// while they fit; a line longer than a field is split at character boundaries,
// never changing a byte, and its later slices are marked continued. This is
// the anchor-detail packing, unchanged.
func bulletChunks(lines []string, bullet string) []detailChunk {
	var chunks []detailChunk
	for _, line := range lines {
		for i, seg := range splitByLen(bullet+line, maxFieldValueLen) {
			last := len(chunks) - 1
			if i == 0 && last >= 0 && discordLen(chunks[last].text)+1+discordLen(seg) <= maxFieldValueLen {
				chunks[last].text += "\n" + seg
				continue
			}
			chunks = append(chunks, detailChunk{text: seg, cont: i > 0})
		}
	}
	return chunks
}

// packVerbatim lays the sections' fields, in order, after the card's own while
// the card stays within Discord's field-count and total budgets; whatever does
// not fit continues in numbered continuation embeds carrying the card's
// sidebar color. Each continuation is titled with the sections it carries, in
// order, and its part (`📝 Week notes · ⚓ Anchor detail · part 2 of 3`). The
// card is part 1. With no section fields it returns the card alone.
func packVerbatim(card notify.Embed, sections []verbatimSection) []notify.Embed {
	names := make([]string, 0, len(sections))
	for _, s := range sections {
		names = append(names, s.name)
	}
	reserve := continuationTitleReserve(names)

	embeds := []notify.Embed{card}
	carried := [][]string{nil} // the section names each embed's verbatim fields belong to
	for _, s := range sections {
		for _, f := range s.fields() {
			if !embedFits(embeds[len(embeds)-1], f) {
				embeds = append(embeds, notify.Embed{Title: reserve, Color: card.Color})
				carried = append(carried, nil)
			}
			last := len(embeds) - 1
			embeds[last].Fields = append(embeds[last].Fields, f)
			if c := carried[last]; len(c) == 0 || c[len(c)-1] != s.name {
				carried[last] = append(c, s.name)
			}
		}
	}
	for i := 1; i < len(embeds); i++ {
		embeds[i].Title = sectionContinuationTitle(carried[i], i+1, len(embeds))
	}
	return embeds
}

// sectionContinuationTitle names continuation message part of total by the
// sections it carries, in order: `📝 Week notes · part 2 of 3`,
// `📝 Week notes · ⚓ Anchor detail · part 2 of 3`, `⚓ Anchor detail · part 3
// of 3`. A continuation carrying only anchor detail is titled exactly as it
// always has been ([continuationTitle]).
func sectionContinuationTitle(sections []string, part, total int) string {
	return fmt.Sprintf("%s%spart %d of %d", strings.Join(sections, titleSeparator), titleSeparator, part, total)
}

// continuationTitle names continuation message part of total of a batch that
// carries only anchor detail — `⚓ Anchor detail · part 2 of 3` — the main
// report card is part 1. It is the anchor-only title anchor detail has always
// carried; [sectionContinuationTitle] produces the same string for it.
func continuationTitle(part, total int) string {
	return sectionContinuationTitle([]string{fieldAnchorDetail}, part, total)
}

// continuationTitleReserve is the longest continuation title a batch carrying
// these sections can need — every section's name plus `part 99999 of 99999` —
// which the packer budgets for before the part count is known. The real title,
// set once every part is packed, names a subset of the same sections in the
// same order, so it is never longer and a packed part never outgrows its
// budget. A batch with both sections reserves both names; an anchor-only batch
// reserves exactly what it always has, so its layout is byte-identical.
func continuationTitleReserve(sections []string) string {
	return sectionContinuationTitle(sections, 99999, 99999)
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

// planRawChunks is the pure chunk planner for a verbatim raw-text section. It
// partitions body into field values of at most maxFieldValueLen UTF-16 code
// units, each holding at least one non-whitespace character, because Discord
// rejects an empty or whitespace-only field value. A chunk boundary either
// consumes one line break — the next chunk starts the next line, unmarked — or
// falls mid-line at a character boundary, and the next chunk is marked cont.
// Joining the chunks in order — a line break before an unmarked chunk, nothing
// before a continued one — reproduces body byte for byte.
//
// Split points shift so whitespace rides with a neighboring non-whitespace
// character: a greedy split that would strand a whitespace-only piece is not
// proof the body cannot be packed. The planner first finds, back to front,
// every position a valid packing of the rest can start at, then packs front to
// back: each chunk takes as many whole lines as fit, and only a line longer
// than a field is split mid-line, as late as a valid packing of the rest
// allows. ok is false when no valid packing exists at all (or body is empty,
// or not valid UTF-8, which cannot be sent byte for byte).
func planRawChunks(body string) (chunks []detailChunk, ok bool) {
	if body == "" || !utf8.ValidString(body) {
		return nil, false
	}
	t := newRawText(body)
	startable := t.startable()
	if !startable[0] {
		return nil, false
	}
	for k, cont := 0, false; ; {
		end := t.chunkEnd(k, startable)
		chunks = append(chunks, detailChunk{text: body[t.offs[k]:t.offs[end]], cont: cont})
		if end == t.n {
			return chunks, true
		}
		k, cont = t.next(end)
	}
}

// rawText is a body indexed by character for the chunk planner: each
// character's byte offset (then len(body)), UTF-16 width, and whether it is
// non-whitespace or a line break.
type rawText struct {
	n     int
	offs  []int
	width []int
	solid []bool
	isNL  []bool
}

// newRawText indexes body by character.
func newRawText(body string) rawText {
	var t rawText
	for i, r := range body {
		t.offs = append(t.offs, i)
		t.width = append(t.width, runeUnits(r))
		t.solid = append(t.solid, !unicode.IsSpace(r))
		t.isNL = append(t.isNL, r == '\n')
	}
	t.n = len(t.width)
	t.offs = append(t.offs, len(body))
	return t
}

// next is where the chunk after one ending before character e starts, and
// whether that boundary is mid-line. A boundary at a line break consumes it.
func (t rawText) next(e int) (start int, cont bool) {
	if t.isNL[e] {
		return e + 1, false
	}
	return e, true
}

// validEnds calls visit for every e such that characters [k, e) form a valid
// chunk — within the field budget and holding a non-whitespace character — in
// increasing order, until visit returns false.
func (t rawText) validEnds(k int, visit func(e int) bool) {
	units, hasSolid := 0, false
	for e := k + 1; e <= t.n; e++ {
		units += t.width[e-1]
		if units > maxFieldValueLen {
			return
		}
		hasSolid = hasSolid || t.solid[e-1]
		if hasSolid && !visit(e) {
			return
		}
	}
}

// startable reports, for each character k (and the end, which is never
// startable — a chunk is never empty), whether the text from k to the end
// packs validly with a chunk starting at k. It is filled back to front.
func (t rawText) startable() []bool {
	ok := make([]bool, t.n+1)
	for k := t.n - 1; k >= 0; k-- {
		t.validEnds(k, func(e int) bool {
			ok[k] = t.endsCleanly(e, ok)
			return !ok[k]
		})
	}
	return ok
}

// endsCleanly reports whether a chunk ending before character e leaves a rest
// that packs: nothing at all, or a startable next chunk.
func (t rawText) endsCleanly(e int, startable []bool) bool {
	if e == t.n {
		return true
	}
	s, _ := t.next(e)
	return startable[s]
}

// chunkEnd picks where the chunk starting at k ends: the last whole-line end
// that fits and leaves a packable rest, else — a line longer than a field —
// the latest mid-line end that does. startable[k] guarantees one exists.
func (t rawText) chunkEnd(k int, startable []bool) int {
	lineEnd, midEnd := -1, -1
	t.validEnds(k, func(e int) bool {
		if !t.endsCleanly(e, startable) {
			return true
		}
		if e == t.n || t.isNL[e] {
			lineEnd = e
		} else {
			midEnd = e
		}
		return true
	})
	if lineEnd >= 0 {
		return lineEnd
	}
	return midEnd
}
