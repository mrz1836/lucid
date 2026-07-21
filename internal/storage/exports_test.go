package storage

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// appendObs appends one observation with an explicit payload on a logical day.
func appendObs(t *testing.T, a *Adapter, kind observations.Kind, date string, payload map[string]any) {
	t.Helper()
	_, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: kind,
		RecordedAt: date + "T12:00:00Z", OccurredAt: date + "T12:00:00Z",
		OccurredAtPrecision: observations.PrecisionExact, LogicalDate: date,
		Source: observations.SourceMicrolog, Payload: payload,
	})
	require.NoError(t, err)
}

// seedEngineDay writes a folded engine day record carrying capacity/mode.
func seedEngineDay(t *testing.T, a *Adapter, date string, capacity int, mode engine.Mode) {
	t.Helper()
	d, _ := time.Parse("2006-01-02", date)
	require.NoError(t, a.WriteEngineDay(engine.DayRecord{
		DayID: engine.DayID(d), LogicalDate: date, RecordedAt: date + "T22:00:00Z",
		Mode: mode, Links: map[string]string{"journal": engine.StatusDone},
		Completed: true, Capacity: capacity, Profile: engine.DefaultProfile, Corrections: []engine.Correction{},
	}))
}

func TestExportSeriesCSV(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())
	appendObs(t, a, observations.KindPain, "2026-07-01", map[string]any{"intensity": 6, "site": "knee"})
	appendObs(t, a, observations.KindMood, "2026-07-01", map[string]any{"level": 3})
	seedEngineDay(t, a, "2026-07-01", 4, engine.ModeGreen)

	res, err := a.ExportSeriesCSV(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err)

	body, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	assert.Equal(t, "logical_date,pain,mood,capacity", lines[0])
	assert.Contains(t, string(body), "2026-07-01,6,3,4")

	// The disclosure log recorded the export.
	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Contains(t, log[0], "series")
	assert.Contains(t, log[0], res.Path)
}

func TestExportClinicianPacket_FirstRenderTrailing90(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())

	// Clinical context + an active injury.
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.Packet.ClinicalContext = []string{"in recovery — flag anything habit-forming"}
	require.NoError(t, a.SaveObservationsConfig(cfg))
	_, err = a.UpdateRegistry(observations.RegistryInjury, "injury_a-cedar", observations.RegistryPatch{
		DisplayName: "left knee", At: "2026-06-01T09:00:00Z", Status: observations.StatusManaged,
	})
	require.NoError(t, err)

	// A pain flare with a secret note, meds (one taken, one skipped latest), an
	// intervention, a location, and a weather event — the last three must never
	// appear in the packet body.
	for _, d := range []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-04"} {
		appendObs(t, a, observations.KindPain, d, map[string]any{"intensity": 6, "site": "knee", "note": "SECRETPAINNOTE"})
		seedEngineDay(t, a, d, 4, engine.ModeYellow)
	}
	appendObs(t, a, observations.KindMed, "2026-07-02", map[string]any{"what": "ibuprofen", "dose": "400", "taken": true})
	appendObs(t, a, observations.KindMed, "2026-07-01", map[string]any{"what": "naproxen", "dose": "250", "taken": true})
	appendObs(t, a, observations.KindMed, "2026-07-04", map[string]any{"what": "naproxen", "taken": false})
	appendObs(t, a, observations.KindIntervention, "2026-07-03", map[string]any{"what": "physio", "body_site": "left-knee"})
	appendObs(t, a, observations.KindLocation, "2026-07-01", map[string]any{"place_ref": "place_a-river", "note": "SECRETPLACE"})
	appendObs(t, a, observations.KindContextDay, "2026-07-02", map[string]any{"place_ref": "place_a-river", "temp_mean_c": 21.3, "pressure_msl_hpa": 1015.2})

	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	res, err := a.ExportClinicianPacket(now, time.UTC, "", false)
	require.NoError(t, err)

	assert.Equal(t, "2026-04-07", res.WindowStart, "first-ever export is the trailing 90 days")
	assert.Equal(t, "2026-07-05", res.WindowEnd)

	body, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	packet := string(body)

	// Header: clinical context verbatim, injury, regimen incl. the skipped med.
	assert.Contains(t, packet, "in recovery — flag anything habit-forming")
	assert.Contains(t, packet, "left knee (managed)")
	assert.Contains(t, packet, "ibuprofen 400")
	assert.Contains(t, packet, "naproxen (last logged: skipped 2026-07-04)")
	assert.Contains(t, packet, "Pain episodes in range: 1")
	// Body: capacity/mode + pain series with markers.
	assert.Contains(t, packet, "capacity 4")
	assert.Contains(t, packet, "mode yellow")
	assert.Contains(t, packet, "[intervention physio left-knee]")

	// Excludes note fields, location, and weather by default.
	assert.NotContains(t, packet, "SECRETPAINNOTE")
	assert.NotContains(t, packet, "SECRETPLACE")
	assert.NotContains(t, packet, "place_a-river")
	assert.NotContains(t, packet, "1015.2", "weather never rides the packet")

	// Only the path is postable — no body content in the returned message.
	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Contains(t, log[0], "clinician")
	assert.Contains(t, log[0], res.Path)
}

func TestExportClinicianPacket_SinceLastExport(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())

	first, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", false)
	require.NoError(t, err)
	assert.Equal(t, "2026-04-07", first.WindowStart)

	// A later export starts at the previous export's window end.
	second, err := a.ExportClinicianPacket(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), time.UTC, "", false)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", second.WindowStart, "since the last export")
	assert.Equal(t, "2026-07-20", second.WindowEnd)

	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	assert.Len(t, log, 2, "every render appends one disclosure-log line")
}

// seedInjury writes an injury registry record with a status and convention
// Fields — the storage-layer equivalent of a `lucid injury` write.
func seedInjury(t *testing.T, a *Adapter, key, name, status string, fields map[string]any) {
	t.Helper()
	_, err := a.UpdateRegistry(observations.RegistryInjury, key, observations.RegistryPatch{
		DisplayName: name, At: "2026-06-01T09:00:00Z", Status: status, Fields: fields,
	})
	require.NoError(t, err)
}

// TestInjuryContext proves the structured projection: active + managed injuries
// are included, resolved is excluded, the output is byte-stable in key order,
// and the convention Fields map onto the struct. Seeded out of key order to
// prove the sort.
func TestInjuryContext(t *testing.T) {
	a := newObsStore(t)

	// Seeded deliberately out of alphabetical key order.
	seedInjury(t, a, "injury_c-oak", "right shoulder", observations.StatusManaged, map[string]any{
		"body_area": "right shoulder", "current_limitations": "no overhead press",
		"timeline": "since 2019", "severity": "moderate",
	})
	seedInjury(t, a, "injury_a-cedar", "left knee", observations.StatusActive, map[string]any{
		"body_area": "left knee", "current_limitations": "no deep squats under load",
		"timeline": "since 2014", "severity": "mild now",
	})
	seedInjury(t, a, "injury_b-maple", "old ankle roll", observations.StatusResolved, map[string]any{
		"body_area": "left ankle",
	})

	ctx, err := a.InjuryContext()
	require.NoError(t, err)

	// Resolved excluded; active + managed only, in byte-stable key order.
	require.Len(t, ctx, 2, "resolved injuries are excluded")
	assert.Equal(t, "injury_a-cedar", ctx[0].Key, "sorted by key")
	assert.Equal(t, "injury_c-oak", ctx[1].Key)

	// Convention Fields mapped onto the struct.
	assert.Equal(t, "left knee", ctx[0].DisplayName)
	assert.Equal(t, observations.StatusActive, ctx[0].Status)
	assert.Equal(t, "left knee", ctx[0].BodyArea)
	assert.Equal(t, "no deep squats under load", ctx[0].CurrentLimitations)
	assert.Equal(t, "since 2014", ctx[0].Timeline)
	assert.Equal(t, "mild now", ctx[0].Severity)
	assert.Equal(t, observations.StatusManaged, ctx[1].Status)
	assert.Equal(t, "no overhead press", ctx[1].CurrentLimitations)
}

// TestInjuryContext_EmptyAndMissingFields proves an empty store is an
// empty-but-valid projection, and an injury with no convention Fields yields
// empty strings (capture never blocks, so a bare injury is projectable).
func TestInjuryContext_EmptyAndMissingFields(t *testing.T) {
	a := newObsStore(t)

	empty, err := a.InjuryContext()
	require.NoError(t, err)
	assert.Empty(t, empty, "an empty registry is an empty projection, not an error")

	seedInjury(t, a, "injury_a-cedar", "bare injury", observations.StatusActive, nil)
	bare, err := a.InjuryContext()
	require.NoError(t, err)
	require.Len(t, bare, 1)
	assert.Equal(t, "bare injury", bare[0].DisplayName)
	assert.Empty(t, bare[0].BodyArea, "a missing convention field projects as empty, not a panic")
	assert.Empty(t, bare[0].Severity)
}

// TestInjuryContext_NoDiagnosticLanguage is the sanctuary guard: the projection
// renders the registry facts verbatim and synthesizes no clinical-advice
// language of its own (observations.md §9, "never diagnosis, never treatment
// advice"). Facts go in; the projection adds no diagnostic tokens.
func TestInjuryContext_NoDiagnosticLanguage(t *testing.T) {
	a := newObsStore(t)
	seedInjury(t, a, "injury_a-cedar", "left knee", observations.StatusManaged, map[string]any{
		"body_area": "left knee", "current_limitations": "no deep squats", "severity": "moderate",
	})

	ctx, err := a.InjuryContext()
	require.NoError(t, err)
	require.Len(t, ctx, 1)

	rendered := strings.ToLower(strings.Join([]string{
		ctx[0].Key, ctx[0].DisplayName, ctx[0].Status, ctx[0].BodyArea,
		ctx[0].CurrentLimitations, ctx[0].Timeline, ctx[0].Severity,
	}, " "))
	for _, banned := range []string{"diagnos", "prescrib", "you should", "treatment plan", "recommend", "consult a"} {
		assert.NotContains(t, rendered, banned,
			"the projection renders registry facts only, never synthesized clinical advice")
	}
}

func TestExportClinicianPacket_AllAndOverride(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())

	all, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "", true)
	require.NoError(t, err)
	assert.Equal(t, "all", all.WindowStart)

	override, err := a.ExportClinicianPacket(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC, "2026-06-01", false)
	require.NoError(t, err)
	assert.Equal(t, "2026-06-01", override.WindowStart)
}
