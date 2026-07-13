package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// statsJSON mirrors the documented `lucid stats --json` shape
// (docs/usage/commands.md §stats) with observations_by_kind decoded as a plain
// map — the router marshals a stable, config-ordered object, and a harness
// treats a missing kind as zero, so the map decode is the consumer's view.
type statsJSON struct {
	From               string         `json:"from"`
	To                 string         `json:"to"`
	LogicalDay         bool           `json:"logical_day"`
	RawEntries         int            `json:"raw_entries"`
	Observations       int            `json:"observations"`
	ObservationsByKind map[string]int `json:"observations_by_kind"`
	TotalEvents        int            `json:"total_events"`
	Days               []struct {
		Date         string `json:"date"`
		RawEntries   int    `json:"raw_entries"`
		Observations int    `json:"observations"`
		TotalEvents  int    `json:"total_events"`
	} `json:"days"`
}

// statsSeededHome points LUCID_HOME at a fresh, scaffolded Ledger and returns a
// storage adapter over the same home for seeding raw entries and observations
// at controlled dates. The CLI boots its own adapter over LUCID_HOME, so what
// this seeds on disk is exactly what `lucid stats` reads back.
func statsSeededHome(t *testing.T) (string, *storage.Adapter) {
	t.Helper()
	home := isolatedHome(t)
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	require.NoError(t, a.ScaffoldEngine())
	return home, a
}

// seedStatsRaw writes one raw entry recorded at at (bucketed by its recorded
// civil date, the exact join `lucid day` uses), with a verbatim body.
func seedStatsRaw(t *testing.T, a *storage.Adapter, at time.Time, body string) {
	t.Helper()
	_, err := a.WriteRaw(storage.RawEntry{
		RecordedAt: at, OccurredAt: at, OccurredAtPrecision: storage.PrecisionExact,
		Source: "cli", Command: "/log", Body: body,
	})
	require.NoError(t, err)
}

// seedStatsObs appends one exact-precision observation of kind on logicalDate,
// with an optional payload so a no-content test can plant distinctive text.
func seedStatsObs(t *testing.T, a *storage.Adapter, kind observations.Kind, logicalDate string, at time.Time, payload map[string]any) {
	t.Helper()
	occ := at.Format(time.RFC3339)
	_, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: kind,
		RecordedAt: occ, OccurredAt: occ, OccurredAtPrecision: observations.PrecisionExact,
		LogicalDate: logicalDate, Source: observations.SourceMicrolog, Payload: payload,
	})
	require.NoError(t, err)
}

// TestStats_CLI_BareFreshLedger: bare `lucid stats` on a fresh Ledger reports
// the current logical day only, all zeros — the today-only default (AC-4),
// mirroring bare `lucid day`. It exercises the bare-scaffold path (no explicit
// init) the way the metrics CLI test does.
func TestStats_CLI_BareFreshLedger(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon()) // logical day 2026-07-05

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "stats")
	require.NoError(t, err)
	assert.Contains(t, out, "Stats 2026-07-05..2026-07-05 (logical days)")
	assert.Contains(t, out, "Raw entries: 0")
	assert.Contains(t, out, "Observations: 0")
	assert.Contains(t, out, "Total events: 0")

	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "stats", "--json")
	require.NoError(t, err)
	var v statsJSON
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &v))
	assert.Equal(t, "2026-07-05", v.From)
	assert.Equal(t, "2026-07-05", v.To)
	assert.True(t, v.LogicalDay)
	assert.Equal(t, 0, v.TotalEvents)
	require.Len(t, v.Days, 1)
}

// TestStats_CLI_LastTwoDays: `--last 2` after seeding two distinct days reports
// both days, each with its own counts, and the window totals are the sum
// (AC-2). "today" resolves through the same rollover the day view uses.
func TestStats_CLI_LastTwoDays(t *testing.T) {
	_, a := statsSeededHome(t)
	withClock(t, afternoon()) // today = 2026-07-05, so --last 2 → 07-04..07-05

	// 2026-07-04: two raw entries + one pain observation.
	seedStatsRaw(t, a, time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC), "seed a")
	seedStatsRaw(t, a, time.Date(2026, 7, 4, 12, 1, 0, 0, time.UTC), "seed b")
	seedStatsObs(t, a, observations.KindPain, "2026-07-04", time.Date(2026, 7, 4, 12, 30, 0, 0, time.UTC), nil)
	// 2026-07-05: one raw entry + one intake observation.
	seedStatsRaw(t, a, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), "seed c")
	seedStatsObs(t, a, observations.KindIntake, "2026-07-05", time.Date(2026, 7, 5, 12, 30, 0, 0, time.UTC), nil)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "stats", "--last", "2", "--json")
	require.NoError(t, err)
	var v statsJSON
	require.NoError(t, json.Unmarshal([]byte(out), &v))

	assert.Equal(t, "2026-07-04", v.From)
	assert.Equal(t, "2026-07-05", v.To)
	assert.Equal(t, 3, v.RawEntries)
	assert.Equal(t, 2, v.Observations)
	assert.Equal(t, 5, v.TotalEvents)
	require.Len(t, v.Days, 2)
	assert.Equal(t, "2026-07-04", v.Days[0].Date)
	assert.Equal(t, 2, v.Days[0].RawEntries)
	assert.Equal(t, 1, v.Days[0].Observations)
	assert.Equal(t, "2026-07-05", v.Days[1].Date)
	assert.Equal(t, 1, v.Days[1].RawEntries)
	assert.Equal(t, 1, v.Days[1].Observations)
	// Per-day columns sum exactly to the top-line window counts.
	assert.Equal(t, v.RawEntries, v.Days[0].RawEntries+v.Days[1].RawEntries)
	assert.Equal(t, v.Observations, v.Days[0].Observations+v.Days[1].Observations)
}

// TestStats_CLI_LastAndRangeMutuallyExclusive: `--last` combined with `--from`
// is a usage error that maps to exit 2 (AC-4). The router owns the policy; the
// CLI just surfaces the error and the exit-code mapping proves it.
func TestStats_CLI_LastAndRangeMutuallyExclusive(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "stats", "--last", "2", "--from", "2026-07-01")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid argument")
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestStats_CLI_FromAloneDefaultsToToday: `--from` without `--to` defaults `--to`
// to today's logical day, so the window runs from --from through today (AC-4).
func TestStats_CLI_FromAloneDefaultsToToday(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon()) // today = 2026-07-05

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "stats", "--from", "2026-07-04", "--json")
	require.NoError(t, err)
	var v statsJSON
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.Equal(t, "2026-07-04", v.From)
	assert.Equal(t, "2026-07-05", v.To)
	require.Len(t, v.Days, 2)
}

// TestStats_CLI_LastZeroUsageError: `--last 0` is a usage error (exit 2) — a
// window must be at least one day (AC-4).
func TestStats_CLI_LastZeroUsageError(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "stats", "--last", "0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid argument")
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestStats_CLI_JSONDenseByKind: `--json` emits a dense observations_by_kind —
// every enabled kind present (zeros included) in config order — so a harness
// reads a stable key set across runs (AC-6).
func TestStats_CLI_JSONDenseByKind(t *testing.T) {
	_, a := statsSeededHome(t)
	withClock(t, afternoon())
	seedStatsObs(t, a, observations.KindPain, "2026-07-05", time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), nil)
	seedStatsObs(t, a, observations.KindIntake, "2026-07-05", time.Date(2026, 7, 5, 12, 30, 0, 0, time.UTC), nil)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "stats", "--json")
	require.NoError(t, err)

	var v statsJSON
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	// Dense: all four enabled kinds present, zeros included.
	for _, kind := range []string{"pain", "intake", "elimination", "mood"} {
		_, ok := v.ObservationsByKind[kind]
		assert.Truef(t, ok, "observations_by_kind is dense: %q present", kind)
	}
	assert.Equal(t, 1, v.ObservationsByKind["pain"])
	assert.Equal(t, 1, v.ObservationsByKind["intake"])
	assert.Equal(t, 0, v.ObservationsByKind["elimination"])
	assert.Equal(t, 0, v.ObservationsByKind["mood"])

	// Config-order on the wire (Go would sort a bare map alphabetically).
	iPain := strings.Index(out, `"pain"`)
	iIntake := strings.Index(out, `"intake"`)
	iElim := strings.Index(out, `"elimination"`)
	iMood := strings.Index(out, `"mood"`)
	require.NotEqual(t, -1, iPain)
	assert.Less(t, iPain, iIntake, "pain before intake")
	assert.Less(t, iIntake, iElim, "intake before elimination")
	assert.Less(t, iElim, iMood, "elimination before mood")
}

// TestStats_CLI_NoContentLeak: the command reports counts but never the body of
// a raw entry or an observation payload — the projection exposes structured
// metadata only (AC-8). Distinctive text is planted in a raw entry body and an
// observation note; neither may appear in the human or the --json output.
func TestStats_CLI_NoContentLeak(t *testing.T) {
	_, a := statsSeededHome(t)
	withClock(t, afternoon())

	const rawMarker = "raw-body-sentinel-7788"
	const obsMarker = "obs-note-sentinel-9911"
	seedStatsRaw(t, a, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), rawMarker)
	seedStatsObs(t, a, observations.KindPain, "2026-07-05",
		time.Date(2026, 7, 5, 12, 30, 0, 0, time.UTC), map[string]any{"note": obsMarker})

	human, _, err := runRoot(t, BuildInfo{Version: "dev"}, "stats")
	require.NoError(t, err)
	assert.Contains(t, human, "Raw entries: 1")
	assert.Contains(t, human, "Observations: 1")
	assert.NotContains(t, human, rawMarker, "raw entry body never surfaces")
	assert.NotContains(t, human, obsMarker, "observation content never surfaces")

	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "stats", "--json")
	require.NoError(t, err)
	assert.NotContains(t, jsonOut, rawMarker, "raw entry body never surfaces in --json")
	assert.NotContains(t, jsonOut, obsMarker, "observation content never surfaces in --json")
	var v statsJSON
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &v))
	assert.Equal(t, 1, v.RawEntries)
	assert.Equal(t, 1, v.Observations)
}
