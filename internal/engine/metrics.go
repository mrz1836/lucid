package engine

import (
	"cmp"
	"slices"
	"time"
)

// DaysSince returns the whole civil days elapsed from anchor to current with
// the anchor date as day 0 — "recorded today" reads 0, tomorrow 1. Both
// instants are re-anchored to UTC civil midnight (from each date's own
// Y/M/D) before differencing, so a 23-hour spring-forward day — or any other
// DST transition — cannot make the count drift. This deliberately does not
// reuse DaysBetween, which truncates the span in the host location and would
// under-count by one across such a day.
func DaysSince(anchor, current time.Time) int {
	ay, am, ad := anchor.Date()
	cy, cm, cd := current.Date()
	a := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	c := time.Date(cy, cm, cd, 0, 0, 0, 0, time.UTC)
	return int(c.Sub(a).Hours() / 24)
}

// GateWindow is one gate-length adherence rollup (30/60/90) carried in the
// metrics projection so a harness reads every gate number without recomputing
// downstream.
type GateWindow struct {
	Length    int    `json:"length"`
	Adherence Window `json:"adherence"`
}

// AnchorDaysSince is one folded, still-counting anchor with its running day
// count (day 0 on the anchor date). A retired anchor is not here — it is in
// AnchorSunset, or AnchorsGraduated.
//
// ID is unconditional: every projected row publishes an address, either the
// anchor's minted id or the synthetic identity of a record written before ids
// existed (AnchorIdentity), so a caller can always refer to what it just read.
//
// Date and DaysSince keep their meaning for every kind: the plain fold of
// anchors.json, so for a quit after a slip they show the re-dated date —
// exactly what an older binary shows. Kind and the one kind object matching it
// are set for a quit, limit or habit and omitted for a milestone, so absence
// means milestone (the same convention state uses) and a Ledger with no kind
// events projects byte-for-byte as before.
type AnchorDaysSince struct {
	ID        string      `json:"id"`
	Label     string      `json:"label"`
	Date      string      `json:"date"`
	Note      string      `json:"note,omitempty"`
	DaysSince int         `json:"days_since"`
	Kind      string      `json:"kind,omitempty"`
	Quit      *QuitStats  `json:"quit,omitempty"`
	Limit     *LimitStats `json:"limit,omitempty"`
	Habit     *HabitStats `json:"habit,omitempty"`
}

// AnchorSunset is one retired anchor: it no longer counts, but it never
// leaves the ledger. A graduated anchor has the same shape. Date stays the milestone date the anchor counted from —
// it is not overloaded with the retirement day, which SunsetAt carries. Note is
// the reason given at retirement, when one was.
type AnchorSunset struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Date     string `json:"date"`
	Note     string `json:"note,omitempty"`
	SunsetAt string `json:"sunset_at"`
}

// Metrics is the derived `lucid metrics` projection — the committed chain's
// *quality* read: current/longest streak, adherence over the default 30-day
// window (plus the 30/60/90 gate rollups), misses in that window, the error
// budget, and days-since for each active anchor. Streak/adherence/misses
// reuse the existing chain folds; only days-since is new math. Field order is
// fixed for a stable on-disk form. Ref is the anchoring logical day the
// rolling windows resolve to (nil when no day is decided yet).
//
// Anchors carries only anchors that still count; AnchorsSunset carries the
// retired ones, so a caller reading Anchors can never accidentally count a
// milestone that was deliberately retired, and a caller that wants the retired
// ones does not have to read the store to find them. AnchorsGraduated carries
// the anchors retired with sunset --graduated — kept apart from AnchorsSunset
// because "this became who I am" is not an abandonment — and is omitted when
// empty, so a Ledger with no graduation projects exactly as before.
type Metrics struct {
	CurrentStreak    int               `json:"current_streak"`
	LongestStreak    int               `json:"longest_streak"`
	Adherence        Window            `json:"adherence"`
	MissesInWindow   int               `json:"misses_in_window"`
	ErrorBudget      ErrorBudget       `json:"error_budget"`
	Gates            []GateWindow      `json:"gates"`
	Anchors          []AnchorDaysSince `json:"anchors"`
	AnchorsSunset    []AnchorSunset    `json:"anchors_sunset"`
	AnchorsGraduated []AnchorSunset    `json:"anchors_graduated,omitempty"`
	Ref              *string           `json:"ref"`
}

// MetricsInput is everything BuildMetrics folds into the projection: the
// already-folded day records, the chain config (for the SLO budget and gate
// marks), the effective anchors (latest per label — fold with LatestAnchors
// before calling), the current instant, the active profile's clocks, and the
// location logical dates are interpreted in.
//
// The kind math reads three more inputs, all zero-value safe. Events is the
// anchor event store; AnchorLog is the full anchors.json it joins (the quit
// start and the pair states need the whole history, not just the latest
// records); Thresholds carries the lucid.json anchors block, with unset fields
// taking their defaults. With no events every anchor is a milestone and the
// projection is exactly what it was before kinds existed.
type MetricsInput struct {
	Records    []DayRecord
	Chain      ChainConfig
	Anchors    []Anchor
	Now        time.Time
	Clocks     Clocks
	Loc        *time.Location
	AnchorLog  AnchorLog
	Events     AnchorEventLog
	Thresholds AnchorThresholds
}

// BuildMetrics computes the metrics projection. It is pure and deterministic
// for the Ledger-anchored fields (streak, adherence, misses, gates), which
// resolve to the latest recorded logical day — a stored anchor, not the wall
// clock. Days-since is clock-relative on purpose: it counts from the current
// logical day (Now through the rollover), because "days since X" advances
// with the wall clock, and so are the per-kind anchor numbers. No model is ever
// in the path.
func BuildMetrics(in MetricsInput) Metrics {
	loc := in.Loc
	if loc == nil {
		loc = time.UTC
	}
	records := in.Records
	streaks := ComputeStreaks(records, loc)
	budget := in.Chain.SLO.IsolatedMissBudgetPer30d

	active, sunset, graduated := partitionAnchors(in.Anchors, in.Clocks.BaseLogicalDate(in.Now), newAnchorKindView(in))
	m := Metrics{
		CurrentStreak:    streaks.Current,
		LongestStreak:    streaks.Longest,
		Adherence:        Window{Length: 30},
		ErrorBudget:      ErrorBudget{Budget: budget, Remaining: budget},
		Anchors:          active,
		AnchorsSunset:    sunset,
		AnchorsGraduated: graduated,
	}

	ref, hasRef := latestRecordDate(records, loc)
	if hasRef {
		refStr := DateString(DateOf(ref))
		m.Ref = &refStr
		m.Adherence = windowStats(records, ref, 30, loc)
		m.MissesInWindow = m.Adherence.Decided - m.Adherence.Completed
		m.ErrorBudget = errorBudget(records, ref, budget, loc)
	}
	m.Gates = gateWindows(records, ref, hasRef, in.Chain.SLO.Gates, loc)
	return m
}

// gateWindows builds one adherence rollup per gate length (SLO.Gates, e.g.
// 30/60/90). Before any day is decided (hasRef false) each gate stays an
// honest zero-length window (Decided==0), never a hollow 1.0.
func gateWindows(records []DayRecord, ref time.Time, hasRef bool, gates []int, loc *time.Location) []GateWindow {
	out := make([]GateWindow, 0, len(gates))
	for _, g := range gates {
		w := Window{Length: g}
		if hasRef {
			w = windowStats(records, ref, g, loc)
		}
		out = append(out, GateWindow{Length: g, Adherence: w})
	}
	return out
}

// anchorKindView is what partitionAnchors reads to give an anchor its kind: the
// folded facts keyed by folded identity, the adoption remap that finds an
// anchor's entry, and the thresholds. Its zero value holds no facts, so every
// anchor projects as a milestone, exactly as before kinds existed.
type anchorKindView struct {
	facts      map[string]AnchorFacts
	remap      map[string]string
	thresholds AnchorThresholds
}

// newAnchorKindView folds the event store over anchors.json. With no events
// there is nothing to fold, so the view stays empty and the projection is
// byte-identical to a Ledger without the store.
func newAnchorKindView(in MetricsInput) anchorKindView {
	if len(in.Events.Events) == 0 {
		return anchorKindView{}
	}
	return anchorKindView{
		facts:      FoldAnchorEvents(in.Events, in.AnchorLog),
		remap:      adoptionRemap(in.AnchorLog),
		thresholds: in.Thresholds,
	}
}

// factsFor returns the facts of the anchor a folded record belongs to.
func (v anchorKindView) factsFor(a Anchor) (AnchorFacts, bool) {
	f, ok := v.facts[foldedAnchorIdentity(a, v.remap)]
	return f, ok
}

// applyKind sets the row's kind and kind object from the anchor's facts. A
// milestone sets neither — an explicit milestone kind event reads exactly as
// an anchor that never had one.
func (r *AnchorDaysSince) applyKind(f AnchorFacts, logicalDay time.Time, th AnchorThresholds) {
	switch f.Kind {
	case AnchorKindQuit:
		s := ComputeQuitStats(f, logicalDay)
		r.Kind, r.Quit = f.Kind, &s
	case AnchorKindLimit:
		s := ComputeLimitStats(f, logicalDay)
		r.Kind, r.Limit = f.Kind, &s
	case AnchorKindHabit:
		s := ComputeHabitStats(f, logicalDay, th)
		r.Kind, r.Habit = f.Kind, &s
	}
}

// compareAnchorSunset orders retired rows by label, then identity.
func compareAnchorSunset(a, b AnchorSunset) int {
	if c := cmp.Compare(a.Label, b.Label); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

// partitionAnchors splits the folded anchors into the projected surfaces in a
// single walk: the ones still counting (with their running day count relative
// to the current logical day, day 0 on the anchor date, plus their kind and
// kind numbers from the view), the retired ones, and the graduated ones — a
// sunset anchor whose facts hold a graduation whose pair is whole (PD-11).
//
// This is the only place a sunset anchor is filtered out. Every downstream
// surface — the metrics prose, the anchors[] JSON array, and the witness
// aging-anchor check — reads Metrics.Anchors, so filtering here is what keeps a
// retired milestone off all three without a second patch point.
//
// A malformed date is skipped on every side rather than counted, so a
// hand-corrupted anchors.json cannot panic the read and a retired record with a
// broken date is dropped rather than surfaced with a hollow zero. Every output
// is sorted by label, then identity, so two records sharing a display name
// have a total, stable order; active and sunset are non-nil so the JSON renders
// [] and never null, while graduated stays nil when empty so it is omitted.
func partitionAnchors(anchors []Anchor, logicalDay time.Time, kinds anchorKindView) (
	active []AnchorDaysSince, sunset, graduated []AnchorSunset,
) {
	active = make([]AnchorDaysSince, 0, len(anchors))
	sunset = make([]AnchorSunset, 0, len(anchors))
	for _, a := range anchors {
		d, err := time.Parse(dateLayout, a.Date)
		if err != nil {
			continue
		}
		f, hasFacts := kinds.factsFor(a)
		if a.IsSunset() {
			row := AnchorSunset{
				ID:       AnchorIdentity(a),
				Label:    a.Label,
				Date:     a.Date,
				Note:     a.Note,
				SunsetAt: a.RecordedAt,
			}
			if hasFacts && f.Graduated {
				graduated = append(graduated, row)
			} else {
				sunset = append(sunset, row)
			}
			continue
		}
		row := AnchorDaysSince{
			ID:        AnchorIdentity(a),
			Label:     a.Label,
			Date:      a.Date,
			Note:      a.Note,
			DaysSince: DaysSince(d, logicalDay),
		}
		if hasFacts {
			row.applyKind(f, logicalDay, kinds.thresholds)
		}
		active = append(active, row)
	}
	slices.SortFunc(active, func(a, b AnchorDaysSince) int {
		if c := cmp.Compare(a.Label, b.Label); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	slices.SortFunc(sunset, compareAnchorSunset)
	slices.SortFunc(graduated, compareAnchorSunset)
	return active, sunset, graduated
}
