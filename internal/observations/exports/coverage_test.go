package exports

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestPayloadInt_CoercesAndFallsBack pins the numeric coercion: an int, a JSON
// float64, and an int64 all read as ints, while a wrong-typed or absent value
// reports not-a-number (the zero/false fallback) rather than fabricating a
// reading.
func TestPayloadInt_CoercesAndFallsBack(t *testing.T) {
	cases := []struct {
		name    string
		val     any
		want    int
		present bool
	}{
		{"int", 6, 6, true},
		{"int64", int64(7), 7, true},
		{"float64 from disk", 9.0, 9, true},
		{"string", "8", 0, false},
		{"bool", true, 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := payloadInt(map[string]any{"intensity": tc.val}, "intensity")
			assert.Equal(t, tc.present, ok)
			assert.Equal(t, tc.want, got)
		})
	}
	// A missing key is also not-a-number.
	got, ok := payloadInt(map[string]any{}, "intensity")
	assert.False(t, ok)
	assert.Zero(t, got)
}

// TestPayloadStr_FallsBackToEmpty returns the string when present and the
// correct type, and "" for a wrong-typed or absent field.
func TestPayloadStr_FallsBackToEmpty(t *testing.T) {
	assert.Equal(t, "knee", payloadStr(map[string]any{"site": "knee"}, "site"))
	assert.Empty(t, payloadStr(map[string]any{"site": 42}, "site"), "wrong type → empty")
	assert.Empty(t, payloadStr(map[string]any{}, "site"), "absent → empty")
}

// TestPayloadBool_FallsBackToDefault returns the bool when present, and the
// caller's default for a wrong-typed or absent field. It also reads a
// non-"taken" key to prove the lookup honors the key argument rather than a
// fixed field name.
func TestPayloadBool_FallsBackToDefault(t *testing.T) {
	assert.False(t, payloadBool(map[string]any{"taken": false}, "taken", true))
	assert.True(t, payloadBool(map[string]any{"taken": "yes"}, "taken", true), "wrong type → default")
	assert.True(t, payloadBool(map[string]any{}, "taken", true), "absent → default true")
	assert.False(t, payloadBool(map[string]any{}, "taken", false), "absent → default false")
	assert.True(t, payloadBool(map[string]any{"present": true}, "present", false), "reads the named key, not a fixed one")
}

// TestBuildSeriesRows_MaxKeepsExistingWhenLower: a later same-day reading below
// the running max leaves the max untouched (the retain branch of maxPtr).
func TestBuildSeriesRows_MaxKeepsExistingWhenLower(t *testing.T) {
	pain := []observations.Event{
		painEvent("obs_2026_07_01_001", "2026-07-01", 6),
		painEvent("obs_2026_07_01_002", "2026-07-01", 3), // lower → max stays 6
	}
	rows := BuildSeriesRows(pain, nil, nil)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].Pain)
	assert.Equal(t, 6, *rows[0].Pain)
}

// TestDeriveRegimen_SkipsEmptyWhatAndBreaksTieByID: a med event with no `what`
// is ignored, and two events for the same med on the same day resolve by id
// (later seq wins) — the eventLater same-date tiebreak.
func TestDeriveRegimen_SkipsEmptyWhatAndBreaksTieByID(t *testing.T) {
	meds := []observations.Event{
		medEvent("obs_2026_07_01_000", "2026-07-01", "", "999", true), // no what → skipped
		medEvent("obs_2026_07_01_001", "2026-07-01", "ibuprofen", "400", true),
		medEvent("obs_2026_07_01_002", "2026-07-01", "ibuprofen", "600", true), // same day, higher id wins
	}
	reg := DeriveRegimen(meds)
	require.Len(t, reg, 1, "the empty-what event never becomes a line")
	assert.Equal(t, "ibuprofen", reg[0].What)
	assert.Equal(t, "600", reg[0].Detail, "the higher-id same-day event wins the tie")
}

// TestBuildPacketDayRows_SortsMultipleDays exercises the row comparator: two
// days sort ascending, and a pain reading on a day with no Engine record still
// produces a row.
func TestBuildPacketDayRows_SortsMultipleDays(t *testing.T) {
	pain := []observations.Event{
		painEvent("obs_2026_07_02_001", "2026-07-02", 5),
		painEvent("obs_2026_07_01_001", "2026-07-01", 6),
	}
	engine := map[string]EngineDayFacts{"2026-07-01": {Capacity: 4, Mode: "green"}}
	rows := BuildPacketDayRows(pain, nil, nil, engine)
	require.Len(t, rows, 2)
	assert.Equal(t, "2026-07-01", rows[0].Date, "rows sort ascending by date")
	assert.Equal(t, "2026-07-02", rows[1].Date)
	assert.Equal(t, 0, rows[1].Capacity, "a pain-only day carries no Engine capacity")
}

// TestAddMarks_SkipEmptyAndFlagSkipped covers the marker builders' guard and
// flag branches: an unreadable pain, a med/intervention with no `what`, and a
// deliberately skipped med.
func TestAddMarks_SkipEmptyAndFlagSkipped(t *testing.T) {
	pain := []observations.Event{
		{ID: "obs_2026_07_01_001", Kind: observations.KindPain, LogicalDate: "2026-07-01", Payload: map[string]any{"note": "no number"}}, // no intensity → skipped
		{ID: "obs_2026_07_01_002", Kind: observations.KindPain, LogicalDate: "2026-07-01", Payload: map[string]any{"intensity": 5}},      // no site
	}
	med := []observations.Event{
		medEvent("obs_2026_07_01_003", "2026-07-01", "", "400", true),          // no what → skipped
		medEvent("obs_2026_07_01_004", "2026-07-01", "naproxen", "250", false), // skipped flag
	}
	interv := []observations.Event{
		{ID: "obs_2026_07_01_005", Kind: observations.KindIntervention, LogicalDate: "2026-07-01", Payload: map[string]any{"body_site": "knee"}}, // no what → skipped
		{ID: "obs_2026_07_01_006", Kind: observations.KindIntervention, LogicalDate: "2026-07-01", Payload: map[string]any{"what": "stretch"}},   // no body_site
	}
	rows := BuildPacketDayRows(pain, med, interv, nil)
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, []string{"5"}, r.Pains, "the unreadable pain is dropped; the sited-less one has no site suffix")
	assert.Equal(t, []string{"naproxen 250 (skipped)"}, r.Meds, "the empty-what med is dropped; the skip is flagged")
	assert.Equal(t, []string{"stretch"}, r.Interventions, "the empty-what intervention is dropped")
}

// TestRenderClinician_RegimenLineWithoutDetail covers the header's detail-less
// regimen branch: a line with no dose renders bare.
func TestRenderClinician_RegimenLineWithoutDetail(t *testing.T) {
	out := RenderClinician(ClinicianInput{
		WindowStart: "2026-07-01", WindowEnd: "2026-07-07",
		Regimen: []RegimenLine{{What: "vitamin d", Detail: ""}},
	})
	require.Contains(t, out, "Current regimen:")
	lines := strings.Split(out, "\n")
	assert.Contains(t, lines, "  vitamin d", "a detail-less regimen line renders bare")
}

// TestDaysBetween_BadDateIsZero: an unparseable date yields 0 rather than
// panicking — the defensive guard for a value outside the sorted, well-formed
// set the episode detector feeds it.
func TestDaysBetween_BadDateIsZero(t *testing.T) {
	assert.Zero(t, daysBetween("not-a-date", "2026-07-02"))
	assert.Zero(t, daysBetween("2026-07-02", "also-bad"))
	assert.Equal(t, 1, daysBetween("2026-07-01", "2026-07-02"))
}
