package witnessreport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/composekit"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
)

// providerFake is the provider config the compose tests run under; the
// builder below hands back the scripted fake regardless.
func providerFake() config.ProviderConfig { return config.ProviderConfig{Backend: "fake"} }

// buildWith returns a provider builder that always hands back p.
func buildWith(p provider.Provider) composekit.ProviderBuilder {
	return func(config.ProviderConfig) (provider.Provider, error) { return p, nil }
}

// Synthetic notes for the full-detail week. Two of them carry markers the
// witness-safe scan trips on (a dosage, a relationship detail), so a test can
// prove the section is never scanned: the notes render verbatim anyway.
const (
	noteSlip      = "after a long shift; the lab scale read 5 mg"
	noteUse       = "a slice of cake with my partner"
	noteMissOne   = "flight day"
	noteMissTwo   = "still traveling\nhotel gym closed"
	noteOutside   = "outside-the-week"
	noteVoidedUse = "misheard as a slip"
)

// detailWeekLedger writes a synthetic week of anchor events (the report week
// is 07-07..07-13): a quit slip, a limit that became one mid-week with a
// planned use and a voided slip use, a habit with two misses and a confirm, a
// habit graduated mid-week, and a miss before the week.
func detailWeekLedger(t *testing.T) *anchorLedger {
	t.Helper()
	l := newAnchorLedger(t)
	l.add("sobriety", "2026-06-01", engine.AnchorKindQuit, 0, 0)
	l.add("morning-walk", "2026-06-01", engine.AnchorKindHabit, 0, 0)
	l.add("evening-stretch", "2026-06-01", engine.AnchorKindHabit, 0, 0)
	l.miss("morning-walk", "2026-07-05", noteOutside)
	l.add("late-night-snacks", "2026-07-07", engine.AnchorKindLimit, 4, 90)
	l.slip("sobriety", "2026-07-08", noteSlip)
	l.miss("morning-walk", "2026-07-08", noteMissOne)
	l.miss("morning-walk", "2026-07-09", noteMissTwo)
	l.use("late-night-snacks", "2026-07-09", engine.AnchorIntentPlanned, noteUse)
	wrong := l.use("late-night-snacks", "2026-07-10", engine.AnchorIntentSlip, noteVoidedUse)
	l.void(wrong, "2026-07-10", "")
	_, err := l.r.AnchorSunset(router.AnchorSunsetRequest{Label: "evening-stretch", Graduated: true, Now: l.at("2026-07-11")})
	require.NoError(t, err)
	l.confirm("morning-walk", "2026-07-13", "")
	return l
}

// detailWeekLines is the section detailWeekLedger's week renders: ordered by
// label, then day, then receipt; the voided use, its void and the pre-week miss
// left out; every note verbatim.
func detailWeekLines() []string {
	return []string{
		"evening-stretch (habit) — 2026-07-11 · graduated",
		"late-night-snacks (limit) — 2026-07-07 · kind: limit, 4 in 90 days",
		"late-night-snacks (limit) — 2026-07-09 · use, planned · " + noteUse,
		"morning-walk (habit) — 2026-07-08 · miss · " + noteMissOne,
		"morning-walk (habit) — 2026-07-09 · miss · " + noteMissTwo,
		"morning-walk (habit) — 2026-06-01–2026-07-12 · confirmed",
		"sobriety (quit) — 2026-07-08 · slip · " + noteSlip,
	}
}

// detailComposer composes over the Ledger's real projection with the scripted
// provider; withDetail wires the router as the anchor-detail reader, as full
// mode does.
func detailComposer(t *testing.T, l *anchorLedger, withDetail bool, script ...provider.Exchange) (*Composer, *provider.Fake) {
	t.Helper()
	dir := t.TempDir()
	fake := &provider.Fake{Script: script}
	d := Deps{
		SystemPrompt: writeFile(t, dir, "system.md", "You are the witness-report voice."),
		Template:     writeFile(t, dir, "template.md", "Weekly witness report template."),
		Provider:     providerFake(),
		Numbers:      l.r,
		Records:      strongWeekRecords(t),
		Build:        buildWith(fake),
	}
	if withDetail {
		d.AnchorDetail = l.r
	}
	return New(d), fake
}

// reassembleDetail joins the anchor-detail fields of a rendered batch back
// into the section text: a line break before each field, except none before
// the first and none before a field marked continued (it carries on the
// previous field's line mid-text).
func reassembleDetail(embeds []notify.Embed) string {
	var b strings.Builder
	first := true
	for _, e := range embeds {
		for _, f := range e.Fields {
			if !strings.HasPrefix(f.Name, fieldAnchorDetail) {
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

// TestAnchorDetailFullRendersEveryEventVerbatim: with full detail opted in,
// every applied anchor event dated in the report week renders — slips, uses
// with their intent, misses, the confirmed span, the mid-week kind change and
// the graduation of an anchor retired during the week — with its note
// verbatim, including notes that would trip the witness-safe scan. The voided
// use, its void and the pre-week miss are left out. The embed batch carries
// exactly the markdown's text, and the model's prose still lands untouched.
func TestAnchorDetailFullRendersEveryEventVerbatim(t *testing.T) {
	l := detailWeekLedger(t)
	c, _ := detailComposer(t, l, true, provider.Exchange{Content: wellFormedReply})

	r, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	assert.True(t, r.UsedLLM, "the narrative was composed")
	assert.False(t, r.SafetyTripped, "the notes never reached the scan")
	assert.False(t, witnessSafe(noteSlip), "the dosage note would trip the scan")
	assert.False(t, witnessSafe(noteUse), "the relationship note would trip the scan")

	assert.Equal(t, detailWeekLines(), anchorDetailLines(r.AnchorDetail))
	md := RenderMarkdown(r)
	assert.Contains(t, md, "**"+fieldAnchorDetail+"**\n"+bulletList(detailWeekLines())+"\n\n_"+footerNote)
	for _, n := range []string{noteOutside, noteVoidedUse} {
		assert.NotContains(t, md, n, "a voided or out-of-week event is left out")
	}

	embeds := RenderEmbeds(r)
	assert.Equal(t, bulletList(detailWeekLines()), reassembleDetail(embeds), "the embeds carry exactly the markdown text")
	require.Len(t, embeds, 1, "a small week fits on the report card")
	assert.Equal(t, RenderEmbed(r).Fields, embeds[0].Fields[:len(RenderEmbed(r).Fields)], "the card's own fields come first, unchanged")
}

// TestAnchorDetailNeverInModelInput: the model's request is byte-identical with
// full detail on and off — the section is attached after the model pass and is
// never part of the compose body or the digest — and no anchor note reaches it.
func TestAnchorDetailNeverInModelInput(t *testing.T) {
	l := detailWeekLedger(t)
	off, fakeOff := detailComposer(t, l, false, provider.Exchange{Content: wellFormedReply})
	on, fakeOn := detailComposer(t, l, true, provider.Exchange{Content: wellFormedReply})

	rOff, err := off.Compose(context.Background(), reportNow())
	require.NoError(t, err)
	rOn, err := on.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	assert.Empty(t, rOff.AnchorDetail)
	require.NotEmpty(t, rOn.AnchorDetail, "full mode read the week's events")

	require.Len(t, fakeOff.Requests, 1)
	require.Len(t, fakeOn.Requests, 1)
	assert.Equal(t, fakeOff.Requests, fakeOn.Requests, "the model input is identical with the detail on and off")
	for _, m := range fakeOn.Requests[0].Messages {
		for _, n := range []string{noteSlip, noteUse, noteMissOne, noteMissTwo} {
			assert.NotContains(t, m.Content, n, "an anchor note never reaches the model")
		}
	}
	assert.NotContains(t, fakeOn.Requests[0].System, noteSlip)
}

// TestAnchorDetailSurvivesFallback: a provider outage, an unusable reply and a
// tripped witness-safe scan each drop only the prose — the metrics-only report
// keeps the full anchor detail, verbatim, in both renders.
func TestAnchorDetailSurvivesFallback(t *testing.T) {
	cases := []struct {
		name    string
		ex      provider.Exchange
		tripped bool
	}{
		{"provider outage", provider.Exchange{Err: fmt.Errorf("backend: %w", provider.ErrUnavailable)}, false},
		{"unusable reply", provider.Exchange{Content: "  \n "}, false},
		{"safety trip", provider.Exchange{Content: "%%NARRATIVE%%\nA good week out with my partner."}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := detailWeekLedger(t)
			c, _ := detailComposer(t, l, true, tc.ex)

			r, err := c.Compose(context.Background(), reportNow())
			require.NoError(t, err)

			assert.True(t, r.Fallback)
			assert.Equal(t, tc.tripped, r.SafetyTripped)
			assert.Empty(t, r.Narrative, "only the prose is lost")
			assert.Equal(t, detailWeekLines(), anchorDetailLines(r.AnchorDetail), "the detail survives the fallback")
			assert.Contains(t, RenderMarkdown(r), bulletList(detailWeekLines()))
			assert.Equal(t, bulletList(detailWeekLines()), reassembleDetail(RenderEmbeds(r)))
		})
	}
}

// TestAnchorDetailComposeReadErrorIsLoud: a failing detail read never yields a
// report quietly missing the section its operator opted in to.
func TestAnchorDetailComposeReadErrorIsLoud(t *testing.T) {
	l := newAnchorLedger(t)
	c, _ := detailComposer(t, l, false, provider.Exchange{Content: wellFormedReply})
	c.detail = failingDetail{}

	_, err := c.Compose(context.Background(), reportNow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read anchor detail")
}

type failingDetail struct{}

func (failingDetail) AnchorDetail(_ time.Time) ([]router.AnchorDetailEvent, error) {
	return nil, errors.New("synthetic read failure")
}

// oversizedDetailReport is a synthetic report whose full detail far outgrows
// one embed: a long warm read on the card, dozens of events, and notes that are
// long, multi-line, and full of characters outside the Basic Multilingual Plane
// (each counting two toward Discord's limits) and accented text.
func oversizedDetailReport() Report {
	events := make([]router.AnchorDetailEvent, 0, 48)
	labels := []string{"evening-stretch", "late-night-snacks", "morning-walk", "sobriety"}
	for i := range 48 {
		note := fmt.Sprintf("note %02d — short and plain", i)
		switch i % 6 {
		case 1:
			note = fmt.Sprintf("note %02d 🌊🌲 ", i) + strings.Repeat("crème brûlée by the sea 🌊 ", 60)
		case 3:
			note = fmt.Sprintf("note %02d line one\nline two\n\nline four ", i) + strings.Repeat("long day, still went. ", 90)
		case 5:
			note = fmt.Sprintf("note %02d ", i) + strings.Repeat("🚶", 700) // 1400 code units alone
		}
		events = append(events, router.AnchorDetailEvent{
			ID: fmt.Sprintf("anchorev_2026_07_%02d_%s", 7+i%7, engine.SlotLabel(i)), Label: labels[i%len(labels)],
			AnchorKind: engine.AnchorKindHabit, Type: engine.AnchorEventMiss,
			Day: fmt.Sprintf("2026-07-%02d", 7+i%7), Note: note,
		})
	}
	return Report{
		ISOWeek:      "2026-W29",
		Adherence:    engine.Window{Length: 30},
		Narrative:    strings.Repeat("A steady, honest week. ", 120),
		Asks:         []string{askGeneric},
		WatchOuts:    []string{"morning-walk: missed 2 days in a row."},
		AnchorDetail: events,
	}
}

// TestAnchorDetailFitsDiscordLimits: an oversized full detail is chunked into
// numbered fields and numbered continuation messages that each stay within
// Discord's budgets — every field value at most 1024 characters, every embed at
// most 25 fields and 6000 characters, whether counted in characters or UTF-16
// code units — while nothing is dropped: no character is ever split, and
// joining the parts in order reproduces the markdown section exactly, with
// every note included.
func TestAnchorDetailFitsDiscordLimits(t *testing.T) {
	r := oversizedDetailReport()
	embeds := RenderEmbeds(r)

	require.Greater(t, len(embeds), 2, "the detail continues across several messages")
	card := RenderEmbed(r)
	assert.Equal(t, card.Title, embeds[0].Title)
	assert.Equal(t, card.Description, embeds[0].Description)
	assert.Equal(t, card.Footer, embeds[0].Footer)
	assert.Equal(t, card.Fields, embeds[0].Fields[:len(card.Fields)], "the card's own fields come first, unchanged")

	sawContinued := false
	for i, e := range embeds {
		assert.LessOrEqual(t, len(e.Fields), maxEmbedFields, "embed %d field count", i+1)
		assert.LessOrEqual(t, embedLen(e), maxEmbedLen, "embed %d total (UTF-16 units)", i+1)
		chars := utf8.RuneCountInString(e.Title) + utf8.RuneCountInString(e.Description) + utf8.RuneCountInString(e.Footer)
		for _, f := range e.Fields {
			assert.True(t, utf8.ValidString(f.Value), "a field never splits a character")
			assert.NotEmpty(t, strings.TrimSpace(f.Value), "a field value is never empty")
			assert.LessOrEqual(t, discordLen(f.Value), maxFieldValueLen, "field %q value (UTF-16 units)", f.Name)
			assert.LessOrEqual(t, utf8.RuneCountInString(f.Value), maxFieldValueLen, "field %q value (characters)", f.Name)
			assert.LessOrEqual(t, utf8.RuneCountInString(f.Name), 256)
			chars += utf8.RuneCountInString(f.Name) + utf8.RuneCountInString(f.Value)
			sawContinued = sawContinued || strings.HasSuffix(f.Name, continuedSuffix)
		}
		assert.LessOrEqual(t, chars, maxEmbedLen, "embed %d total (characters)", i+1)
		if i > 0 {
			assert.Equal(t, continuationTitle(i+1, len(embeds)), e.Title)
			assert.Equal(t, card.Color, e.Color, "a continuation carries the card's sidebar color")
		}
	}
	assert.True(t, sawContinued, "a note longer than one field continues in a numbered continued field")

	joined := reassembleDetail(embeds)
	assert.Equal(t, anchorDetailMarkdown(r.AnchorDetail), joined, "joining the parts reproduces the section exactly")
	for _, e := range r.AnchorDetail {
		assert.Contains(t, joined, e.Note, "no note is dropped")
	}
	assert.NotContains(t, joined, "more", "there is never an \"and N more\"")
	assert.Contains(t, RenderMarkdown(r), anchorDetailMarkdown(r.AnchorDetail))
}

// TestAnchorDetailSplitByLen pins the splitter: within the limit a string comes
// back whole; past it, slices respect the limit in UTF-16 units, never split a
// character, and concatenate back byte for byte.
func TestAnchorDetailSplitByLen(t *testing.T) {
	assert.Equal(t, []string{"short"}, splitByLen("short", 10))
	s := strings.Repeat("a🌊", 5) // 15 units
	parts := splitByLen(s, 4)
	assert.Equal(t, s, strings.Join(parts, ""))
	for _, p := range parts {
		assert.LessOrEqual(t, discordLen(p), 4)
		assert.True(t, utf8.ValidString(p))
	}
	assert.Equal(t, 2, discordLen("🌊"))
}
