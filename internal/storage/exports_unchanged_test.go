package storage

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// These tests pin the exact bytes the series export and the clinician packet
// produce today, so later work on measurements can prove it left both
// untouched. The expected bytes are inline constants (no golden files): each
// was derived by hand from its fixture and confirmed against the code before
// any measurement projection existed. Never edit a want… constant to make a
// test pass — a mismatch means a byte-identical surface changed.

// appendMeasurementObs appends one measurement reading in the shape the capture
// grammar writes (metric, value as text, optional unit) and returns the stored
// event, so a test can name its id.
func appendMeasurementObs(t *testing.T, a *Adapter, date, metric, value, unit string) observations.Event {
	t.Helper()
	payload := map[string]any{"metric": metric, "value": value}
	if unit != "" {
		payload["unit"] = unit
	}
	ev, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindMeasurement,
		RecordedAt: date + "T12:00:00Z", OccurredAt: date + "T12:00:00Z",
		OccurredAtPrecision: observations.PrecisionExact, LogicalDate: date,
		Source: observations.SourceMicrolog, Payload: payload,
	})
	require.NoError(t, err)
	return ev
}

// appendMeasurementVoidObs appends a measurement void by hand: an empty-payload
// measurement event whose refs name the reading it retracts and carry the
// boolean void modifier, filed on its own logical day.
func appendMeasurementVoidObs(t *testing.T, a *Adapter, date, target string) observations.Event {
	t.Helper()
	ev, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindMeasurement,
		RecordedAt: date + "T12:00:00Z", OccurredAt: date + "T12:00:00Z",
		OccurredAtPrecision: observations.PrecisionExact, LogicalDate: date,
		Source: observations.SourceMicrolog, Payload: map[string]any{},
		Tags: []string{}, Refs: map[string]any{"corrects": target, "void": true},
	})
	require.NoError(t, err)
	return ev
}

// wantSeriesCSV is the series projection for the fixture below: pain and mood
// are the day's maximum, capacity is the engine day's value, empty cells mark
// no reading, and measurements never appear.
const wantSeriesCSV = "logical_date,pain,mood,capacity\n" +
	"2026-07-01,6,3,4\n" +
	"2026-07-02,3,,\n" +
	"2026-07-03,,,5\n"

func TestExportSeriesCSV_UnchangedWithMeasurementsAndVoids(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())

	appendObs(t, a, observations.KindPain, "2026-07-01", map[string]any{"intensity": 6, "site": "knee"})
	appendObs(t, a, observations.KindPain, "2026-07-02", map[string]any{"intensity": 3})
	appendObs(t, a, observations.KindMood, "2026-07-01", map[string]any{"level": 3})
	seedEngineDay(t, a, "2026-07-01", 4, engine.ModeGreen)
	seedEngineDay(t, a, "2026-07-03", 5, engine.ModeGreen)

	appendMeasurementObs(t, a, "2026-07-02", "weight", "70.5", "kg")
	firstBP := appendMeasurementObs(t, a, "2026-07-02", "bp-systolic", "121", "mmHg")
	appendMeasurementObs(t, a, "2026-07-02", "bp-systolic", "119", "mmHg")
	appendMeasurementVoidObs(t, a, "2026-07-04", firstBP.ID)

	res, err := a.ExportSeriesCSV(time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err)

	body, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	assert.Equal(t, wantSeriesCSV, string(body))

	assert.Equal(t, "series", res.What)
	assert.Equal(t, "all", res.WindowStart)
	assert.Equal(t, "2026-07-05", res.WindowEnd)

	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Equal(t, "2026-07-05T12:00:00Z\tseries\tall..2026-07-05\t"+res.Path, log[0])
}

// seedPinnedPacketLedger writes the clinical fixture both packet subtests share:
// clinical context, a managed injury, a two-day pain flare, meds (the latest
// naproxen a skip), an intervention, and engine days — every event dated inside
// 2026-07-01..2026-07-05.
func seedPinnedPacketLedger(t *testing.T, a *Adapter) {
	t.Helper()
	require.NoError(t, a.ScaffoldEngine())

	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.Packet.ClinicalContext = []string{"post-op knee, synthetic fixture", "flag anything sedating"}
	require.NoError(t, a.SaveObservationsConfig(cfg))
	_, err = a.UpdateRegistry(observations.RegistryInjury, "injury_a-cedar", observations.RegistryPatch{
		DisplayName: "left knee", At: "2026-06-01T09:00:00Z", Status: observations.StatusManaged,
	})
	require.NoError(t, err)

	appendObs(t, a, observations.KindPain, "2026-07-01", map[string]any{"intensity": 6, "site": "knee", "note": "SECRETPAINNOTE"})
	appendObs(t, a, observations.KindPain, "2026-07-02", map[string]any{"intensity": 5, "site": "knee"})
	appendObs(t, a, observations.KindPain, "2026-07-04", map[string]any{"intensity": 2})
	appendObs(t, a, observations.KindMed, "2026-07-01", map[string]any{"what": "ibuprofen", "dose": "400", "taken": true})
	appendObs(t, a, observations.KindMed, "2026-07-03", map[string]any{"what": "ibuprofen", "dose": "400", "taken": true})
	appendObs(t, a, observations.KindMed, "2026-07-04", map[string]any{"what": "naproxen", "taken": false})
	appendObs(t, a, observations.KindIntervention, "2026-07-02", map[string]any{"what": "physio", "body_site": "left-knee"})
	seedEngineDay(t, a, "2026-07-01", 4, engine.ModeYellow)
	seedEngineDay(t, a, "2026-07-02", 3, engine.ModeYellow)
	seedEngineDay(t, a, "2026-07-03", 5, engine.ModeGreen)
}

// wantPacketBody is the packet body after the title line for the shared
// fixture: the header (context verbatim, injury, regimen, episodes) then the
// per-day timeline. No measurement section appears when no reading is in range.
const wantPacketBody = "\n" +
	"post-op knee, synthetic fixture\n" +
	"flag anything sedating\n" +
	"\n" +
	"Active injuries: left knee (managed)\n" +
	"Current regimen:\n" +
	"  ibuprofen 400\n" +
	"  naproxen (last logged: skipped 2026-07-04)\n" +
	"Pain episodes in range: 1\n" +
	"  2026-07-01..2026-07-02 (2 days)\n" +
	"\n" +
	"Daily record:\n" +
	"  2026-07-01  capacity 4, mode yellow  pain 6 (knee)  [med ibuprofen 400]\n" +
	"  2026-07-02  capacity 3, mode yellow  pain 5 (knee)  [intervention physio left-knee]\n" +
	"  2026-07-03  capacity 5, mode green  [med ibuprofen 400]\n" +
	"  2026-07-04  pain 2  [med naproxen (skipped)]\n"

const (
	wantPacketAll      = "Clinician packet — all to 2026-07-05\n" + wantPacketBody
	wantPacketOverride = "Clinician packet — 2026-07-01 to 2026-07-05\n" + wantPacketBody
)

func TestExportClinicianPacket_UnchangedWithoutInWindowMeasurements(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

	t.Run("no_measurements", func(t *testing.T) {
		a := newObsStore(t)
		seedPinnedPacketLedger(t, a)

		res, err := a.ExportClinicianPacket(now, time.UTC, "", true)
		require.NoError(t, err)
		body, err := os.ReadFile(res.Path)
		require.NoError(t, err)
		assert.Equal(t, wantPacketAll, string(body))
	})

	t.Run("measurements_outside_window", func(t *testing.T) {
		a := newObsStore(t)
		seedPinnedPacketLedger(t, a)
		appendMeasurementObs(t, a, "2026-06-10", "ldl-c", "104", "mg/dL")
		weight := appendMeasurementObs(t, a, "2026-06-15", "weight", "70.5", "kg")
		appendMeasurementVoidObs(t, a, "2026-06-20", weight.ID)

		res, err := a.ExportClinicianPacket(now, time.UTC, "2026-07-01", false)
		require.NoError(t, err)
		body, err := os.ReadFile(res.Path)
		require.NoError(t, err)
		assert.Equal(t, wantPacketOverride, string(body))
	})
}
