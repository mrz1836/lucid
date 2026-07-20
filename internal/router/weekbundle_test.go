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

	b, err := r.BuildWeekBundle(weekOf())
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

	b, err := r.BuildWeekBundle(weekOf())
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

	b, err := r.BuildWeekBundle(weekOf())
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

	first, err := r.BuildWeekBundle(weekOf())
	require.NoError(t, err)
	second, err := r.BuildWeekBundle(weekOf())
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
}
