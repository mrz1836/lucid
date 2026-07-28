package router

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// weekOf is a mid-week instant inside the ISO week Mon 2026-06-29 … Sun
// 2026-07-05. Its logical day is 2026-07-02 (Thursday), so the week bundle's
// seven day rows are 06-29 … 07-05 and every seeded fixture below lands inside
// those bounds.
func weekOf() time.Time { return time.Date(2026, 7, 2, 21, 45, 0, 0, edt) }

// weekWindow resolves the ISO-week window for `now` through the real resolver,
// so the bundle fixtures below exercise the production path rather than a
// hand-built struct. It is byte-identical to the bounds the assembler derived
// for itself before the window became an input.
func weekWindow(t *testing.T, r *Router, now time.Time) ReflectWindow {
	t.Helper()
	win, err := r.resolveReflectWindow(now, ReflectWindowOptions{Mode: ReflectWindowWeek})
	require.NoError(t, err)
	return win
}

// sinceWindow resolves a catch-up-shaped window running from `from` through the
// end of `now`'s day — the shape a reflection that skipped a week resolves to.
func sinceWindow(t *testing.T, r *Router, now, from time.Time) ReflectWindow {
	t.Helper()
	win, err := r.resolveReflectWindow(now, ReflectWindowOptions{Mode: ReflectWindowSince, Since: from})
	require.NoError(t, err)
	return win
}

// seedAcceptedInsight writes one accepted insight created at `at`, satisfying
// the provenance validation so it round-trips into the recall window. It is the
// insight-slice seed the bundle's AcceptedInsights window is asserted against.
func seedAcceptedInsight(t *testing.T, a *storage.Adapter, at time.Time, body string) string {
	t.Helper()
	res, err := a.WriteInsight(storage.Insight{
		CreatedAt: at,
		Status:    storage.InsightStatusAccepted,
		Provenance: storage.InsightProvenance{
			RawEntryIDs:             []string{"raw_2026_07_01_09_00"},
			ProcessedArtifactID:     "raw_2026_07_01_09_00",
			ReflectionPromptVersion: "reflection-2026.07.0",
			UserResponseKind:        storage.ResponseAccepted,
			UserResponseText:        "Yes, that fits.",
		},
		Body: body,
	})
	require.NoError(t, err)
	return res.InsightID
}

// TestBuildWeekBundle_MissingContext: a fresh, empty store yields an
// empty-but-valid bundle — no panic, the ISO-week label and bounds resolved, a
// dense seven-day zero row set, and empty (never nil) digest / observation /
// insight slices. This is the missing-context safety fixture (AC-11): the
// assembler is safe over a store with nothing logged.
func TestBuildWeekBundle_MissingContext(t *testing.T) {
	r, _ := statsRouter(t)

	b, err := r.BuildWeekBundle(weekOf(), weekWindow(t, r, weekOf()))
	require.NoError(t, err)

	assert.Equal(t, isoweek.Label(weekOf()), b.ISOWeek)
	wantStart, wantEnd := isoweek.Bounds(weekOf())
	assert.Equal(t, wantStart, b.WindowStart)
	assert.Equal(t, wantEnd, b.WindowEnd)

	require.Len(t, b.Stats, 7, "one row per day of the ISO week")
	assert.Equal(t, "2026-06-29", b.Stats[0].Date, "the week starts on Monday")
	assert.Equal(t, "2026-07-05", b.Stats[6].Date, "and ends on Sunday")
	for _, d := range b.Stats {
		assert.Equal(t, StatsDay{Date: d.Date}, d, "an empty week is all zeros")
	}

	assert.Empty(t, b.RawDigest)
	assert.Empty(t, b.Observations)
	assert.Empty(t, b.AcceptedInsights)
	// Empty, never nil — an empty-but-valid bundle for the deep-dive to degrade on.
	assert.NotNil(t, b.RawDigest)
	assert.NotNil(t, b.Observations)
	assert.NotNil(t, b.AcceptedInsights)
}

// TestBuildWeekBundle_ThinWeek: a single raw entry in the week surfaces as one
// digest row (id, day, verbatim text) and one raw count on its day, with every
// other day zero and no observations (AC-4, AC-11).
func TestBuildWeekBundle_ThinWeek(t *testing.T) {
	r, a := statsRouter(t)
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 1)

	b, err := r.BuildWeekBundle(weekOf(), weekWindow(t, r, weekOf()))
	require.NoError(t, err)

	require.Len(t, b.RawDigest, 1)
	assert.Equal(t, "2026-07-01", b.RawDigest[0].Date)
	assert.Equal(t, "seed", b.RawDigest[0].Text, "the digest carries the verbatim entry text, heading stripped")
	assert.NotEmpty(t, b.RawDigest[0].ID)
	assert.Empty(t, b.Observations)

	// Exactly the 07-01 row carries the single raw entry; the week totals it.
	var totalRaw int
	for _, d := range b.Stats {
		totalRaw += d.RawEntries
		if d.Date == "2026-07-01" {
			assert.Equal(t, 1, d.RawEntries)
			assert.Equal(t, 1, d.TotalEvents)
		}
	}
	assert.Equal(t, 1, totalRaw)
}

// TestBuildWeekBundle_RichWeek: raws and observations spread across the week,
// plus an in-window and an out-of-window insight. The digest and observation
// slices carry every event once, the per-day rows sum to the slice lengths (the
// spanning-safe `lucid stats` invariant), the day counts match the `/day` join
// verbatim (projection parity — AC-4), and only the in-window insight surfaces
// (AC-4, AC-11).
func TestBuildWeekBundle_RichWeek(t *testing.T) {
	r, a := statsRouter(t)

	seedRaws(t, a, time.Date(2026, 6, 30, 9, 0, 0, 0, edt), 2)
	seedRaws(t, a, time.Date(2026, 7, 2, 9, 0, 0, 0, edt), 3)
	seedObs(t, a, observations.KindPain, "2026-07-01", "2026-07-01T09:00:00-04:00")
	seedObs(t, a, observations.KindMood, "2026-07-02", "2026-07-02T11:00:00-04:00")
	seedObs(t, a, observations.KindIntake, "2026-07-03", "2026-07-03T08:00:00-04:00")

	inWindow := seedAcceptedInsight(t, a, time.Date(2026, 7, 1, 12, 0, 0, 0, edt), "In-window pattern.")
	seedAcceptedInsight(t, a, time.Date(2026, 6, 20, 12, 0, 0, 0, edt), "Older, out-of-window pattern.")

	b, err := r.BuildWeekBundle(weekOf(), weekWindow(t, r, weekOf()))
	require.NoError(t, err)

	require.Len(t, b.RawDigest, 5, "two on 06-30 plus three on 07-02")
	require.Len(t, b.Observations, 3, "pain, mood, intake — one each")

	// Per-day rows sum exactly to the slice lengths: nothing double-counted.
	var sumRaw, sumObs int
	for _, d := range b.Stats {
		sumRaw += d.RawEntries
		sumObs += d.Observations
	}
	assert.Equal(t, len(b.RawDigest), sumRaw)
	assert.Equal(t, len(b.Observations), sumObs)

	// Projection parity: each day's counts equal len(ReadDayView(d)) — the bundle
	// draws from the sanctioned `/day` join, not a recomputation.
	for _, d := range b.Stats {
		dv, derr := a.ReadDayView(d.Date, edt)
		require.NoError(t, derr)
		assert.Equalf(t, len(dv.RawEntryIDs), d.RawEntries, "raw parity for %s", d.Date)
		assert.Equalf(t, len(dv.Obs.Events), d.Observations, "obs parity for %s", d.Date)
	}

	// Only the insight validated inside the seven-day recall window surfaces.
	require.Len(t, b.AcceptedInsights, 1)
	assert.Equal(t, inWindow, b.AcceptedInsights[0].ID)
	assert.Equal(t, "In-window pattern.", b.AcceptedInsights[0].Body)
}

// TestBuildWeekBundle_Deterministic: the assembler is a pure read, so repeated
// calls over a fixed store and clock render identical bundles.
func TestBuildWeekBundle_Deterministic(t *testing.T) {
	r, a := statsRouter(t)
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 2)
	seedObs(t, a, observations.KindPain, "2026-07-01", "2026-07-01T09:30:00-04:00")

	first, err := r.BuildWeekBundle(weekOf(), weekWindow(t, r, weekOf()))
	require.NoError(t, err)
	second, err := r.BuildWeekBundle(weekOf(), weekWindow(t, r, weekOf()))
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

// TestBuildWeekBundle_SanctuarySource is the sanctuary guard (AC-4): the
// assembler must draw only from the sanctioned projections and never name a
// direct read of the engine|observations|registries trees. A source grep is the
// deterministic enforcement — mirroring the agent-contracts denylist grep in
// sanctuary_obs_test.go — so a future edit that reaches past a projection into
// the sanctuary tree fails the build.
func TestBuildWeekBundle_SanctuarySource(t *testing.T) {
	src, err := os.ReadFile("weekbundle.go")
	require.NoError(t, err, "the assembler source must be present for the sanctuary grep")
	doc := string(src)

	// It draws from the sanctioned projections.
	for _, want := range []string{"ReadDayView", "ReadRaw", "ReadInsightsWindow", "r.Metrics(", "r.Status("} {
		assert.Containsf(t, doc, want, "the bundle must assemble through the %s projection", want)
	}

	// It never reaches the sanctuary trees directly. These are the storage ops
	// that read engine|observations|registries below the `/day` projection; the
	// bundle must go through ReadDayView / Metrics / Status instead.
	for _, forbidden := range []string{
		"ReadObservationsDay",
		"ReadObservationsRange",
		"ReadObservationsKind",
		"ReadEngineDays",
		"ReadEngineDayFolded",
		"ReadEngineStatus",
		"ReadRegistry",
		"observationsDir",
		"registriesDir",
	} {
		assert.NotContainsf(t, doc, forbidden, "sanctuary breach: the bundle reads %s directly", forbidden)
	}

	// The assembler must not resolve its own window either. The reflected-through
	// cursor is read one layer up, in the resolver, and handed down: keeping that
	// read out of this file is what lets the bundle stay a pure projection over
	// bounds it was given.
	assert.NotContains(t, doc, "ReadReflectionReceipt",
		"layering breach: the bundle must take its window as an input, not resolve one")
}

// TestBuildWeekBundle_CatchUpWindowSpansSkippedWeek is the headline assembler
// case: a fourteen-day catch-up window yields fourteen day rows, folds the raw
// entries from the OLDER of the two weeks (the ones a fixed ISO week would have
// dropped), and widens the accepted-insight slice to match the window rather
// than the seven-day floor (AC-1, AC-10).
func TestBuildWeekBundle_CatchUpWindowSpansSkippedWeek(t *testing.T) {
	r, a := statsRouter(t)
	from := time.Date(2026, 6, 19, 0, 0, 0, 0, edt)

	// One entry in the skipped week, one in the current week.
	seedRaws(t, a, time.Date(2026, 6, 20, 9, 0, 0, 0, edt), 1)
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 1)

	// Validated ten days back: inside the catch-up window, outside the floor.
	widened := seedAcceptedInsight(t, a, time.Date(2026, 6, 22, 12, 0, 0, 0, edt), "Widened-window pattern.")
	seedAcceptedInsight(t, a, time.Date(2026, 6, 10, 12, 0, 0, 0, edt), "Older, out-of-window pattern.")

	b, err := r.BuildWeekBundle(weekOf(), sinceWindow(t, r, weekOf(), from))
	require.NoError(t, err)

	require.Len(t, b.Stats, 14, "one row per day of the catch-up window")
	assert.Equal(t, "2026-06-19", b.Stats[0].Date)
	assert.Equal(t, "2026-07-02", b.Stats[13].Date)
	assert.Equal(t, 14, b.DaysCovered)

	// The skipped week's entry is actually read — a window can be wide and still
	// fail to fold the older days, so assert the digest carries both.
	require.Len(t, b.RawDigest, 2, "the skipped week's entry is caught up, not dropped")
	assert.Equal(t, "2026-06-20", b.RawDigest[0].Date)
	assert.Equal(t, "2026-07-01", b.RawDigest[1].Date)

	require.Len(t, b.AcceptedInsights, 1, "the insight slice widens with the window")
	assert.Equal(t, widened, b.AcceptedInsights[0].ID)
}

// TestBuildWeekBundle_ShortWindowKeepsInsightFloor: a window shorter than the
// recall floor still reads seven days of accepted insights. The floor never
// narrows, so no existing short-window behavior regresses (AC-10).
func TestBuildWeekBundle_ShortWindowKeepsInsightFloor(t *testing.T) {
	r, a := statsRouter(t)

	// Five days back: outside the three-day window, inside the seven-day floor.
	onFloor := seedAcceptedInsight(t, a, time.Date(2026, 6, 27, 12, 0, 0, 0, edt), "Floor-window pattern.")
	seedAcceptedInsight(t, a, time.Date(2026, 6, 20, 12, 0, 0, 0, edt), "Beyond the floor.")

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{Mode: ReflectWindowDays, Days: 3})
	require.NoError(t, err)

	b, err := r.BuildWeekBundle(weekOf(), win)
	require.NoError(t, err)

	require.Len(t, b.Stats, 3, "three day rows for a three-day window")
	assert.Equal(t, 3, b.DaysCovered)
	require.Len(t, b.AcceptedInsights, 1, "the seven-day floor still applies")
	assert.Equal(t, onFloor, b.AcceptedInsights[0].ID)
}

// TestBuildWeekBundle_MultiWeekLabelsTheEndDay: a multi-week window has no
// single ISO week, so the bundle labels the week its END day falls in — the week
// the reflection closes in. Downstream consumers keying on iso_week keep a
// stable, meaningful value instead of a null (AC-11).
func TestBuildWeekBundle_MultiWeekLabelsTheEndDay(t *testing.T) {
	r, _ := statsRouter(t)
	from := time.Date(2026, 6, 19, 0, 0, 0, 0, edt)

	win := sinceWindow(t, r, weekOf(), from)
	b, err := r.BuildWeekBundle(weekOf(), win)
	require.NoError(t, err)

	assert.Equal(t, isoweek.Label(win.End), b.ISOWeek)
	assert.Equal(t, "2026-W27", b.ISOWeek, "the window ends in W27")
	assert.NotEqual(t, isoweek.Label(win.Start), b.ISOWeek, "the window opens in an earlier week")
}

// TestBuildWeekBundle_CarriesCapShortfall: a capped window's shortfall reaches
// the bundle verbatim, so the surface layer can state what it did not cover
// rather than presenting a truncated read as a complete one (AC-9).
func TestBuildWeekBundle_CarriesCapShortfall(t *testing.T) {
	r, a := statsRouter(t)
	seedReflectionCursor(t, a, weekOf().AddDate(0, 0, -95))

	win, err := r.resolveReflectWindow(weekOf(), ReflectWindowOptions{})
	require.NoError(t, err)
	require.True(t, win.Capped, "a 96-day gap exceeds the default ceiling")

	b, err := r.BuildWeekBundle(weekOf(), win)
	require.NoError(t, err)

	assert.True(t, b.Capped)
	assert.Equal(t, 35, b.DaysCovered)
	assert.Equal(t, 61, b.UncoveredDays)
	assert.Len(t, b.Stats, 35, "the day rows follow the capped window")
}
