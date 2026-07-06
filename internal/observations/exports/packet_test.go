package exports

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

func medEvent(id, date, what, dose string, taken bool) observations.Event {
	return observations.Event{
		ID: id, Kind: observations.KindMed, LogicalDate: date,
		Payload: map[string]any{"what": what, "dose": dose, "taken": taken},
	}
}

func TestDeriveRegimen_LatestPerMedAndSkippedKept(t *testing.T) {
	meds := []observations.Event{
		medEvent("obs_2026_07_01_001", "2026-07-01", "ibuprofen", "400", true),
		medEvent("obs_2026_07_03_001", "2026-07-03", "ibuprofen", "600", true), // later wins
		medEvent("obs_2026_07_02_001", "2026-07-02", "naproxen", "250", true),
		medEvent("obs_2026_07_04_001", "2026-07-04", "naproxen", "", false), // latest is a skip
	}
	reg := DeriveRegimen(meds)
	require.Len(t, reg, 2)
	// Sorted case-insensitively by med name: ibuprofen, naproxen.
	assert.Equal(t, "ibuprofen", reg[0].What)
	assert.Equal(t, "600", reg[0].Detail, "the most recent taken dose")
	assert.Equal(t, "naproxen", reg[1].What)
	assert.Equal(t, "(last logged: skipped 2026-07-04)", reg[1].Detail, "a skipped med is never dropped")
}

func TestCountEpisodes_RunsWithGapTolerance(t *testing.T) {
	// Days 01,02 above threshold; a one-day gap on 03 (below threshold); 04
	// above → one bridged episode. Then a break, then 07 alone → second episode.
	pain := []observations.Event{
		painEvent("obs_2026_07_01_001", "2026-07-01", 6),
		painEvent("obs_2026_07_02_001", "2026-07-02", 5),
		painEvent("obs_2026_07_03_001", "2026-07-03", 2), // below threshold
		painEvent("obs_2026_07_04_001", "2026-07-04", 7),
		painEvent("obs_2026_07_07_001", "2026-07-07", 8),
	}
	count, eps := CountEpisodes(pain, EpisodeThreshold, EpisodeGapDays)
	require.Equal(t, 2, count)
	assert.Equal(t, "2026-07-01", eps[0].Start)
	assert.Equal(t, "2026-07-04", eps[0].End)
	assert.Equal(t, 4, eps[0].DaysSpanned)
	assert.Equal(t, "2026-07-07", eps[1].Start)
	assert.Equal(t, 1, eps[1].DaysSpanned)
}

func TestCountEpisodes_None(t *testing.T) {
	count, eps := CountEpisodes([]observations.Event{painEvent("obs_2026_07_01_001", "2026-07-01", 2)}, EpisodeThreshold, EpisodeGapDays)
	assert.Zero(t, count)
	assert.Empty(t, eps)
}

func TestBuildPacketDayRows(t *testing.T) {
	pain := []observations.Event{{ID: "obs_2026_07_01_001", Kind: observations.KindPain, LogicalDate: "2026-07-01", Payload: map[string]any{"intensity": 6, "site": "knee"}}}
	med := []observations.Event{medEvent("obs_2026_07_01_002", "2026-07-01", "ibuprofen", "400", true)}
	interv := []observations.Event{{ID: "obs_2026_07_01_003", Kind: observations.KindIntervention, LogicalDate: "2026-07-01", Payload: map[string]any{"what": "physio", "body_site": "left-knee"}}}
	engine := map[string]EngineDayFacts{"2026-07-01": {Capacity: 4, Mode: "green"}}

	rows := BuildPacketDayRows(pain, med, interv, engine)
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, 4, r.Capacity)
	assert.Equal(t, "green", r.Mode)
	assert.Equal(t, []string{"6 (knee)"}, r.Pains)
	assert.Equal(t, []string{"ibuprofen 400"}, r.Meds)
	assert.Equal(t, []string{"physio left-knee"}, r.Interventions)
}

func TestRenderClinician_HeaderAndBodyDeterministic(t *testing.T) {
	in := ClinicianInput{
		WindowStart:     "2026-04-06",
		WindowEnd:       "2026-07-05",
		ClinicalContext: []string{"in recovery — flag anything habit-forming"},
		Injuries:        []string{"left knee (managed)"},
		Regimen:         []RegimenLine{{What: "ibuprofen", Detail: "400"}, {What: "naproxen", Detail: "(last logged: skipped 2026-07-04)"}},
		EpisodeCount:    2,
		Episodes:        []Episode{{Start: "2026-07-01", End: "2026-07-04", DaysSpanned: 4}},
		Days: []PacketDayRow{
			{Date: "2026-07-01", Capacity: 4, Mode: "green", Pains: []string{"6 (knee)"}, Meds: []string{"ibuprofen 400"}, Interventions: []string{"physio"}},
		},
	}
	out := RenderClinician(in)
	assert.Contains(t, out, "Clinician packet — 2026-04-06 to 2026-07-05")
	assert.Contains(t, out, "in recovery — flag anything habit-forming")
	assert.Contains(t, out, "Active injuries: left knee (managed)")
	assert.Contains(t, out, "ibuprofen 400")
	assert.Contains(t, out, "(last logged: skipped 2026-07-04)")
	assert.Contains(t, out, "Pain episodes in range: 2")
	assert.Contains(t, out, "[med ibuprofen 400]")
	assert.Contains(t, out, "[intervention physio]")
	// Byte-stable.
	assert.Equal(t, out, RenderClinician(in))
}

// TestExportsIsPure asserts the exports package touches no I/O and no
// model/provider/agent — the render path stays deterministic (P9). It scans
// non-test sources for an allowlisted import set.
func TestExportsIsPure(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	allowed := map[string]bool{
		`"fmt"`:     true,
		`"sort"`:    true,
		`"strconv"`: true,
		`"strings"`: true,
		`"time"`:    true,
		`"github.com/mrz1836/lucid/internal/observations"`: true,
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		require.NoError(t, perr)
		for _, imp := range f.Imports {
			require.Truef(t, allowed[imp.Path.Value], "exports file %s imports disallowed %s", name, imp.Path.Value)
		}
	}
}
