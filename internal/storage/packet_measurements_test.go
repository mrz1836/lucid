package storage

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// packetNow is the export clock the packet measurement tests share; the window
// override below starts 2026-07-01, so the packet covers 2026-07-01..2026-07-05.
func packetNow() time.Time { return time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC) }

// readPacket exports the clinician packet with an @2026-07-01 start and returns
// its body.
func readPacket(t *testing.T, a *Adapter) string {
	t.Helper()
	res, err := a.ExportClinicianPacket(packetNow(), time.UTC, "2026-07-01", false)
	require.NoError(t, err)
	body, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	return string(body)
}

// measurementsSection returns the packet's "Measurements in range" section,
// from its heading up to the daily record, or "" when the packet has none.
func measurementsSection(t *testing.T, packet string) string {
	t.Helper()
	_, rest, found := strings.Cut(packet, "\nMeasurements in range:\n")
	if !found {
		return ""
	}
	section, _, found := strings.Cut(rest, "\nDaily record:\n")
	require.True(t, found, "the section sits before the daily record")
	return section
}

func TestExportClinicianPacket_MeasurementsInRangeSection(t *testing.T) {
	a := newObsStore(t)
	seedPinnedPacketLedger(t, a)
	appendMeasurementObs(t, a, "2026-06-20", "ldl-c", "104", "mg/dL") // before the start
	appendMeasurementObs(t, a, "2026-07-02", "weight", "70.5", "kg")
	appendMeasurementObs(t, a, "2026-07-02", "bp-systolic", "121", "mmHg")
	appendMeasurementObs(t, a, "2026-07-03", "a1c", "5.6", "%")
	appendMeasurementObs(t, a, "2026-07-03", "weight", "heavy", "")  // non-numeric
	appendMeasurementObs(t, a, "2026-07-04", "resting-hr", "58", "") // no unit
	appendMeasurementObs(t, a, "2026-07-06", "weight", "70.3", "kg") // after today

	packet := readPacket(t, a)

	assert.Equal(t, ""+
		"  2026-07-02  bp-systolic 121 mmHg\n"+
		"  2026-07-02  weight 70.5 kg\n"+
		"  2026-07-03  a1c 5.6 %\n"+
		"  2026-07-04  resting-hr 58\n", measurementsSection(t, packet))

	// The section follows the header and precedes the daily record; the header
	// and the timeline themselves are untouched.
	header, _, _ := strings.Cut(packet, "\nMeasurements in range:\n")
	assert.True(t, strings.HasSuffix(header, "  2026-07-01..2026-07-02 (2 days)\n"), "the section follows the episodes lines")
	assert.Contains(t, packet, "\nDaily record:\n  2026-07-01  capacity 4, mode yellow  pain 6 (knee)  [med ibuprofen 400]\n")
	assert.NotContains(t, packet, "obs_", "packet lines carry no ids")
	assert.NotContains(t, packet, "ldl-c", "a reading before the window is excluded")
	assert.NotContains(t, packet, "70.3", "a reading after today is excluded")
	assert.NotContains(t, packet, "heavy", "a non-numeric reading is skipped")
}

func TestExportClinicianPacket_AllWindowIncludesEveryReading(t *testing.T) {
	a := newObsStore(t)
	seedPinnedPacketLedger(t, a)
	appendMeasurementObs(t, a, "2026-06-20", "ldl-c", "104", "mg/dL")
	appendMeasurementObs(t, a, "2026-07-02", "weight", "70.5", "kg")

	res, err := a.ExportClinicianPacket(packetNow(), time.UTC, "", true)
	require.NoError(t, err)
	body, err := os.ReadFile(res.Path)
	require.NoError(t, err)

	assert.Equal(t, ""+
		"  2026-06-20  ldl-c 104 mg/dL\n"+
		"  2026-07-02  weight 70.5 kg\n", measurementsSection(t, string(body)))
}

func TestExportClinicianPacket_MeasurementNoteNeverLeaks(t *testing.T) {
	a := newObsStore(t)
	seedPinnedPacketLedger(t, a)
	_, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindMeasurement,
		RecordedAt: "2026-07-02T12:00:00Z", OccurredAt: "2026-07-02T12:00:00Z",
		OccurredAtPrecision: observations.PrecisionExact, LogicalDate: "2026-07-02",
		Source: observations.SourceMicrolog,
		Payload: map[string]any{
			"metric": "weight", "value": "70.5", "unit": "kg", "note": "SENTINEL-MEAS-NOTE after the draw",
		},
		Tags: []string{"sentinel-meas-tag"},
	})
	require.NoError(t, err)

	packet := readPacket(t, a)

	assert.Equal(t, "  2026-07-02  weight 70.5 kg\n", measurementsSection(t, packet))
	assert.NotContains(t, packet, "SENTINEL-MEAS-NOTE")
	assert.NotContains(t, packet, "after the draw")
	assert.NotContains(t, packet, "sentinel-meas-tag")
	assert.NotContains(t, packet, "obs_")
}

func TestExportClinicianPacket_VoidOutsideWindowStillDrops(t *testing.T) {
	a := newObsStore(t)
	seedPinnedPacketLedger(t, a)
	early := appendMeasurementObs(t, a, "2026-07-02", "weight", "70.5", "kg")
	late := appendMeasurementObs(t, a, "2026-07-03", "bp-systolic", "121", "mmHg")
	appendMeasurementObs(t, a, "2026-07-03", "bp-systolic", "119", "mmHg")
	// One void filed before the packet window opens and one filed after it
	// closes: the fold runs over the whole history, so both targets drop.
	appendMeasurementVoidObs(t, a, "2026-06-25", early.ID)
	appendMeasurementVoidObs(t, a, "2026-07-06", late.ID)

	packet := readPacket(t, a)

	assert.Equal(t, "  2026-07-03  bp-systolic 119 mmHg\n", measurementsSection(t, packet))
	assert.NotContains(t, packet, "70.5")
	assert.NotContains(t, packet, "bp-systolic 121")
}

func TestExportClinicianPacket_EveryReadingVoidedOmitsSection(t *testing.T) {
	a := newObsStore(t)
	seedPinnedPacketLedger(t, a)
	weight := appendMeasurementObs(t, a, "2026-07-02", "weight", "70.5", "kg")
	appendMeasurementVoidObs(t, a, "2026-07-04", weight.ID)

	assert.Equal(t, wantPacketOverride, readPacket(t, a),
		"with every in-window reading voided the packet is byte-identical to one without measurements")
}

func TestExportClinicianPacket_MeasurementsExportKeepsSinceLastWindow(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldEngine())

	first, err := a.ExportClinicianPacket(packetNow(), time.UTC, "", false)
	require.NoError(t, err)
	require.Equal(t, "2026-07-05", first.WindowEnd)

	// A measurement export between two packets is logged but never moves the
	// packet's since-last-export start.
	_, err = a.ExportMeasurementsCSV(time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC), time.UTC, "", "weight")
	require.NoError(t, err)

	second, err := a.ExportClinicianPacket(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), time.UTC, "", false)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", second.WindowStart, "since the last clinician export")
}
