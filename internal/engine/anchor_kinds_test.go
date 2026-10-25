package engine

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kindAnchorID is the synthetic anchor a single-anchor kind ledger holds.
const kindAnchorID = "anchor_2025_12_01_a"

// kindLedger is a synthetic Ledger the kind-math tests build write by write,
// the way the verbs append: definition records to anchors.json, events and
// pair intents to the event store. Every event is minted a real id from its
// day, so the numbers a test reads come through the same fold metrics uses.
type kindLedger struct {
	t       *testing.T
	anchors AnchorLog
	events  AnchorEventLog
	writes  int
}

func newKindLedger(t *testing.T) *kindLedger {
	t.Helper()
	return &kindLedger{
		t:       t,
		anchors: AnchorLog{Version: AnchorVersion},
		events:  AnchorEventLog{Version: AnchorEventsVersion},
	}
}

// stamp returns an RFC3339 append time on day, distinct for every write, so a
// pair intent matches only its own halves.
func (l *kindLedger) stamp(day string) string {
	l.writes++
	return fmt.Sprintf("%sT21:%02d:%02dZ", day, l.writes/60%60, l.writes%60)
}

// metricsAt projects the ledger through BuildMetrics at now in loc.
func (l *kindLedger) metricsAt(now time.Time, loc *time.Location, th AnchorThresholds) Metrics {
	return BuildMetrics(MetricsInput{
		Chain:      DefaultChain(),
		Anchors:    LatestAnchors(l.anchors),
		Now:        now,
		Clocks:     defaultClocks(l.t),
		Loc:        loc,
		AnchorLog:  l.anchors,
		Events:     l.events,
		Thresholds: th,
	})
}

// metrics projects the ledger at noon UTC on today, past the 04:00 rollover,
// so the logical day is today.
func (l *kindLedger) metrics(today string, th AnchorThresholds) Metrics {
	return l.metricsAt(civil(today).Add(12*time.Hour), time.UTC, th)
}

// kindAnchor is one anchor of a kind ledger; its methods append as that anchor.
type kindAnchor struct {
	l  *kindLedger
	id string
}

// anchor records a new anchor's first definition.
func (l *kindLedger) anchor(id, label, date string) kindAnchor {
	l.anchors.History = append(l.anchors.History, Anchor{ID: id, Label: label, Date: date, RecordedAt: l.stamp(date)})
	return kindAnchor{l: l, id: id}
}

// newKindAnchor is a one-anchor ledger.
func newKindAnchor(t *testing.T, label, date string) kindAnchor {
	t.Helper()
	return newKindLedger(t).anchor(kindAnchorID, label, date)
}

// latest returns the anchor's latest definition record.
func (a kindAnchor) latest() Anchor {
	h := a.l.anchors.History
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].ID == a.id {
			return h[i]
		}
	}
	a.l.t.Fatalf("no record for %s", a.id)
	return Anchor{}
}

// correct appends a definition record moving the date: an anchor add
// correction, which moves a quit's start.
func (a kindAnchor) correct(date string) {
	rec := a.latest()
	rec.Date, rec.RecordedAt = date, a.l.stamp(date)
	a.l.anchors.History = append(a.l.anchors.History, rec)
}

// minted stamps an event as this anchor's, with a fresh id from its day.
func (a kindAnchor) minted(e AnchorEvent) AnchorEvent {
	day, err := time.Parse(dateLayout, e.Day)
	require.NoError(a.l.t, err)
	e.ID = NextAnchorEventID(a.l.events, day)
	e.AnchorID = a.id
	e.RecordedAt = a.l.stamp(e.Day)
	return e
}

// add appends one event and returns its id (the receipt).
func (a kindAnchor) add(e AnchorEvent) string {
	e = a.minted(e)
	a.l.events.Events = append(a.l.events.Events, e)
	return e.ID
}

func (a kindAnchor) kind(day, kind string, allowance, window int) {
	a.add(AnchorEvent{Type: AnchorEventKind, Day: day, Kind: kind, Allowance: allowance, WindowDays: window})
}

func (a kindAnchor) slip(day string) {
	a.add(AnchorEvent{Type: AnchorEventSlip, Day: day})
}

func (a kindAnchor) use(day, intent string) string {
	return a.add(AnchorEvent{Type: AnchorEventUse, Day: day, Intent: intent})
}

func (a kindAnchor) miss(day string) string {
	return a.add(AnchorEvent{Type: AnchorEventMiss, Day: day})
}

func (a kindAnchor) confirm(from, through string) string {
	return a.add(AnchorEvent{Type: AnchorEventConfirm, Day: through, From: from})
}

func (a kindAnchor) void(day, target string) {
	a.add(AnchorEvent{Type: AnchorEventVoid, Day: day, Target: target})
}

// paired writes a two-file operation the way the verbs do: a pair intent
// reserving the event and its compat record (the anchor's latest definition
// with only its date changed), then whichever halves land. Leaving a half out
// models a crash between the writes.
func (a kindAnchor) paired(e AnchorEvent, order, compatDate string, writeCompat, writeEvent bool) string {
	e = a.minted(e)
	compat := a.latest()
	compat.Date, compat.RecordedAt = compatDate, e.RecordedAt
	compat.Adopts, compat.AdoptedAt = "", ""
	a.l.events.PairIntents = append(a.l.events.PairIntents, AnchorPairIntent{Order: order, Event: e, Compat: compat})
	if writeCompat {
		a.l.anchors.History = append(a.l.anchors.History, compat)
	}
	if writeEvent {
		a.l.events.Events = append(a.l.events.Events, e)
	}
	return e.ID
}

// slipRedated writes a slip that moves the effective date: intent, compat
// re-date to the slip day, slip event.
func (a kindAnchor) slipRedated(day string) string {
	return a.paired(AnchorEvent{Type: AnchorEventSlip, Day: day}, AnchorPairCompatFirst, day, true, true)
}

// slipPending is that write interrupted after its first half: the re-date
// landed, the slip event did not.
func (a kindAnchor) slipPending(day string) string {
	return a.paired(AnchorEvent{Type: AnchorEventSlip, Day: day}, AnchorPairCompatFirst, day, true, false)
}

// quitRedated reclassifies to quit past a later use-day: the compat re-date to
// that use-day, then the kind event.
func (a kindAnchor) quitRedated(day, useDay string) {
	a.paired(AnchorEvent{Type: AnchorEventKind, Day: day, Kind: AnchorKindQuit}, AnchorPairCompatFirst, useDay, true, true)
}

// voidSlip voids a slip the way anchor void does: the void event, then the
// record restoring the date.
func (a kindAnchor) voidSlip(day, target, restore string) {
	a.paired(AnchorEvent{Type: AnchorEventVoid, Day: day, Target: target}, AnchorPairEventFirst, restore, true, true)
}

// facts folds the ledger and returns this anchor's facts.
func (a kindAnchor) facts() AnchorFacts {
	f, ok := FoldAnchorEvents(a.l.events, a.l.anchors)[a.id]
	require.True(a.l.t, ok, "no facts for %s", a.id)
	return f
}

// rowIn finds this anchor's active row in a projection.
func (a kindAnchor) rowIn(m Metrics) AnchorDaysSince {
	for _, r := range m.Anchors {
		if r.ID == a.id {
			return r
		}
	}
	a.l.t.Fatalf("no active row for %s", a.id)
	return AnchorDaysSince{}
}

// row projects the ledger as of today and returns this anchor's active row.
func (a kindAnchor) row(today string) AnchorDaysSince {
	return a.rowIn(a.l.metrics(today, AnchorThresholds{}))
}

// civil parses a YYYY-MM-DD test date at UTC midnight.
func civil(s string) time.Time {
	d, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return d
}

// intPtr returns a pointer to n, for the optional days_since_last_use.
func intPtr(n int) *int { return &n }

// TestMilestoneMathUnchanged: a milestone reads exactly today's days-since
// math, whichever way it is a milestone — no kind event, an explicit milestone
// kind event, or a quit converted back with its slips still in the store. The
// projection with the event store is byte-identical to the one without it,
// and no row carries a kind or a kind object.
func TestMilestoneMathUnchanged(t *testing.T) {
	l := newKindLedger(t)
	l.anchor("anchor_2025_12_16_a", "first gate", "2025-12-16")
	countdown := l.anchor("anchor_2026_01_02_a", "countdown", "2026-02-01")
	countdown.kind("2026-01-02", AnchorKindMilestone, 0, 0)
	back := l.anchor("anchor_2026_01_01_a", "sobriety", "2026-01-01")
	back.kind("2026-01-02", AnchorKindQuit, 0, 0)
	back.slipRedated("2026-01-10")
	back.kind("2026-01-12", AnchorKindMilestone, 0, 0)

	withEvents := l.metrics("2026-01-25", AnchorThresholds{})
	saved := l.events
	l.events = AnchorEventLog{}
	without := l.metrics("2026-01-25", AnchorThresholds{})
	l.events = saved

	assert.Equal(t, without, withEvents, "milestones project identically with and without the store")
	gotJSON, err := json.Marshal(withEvents)
	require.NoError(t, err)
	wantJSON, err := json.Marshal(without)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantJSON), string(gotJSON))

	require.Len(t, withEvents.Anchors, 3)
	want := map[string]int{"first gate": 40, "countdown": -7, "sobriety": 15}
	for _, r := range withEvents.Anchors {
		assert.Equal(t, want[r.Label], r.DaysSince, "%s: today's days-since math", r.Label)
		assert.Equal(t, DaysSince(civil(r.Date), civil("2026-01-25")), r.DaysSince, r.Label)
		assert.Empty(t, r.Kind, "%s: a milestone omits kind", r.Label)
		assert.Nil(t, r.Quit, r.Label)
		assert.Nil(t, r.Limit, r.Label)
		assert.Nil(t, r.Habit, r.Label)
	}
	assert.Nil(t, withEvents.AnchorsGraduated, "no graduation, so the array is omitted")
}

// TestQuitMath: days since the last slip, the longest clean run, the slip
// count and the note-free week count, table-driven over synthetic histories.
func TestQuitMath(t *testing.T) {
	tests := []struct {
		name     string
		date     string
		today    string
		build    func(a kindAnchor)
		want     QuitStats
		wantDays int // the row's plain days_since: what an older binary shows
	}{
		{
			name:  "no slips is exactly a milestone's count",
			date:  "2026-01-01",
			today: "2026-04-10",
			build: func(a kindAnchor) { a.kind("2026-01-02", AnchorKindQuit, 0, 0) },
			want:  QuitStats{CurrentRun: 99, LongestRun: 99},

			wantDays: 99,
		},
		{
			name:  "a slip restarts the run and keeps the longest",
			date:  "2026-01-01",
			today: "2026-04-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-02", AnchorKindQuit, 0, 0)
				a.slipRedated("2026-03-01")
			},
			want:     QuitStats{CurrentRun: 40, LongestRun: 59, Slips: 1, LastSlip: "2026-03-01"},
			wantDays: 40,
		},
		{
			name:  "the documented example",
			date:  "2025-05-28",
			today: "2026-01-25",
			build: func(a kindAnchor) {
				a.kind("2025-05-29", AnchorKindQuit, 0, 0)
				a.slipRedated("2025-10-15")
			},
			want:     QuitStats{CurrentRun: 102, LongestRun: 140, Slips: 1, LastSlip: "2025-10-15"},
			wantDays: 102,
		},
		{
			name:  "slips on one day are one use-day",
			date:  "2026-01-01",
			today: "2026-04-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-02", AnchorKindQuit, 0, 0)
				a.slipRedated("2026-02-01")
				a.slip("2026-02-01") // same day: the effective date does not move, so no re-date
				a.slipRedated("2026-03-15")
			},
			want:     QuitStats{CurrentRun: 26, LongestRun: 42, Slips: 2, LastSlip: "2026-03-15"},
			wantDays: 26,
		},
		{
			name:  "slips_7d is the last seven logical days, today included",
			date:  "2026-01-01",
			today: "2026-04-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-02", AnchorKindQuit, 0, 0)
				a.slipRedated("2026-04-03") // eight days back: outside the week
				a.slipRedated("2026-04-04") // seven days back: inside
			},
			want:     QuitStats{CurrentRun: 6, LongestRun: 92, Slips: 2, Slips7d: 1, LastSlip: "2026-04-04"},
			wantDays: 6,
		},
		{
			name:  "a pending re-date floors the run but is not yet a slip",
			date:  "2026-01-01",
			today: "2026-04-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-02", AnchorKindQuit, 0, 0)
				a.slipPending("2026-03-01")
			},
			want:     QuitStats{CurrentRun: 40, LongestRun: 59},
			wantDays: 40,
		},
		{
			name:  "a correction moves the start past earlier slips",
			date:  "2026-01-01",
			today: "2026-04-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-02", AnchorKindQuit, 0, 0)
				a.slipRedated("2026-02-01")
				a.correct("2026-03-01")
			},
			want:     QuitStats{CurrentRun: 40, LongestRun: 40},
			wantDays: 40,
		},
		{
			name:  "a slip on the start day still counts",
			date:  "2026-01-01",
			today: "2026-04-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindQuit, 0, 0)
				a.slip("2026-01-01") // the effective date does not move, so no re-date
			},
			want:     QuitStats{CurrentRun: 99, LongestRun: 99, Slips: 1, LastSlip: "2026-01-01"},
			wantDays: 99,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newKindAnchor(t, "sobriety", tt.date)
			tt.build(a)

			assert.Equal(t, tt.want, ComputeQuitStats(a.facts(), civil(tt.today)))

			r := a.row(tt.today)
			assert.Equal(t, AnchorKindQuit, r.Kind)
			require.NotNil(t, r.Quit)
			assert.Equal(t, tt.want, *r.Quit, "the metrics row carries the same numbers")
			assert.Equal(t, tt.wantDays, r.DaysSince, "days_since stays the plain fold of anchors.json")
			assert.LessOrEqual(t, r.Quit.CurrentRun, r.Quit.LongestRun)
		})
	}
}

// TestKindConversion: events are facts that outlive a kind change. Quit slips
// and limit uses are both use-days; each kind reads only the facts it
// understands; converting back brings the others back into the math.
func TestKindConversion(t *testing.T) {
	t.Run("a former limit's uses all count as slips under quit", func(t *testing.T) {
		a := newKindAnchor(t, "late-night-snacks", "2026-01-01")
		a.kind("2026-01-02", AnchorKindLimit, 4, 90)
		a.use("2026-02-10", AnchorIntentPlanned)
		a.use("2026-03-01", AnchorIntentSlip)
		a.quitRedated("2026-03-05", "2026-03-01")

		r := a.row("2026-04-10")
		require.NotNil(t, r.Quit)
		assert.Equal(t, QuitStats{CurrentRun: 40, LongestRun: 40, Slips: 2, LastSlip: "2026-03-01"}, *r.Quit)
		assert.Equal(t, 40, r.DaysSince, "the compat re-date makes an older binary agree")
	})

	t.Run("a planned use is a slip under quit but never a witness-week slip", func(t *testing.T) {
		a := newKindAnchor(t, "late-night-snacks", "2026-01-01")
		a.kind("2026-01-02", AnchorKindLimit, 4, 90)
		a.use("2026-04-08", AnchorIntentPlanned)
		a.quitRedated("2026-04-09", "2026-04-08")

		q := ComputeQuitStats(a.facts(), civil("2026-04-10"))
		assert.Equal(t, 1, q.Slips)
		assert.Equal(t, 0, q.Slips7d, "slips_7d counts only slip-intent days")
		assert.Equal(t, 2, q.CurrentRun)
	})

	t.Run("quit slips read as slip-intent uses under limit", func(t *testing.T) {
		a := newKindAnchor(t, "sobriety", "2026-01-01")
		a.kind("2026-01-02", AnchorKindQuit, 0, 0)
		a.slipRedated("2026-02-01")
		a.slipRedated("2026-03-20")
		a.kind("2026-04-01", AnchorKindLimit, 4, 90)

		r := a.row("2026-04-10")
		require.NotNil(t, r.Limit)
		// The folded date is the 2026-03-20 re-date; the window clips at the
		// anchor's start instead, so the 2026-02-01 slip still spends allowance.
		assert.Equal(t, LimitStats{
			Allowance: 4, WindowDays: 90, Used: 2, Remaining: 2, Slips: 2,
			LastUse: "2026-03-20", DaysSinceLastUse: intPtr(21),
		}, *r.Limit)
	})

	t.Run("habit reads misses and confirms; quit reads use-days; back again restores them", func(t *testing.T) {
		a := newKindAnchor(t, "sobriety", "2026-01-01")
		a.kind("2026-01-02", AnchorKindQuit, 0, 0)
		a.slipRedated("2026-02-01")
		a.kind("2026-03-01", AnchorKindHabit, 0, 0)
		a.confirm("2026-03-01", "2026-03-09")
		a.miss("2026-03-05")

		h := a.row("2026-03-10")
		require.NotNil(t, h.Habit)
		assert.Nil(t, h.Quit)
		assert.Equal(t, 9, h.Habit.Accounted)
		assert.Equal(t, 8, h.Habit.Kept)
		assert.Equal(t, 1, h.Habit.Misses, "the habit ignores the slip")

		a.kind("2026-03-10", AnchorKindQuit, 0, 0) // latest use-day is the folded date: no re-date
		q := a.row("2026-03-10")
		require.NotNil(t, q.Quit)
		assert.Nil(t, q.Habit)
		assert.Equal(t, QuitStats{CurrentRun: 37, LongestRun: 37, Slips: 1, LastSlip: "2026-02-01"}, *q.Quit,
			"the quit ignores the miss, and its slip is back")
	})

	t.Run("converting back to a limit restores its uses", func(t *testing.T) {
		a := newKindAnchor(t, "late-night-snacks", "2026-01-01")
		a.kind("2026-01-02", AnchorKindLimit, 4, 90)
		a.use("2026-03-01", AnchorIntentPlanned)
		a.use("2026-03-02", AnchorIntentPlanned)
		a.kind("2026-03-03", AnchorKindHabit, 0, 0)
		a.kind("2026-03-05", AnchorKindLimit, 4, 90)

		l := ComputeLimitStats(a.facts(), civil("2026-03-10"))
		assert.Equal(t, 2, l.Used)
		assert.Equal(t, 2, l.Planned)
	})

	t.Run("a limit ignores a habit's misses", func(t *testing.T) {
		a := newKindAnchor(t, "morning-walk", "2026-01-01")
		a.kind("2026-01-01", AnchorKindHabit, 0, 0)
		a.miss("2026-01-05")
		a.kind("2026-01-10", AnchorKindLimit, 4, 30)

		l := ComputeLimitStats(a.facts(), civil("2026-01-20"))
		assert.Equal(t, 0, l.Used)
		assert.Nil(t, l.DaysSinceLastUse)
	})
}

// TestLimitMath: use-days against an allowance over a rolling window,
// table-driven.
func TestLimitMath(t *testing.T) {
	tests := []struct {
		name  string
		date  string
		today string
		build func(a kindAnchor)
		want  LimitStats
	}{
		{
			name:  "the documented example",
			date:  "2025-09-02",
			today: "2026-01-25",
			build: func(a kindAnchor) {
				a.kind("2025-09-03", AnchorKindLimit, 4, 90)
				a.use("2026-01-18", AnchorIntentPlanned)
			},
			want: LimitStats{
				Allowance: 4, WindowDays: 90, Used: 1, Remaining: 3, Planned: 1,
				LastUse: "2026-01-18", DaysSinceLastUse: intPtr(7),
			},
		},
		{
			name:  "several uses on a day are one use-day, and both intents make a slip day",
			date:  "2026-01-01",
			today: "2026-03-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-02", AnchorKindLimit, 4, 90)
				a.use("2026-03-01", AnchorIntentPlanned)
				a.use("2026-03-01", AnchorIntentPlanned)
				a.use("2026-03-05", AnchorIntentPlanned)
				a.use("2026-03-05", AnchorIntentSlip)
			},
			want: LimitStats{
				Allowance: 4, WindowDays: 90, Used: 2, Remaining: 2, Planned: 1, Slips: 1, Slips7d: 1,
				LastUse: "2026-03-05", DaysSinceLastUse: intPtr(5),
			},
		},
		{
			name:  "spending exactly the allowance is fine",
			date:  "2026-01-01",
			today: "2026-03-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-02", AnchorKindLimit, 2, 30)
				a.use("2026-03-01", AnchorIntentPlanned)
				a.use("2026-03-02", AnchorIntentPlanned)
			},
			want: LimitStats{
				Allowance: 2, WindowDays: 30, Used: 2, Planned: 2,
				LastUse: "2026-03-02", DaysSinceLastUse: intPtr(8),
			},
		},
		{
			name:  "one more is over the allowance",
			date:  "2026-01-01",
			today: "2026-03-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-02", AnchorKindLimit, 2, 30)
				a.use("2026-03-01", AnchorIntentPlanned)
				a.use("2026-03-02", AnchorIntentPlanned)
				a.use("2026-03-09", AnchorIntentSlip)
			},
			want: LimitStats{
				Allowance: 2, WindowDays: 30, Used: 3, Planned: 2, Slips: 1, Slips7d: 1,
				LastUse: "2026-03-09", DaysSinceLastUse: intPtr(1), OverAllowance: true,
			},
		},
		{
			name:  "a planned use in the week is never a week slip",
			date:  "2026-01-01",
			today: "2026-03-10",
			build: func(a kindAnchor) {
				a.kind("2026-01-02", AnchorKindLimit, 4, 90)
				a.use("2026-03-10", AnchorIntentPlanned)
			},
			want: LimitStats{
				Allowance: 4, WindowDays: 90, Used: 1, Remaining: 3, Planned: 1,
				LastUse: "2026-03-10", DaysSinceLastUse: intPtr(0),
			},
		},
		{
			name:  "no use yet",
			date:  "2026-01-01",
			today: "2026-03-10",
			build: func(a kindAnchor) { a.kind("2026-01-02", AnchorKindLimit, 4, 90) },
			want:  LimitStats{Allowance: 4, WindowDays: 90, Remaining: 4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newKindAnchor(t, "late-night-snacks", tt.date)
			tt.build(a)

			assert.Equal(t, tt.want, ComputeLimitStats(a.facts(), civil(tt.today)))

			r := a.row(tt.today)
			assert.Equal(t, AnchorKindLimit, r.Kind)
			require.NotNil(t, r.Limit)
			assert.Equal(t, tt.want, *r.Limit)
			assert.Equal(t, r.Limit.Used, r.Limit.Planned+r.Limit.Slips, "planned + slips = used")
		})
	}
}

// TestHabitMath: kept of accounted days, misses, runs and the confirm
// bookkeeping, table-driven.
func TestHabitMath(t *testing.T) {
	tests := []struct {
		name  string
		date  string
		today string
		th    AnchorThresholds
		build func(a kindAnchor)
		want  HabitStats
	}{
		{
			name:  "the documented example",
			date:  "2025-11-03",
			today: "2026-01-25",
			build: func(a kindAnchor) {
				a.kind("2025-11-03", AnchorKindHabit, 0, 0)
				a.confirm("2025-11-03", "2026-01-24")
				a.miss("2026-01-25")
			},
			want: HabitStats{
				WindowDays: 30, HabitSince: "2025-11-03", Accounted: 30, Kept: 29, Misses: 1,
				CurrentMissRun: 1, MissRun7d: 1, LastConfirmed: "2026-01-24", NextConfirmFrom: "2026-01-25",
			},
		},
		{
			name:  "an unconfirmed day is unaccounted, never kept",
			date:  "2026-01-01",
			today: "2026-01-20",
			build: func(a kindAnchor) { a.kind("2026-01-01", AnchorKindHabit, 0, 0) },
			want:  HabitStats{WindowDays: 30, HabitSince: "2026-01-01", NextConfirmFrom: "2026-01-01"},
		},
		{
			name:  "a miss wins over a confirm on the same day",
			date:  "2026-01-01",
			today: "2026-01-11",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-01-10")
				a.miss("2026-01-05")
			},
			want: HabitStats{
				WindowDays: 30, HabitSince: "2026-01-01", Accounted: 10, Kept: 9, Misses: 1,
				MissRun7d: 1, LastConfirmed: "2026-01-10", NextConfirmFrom: "2026-01-11",
			},
		},
		{
			name:  "two misses in a row alarm",
			date:  "2026-01-01",
			today: "2026-01-11",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-01-08")
				a.miss("2026-01-09")
				a.miss("2026-01-10")
			},
			want: HabitStats{
				WindowDays: 30, HabitSince: "2026-01-01", Accounted: 10, Kept: 8, Misses: 2,
				CurrentMissRun: 2, Alarm: true, MissRun7d: 2, LastConfirmed: "2026-01-08", NextConfirmFrom: "2026-01-09",
			},
		},
		{
			name:  "a higher miss_alarm needs a longer run",
			date:  "2026-01-01",
			today: "2026-01-11",
			th:    AnchorThresholds{MissAlarm: 3},
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-01-08")
				a.miss("2026-01-09")
				a.miss("2026-01-10")
			},
			want: HabitStats{
				WindowDays: 30, HabitSince: "2026-01-01", Accounted: 10, Kept: 8, Misses: 2,
				CurrentMissRun: 2, MissRun7d: 2, LastConfirmed: "2026-01-08", NextConfirmFrom: "2026-01-09",
			},
		},
		{
			name:  "an unaccounted day between misses breaks the run",
			date:  "2026-01-01",
			today: "2026-01-11",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-01-07")
				a.miss("2026-01-08")
				a.miss("2026-01-10")
			},
			want: HabitStats{
				WindowDays: 30, HabitSince: "2026-01-01", Accounted: 9, Kept: 7, Misses: 2,
				CurrentMissRun: 1, MissRun7d: 1, LastConfirmed: "2026-01-07", NextConfirmFrom: "2026-01-08",
			},
		},
		{
			name:  "a recovery ends the current run, but the week still shows the slide",
			date:  "2026-01-01",
			today: "2026-01-11",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-01-10")
				a.miss("2026-01-05")
				a.miss("2026-01-06")
			},
			want: HabitStats{
				WindowDays: 30, HabitSince: "2026-01-01", Accounted: 10, Kept: 8, Misses: 2,
				MissRun7d: 2, LastConfirmed: "2026-01-10", NextConfirmFrom: "2026-01-11",
			},
		},
		{
			name:  "a run that began before the week counts at its full length",
			date:  "2026-01-01",
			today: "2026-01-11",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-01-10")
				for _, d := range []string{"2026-01-02", "2026-01-03", "2026-01-04", "2026-01-05"} {
					a.miss(d)
				}
			},
			want: HabitStats{
				WindowDays: 30, HabitSince: "2026-01-01", Accounted: 10, Kept: 6, Misses: 4,
				MissRun7d: 4, LastConfirmed: "2026-01-10", NextConfirmFrom: "2026-01-11",
			},
		},
		{
			name:  "a run that ended before the week is not in it",
			date:  "2026-01-01",
			today: "2026-01-12",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-01-11")
				for _, d := range []string{"2026-01-02", "2026-01-03", "2026-01-04", "2026-01-05"} {
					a.miss(d)
				}
			},
			want: HabitStats{
				WindowDays: 30, HabitSince: "2026-01-01", Accounted: 11, Kept: 7, Misses: 4,
				LastConfirmed: "2026-01-11", NextConfirmFrom: "2026-01-12",
			},
		},
		{
			name:  "a habit's own window",
			date:  "2026-01-01",
			today: "2026-01-31",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 14)
				a.confirm("2026-01-01", "2026-01-30")
			},
			want: HabitStats{
				WindowDays: 14, HabitSince: "2026-01-01", Accounted: 13, Kept: 13,
				LastConfirmed: "2026-01-30", NextConfirmFrom: "2026-01-31",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newKindAnchor(t, "morning-walk", tt.date)
			tt.build(a)

			assert.Equal(t, tt.want, ComputeHabitStats(a.facts(), civil(tt.today), tt.th))

			r := a.rowIn(a.l.metrics(tt.today, tt.th))
			assert.Equal(t, AnchorKindHabit, r.Kind)
			require.NotNil(t, r.Habit)
			assert.Equal(t, tt.want, *r.Habit)
			assert.Equal(t, r.Habit.Accounted-r.Habit.Misses, r.Habit.Kept, "kept = accounted - misses")
		})
	}
}

// TestHabitGraduationEligible: eligible once the accounted days since the last
// alarm run ended — or since the anchor became a habit — reach the bar. An old
// rough patch never disqualifies a habit forever.
func TestHabitGraduationEligible(t *testing.T) {
	tests := []struct {
		name  string
		date  string
		today string
		th    AnchorThresholds
		build func(a kindAnchor)
		want  bool
	}{
		{
			name:  "90 accounted days since becoming a habit",
			date:  "2026-01-01",
			today: "2026-04-01",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-03-31")
			},
			want: true,
		},
		{
			name:  "89 is not enough",
			date:  "2026-01-01",
			today: "2026-04-01",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-03-30")
			},
		},
		{
			name:  "an isolated miss does not reset the clock",
			date:  "2026-01-01",
			today: "2026-04-01",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-03-31")
				a.miss("2026-02-10")
			},
			want: true,
		},
		{
			name:  "an alarm run restarts the clock",
			date:  "2026-01-01",
			today: "2026-04-01",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-03-31")
				a.miss("2026-02-09")
				a.miss("2026-02-10")
			},
		},
		{
			name:  "an old rough patch does not disqualify forever",
			date:  "2026-01-01",
			today: "2026-05-01",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-04-30")
				a.miss("2026-01-05")
				a.miss("2026-01-06")
			},
			want: true,
		},
		{
			name:  "the bar comes from the thresholds",
			date:  "2026-01-01",
			today: "2026-02-01",
			th:    AnchorThresholds{GraduationDays: 30},
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-01-01", "2026-01-30")
			},
			want: true,
		},
		{
			name:  "vouched days before the habit day do not count toward the bar",
			date:  "2025-10-01",
			today: "2026-02-01",
			build: func(a kindAnchor) {
				a.kind("2026-01-01", AnchorKindHabit, 0, 0)
				a.confirm("2025-10-01", "2026-01-31")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newKindAnchor(t, "morning-walk", tt.date)
			tt.build(a)
			got := ComputeHabitStats(a.facts(), civil(tt.today), tt.th)
			assert.Equal(t, tt.want, got.GraduationEligible)
		})
	}
}

// TestConfirmSpan: the span one confirm attests (PD-8), table-driven. The
// default start is also what metrics publishes as next_confirm_from.
func TestConfirmSpan(t *testing.T) {
	const label = "morning-walk"
	tests := []struct {
		name      string
		date      string
		build     func(a kindAnchor)
		through   string
		from      string
		wantStart string
		wantEmpty bool
		wantErr   string
	}{
		{
			name:      "a first confirm starts the day it became a habit",
			build:     func(a kindAnchor) { a.kind("2026-03-01", AnchorKindHabit, 0, 0) },
			through:   "2026-03-07",
			wantStart: "2026-03-01",
		},
		{
			name: "the next confirm starts the day after the last",
			build: func(a kindAnchor) {
				a.kind("2026-03-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-03-01", "2026-03-07")
			},
			through:   "2026-03-14",
			wantStart: "2026-03-08",
		},
		{
			name: "a skipped week heals itself",
			build: func(a kindAnchor) {
				a.kind("2026-03-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-03-01", "2026-03-07")
			},
			through:   "2026-03-21",
			wantStart: "2026-03-08",
		},
		{
			name: "after a kind change away and back, the later of the two: the new habit day",
			build: func(a kindAnchor) {
				a.kind("2026-03-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-03-01", "2026-03-07")
				a.kind("2026-03-10", AnchorKindQuit, 0, 0)
				a.kind("2026-03-20", AnchorKindHabit, 0, 0)
			},
			through:   "2026-03-27",
			wantStart: "2026-03-20",
		},
		{
			name: "after a kind change away and back, the later of the two: the day after a later confirm",
			build: func(a kindAnchor) {
				a.kind("2026-03-01", AnchorKindHabit, 0, 0)
				a.kind("2026-03-05", AnchorKindQuit, 0, 0)
				a.kind("2026-03-10", AnchorKindHabit, 0, 0)
				a.confirm("2026-03-10", "2026-03-20")
			},
			through:   "2026-03-27",
			wantStart: "2026-03-21",
		},
		{
			name: "re-recording habit moves the became-a-habit day",
			build: func(a kindAnchor) {
				a.kind("2026-03-01", AnchorKindHabit, 0, 30)
				a.kind("2026-03-15", AnchorKindHabit, 0, 14)
			},
			through:   "2026-03-20",
			wantStart: "2026-03-15",
		},
		{
			name:      "--from vouches for an earlier stretch, back to the anchor date",
			build:     func(a kindAnchor) { a.kind("2026-03-01", AnchorKindHabit, 0, 0) },
			through:   "2026-03-07",
			from:      "2026-01-05",
			wantStart: "2026-01-05",
		},
		{
			name:      "--from later than the default narrows the span to what is vouched for",
			build:     func(a kindAnchor) { a.kind("2026-03-01", AnchorKindHabit, 0, 0) },
			through:   "2026-03-07",
			from:      "2026-03-04",
			wantStart: "2026-03-04",
		},
		{
			name:    "--from before the anchor date is rejected",
			build:   func(a kindAnchor) { a.kind("2026-03-01", AnchorKindHabit, 0, 0) },
			through: "2026-03-07",
			from:    "2026-01-04",
			wantErr: `--from can't be before "morning-walk" started (2026-01-05)`,
		},
		{
			name:    "--from after the through-day is rejected",
			build:   func(a kindAnchor) { a.kind("2026-03-01", AnchorKindHabit, 0, 0) },
			through: "2026-03-07",
			from:    "2026-03-08",
			wantErr: "--from 2026-03-08 is after the through-day 2026-03-07",
		},
		{
			name: "an already-confirmed span is empty, not an error",
			build: func(a kindAnchor) {
				a.kind("2026-03-01", AnchorKindHabit, 0, 0)
				a.confirm("2026-03-01", "2026-03-07")
			},
			through:   "2026-03-07",
			wantStart: "2026-03-08",
			wantEmpty: true,
		},
		{
			name:      "a through-day before the habit day is empty",
			build:     func(a kindAnchor) { a.kind("2026-03-01", AnchorKindHabit, 0, 0) },
			through:   "2026-02-20",
			wantStart: "2026-03-01",
			wantEmpty: true,
		},
		{
			name: "a voided confirm no longer counts",
			build: func(a kindAnchor) {
				a.kind("2026-03-01", AnchorKindHabit, 0, 0)
				c := a.confirm("2026-03-01", "2026-03-07")
				a.void("2026-03-08", c)
			},
			through:   "2026-03-14",
			wantStart: "2026-03-01",
		},
		{
			name:      "a habit day before the anchor date starts at the anchor date",
			date:      "2026-06-01",
			build:     func(a kindAnchor) { a.kind("2026-05-01", AnchorKindHabit, 0, 0) },
			through:   "2026-06-05",
			wantStart: "2026-06-01",
		},
		{
			name:    "a quit is a wrong-kind rejection",
			build:   func(a kindAnchor) { a.kind("2026-03-01", AnchorKindQuit, 0, 0) },
			through: "2026-03-07",
			wantErr: `"morning-walk" is a quit, and a confirm belongs to a habit — reclassify it as a habit first: lucid anchor kind morning-walk habit`,
		},
		{
			name:    "an anchor with no kind event is a milestone",
			build:   func(kindAnchor) {},
			through: "2026-03-07",
			wantErr: `"morning-walk" is a milestone, and a confirm belongs to a habit`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			date := tt.date
			if date == "" {
				date = "2026-01-05"
			}
			a := newKindAnchor(t, label, date)
			tt.build(a)
			f := a.facts()

			var from *time.Time
			if tt.from != "" {
				d := civil(tt.from)
				from = &d
			}
			start, last, empty, err := ConfirmSpan(f, civil(tt.through), from)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantStart, DateString(start))
			assert.Equal(t, tt.through, DateString(last))
			assert.Equal(t, tt.wantEmpty, empty)
			if from == nil {
				h := ComputeHabitStats(f, civil(tt.through).AddDate(0, 0, 1), AnchorThresholds{})
				assert.Equal(t, tt.wantStart, h.NextConfirmFrom, "next_confirm_from is the default start")
			}
		})
	}
}

// TestAnchorKindsWindowEdges: the first and last day of each rolling window,
// and the clip at the anchor's start.
func TestAnchorKindsWindowEdges(t *testing.T) {
	t.Run("limit: the first window day is in, the day before it is out, today is in", func(t *testing.T) {
		a := newKindAnchor(t, "late-night-snacks", "2026-01-01")
		a.kind("2026-01-02", AnchorKindLimit, 4, 7)
		a.use("2026-03-03", AnchorIntentPlanned) // today − 7: outside
		a.use("2026-03-04", AnchorIntentPlanned) // today − 6: the first window day
		a.use("2026-03-10", AnchorIntentSlip)    // today: the last window day

		l := ComputeLimitStats(a.facts(), civil("2026-03-10"))
		assert.Equal(t, 2, l.Used)
		assert.Equal(t, 1, l.Planned)
		assert.Equal(t, 1, l.Slips)
		assert.Equal(t, "2026-03-10", l.LastUse)
		assert.Equal(t, intPtr(0), l.DaysSinceLastUse)
	})

	t.Run("limit: the window clips at the anchor's start", func(t *testing.T) {
		a := newKindAnchor(t, "late-night-snacks", "2026-03-01")
		a.kind("2026-03-01", AnchorKindLimit, 4, 7)
		a.use("2026-03-02", AnchorIntentSlip)
		a.use("2026-03-06", AnchorIntentPlanned)
		a.correct("2026-03-06") // the start moves past the first use

		l := ComputeLimitStats(a.facts(), civil("2026-03-10"))
		assert.Equal(t, 1, l.Used, "a use before the start is outside the window")
		assert.Equal(t, 0, l.Slips7d, "and outside the week")
		assert.Equal(t, "2026-03-06", l.LastUse)
	})

	t.Run("habit: the first and last window days", func(t *testing.T) {
		a := newKindAnchor(t, "morning-walk", "2025-12-01")
		a.kind("2025-12-01", AnchorKindHabit, 0, 0)
		a.confirm("2025-12-01", "2026-01-30")
		a.miss("2026-01-01") // today − 30: outside
		a.miss("2026-01-02") // today − 29: the first window day
		a.miss("2026-01-31") // today: the last window day

		h := ComputeHabitStats(a.facts(), civil("2026-01-31"), AnchorThresholds{})
		assert.Equal(t, 30, h.Accounted)
		assert.Equal(t, 2, h.Misses)
		assert.Equal(t, 28, h.Kept)
	})

	t.Run("habit: the window and every span clip at the anchor's start", func(t *testing.T) {
		a := newKindAnchor(t, "morning-walk", "2026-01-20")
		a.kind("2026-01-20", AnchorKindHabit, 0, 0)
		a.confirm("2026-01-20", "2026-01-30")

		h := ComputeHabitStats(a.facts(), civil("2026-01-31"), AnchorThresholds{})
		assert.Equal(t, 11, h.Accounted, "the window starts at the anchor date, not 30 days back")

		a.correct("2026-01-25")
		h = ComputeHabitStats(a.facts(), civil("2026-01-31"), AnchorThresholds{})
		assert.Equal(t, 6, h.Accounted, "a confirm span never accounts a day before the start")
	})
}

// TestAnchorKindsBackdated: a backdated event lands in day order, not append
// order, and never moves anything backwards.
func TestAnchorKindsBackdated(t *testing.T) {
	t.Run("quit: a backdated slip splits the run without moving the effective date", func(t *testing.T) {
		a := newKindAnchor(t, "sobriety", "2026-01-01")
		a.kind("2026-01-02", AnchorKindQuit, 0, 0)
		a.slipRedated("2026-03-15")
		a.slip("2026-02-01") // logged later, dated earlier: the effective date stays 2026-03-15

		r := a.row("2026-04-10")
		require.NotNil(t, r.Quit)
		assert.Equal(t, QuitStats{CurrentRun: 26, LongestRun: 42, Slips: 2, LastSlip: "2026-03-15"}, *r.Quit)
		assert.Equal(t, 26, r.DaysSince)
	})

	t.Run("limit: a backdated use spends allowance in its own day", func(t *testing.T) {
		a := newKindAnchor(t, "late-night-snacks", "2026-01-01")
		a.kind("2026-01-02", AnchorKindLimit, 4, 90)
		a.use("2026-03-09", AnchorIntentPlanned)
		a.use("2026-02-01", AnchorIntentPlanned)

		l := ComputeLimitStats(a.facts(), civil("2026-03-10"))
		assert.Equal(t, 2, l.Used)
		assert.Equal(t, "2026-03-09", l.LastUse, "the latest use-day, not the latest append")
	})

	t.Run("habit: a backdated miss on a confirmed day stays a miss", func(t *testing.T) {
		a := newKindAnchor(t, "morning-walk", "2026-01-01")
		a.kind("2026-01-01", AnchorKindHabit, 0, 0)
		a.confirm("2026-01-01", "2026-01-10")
		a.miss("2026-01-03")

		h := ComputeHabitStats(a.facts(), civil("2026-01-11"), AnchorThresholds{})
		assert.Equal(t, 10, h.Accounted)
		assert.Equal(t, 1, h.Misses)
		assert.Equal(t, 9, h.Kept)
	})
}

// TestAnchorKindsDST: every kind counts whole civil days across the
// spring-forward and fall-back days in America/New_York, through the same
// UTC-civil discipline as DaysSince.
func TestAnchorKindsDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	cases := []struct {
		name       string
		before     string // the day before the transition
		transition string
		after      string // the day after: "today"
	}{
		{name: "spring forward", before: "2026-03-07", transition: "2026-03-08", after: "2026-03-09"},
		{name: "fall back", before: "2026-10-31", transition: "2026-11-01", after: "2026-11-02"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newKindLedger(t)
			milestone := l.anchor("anchor_2026_01_01_a", "first gate", tc.before)
			quit := l.anchor("anchor_2026_01_01_b", "sobriety", tc.before)
			quit.kind(tc.before, AnchorKindQuit, 0, 0)
			limit := l.anchor("anchor_2026_01_01_c", "late-night-snacks", "2026-01-01")
			limit.kind("2026-01-01", AnchorKindLimit, 4, 2)
			limit.use(tc.before, AnchorIntentPlanned)     // outside a 2-day window ending after
			limit.use(tc.transition, AnchorIntentPlanned) // inside
			habit := l.anchor("anchor_2026_01_01_d", "morning-walk", "2026-01-01")
			habit.kind(tc.before, AnchorKindHabit, 0, 0)
			habit.confirm(tc.before, tc.transition)

			now := time.Date(civil(tc.after).Year(), civil(tc.after).Month(), civil(tc.after).Day(), 12, 0, 0, 0, ny)
			m := l.metricsAt(now, ny, AnchorThresholds{})

			assert.Equal(t, 2, milestone.rowIn(m).DaysSince)
			q := quit.rowIn(m)
			require.NotNil(t, q.Quit)
			assert.Equal(t, 2, q.Quit.CurrentRun, "a quit with no slips is a milestone's count across the transition")
			assert.Equal(t, q.DaysSince, q.Quit.CurrentRun)
			lim := limit.rowIn(m)
			require.NotNil(t, lim.Limit)
			assert.Equal(t, 1, lim.Limit.Used, "a 2-day window across the transition holds exactly 2 days")
			assert.Equal(t, intPtr(1), lim.Limit.DaysSinceLastUse)
			h := habit.rowIn(m)
			require.NotNil(t, h.Habit)
			assert.Equal(t, 2, h.Habit.Accounted, "a span across the transition accounts both days")
			assert.Equal(t, tc.after, h.Habit.NextConfirmFrom)
		})
	}
}

// TestAnchorKindsVoided: a voided event drops out of all math; the record of
// it stays in the store.
func TestAnchorKindsVoided(t *testing.T) {
	t.Run("quit: a voided slip and its restoring record give the run back", func(t *testing.T) {
		a := newKindAnchor(t, "sobriety", "2026-01-01")
		a.kind("2026-01-02", AnchorKindQuit, 0, 0)
		s := a.slipRedated("2026-03-01")
		a.voidSlip("2026-03-02", s, "2026-01-01")

		r := a.row("2026-04-10")
		require.NotNil(t, r.Quit)
		assert.Equal(t, QuitStats{CurrentRun: 99, LongestRun: 99}, *r.Quit)
		assert.Equal(t, 99, r.DaysSince, "the restoring record makes an older binary agree")
		assert.Len(t, a.l.events.Events, 3, "the slip and its void both stay on the record")
	})

	t.Run("limit: a voided use stops spending allowance", func(t *testing.T) {
		a := newKindAnchor(t, "late-night-snacks", "2026-01-01")
		a.kind("2026-01-02", AnchorKindLimit, 1, 90)
		a.use("2026-03-01", AnchorIntentPlanned)
		u := a.use("2026-03-05", AnchorIntentSlip)
		a.void("2026-03-06", u)

		l := ComputeLimitStats(a.facts(), civil("2026-03-10"))
		assert.Equal(t, 1, l.Used)
		assert.Equal(t, 0, l.Slips)
		assert.Equal(t, 0, l.Slips7d)
		assert.False(t, l.OverAllowance)
		assert.Equal(t, "2026-03-01", l.LastUse)
	})

	t.Run("habit: a voided miss leaves its day unaccounted, a voided confirm its span", func(t *testing.T) {
		a := newKindAnchor(t, "morning-walk", "2026-01-01")
		a.kind("2026-01-01", AnchorKindHabit, 0, 0)
		a.confirm("2026-01-01", "2026-01-05")
		c := a.confirm("2026-01-06", "2026-01-10")
		m := a.miss("2026-01-11")
		a.void("2026-01-12", m)
		a.void("2026-01-12", c)

		h := ComputeHabitStats(a.facts(), civil("2026-01-12"), AnchorThresholds{})
		assert.Equal(t, 5, h.Accounted)
		assert.Equal(t, 0, h.Misses)
		assert.Equal(t, "2026-01-05", h.LastConfirmed)
		assert.Equal(t, "2026-01-06", h.NextConfirmFrom, "the voided span is asked about again")
	})
}

// TestAnchorKindsConversionMidLife walks one anchor through quit → limit →
// quit → habit → quit. Every event logged before a conversion stays a fact,
// and each stage reads exactly the facts its kind understands.
func TestAnchorKindsConversionMidLife(t *testing.T) {
	a := newKindAnchor(t, "sobriety", "2026-01-01")
	a.kind("2026-01-02", AnchorKindQuit, 0, 0)
	a.slipRedated("2026-02-01")
	q := a.row("2026-03-01")
	require.NotNil(t, q.Quit)
	assert.Equal(t, QuitStats{CurrentRun: 28, LongestRun: 31, Slips: 1, LastSlip: "2026-02-01"}, *q.Quit)

	a.kind("2026-03-01", AnchorKindLimit, 4, 90)
	a.use("2026-03-05", AnchorIntentPlanned)
	l := a.row("2026-03-10")
	require.NotNil(t, l.Limit)
	assert.Equal(t, 2, l.Limit.Used, "the quit slip predating the conversion spends allowance")
	assert.Equal(t, 1, l.Limit.Planned)
	assert.Equal(t, 1, l.Limit.Slips)

	a.quitRedated("2026-03-12", "2026-03-05") // the planned use is later than the folded date
	q = a.row("2026-03-20")
	require.NotNil(t, q.Quit)
	assert.Equal(t, QuitStats{CurrentRun: 15, LongestRun: 32, Slips: 2, LastSlip: "2026-03-05"}, *q.Quit)
	assert.Equal(t, 15, q.DaysSince, "an older binary counts from the planned use too")

	a.kind("2026-03-21", AnchorKindHabit, 0, 0)
	a.confirm("2026-03-21", "2026-03-24")
	h := a.row("2026-03-25")
	require.NotNil(t, h.Habit)
	assert.Equal(t, 4, h.Habit.Accounted)
	assert.Equal(t, 4, h.Habit.Kept, "the slips and uses are not misses")

	a.kind("2026-03-26", AnchorKindQuit, 0, 0) // latest use-day is the folded date: no re-date
	q = a.row("2026-03-30")
	require.NotNil(t, q.Quit)
	assert.Equal(t, QuitStats{CurrentRun: 25, LongestRun: 32, Slips: 2, LastSlip: "2026-03-05"}, *q.Quit,
		"converting back restores the use-days; the confirms drop out")
}

// TestAnchorKindsEmptyStates: an empty kind reads as honestly empty — no
// hollow 100%, no zero dates — and an event store holding nothing that applies
// projects every anchor as a milestone.
func TestAnchorKindsEmptyStates(t *testing.T) {
	t.Run("a habit with nothing accounted reads zero of zero, never kept", func(t *testing.T) {
		a := newKindAnchor(t, "morning-walk", "2026-01-01")
		a.kind("2026-01-01", AnchorKindHabit, 0, 0)
		r := a.row("2026-01-20")
		require.NotNil(t, r.Habit)
		assert.Zero(t, r.Habit.Accounted)
		assert.Zero(t, r.Habit.Kept)
		assert.False(t, r.Habit.GraduationEligible)

		raw, err := json.Marshal(r.Habit)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"accounted":0,"kept":0`)
		assert.NotContains(t, string(raw), "last_confirmed", "omitted until the first confirm")
	})

	t.Run("a limit with no use spends nothing and dates nothing", func(t *testing.T) {
		a := newKindAnchor(t, "late-night-snacks", "2026-01-01")
		a.kind("2026-01-02", AnchorKindLimit, 4, 90)
		raw, err := json.Marshal(a.row("2026-03-10").Limit)
		require.NoError(t, err)
		assert.JSONEq(t, `{"allowance":4,"window_days":90,"used":0,"remaining":4,"planned":0,"slips":0,"slips_7d":0,"over_allowance":false}`, string(raw))
	})

	t.Run("a quit with no slip has no last slip and its longest is its current run", func(t *testing.T) {
		a := newKindAnchor(t, "sobriety", "2026-01-01")
		a.kind("2026-01-02", AnchorKindQuit, 0, 0)
		raw, err := json.Marshal(a.row("2026-01-11").Quit)
		require.NoError(t, err)
		assert.JSONEq(t, `{"current_run":10,"longest_run":10,"slips":0,"slips_7d":0}`, string(raw))
	})

	t.Run("pair intents alone, or events for no known anchor, change nothing", func(t *testing.T) {
		a := newKindAnchor(t, "sobriety", "2026-01-01")
		a.paired(AnchorEvent{Type: AnchorEventSlip, Day: "2026-01-05"}, AnchorPairCompatFirst, "2026-01-05", false, false)
		other := kindAnchor{l: a.l, id: "anchor_2026_01_02_z"} // never defined
		other.kind("2026-01-03", AnchorKindQuit, 0, 0)

		r := a.row("2026-01-11")
		assert.Empty(t, r.Kind)
		assert.Nil(t, r.Quit)
		assert.Equal(t, 10, r.DaysSince)
	})
}

// TestAnchorThresholdsDefaults: unset thresholds take their defaults, and a
// miss alarm below 2 is raised to 2 so one isolated miss can never alarm.
func TestAnchorThresholdsDefaults(t *testing.T) {
	assert.Equal(t, AnchorThresholds{MissAlarm: 2, GraduationDays: 90}, AnchorThresholds{}.withDefaults())
	assert.Equal(t, AnchorThresholds{MissAlarm: 2, GraduationDays: 90}, AnchorThresholds{MissAlarm: 1, GraduationDays: -3}.withDefaults())
	assert.Equal(t, AnchorThresholds{MissAlarm: 4, GraduationDays: 30}, AnchorThresholds{MissAlarm: 4, GraduationDays: 30}.withDefaults())
}

// TestBuildMetricsGraduatedAnchors: a sunset anchor whose graduation pair is
// whole is projected in anchors_graduated and not in anchors_sunset; a plain
// sunset, and a graduation whose sunset half never landed, are not.
func TestBuildMetricsGraduatedAnchors(t *testing.T) {
	l := newKindLedger(t)
	graduate := func(a kindAnchor, day string, writeSunset bool) {
		e := a.minted(AnchorEvent{Type: AnchorEventGraduate, Day: day})
		sunset := a.latest()
		sunset.State, sunset.RecordedAt = AnchorStateSunset, e.RecordedAt
		a.l.events.PairIntents = append(a.l.events.PairIntents, AnchorPairIntent{Order: AnchorPairEventFirst, Event: e, Compat: sunset})
		a.l.events.Events = append(a.l.events.Events, e)
		if writeSunset {
			a.l.anchors.History = append(a.l.anchors.History, sunset)
		}
	}
	walk := l.anchor("anchor_2026_01_01_a", "morning-walk", "2026-01-01")
	walk.kind("2026-01-01", AnchorKindHabit, 0, 0)
	graduate(walk, "2026-04-01", true)
	retired := l.anchor("anchor_2026_01_01_b", "evening-stretch", "2026-01-01")
	retired.kind("2026-01-01", AnchorKindHabit, 0, 0)
	rec := retired.latest()
	rec.State, rec.RecordedAt = AnchorStateSunset, l.stamp("2026-04-01")
	l.anchors.History = append(l.anchors.History, rec)
	pending := l.anchor("anchor_2026_01_01_c", "first gate", "2026-01-01")
	graduate(pending, "2026-04-01", false)

	m := l.metrics("2026-04-02", AnchorThresholds{})
	require.Len(t, m.AnchorsGraduated, 1)
	assert.Equal(t, "anchor_2026_01_01_a", m.AnchorsGraduated[0].ID)
	assert.Equal(t, "2026-01-01", m.AnchorsGraduated[0].Date)
	require.Len(t, m.AnchorsSunset, 1)
	assert.Equal(t, "anchor_2026_01_01_b", m.AnchorsSunset[0].ID)
	require.Len(t, m.Anchors, 1, "a graduation without its sunset half leaves the anchor active")
	assert.Equal(t, "anchor_2026_01_01_c", m.Anchors[0].ID)
}

// TestAnchorKindsUnreadableDates: facts with no readable start date — only a
// hand-corrupted store produces them, and partitionAnchors already skips such a
// row — degrade to empty numbers and a loud confirm error, never a panic or a
// guessed day. With no Start, the anchor's own date is the fallback.
func TestAnchorKindsUnreadableDates(t *testing.T) {
	today := civil("2026-03-10")
	broken := Anchor{Label: "sobriety", Date: "not-a-date"}

	assert.Equal(t, QuitStats{}, ComputeQuitStats(AnchorFacts{Kind: AnchorKindQuit, Anchor: broken}, today))
	assert.Equal(t, LimitStats{Allowance: 4, WindowDays: 90, Remaining: 4},
		ComputeLimitStats(AnchorFacts{Kind: AnchorKindLimit, Allowance: 4, WindowDays: 90, Anchor: broken}, today))
	assert.Equal(t, HabitStats{WindowDays: DefaultHabitWindowDays},
		ComputeHabitStats(AnchorFacts{Kind: AnchorKindHabit, Anchor: broken, KindSince: "2026-03-01"}, today, AnchorThresholds{}))

	start, last, empty, err := ConfirmSpan(AnchorFacts{Kind: AnchorKindHabit, Anchor: broken}, today, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"sobriety" has no readable start date or habit day`)
	assert.True(t, start.IsZero() && last.IsZero() && !empty, "no span is guessed")

	notHabit := AnchorFacts{Kind: AnchorKindQuit, Start: "2026-01-01", KindSince: "2026-01-02"}
	assert.Equal(t, HabitStats{WindowDays: DefaultHabitWindowDays}, ComputeHabitStats(notHabit, today, AnchorThresholds{}),
		"a non-habit has no habit day to count from")

	fallback := AnchorFacts{Kind: AnchorKindQuit, Anchor: Anchor{Label: "sobriety", Date: "2026-03-01"}}
	assert.Equal(t, 9, ComputeQuitStats(fallback, today).CurrentRun, "an empty Start falls back to the anchor date")
}
