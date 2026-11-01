package witnessreport

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/router"
)

// The continuation titles ADR-0015 §6 pins, spelled out as literals rather than
// built from the package's title functions, so the tests independently prove
// the strings friends read.
const (
	notesOnlyTitle  = "📝 Week notes · part %d of %d"
	mixedTitle      = "📝 Week notes · ⚓ Anchor detail · part %d of %d"
	anchorOnlyTitle = "⚓ Anchor detail · part %d of %d"
)

// notesReport is a minimal synthetic report carrying included week notes: the
// status and body exactly as the reader leaves them for a valid, fresh file.
func notesReport(body string) Report {
	return Report{
		ISOWeek:      "2026-W29",
		Adherence:    engine.Window{Length: 30},
		Asks:         []string{askGeneric},
		NotesStatus:  NotesIncluded,
		NotesWritten: "2026-07-12",
		Notes:        body,
	}
}

// withNotes includes body as the report's week notes, as the compose pass does.
func withNotes(r Report, body string) Report {
	r.NotesStatus, r.NotesWritten, r.Notes = NotesIncluded, "2026-07-12", body
	return r
}

// longWarmRead is a synthetic warm read long enough to take about half of the
// card's budget, so the verbatim sections behind it spill into continuations.
func longWarmRead() string { return strings.Repeat("A steady, honest week. ", 120) }

// includedBody runs a notes body through the real reader, proving the reader
// includes it unchanged, and returns it for the render.
func includedBody(t *testing.T, body string) string {
	t.Helper()
	got := parseWeekNotes([]byte(notesFile("2027-02-07", body)), notesNow())
	require.Equalf(t, NotesIncluded, got.Status, "the reader includes the body (problem: %q)", got.Problem)
	require.Equal(t, body, got.Body, "the reader keeps the body byte for byte")
	return got.Body
}

// reassembleSection joins one verbatim section's fields across a rendered
// batch back into its text: a line break before each field, except none
// before the first and none before a field marked continued.
func reassembleSection(embeds []notify.Embed, name string) string {
	var b strings.Builder
	first := true
	for _, e := range embeds {
		for _, f := range e.Fields {
			if !strings.HasPrefix(f.Name, name) {
				continue
			}
			if !first && !strings.HasSuffix(f.Name, continuedSuffix) {
				b.WriteString("\n")
			}
			b.WriteString(f.Value)
			first = false
		}
	}
	return b.String()
}

// sectionFieldsOf returns a rendered batch's fields belonging to one section.
func sectionFieldsOf(embeds []notify.Embed, name string) []notify.EmbedField {
	var out []notify.EmbedField
	for _, e := range embeds {
		for _, f := range e.Fields {
			if strings.HasPrefix(f.Name, name) {
				out = append(out, f)
			}
		}
	}
	return out
}

// fieldNames lists an embed's field names in order.
func fieldNames(e notify.Embed) []string {
	out := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		out = append(out, f.Name)
	}
	return out
}

// carriedSections names the verbatim sections an embed's fields belong to, in
// order — what its continuation title must name.
func carriedSections(e notify.Embed) []string {
	var out []string
	for _, name := range []string{fieldWeekNotes, fieldAnchorDetail} {
		for _, f := range e.Fields {
			if strings.HasPrefix(f.Name, name) {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// assertInOrder asserts each part occurs in s, each after the one before.
func assertInOrder(t *testing.T, s string, parts ...string) {
	t.Helper()
	at := 0
	for _, p := range parts {
		i := strings.Index(s[at:], p)
		if !assert.GreaterOrEqualf(t, i, 0, "%q missing, or out of order", p) {
			return
		}
		at += i + len(p)
	}
}

// syntheticDetailEvents is a small synthetic full anchor detail inside the
// report week (2026-07-07 → 2026-07-13).
func syntheticDetailEvents() []router.AnchorDetailEvent {
	return []router.AnchorDetailEvent{
		{
			ID: "anchorev_2026_07_08_a", Label: "morning-walk", AnchorKind: engine.AnchorKindHabit,
			Type: engine.AnchorEventMiss, Day: "2026-07-08", Note: "rain all morning",
		},
		{
			ID: "anchorev_2026_07_12_a", Label: "morning-walk", AnchorKind: engine.AnchorKindHabit,
			Type: engine.AnchorEventConfirm, From: "2026-07-09", Day: "2026-07-12",
		},
	}
}

// TestWeekNotesFieldsLossless: the week-notes section packs into numbered
// fields — `📝 Week notes`, `📝 Week notes · 2`, …, marked ` (continued)` when
// a field carries on the previous one's line — whose values reassemble to the
// markdown body byte for byte, across paragraphs, blank lines landing on a
// field boundary, a line longer than a field, emoji (two UTF-16 units each)
// and the shifted-boundary body. No field value is ever empty or
// whitespace-only, and none outgrows the field budget. A body with no valid
// lossless packing yields no chunks, is rejected by the reader with the fixed
// reason, and so never reaches a field.
func TestWeekNotesFieldsLossless(t *testing.T) {
	cases := map[string]string{
		"paragraphs": "A heavy stretch at work; he still closed out every night.\n\n" +
			"A friend's call on Thursday helped.\n\n\nAsk him how the new routine is landing.",
		"blank_lines_at_field_boundary": strings.Repeat("x", 1020) + "\n\n\n\n" + strings.Repeat("y", 10),
		"indented_lines":                "Three things went well:\n  - the early nights\n  - the walks\n\tand the quiet mornings",
		"overlong_single_line":          "Opening line.\n" + strings.TrimSpace(strings.Repeat("long day, still went. ", 120)) + "\nClosing line.",
		"emoji":                         strings.Repeat("🙂", 1500) + "\n\n" + strings.Repeat("🌊 calm water ", 40),
		"shifted_boundary":              strings.Repeat("x", 100) + "a" + strings.Repeat(" ", 2046) + "b",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			body := includedBody(t, raw)
			r := notesReport(body)
			embeds := RenderEmbeds(r)

			fields := sectionFieldsOf(embeds, fieldWeekNotes)
			require.NotEmpty(t, fields)
			for i, f := range fields {
				want := fieldWeekNotes
				if i > 0 {
					want = fmt.Sprintf("%s · %d", fieldWeekNotes, i+1)
				}
				assert.Equal(t, want, strings.TrimSuffix(f.Name, continuedSuffix), "field %d is numbered in order", i+1)
				assert.Falsef(t, i == 0 && strings.HasSuffix(f.Name, continuedSuffix), "the first field never continues anything")
				assert.NotEmptyf(t, strings.TrimSpace(f.Value), "field %q is never empty or whitespace-only", f.Name)
				assert.LessOrEqualf(t, discordLen(f.Value), maxFieldValueLen, "field %q within the budget", f.Name)
				assert.Truef(t, utf8.ValidString(f.Value), "field %q never splits a character", f.Name)
			}
			assert.Equal(t, body, reassembleSection(embeds, fieldWeekNotes), "reassembly is lossless")
			assert.Contains(t, RenderMarkdown(r), "**"+fieldWeekNotes+"**\n"+body+"\n\n_"+footerNote+"_",
				"the markdown prints the same body, raw and not bulleted")
		})
	}

	t.Run("overlong_line_continues", func(t *testing.T) {
		fields := sectionFieldsOf(RenderEmbeds(notesReport(includedBody(t, cases["overlong_single_line"]))), fieldWeekNotes)
		require.Len(t, fields, 4)
		assert.Equal(t, []string{
			fieldWeekNotes,
			fieldWeekNotes + " · 2",
			fieldWeekNotes + " · 3" + continuedSuffix,
			fieldWeekNotes + " · 4" + continuedSuffix,
		}, []string{fields[0].Name, fields[1].Name, fields[2].Name, fields[3].Name},
			"the overlong line starts its own field and its overflow continues it")
	})

	t.Run("blank_lines_ride_with_text", func(t *testing.T) {
		fields := sectionFieldsOf(RenderEmbeds(notesReport(cases["blank_lines_at_field_boundary"])), fieldWeekNotes)
		require.Len(t, fields, 2)
		assert.Equal(t, strings.Repeat("x", 1020)+"\n\n\n", fields[0].Value,
			"the blank lines ride with the text before them rather than standing alone")
		assert.Equal(t, strings.Repeat("y", 10), fields[1].Value, "the next field starts the next line, unmarked")
		assert.False(t, strings.HasSuffix(fields[1].Name, continuedSuffix))
	})

	t.Run("unpackable_body_never_reaches_a_field", func(t *testing.T) {
		body := "a" + strings.Repeat(" ", 3998) + "b"
		chunks, ok := planRawChunks(body)
		assert.False(t, ok, "no valid lossless packing exists")
		assert.Empty(t, chunks)

		got := parseWeekNotes([]byte(notesFile("2027-02-07", body)), notesNow())
		assert.Equal(t, WeekNotes{Status: NotesInvalid, Written: "2027-02-07", Problem: notesProblemUnpackable}, got,
			"the reader rejects it whole, matching TestParseWeekNotes/unpackable_body")

		r := notesReport("")
		r.NotesStatus, r.NotesProblem = got.Status, got.Problem
		assert.Empty(t, sectionFieldsOf(RenderEmbeds(r), fieldWeekNotes))
		assert.NotContains(t, RenderMarkdown(r), fieldWeekNotes)

		forced := notesReport(body) // a report the reader could never produce
		for _, e := range RenderEmbeds(forced) {
			for _, f := range e.Fields {
				assert.NotEmptyf(t, strings.TrimSpace(f.Value), "even a forced body yields no invalid field: %q", f.Name)
			}
		}
		assert.Empty(t, sectionFieldsOf(RenderEmbeds(forced), fieldWeekNotes))
	})
}

// TestWeekNotesSectionOrder: the section follows ⚠️ Watch-outs — or the asks
// when there are none — and precedes ⚓ Anchor detail, in the embeds and in the
// markdown alike. RenderEmbed stays the numbers card, the anchor detail's own
// fields are unchanged by the notes ahead of them, and a report whose notes
// were not included renders exactly as one with no notes_file at all.
func TestWeekNotesSectionOrder(t *testing.T) {
	t.Run("after_watch_outs", func(t *testing.T) {
		base, _ := buildFromFixture(t, "miss-heavy-week.json")
		require.NotEmpty(t, base.WatchOuts)
		r := withNotes(base, synthNotes)

		embeds := RenderEmbeds(r)
		require.Len(t, embeds, 1, "short notes fit on the card")
		names := fieldNames(embeds[0])
		assert.Equal(t, append(fieldNames(RenderEmbed(r)), fieldWeekNotes), names)
		assert.Equal(t, []string{fieldAsks, fieldWatchOuts, fieldWeekNotes}, names[len(names)-3:])
		assertInOrder(t, RenderMarkdown(r),
			"**"+fieldAsks+"**", "**"+fieldWatchOuts+"**", "**"+fieldWeekNotes+"**\n"+synthNotes, "_"+footerNote+"_")
	})

	t.Run("after_asks_without_watch_outs", func(t *testing.T) {
		base, _ := buildFromFixture(t, "strong-week.json")
		require.Empty(t, base.WatchOuts)
		r := withNotes(base, synthNotes)

		names := fieldNames(RenderEmbeds(r)[0])
		assert.Equal(t, []string{fieldAsks, fieldWeekNotes}, names[len(names)-2:])
		md := RenderMarkdown(r)
		assert.NotContains(t, md, fieldWatchOuts)
		assertInOrder(t, md, "**"+fieldAsks+"**", "**"+fieldWeekNotes+"**\n"+synthNotes, "_"+footerNote+"_")
	})

	t.Run("before_anchor_detail", func(t *testing.T) {
		base, _ := buildFromFixture(t, "miss-heavy-week.json")
		base.AnchorDetail = syntheticDetailEvents()
		r := withNotes(base, synthNotes)

		embeds := RenderEmbeds(r)
		require.Len(t, embeds, 1)
		names := fieldNames(embeds[0])
		assert.Equal(t, []string{fieldWatchOuts, fieldWeekNotes, fieldAnchorDetail}, names[len(names)-3:])
		assert.Equal(t, anchorDetailFields(base.AnchorDetail), sectionFieldsOf(embeds, fieldAnchorDetail),
			"the notes ahead never change the anchor detail's own fields")
		assert.Equal(t, RenderEmbeds(base)[0].Fields, append(RenderEmbed(r).Fields, embeds[0].Fields[len(names)-1]),
			"with the notes taken out, the batch is exactly the anchor-detail batch")
		assertInOrder(t, RenderMarkdown(r),
			"**"+fieldWatchOuts+"**", "**"+fieldWeekNotes+"**\n"+synthNotes+"\n\n**"+fieldAnchorDetail+"**\n",
			anchorDetailMarkdown(base.AnchorDetail), "_"+footerNote+"_")
	})

	t.Run("render_embed_is_the_numbers_card", func(t *testing.T) {
		base, _ := buildFromFixture(t, "quiet-week.json")
		r := withNotes(base, synthNotes)
		assert.Equal(t, RenderEmbed(base), RenderEmbed(r), "RenderEmbed never carries the notes")
	})

	t.Run("only_when_included", func(t *testing.T) {
		off, _ := buildFromFixture(t, "miss-heavy-week.json")
		for _, status := range []string{NotesStale, NotesEmpty, NotesInvalid, NotesUnreadable} {
			r := off
			r.NotesStatus, r.NotesProblem = status, "a fixed reason"
			r.Notes = "a body left behind must still not render" // the reader never leaves one; the gate is the status
			assert.Equalf(t, RenderEmbeds(off), RenderEmbeds(r), "%s renders exactly as off", status)
			assert.Equalf(t, RenderMarkdown(off), RenderMarkdown(r), "%s markdown is exactly off's", status)
		}
		assert.Equal(t, []notify.Embed{RenderEmbed(off)}, RenderEmbeds(off), "off is the card alone")
		assert.NotContains(t, RenderMarkdown(off), fieldWeekNotes)
	})
}

// TestContinuationTitles: a continuation embed is titled with the verbatim
// sections it carries, in order, then its part (ADR-0015 §6). A batch with
// only anchor detail keeps exactly today's titles; the reserve the packer
// budgets before the part count is known is never shorter than a real title.
func TestContinuationTitles(t *testing.T) {
	t.Run("title_strings", func(t *testing.T) {
		assert.Equal(t, fmt.Sprintf(notesOnlyTitle, 2, 3), sectionContinuationTitle([]string{fieldWeekNotes}, 2, 3))
		assert.Equal(t, fmt.Sprintf(mixedTitle, 2, 3), sectionContinuationTitle([]string{fieldWeekNotes, fieldAnchorDetail}, 2, 3))
		assert.Equal(t, fmt.Sprintf(anchorOnlyTitle, 3, 3), sectionContinuationTitle([]string{fieldAnchorDetail}, 3, 3))
		assert.Equal(t, "⚓ Anchor detail · part 2 of 3", continuationTitle(2, 3), "the anchor-only title is today's")
		assert.Equal(t, "⚓ Anchor detail · part 99999 of 99999", continuationTitleReserve([]string{fieldAnchorDetail}),
			"an anchor-only batch reserves exactly what it always has")
		assert.Equal(t, "📝 Week notes · ⚓ Anchor detail · part 99999 of 99999",
			continuationTitleReserve([]string{fieldWeekNotes, fieldAnchorDetail}), "a mixed batch reserves both names")
	})

	notes := includedBody(t, strings.TrimSpace(strings.Repeat("A quiet, honest week; every night closed out.\n", 85)))
	notesOnly := notesReport(notes)
	notesOnly.Narrative = longWarmRead()
	mixed := notesOnly
	mixed.AnchorDetail = oversizedDetailReport().AnchorDetail
	anchorOnly := oversizedDetailReport()

	batches := []struct {
		name     string
		r        Report
		sections []string
		titleFor func(part, total int) string
	}{
		{"notes_only", notesOnly, []string{fieldWeekNotes}, func(p, n int) string { return fmt.Sprintf(notesOnlyTitle, p, n) }},
		{"mixed", mixed, []string{fieldWeekNotes, fieldAnchorDetail}, nil},
		{"anchor_only", anchorOnly, []string{fieldAnchorDetail}, func(p, n int) string { return fmt.Sprintf(anchorOnlyTitle, p, n) }},
	}
	for _, b := range batches {
		t.Run(b.name, func(t *testing.T) {
			embeds := RenderEmbeds(b.r)
			require.Greater(t, len(embeds), 1, "the batch continues past the card")
			reserve := continuationTitleReserve(b.sections)
			for i := 1; i < len(embeds); i++ {
				e := embeds[i]
				assert.Equal(t, sectionContinuationTitle(carriedSections(e), i+1, len(embeds)), e.Title,
					"part %d is titled by the sections it carries", i+1)
				if b.titleFor != nil {
					assert.Equal(t, b.titleFor(i+1, len(embeds)), e.Title)
				}
				assert.LessOrEqualf(t, discordLen(e.Title), discordLen(reserve), "the batch reserve covers part %d", i+1)
				assert.LessOrEqualf(t, discordLen(e.Title),
					discordLen(continuationTitleReserve([]string{fieldWeekNotes, fieldAnchorDetail})),
					"the widest reserve covers part %d", i+1)
				assert.LessOrEqual(t, utf8.RuneCountInString(e.Title), 256, "within Discord's title limit")
			}
		})
	}

	t.Run("mixed_layout", func(t *testing.T) {
		embeds := RenderEmbeds(mixed)
		require.Greater(t, len(embeds), 2)
		assert.Equal(t, fmt.Sprintf(mixedTitle, 2, len(embeds)), embeds[1].Title,
			"the notes spill off the card and the anchor detail follows them in part 2")
		assert.Equal(t, fmt.Sprintf(anchorOnlyTitle, len(embeds), len(embeds)), embeds[len(embeds)-1].Title)
		assert.Equal(t, notes, reassembleSection(embeds, fieldWeekNotes))
		assert.Equal(t, anchorDetailMarkdown(mixed.AnchorDetail), reassembleSection(embeds, fieldAnchorDetail))
	})

	t.Run("anchor_only_matches_continuation_title", func(t *testing.T) {
		embeds := RenderEmbeds(anchorOnly)
		for i := 1; i < len(embeds); i++ {
			assert.Equal(t, continuationTitle(i+1, len(embeds)), embeds[i].Title)
		}
	})
}

// atCapEmojiNotes is a synthetic, emoji-heavy notes body of exactly the cap in
// code points: an opening line of emoji longer than one field (each counts two
// UTF-16 units), then short paragraphs mixing text and emoji. It ends on a
// non-space character so the reader keeps every code point.
func atCapEmojiNotes() string {
	var b strings.Builder
	b.WriteString(strings.Repeat("🌊", 700))
	for i := 0; utf8.RuneCountInString(b.String()) < maxWeekNotesRunes; i++ {
		fmt.Fprintf(&b, "\n\nDay %d: %s", i%7+1, strings.Repeat("🌲🚶🌙", 12))
	}
	runes := []rune(b.String())[:maxWeekNotesRunes]
	if unicode.IsSpace(runes[len(runes)-1]) {
		runes[len(runes)-1] = '🙂'
	}
	return string(runes)
}

// TestWeekNotesFitDiscordLimits: a notes body at the 4,000-code-point cap,
// emoji-heavy so it costs far more UTF-16 units than code points, packs within
// Discord's budgets — every field value at most 1024 units (and characters),
// every embed at most 25 fields and 6000 units (and characters) — on its own
// and together with an oversized full anchor detail behind a long warm read,
// and both sections still reassemble exactly.
func TestWeekNotesFitDiscordLimits(t *testing.T) {
	body := includedBody(t, atCapEmojiNotes())
	require.Equal(t, maxWeekNotesRunes, utf8.RuneCountInString(body), "the body is exactly at the cap")
	require.Greater(t, discordLen(body), maxEmbedLen, "the emoji make it longer in UTF-16 units than a whole embed")

	alone := notesReport(body)
	together := oversizedDetailReport()
	together = withNotes(together, body)

	for name, r := range map[string]Report{"notes_alone": alone, "notes_with_anchor_detail": together} {
		t.Run(name, func(t *testing.T) {
			embeds := RenderEmbeds(r)
			require.Greater(t, len(embeds), 1, "the notes continue past the card")
			card := RenderEmbed(r)
			assert.Equal(t, card.Fields, embeds[0].Fields[:len(card.Fields)], "the card's own fields come first, unchanged")
			for i, e := range embeds {
				assert.LessOrEqual(t, len(e.Fields), maxEmbedFields, "embed %d field count", i+1)
				assert.LessOrEqual(t, embedLen(e), maxEmbedLen, "embed %d total (UTF-16 units)", i+1)
				chars := utf8.RuneCountInString(e.Title) + utf8.RuneCountInString(e.Description) + utf8.RuneCountInString(e.Footer)
				for _, f := range e.Fields {
					assert.True(t, utf8.ValidString(f.Value), "a field never splits a character")
					assert.NotEmpty(t, strings.TrimSpace(f.Value), "a field value is never empty or whitespace-only")
					assert.LessOrEqual(t, discordLen(f.Value), maxFieldValueLen, "field %q value (UTF-16 units)", f.Name)
					assert.LessOrEqual(t, utf8.RuneCountInString(f.Value), maxFieldValueLen, "field %q value (characters)", f.Name)
					assert.LessOrEqual(t, utf8.RuneCountInString(f.Name), 256)
					chars += utf8.RuneCountInString(f.Name) + utf8.RuneCountInString(f.Value)
				}
				assert.LessOrEqual(t, chars, maxEmbedLen, "embed %d total (characters)", i+1)
				if i > 0 {
					assert.Equal(t, card.Color, e.Color, "a continuation carries the card's sidebar color")
				}
			}
			assert.Equal(t, body, reassembleSection(embeds, fieldWeekNotes), "the notes reassemble exactly")
			if len(r.AnchorDetail) > 0 {
				assert.Equal(t, anchorDetailMarkdown(r.AnchorDetail), reassembleSection(embeds, fieldAnchorDetail),
					"the anchor detail reassembles exactly")
			}
		})
	}
}
