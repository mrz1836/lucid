package router

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// statsRouter returns a booted router over a fresh temp Ledger with the
// observations tree scaffolded (default kinds enabled: pain, intake,
// elimination, mood). It is the seeding surface for the stats projection tests.
func statsRouter(t *testing.T) (*Router, *storage.Adapter) {
	t.Helper()
	a := storage.New(filepath.Join(t.TempDir(), ".lucid"))
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	require.NoError(t, a.ScaffoldEngine())
	r := New(a)
	_, err = r.Boot()
	require.NoError(t, err)
	return r, a
}

// seedRaws writes n raw entries recorded on the civil date of day (spaced one
// minute apart so their ids never collide). They bucket by recorded civil date,
// the exact join `lucid day` uses.
func seedRaws(t *testing.T, a *storage.Adapter, day time.Time, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		at := day.Add(time.Duration(i) * time.Minute)
		_, err := a.WriteRaw(storage.RawEntry{
			RecordedAt: at, OccurredAt: at, OccurredAtPrecision: storage.PrecisionExact,
			Source: "cli", Command: "/log", Body: "seed",
		})
		require.NoError(t, err)
	}
}

// seedObs appends one exact-precision observation of kind on logicalDate.
func seedObs(t *testing.T, a *storage.Adapter, kind observations.Kind, logicalDate, occurredAt string) {
	t.Helper()
	_, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: kind,
		RecordedAt: occurredAt, OccurredAt: occurredAt,
		OccurredAtPrecision: observations.PrecisionExact,
		LogicalDate:         logicalDate, Source: observations.SourceMicrolog,
	})
	require.NoError(t, err)
}

// TestStats_EmptyRange: a window with no data reports all zeros, one row per
// day, and a DENSE observations_by_kind (all four enabled kinds present, zeros
// included) in --json even though nothing was logged (AC-6, AC-10). It also
// exercises the bare-scaffold path: the default 4-kind config is created by the
// projection's own prepare step.
func TestStats_EmptyRange(t *testing.T) {
	r, _ := statsRouter(t)

	res, err := r.Stats(StatsOptions{From: "2026-07-01", To: "2026-07-03"}, nowEDT())
	require.NoError(t, err)

	assert.Equal(t, 0, res.View.RawEntries)
	assert.Equal(t, 0, res.View.Observations)
	assert.Equal(t, 0, res.View.TotalEvents)
	require.Len(t, res.View.Days, 3)
	for _, d := range res.View.Days {
		assert.Equal(t, 0, d.RawEntries)
		assert.Equal(t, 0, d.Observations)
		assert.Equal(t, 0, d.TotalEvents)
	}

	b, err := json.Marshal(res.View)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"observations_by_kind":{"pain":0,"intake":0,"elimination":0,"mood":0}`)
}

// TestStats_RawOnlyDay: a day with only raw entries (AC-10).
func TestStats_RawOnlyDay(t *testing.T) {
	r, a := statsRouter(t)
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 2)

	res, err := r.Stats(StatsOptions{From: "2026-07-01", To: "2026-07-01"}, nowEDT())
	require.NoError(t, err)

	assert.Equal(t, 2, res.View.RawEntries)
	assert.Equal(t, 0, res.View.Observations)
	assert.Equal(t, 2, res.View.TotalEvents)
	require.Len(t, res.View.Days, 1)
	assert.Equal(t, StatsDay{Date: "2026-07-01", RawEntries: 2, Observations: 0, TotalEvents: 2}, res.View.Days[0])
}

// TestStats_ObsOnlyDay: a day with only observations, with the dense by-kind
// tally (AC-10).
func TestStats_ObsOnlyDay(t *testing.T) {
	r, a := statsRouter(t)
	seedObs(t, a, observations.KindPain, "2026-07-01", "2026-07-01T09:00:00-04:00")
	seedObs(t, a, observations.KindIntake, "2026-07-01", "2026-07-01T10:00:00-04:00")

	res, err := r.Stats(StatsOptions{From: "2026-07-01", To: "2026-07-01"}, nowEDT())
	require.NoError(t, err)

	assert.Equal(t, 0, res.View.RawEntries)
	assert.Equal(t, 2, res.View.Observations)
	assert.Equal(t, 2, res.View.TotalEvents)

	b, err := json.Marshal(res.View)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"observations_by_kind":{"pain":1,"intake":1,"elimination":0,"mood":0}`)
}

// TestStats_MixedDay: raw entries and observations on one day, plus the human
// surface — the header carries the logical-day basis, the by-kind sub-lines are
// SPARSE (nonzero only), and the block is inventory, never a score (AC-5).
func TestStats_MixedDay(t *testing.T) {
	r, a := statsRouter(t)
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 3)
	seedObs(t, a, observations.KindMood, "2026-07-01", "2026-07-01T11:00:00-04:00")

	res, err := r.Stats(StatsOptions{From: "2026-07-01", To: "2026-07-01"}, nowEDT())
	require.NoError(t, err)

	assert.Equal(t, 3, res.View.RawEntries)
	assert.Equal(t, 1, res.View.Observations)
	assert.Equal(t, 4, res.View.TotalEvents)
	require.Len(t, res.View.Days, 1)
	assert.Equal(t, StatsDay{Date: "2026-07-01", RawEntries: 3, Observations: 1, TotalEvents: 4}, res.View.Days[0])

	joined := join(res.Lines)
	assert.Contains(t, joined, "Stats 2026-07-01..2026-07-01 (logical days)")
	assert.Contains(t, joined, "Raw entries: 3")
	assert.Contains(t, joined, "Observations: 1")
	assert.Contains(t, joined, "  mood: 1")
	assert.NotContains(t, joined, "pain") // sparse: zero kinds are omitted from human output
	assert.Contains(t, joined, "Total events: 4")
	assert.Contains(t, joined, "By day:")
	assert.Contains(t, joined, "2026-07-01: 3 entries, 1 observations, 4 total")
	// Inventory only — no evaluative language leaks into the volume surface.
	for _, banned := range []string{"streak", "score", "adherence", "keep it up"} {
		assert.NotContains(t, strings.ToLower(joined), banned)
	}
}

// TestStats_RolloverBoundary is the DST/rollover boundary case (AC-10). A
// same-moment raw entry and observation logged at 02:00 — before the 04:00
// logical-day rollover — land on DIFFERENT days: the raw entry buckets by its
// recorded civil date (what `lucid day` uses), while the observation buckets by
// its rollover-correct logical_date (the prior day). The 04:00 rollover is also
// where a DST spring-forward falls, so civil-date bucketing keeps both trees
// aligned with `lucid day` across the transition. America/New_York exercises a
// real DST-aware zone; the fixed EDT zone is the fallback when tzdata is absent.
func TestStats_RolloverBoundary(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = edt
	}
	r, a := statsRouter(t)

	at := time.Date(2026, 7, 2, 2, 0, 0, 0, loc) // 02:00 local, before the 04:00 rollover
	_, err = a.WriteRaw(storage.RawEntry{
		RecordedAt: at, OccurredAt: at, OccurredAtPrecision: storage.PrecisionExact,
		Source: "cli", Command: "/log", Body: "boundary raw",
	})
	require.NoError(t, err)

	captured, err := r.Capture(CaptureRequest{Tokens: []string{"pain", "3"}, Now: at})
	require.NoError(t, err)
	require.Equal(t, "2026-07-01", captured.LogicalDate, "a pre-rollover observation is rollover-correct: prior logical day")

	res, err := r.Stats(StatsOptions{From: "2026-07-01", To: "2026-07-02"}, at)
	require.NoError(t, err)
	require.Len(t, res.View.Days, 2)

	// Observation placement is rollover-correct (on 2026-07-01, the prior day).
	assert.Equal(t, 1, res.View.Days[0].Observations, "obs on the rollover-correct logical day")
	assert.Equal(t, 0, res.View.Days[0].RawEntries)
	// Raw-entry placement equals lucid day's recorded-civil-date bucketing (2026-07-02).
	assert.Equal(t, 1, res.View.Days[1].RawEntries, "raw entry on its recorded civil date")
	assert.Equal(t, 0, res.View.Days[1].Observations)

	// Parity with the `lucid day` join (same ReadDayView), per day.
	for i, date := range []string{"2026-07-01", "2026-07-02"} {
		dv, derr := a.ReadDayView(date, loc)
		require.NoError(t, derr)
		assert.Equal(t, len(dv.RawEntryIDs), res.View.Days[i].RawEntries, "raw parity for %s", date)
		assert.Equal(t, len(dv.Obs.Events), res.View.Days[i].Observations, "obs parity for %s", date)
	}
}

// TestStats_PerDaySumAndSpanning locks the per-day-sum invariant (AC-9) and the
// spanning-observation rule: a range observation that covers several
// logical days is counted ONCE, on its start day, so the per-day columns sum
// exactly to the top-line totals — a spanning event does not reappear on later
// covered days. It also asserts `lucid day` parity per day (AC-11).
func TestStats_PerDaySumAndSpanning(t *testing.T) {
	r, a := statsRouter(t)

	// 2026-07-01: one raw entry, one same-day pain observation, and a sleep
	// range event spanning 07-01..07-03 (filed under its 07-01 start day).
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 1)
	seedObs(t, a, observations.KindPain, "2026-07-01", "2026-07-01T09:30:00-04:00")
	end := "2026-07-03T06:00:00-04:00"
	_, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindSleep,
		RecordedAt: end, OccurredAt: "2026-07-01T22:00:00-04:00",
		OccurredAtPrecision: observations.PrecisionRange, OccurredAtEnd: &end,
		LogicalDate: "2026-07-01", Source: observations.SourceMicrolog,
	})
	require.NoError(t, err)
	// 2026-07-02: one same-day mood observation.
	seedObs(t, a, observations.KindMood, "2026-07-02", "2026-07-02T11:00:00-04:00")
	// 2026-07-03: nothing of its own.

	res, err := r.Stats(StatsOptions{From: "2026-07-01", To: "2026-07-03"}, nowEDT())
	require.NoError(t, err)
	require.Len(t, res.View.Days, 3)

	// The spanning sleep is counted once, on its start day (07-01, with pain).
	assert.Equal(t, 2, res.View.Days[0].Observations, "start day carries the spanning event exactly once")
	assert.Equal(t, 1, res.View.Days[1].Observations, "mid-range day: only its own event, not the spanning one")
	assert.Equal(t, 0, res.View.Days[2].Observations, "last covered day: the spanning event does not reappear")

	// Per-day columns sum exactly to the top-line window counts (AC-9).
	var sumRaw, sumObs, sumTotal int
	for _, d := range res.View.Days {
		sumRaw += d.RawEntries
		sumObs += d.Observations
		sumTotal += d.TotalEvents
	}
	assert.Equal(t, res.View.RawEntries, sumRaw)
	assert.Equal(t, res.View.Observations, sumObs)
	assert.Equal(t, res.View.TotalEvents, sumTotal)
	assert.Equal(t, 1, res.View.RawEntries)
	assert.Equal(t, 3, res.View.Observations) // pain + sleep (once) + mood
	assert.Equal(t, 4, res.View.TotalEvents)

	// Parity: each day matches len(ReadDayView(d).RawEntryIDs) / .Obs.Events —
	// i.e. `lucid day` up to the two documented divergences (AC-11).
	for i, date := range []string{"2026-07-01", "2026-07-02", "2026-07-03"} {
		dv, derr := a.ReadDayView(date, edt)
		require.NoError(t, derr)
		assert.Equal(t, len(dv.RawEntryIDs), res.View.Days[i].RawEntries, "raw parity for %s", date)
		assert.Equal(t, len(dv.Obs.Events), res.View.Days[i].Observations, "obs parity for %s", date)
	}
}

// TestStats_JSONShapeAndDenseOrder asserts the full --json contract: the fixed
// top-level field order and the DENSE observations_by_kind in config order
// (pain → intake → elimination → mood), every key present even when zero (AC-6).
func TestStats_JSONShapeAndDenseOrder(t *testing.T) {
	r, a := statsRouter(t)
	seedObs(t, a, observations.KindPain, "2026-07-02", "2026-07-02T09:00:00-04:00")
	seedObs(t, a, observations.KindIntake, "2026-07-02", "2026-07-02T10:00:00-04:00")

	res, err := r.Stats(StatsOptions{From: "2026-07-02", To: "2026-07-02"}, nowEDT())
	require.NoError(t, err)
	b, err := json.Marshal(res.View)
	require.NoError(t, err)
	s := string(b)

	// All four enabled kinds present, zeros included.
	assert.Contains(t, s, `"observations_by_kind":{"pain":1,"intake":1,"elimination":0,"mood":0}`)

	// Config-order key sequence (Go would sort a map alphabetically).
	iPain := strings.Index(s, `"pain"`)
	iIntake := strings.Index(s, `"intake"`)
	iElim := strings.Index(s, `"elimination"`)
	iMood := strings.Index(s, `"mood"`)
	require.NotEqual(t, -1, iPain)
	assert.Less(t, iPain, iIntake, "pain before intake")
	assert.Less(t, iIntake, iElim, "intake before elimination")
	assert.Less(t, iElim, iMood, "elimination before mood")

	// Top-level field order matches the documented example.
	fields := []string{
		`"from"`, `"to"`, `"logical_day"`, `"raw_entries"`, `"observations"`,
		`"observations_by_kind"`, `"total_events"`, `"days"`,
	}
	prev := -1
	for _, f := range fields {
		idx := strings.Index(s, f)
		require.NotEqual(t, -1, idx, "field %s present", f)
		assert.Greater(t, idx, prev, "field %s in documented order", f)
		prev = idx
	}
}

// TestStats_RangeResolution covers the flag policy the router owns (AC-2, AC-3,
// AC-4): the default window, --last N inclusive of today, --from/--to defaults,
// mutual exclusion, and the usage errors (each carries "invalid argument" so the
// CLI maps it to the usage exit code).
func TestStats_RangeResolution(t *testing.T) {
	r, _ := statsRouter(t)
	now := time.Date(2026, 7, 11, 21, 0, 0, 0, edt) // logical day 2026-07-11

	// Bare → today only.
	res, err := r.Stats(StatsOptions{}, now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-11", res.View.From)
	assert.Equal(t, "2026-07-11", res.View.To)
	require.Len(t, res.View.Days, 1)

	// --last 2 → 07-10..07-11 (ending at AND including today).
	res, err = r.Stats(StatsOptions{LastSet: true, Last: 2}, now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-10", res.View.From)
	assert.Equal(t, "2026-07-11", res.View.To)
	require.Len(t, res.View.Days, 2)

	// --from alone → --to defaults to today.
	res, err = r.Stats(StatsOptions{From: "2026-07-09"}, now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-09", res.View.From)
	assert.Equal(t, "2026-07-11", res.View.To)
	require.Len(t, res.View.Days, 3)

	// --to alone → single day at --to.
	res, err = r.Stats(StatsOptions{To: "2026-07-05"}, now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", res.View.From)
	assert.Equal(t, "2026-07-05", res.View.To)
	require.Len(t, res.View.Days, 1)

	// --last and --from together → mutually exclusive usage error.
	_, err = r.Stats(StatsOptions{LastSet: true, Last: 1, From: "2026-07-01"}, now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid argument")

	// --last 0 → usage error.
	_, err = r.Stats(StatsOptions{LastSet: true, Last: 0}, now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid argument")

	// from > to → usage error.
	_, err = r.Stats(StatsOptions{From: "2026-07-12", To: "2026-07-10"}, now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid argument")

	// Malformed --from → usage error.
	_, err = r.Stats(StatsOptions{From: "2026-13-40"}, now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid argument")

	// Malformed --to → usage error.
	_, err = r.Stats(StatsOptions{To: "not-a-date"}, now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid argument")
}

// TestLogicalDayRange covers the shared day-enumeration helper directly: an
// inclusive forward range, a single day, and the defensive reversed-range guard
// (never reached through Stats, which validates from <= to, but kept so the
// promotable helper is total).
func TestLogicalDayRange(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, edt)
	to := time.Date(2026, 7, 3, 0, 0, 0, 0, edt)
	days := logicalDayRange(from, to)
	require.Len(t, days, 3)
	assert.Equal(t, "2026-07-01", days[0].Format("2006-01-02"))
	assert.Equal(t, "2026-07-03", days[2].Format("2006-01-02"))

	assert.Len(t, logicalDayRange(from, from), 1)
	assert.Nil(t, logicalDayRange(to, from), "a reversed range yields no days")
}

// TestStats_Deterministic: the projection is a pure read, so repeated calls
// render byte-identical lines and views for a fixed clock.
func TestStats_Deterministic(t *testing.T) {
	r, a := statsRouter(t)
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 2)
	seedObs(t, a, observations.KindPain, "2026-07-01", "2026-07-01T09:30:00-04:00")

	first, err := r.Stats(StatsOptions{From: "2026-07-01", To: "2026-07-02"}, nowEDT())
	require.NoError(t, err)
	second, err := r.Stats(StatsOptions{From: "2026-07-01", To: "2026-07-02"}, nowEDT())
	require.NoError(t, err)
	assert.Equal(t, first.Lines, second.Lines)
	assert.Equal(t, first.View, second.View)
}
