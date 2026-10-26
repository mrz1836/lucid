package witnessreport

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// anchorLedger is a real scaffolded Ledger driven through the router's anchor
// verbs, so the projection and the detail a report reads come from the real
// folds — the same path the CLI and the scheduler take. Every label is
// synthetic. Writes land at noon of the day they name (well past the 04:00
// default rollover), so each event is dated on the day its test says.
type anchorLedger struct {
	t     *testing.T
	r     *router.Router
	store *storage.Adapter
	seq   int
}

func newAnchorLedger(t *testing.T) *anchorLedger {
	t.Helper()
	_, store := lucidtest.Ledger(t, lucidtest.NestedHome())
	r := router.New(store)
	_, err := r.Boot()
	require.NoError(t, err)
	return &anchorLedger{t: t, r: r, store: store}
}

// at returns a distinct instant at noon of day, so each write carries its own
// recorded_at.
func (l *anchorLedger) at(day string) time.Time {
	l.t.Helper()
	d, err := time.Parse("2006-01-02", day)
	require.NoError(l.t, err)
	l.seq++
	return d.Add(12*time.Hour + time.Duration(l.seq)*time.Minute)
}

func (l *anchorLedger) add(label, date, kind string, allowance, window int) {
	l.t.Helper()
	_, err := l.r.AnchorAdd(router.AnchorAddRequest{
		Label: label, Date: date, Kind: kind, Allowance: allowance, Window: window, Now: l.at(date),
	})
	require.NoError(l.t, err)
}

func (l *anchorLedger) milestone(label, date, note string) {
	l.t.Helper()
	_, err := l.r.AnchorAdd(router.AnchorAddRequest{Label: label, Date: date, Note: note, Now: l.at(date)})
	require.NoError(l.t, err)
}

func (l *anchorLedger) slip(label, day, note string) string {
	l.t.Helper()
	res, err := l.r.AnchorSlip(router.AnchorSlipRequest{Label: label, Note: note, Now: l.at(day)})
	require.NoError(l.t, err)
	return res.Event.ID
}

func (l *anchorLedger) use(label, day, intent, note string) string {
	l.t.Helper()
	res, err := l.r.AnchorUse(router.AnchorUseRequest{Label: label, Intent: intent, Note: note, Now: l.at(day)})
	require.NoError(l.t, err)
	return res.Event.ID
}

func (l *anchorLedger) miss(label, day, note string) {
	l.t.Helper()
	_, err := l.r.AnchorMiss(router.AnchorMissRequest{Label: label, Note: note, Now: l.at(day)})
	require.NoError(l.t, err)
}

// confirm attests label's span from its next unconfirmed day (or from) through
// the day before today.
func (l *anchorLedger) confirm(label, today, from string) {
	l.t.Helper()
	_, err := l.r.AnchorConfirm(router.AnchorConfirmRequest{Labels: []string{label}, FromArg: from, Now: l.at(today)})
	require.NoError(l.t, err)
}

func (l *anchorLedger) void(eventID, day, reason string) {
	l.t.Helper()
	_, err := l.r.AnchorVoid(router.AnchorVoidRequest{EventID: eventID, Reason: reason, Now: l.at(day)})
	require.NoError(l.t, err)
}

// strongWeekRecords is a fully committed 07-07..07-13 week, so a report built
// over it carries no thin-logging or miss signal: any watch-out left is an
// anchor's.
func strongWeekRecords(t *testing.T) fakeRecords {
	t.Helper()
	return fakeRecords{days: loadFixture(t, "strong-week.json")}
}

// anchorReport builds the deterministic report over the Ledger's real
// projection and a strong week of day records.
func (l *anchorLedger) anchorReport() Report {
	l.t.Helper()
	r, err := BuildReport(reportNow(), l.r, strongWeekRecords(l.t))
	require.NoError(l.t, err)
	return r
}

// TestWatchOutsKindAware: each kind raises exactly its own signal, read from
// the projection's note-free numbers. A limit over its allowance is flagged
// even when every use was planned; a habit miss run reaching the alarm inside
// the week is flagged at its length; one isolated miss, a milestone and a long
// quit run raise nothing. With no other signal, an anchor watch-out colors the
// sidebar amber.
func TestWatchOutsKindAware(t *testing.T) {
	l := newAnchorLedger(t)
	l.milestone("first-gate", "2025-01-01", "")
	l.add("sobriety", "2026-01-01", engine.AnchorKindQuit, 0, 0)
	l.add("late-night-snacks", "2026-06-01", engine.AnchorKindLimit, 2, 30)
	l.use("late-night-snacks", "2026-07-01", engine.AnchorIntentPlanned, "")
	l.use("late-night-snacks", "2026-07-05", engine.AnchorIntentPlanned, "")
	l.use("late-night-snacks", "2026-07-08", engine.AnchorIntentPlanned, "")
	l.add("morning-walk", "2026-06-01", engine.AnchorKindHabit, 0, 0)
	l.miss("morning-walk", "2026-07-09", "")
	l.miss("morning-walk", "2026-07-10", "")
	l.add("evening-stretch", "2026-06-01", engine.AnchorKindHabit, 0, 0)
	l.miss("evening-stretch", "2026-07-10", "")

	r := l.anchorReport()

	assert.Equal(t, []string{
		"late-night-snacks: over allowance — 3 of 2 in the last 30 days.",
		"morning-walk: missed 2 days in a row.",
	}, r.WatchOuts, "only the over-allowance limit and the two-day miss run are flagged")
	assert.Equal(t, colorCaution, RenderEmbed(r).Color, "an anchor signal is a soft, amber one")

	t.Run("a recovery before the report does not hide a mid-week slide", func(t *testing.T) {
		l.confirm("morning-walk", "2026-07-13", "")
		r := l.anchorReport()
		var walk engine.AnchorDaysSince
		for _, a := range r.Anchors {
			if a.Label == "morning-walk" {
				walk = a
			}
		}
		require.NotNil(t, walk.Habit)
		assert.Zero(t, walk.Habit.CurrentMissRun, "the run is over")
		assert.Contains(t, r.WatchOuts, "morning-walk: missed 2 days in a row.", "it touched the week")
	})

	t.Run("the miss alarm is the configured one", func(t *testing.T) {
		mr, err := l.r.Metrics(reportNow())
		require.NoError(t, err)
		mr.Thresholds.MissAlarm = 3
		r, err := BuildReport(reportNow(), fakeNumbers{result: mr}, strongWeekRecords(t))
		require.NoError(t, err)
		assert.NotContains(t, strings.Join(r.WatchOuts, "\n"), "morning-walk", "a two-day run is below an alarm of three")
	})
}

// TestWatchOutSlipCountLine: one count-only line per quit or limit with a slip
// in the report week — a quit slip or a limit use --slip — counting slip days.
// A voided slip drops out, a slip before the week never counts, and planned
// uses never produce a line however many there are.
func TestWatchOutSlipCountLine(t *testing.T) {
	l := newAnchorLedger(t)
	l.add("doomscroll", "2026-06-01", engine.AnchorKindQuit, 0, 0)
	l.slip("doomscroll", "2026-06-20", "")
	l.slip("doomscroll", "2026-07-08", "")
	l.slip("doomscroll", "2026-07-11", "")
	misheard := l.slip("doomscroll", "2026-07-12", "")
	l.void(misheard, "2026-07-12", "")

	l.add("late-night-snacks", "2026-06-01", engine.AnchorKindLimit, 10, 30)
	l.use("late-night-snacks", "2026-07-09", engine.AnchorIntentSlip, "")
	l.use("late-night-snacks", "2026-07-10", engine.AnchorIntentPlanned, "")
	wrong := l.use("late-night-snacks", "2026-07-12", engine.AnchorIntentSlip, "")
	l.void(wrong, "2026-07-12", "")

	l.add("dessert", "2026-06-01", engine.AnchorKindLimit, 10, 30)
	l.use("dessert", "2026-07-08", engine.AnchorIntentPlanned, "")
	l.use("dessert", "2026-07-09", engine.AnchorIntentPlanned, "")
	l.use("dessert", "2026-07-11", engine.AnchorIntentPlanned, "")

	r := l.anchorReport()

	assert.Equal(t, []string{
		"2 slips on doomscroll this week.",
		"1 slip on late-night-snacks this week.",
	}, r.WatchOuts)
}

// TestHonestyLongRunNeverWatchOut (honesty invariant): a long milestone or a
// long quit run is a win, not a worry — it never produces a watch-out, however
// many days it has run, and a report with nothing else to flag stays green.
func TestHonestyLongRunNeverWatchOut(t *testing.T) {
	l := newAnchorLedger(t)
	l.milestone("first-gate", "2020-01-01", "")
	l.add("sobriety", "2020-01-01", engine.AnchorKindQuit, 0, 0)
	l.slip("sobriety", "2024-03-01", "")
	l.add("early-nights", "2020-01-01", engine.AnchorKindQuit, 0, 0)

	r := l.anchorReport()

	require.Len(t, r.Anchors, 3)
	for _, a := range r.Anchors {
		assert.Greater(t, a.DaysSince, 800, "%s has run for years", a.Label)
	}
	assert.Empty(t, r.WatchOuts, "a long run never raises a watch-out")
	assert.Equal(t, colorClear, RenderEmbed(r).Color)
	assert.NotContains(t, RenderMarkdown(r), fieldWatchOuts)
}

// civilDatePattern matches a YYYY-MM-DD civil date anywhere in a string.
var civilDatePattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// TestHonestySummaryWitnessLineNoNoteNoDate (honesty invariant): in summary
// mode — the default, where no anchor-detail reader is wired — nothing a
// witness receives carries an anchor note or an event date. The fixtures load
// every event with a distinctive note and date, so the assertion is real: no
// watch-out, no embed title, description, field or footer, no markdown line,
// and nothing in the model's request contains a note token or a civil date,
// and the report carries no anchor_detail at all.
func TestHonestySummaryWitnessLineNoNoteNoDate(t *testing.T) {
	notes := []string{"zebra-quokka-11", "zebra-quokka-22", "zebra-quokka-33", "zebra-quokka-44", "zebra-quokka-55"}
	l := newAnchorLedger(t)
	l.milestone("first-gate", "2026-02-01", notes[0])
	l.add("doomscroll", "2026-06-01", engine.AnchorKindQuit, 0, 0)
	l.slip("doomscroll", "2026-07-08", notes[1])
	l.add("late-night-snacks", "2026-06-01", engine.AnchorKindLimit, 1, 30)
	l.use("late-night-snacks", "2026-07-09", engine.AnchorIntentSlip, notes[2])
	l.use("late-night-snacks", "2026-07-10", engine.AnchorIntentPlanned, notes[3])
	l.add("morning-walk", "2026-06-01", engine.AnchorKindHabit, 0, 0)
	l.miss("morning-walk", "2026-07-10", notes[4])
	l.miss("morning-walk", "2026-07-11", notes[4])

	fake := &provider.Fake{Script: []provider.Exchange{{Content: wellFormedReply}}}
	dir := t.TempDir()
	c := New(Deps{
		SystemPrompt: writeFile(t, dir, "system.md", "You are the witness-report voice."),
		Template:     writeFile(t, dir, "template.md", "Weekly witness report template."),
		Provider:     providerFake(),
		Numbers:      l.r,
		Records:      strongWeekRecords(t),
		Build:        buildWith(fake),
	})
	r, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	require.NotEmpty(t, r.WatchOuts, "the week raises anchor watch-outs to inspect")
	assert.Empty(t, r.AnchorDetail, "summary mode reads no anchor detail")

	var surfaces []string
	surfaces = append(surfaces, r.WatchOuts...)
	for _, e := range RenderEmbeds(r) {
		surfaces = append(surfaces, embedTexts(e)...)
	}
	surfaces = append(surfaces, strings.Split(RenderMarkdown(r), "\n")...)
	for _, req := range fake.Requests {
		surfaces = append(surfaces, req.System)
		for _, m := range req.Messages {
			surfaces = append(surfaces, m.Content)
		}
	}
	for _, s := range surfaces {
		for _, n := range notes {
			assert.NotContains(t, s, n, "a summary-mode surface never carries a note")
		}
		assert.Falsef(t, civilDatePattern.MatchString(s), "a summary-mode surface never carries a date: %q", s)
	}

	raw, err := json.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"anchor_detail"`, "summary-mode --json has no anchor_detail key")
}

// embedTexts returns every witness-visible string in an embed.
func embedTexts(e notify.Embed) []string {
	out := make([]string, 0, 3+2*len(e.Fields))
	out = append(out, e.Title, e.Description, e.Footer)
	for _, f := range e.Fields {
		out = append(out, f.Name, f.Value)
	}
	return out
}
