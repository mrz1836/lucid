package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// measurementsNow is the export clock every measurement export test shares, so
// the window end and the disclosure-log stamp are fixed.
func measurementsNow() time.Time { return time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC) }

// seedMeasurementLedger writes a synthetic spread of readings: three on one
// day (out of sort order), a lab value, a percent unit, and one non-numeric
// reading that must never become a row.
func seedMeasurementLedger(t *testing.T, a *Adapter) {
	t.Helper()
	appendMeasurementObs(t, a, "2026-07-02", "weight", "70.5", "kg")
	appendMeasurementObs(t, a, "2026-07-02", "bp-systolic", "121", "mmHg")
	appendMeasurementObs(t, a, "2026-07-02", "bp-diastolic", "79", "mmHg")
	appendMeasurementObs(t, a, "2026-07-03", "ldl-c", "104", "mg/dL")
	appendMeasurementObs(t, a, "2026-07-03", "weight", "heavy", "")
	appendMeasurementObs(t, a, "2026-07-04", "a1c", "5.6", "%")
}

// wantMeasurementsCSV is the projection of seedMeasurementLedger: sorted by
// date, then metric, then id, the non-numeric reading skipped.
const wantMeasurementsCSV = "logical_date,metric,value,unit,id\n" +
	"2026-07-02,bp-diastolic,79,mmHg,obs_2026_07_02_003\n" +
	"2026-07-02,bp-systolic,121,mmHg,obs_2026_07_02_002\n" +
	"2026-07-02,weight,70.5,kg,obs_2026_07_02_001\n" +
	"2026-07-03,ldl-c,104,mg/dL,obs_2026_07_03_001\n" +
	"2026-07-04,a1c,5.6,%,obs_2026_07_04_001\n"

// readMeasurementsFile returns the projection's bytes as a string.
func readMeasurementsFile(t *testing.T, res ExportResult) string {
	t.Helper()
	body, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	return string(body)
}

func TestExportMeasurementsCSV_WritesFixedFileAndLog(t *testing.T) {
	a := newObsStore(t)
	seedMeasurementLedger(t, a)

	res, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(a.projectionsDir(), "measurements.csv"), res.Path)
	assert.Equal(t, wantMeasurementsCSV, readMeasurementsFile(t, res))
	assert.Equal(t, "measurements", res.What)
	assert.Equal(t, "all", res.WindowStart)
	assert.Equal(t, "2026-07-05", res.WindowEnd)

	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Equal(t, "2026-07-05T12:00:00Z\tmeasurements\tall..2026-07-05\t"+res.Path, log[0])
}

func TestExportMeasurementsCSV_NilLocationIsUTC(t *testing.T) {
	a := newObsStore(t)
	seedMeasurementLedger(t, a)

	res, err := a.ExportMeasurementsCSV(measurementsNow(), nil, "", "")
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", res.WindowEnd)
	assert.Equal(t, wantMeasurementsCSV, readMeasurementsFile(t, res))
}

func TestExportMeasurementsCSV_MetricFilterLogsSlug(t *testing.T) {
	a := newObsStore(t)
	seedMeasurementLedger(t, a)
	appendMeasurementObs(t, a, "2026-07-04", "weight", "70.3", "kg")

	res, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "weight")
	require.NoError(t, err)

	assert.Equal(t, "logical_date,metric,value,unit,id\n"+
		"2026-07-02,weight,70.5,kg,obs_2026_07_02_001\n"+
		"2026-07-04,weight,70.3,kg,obs_2026_07_04_002\n", readMeasurementsFile(t, res))
	assert.Equal(t, "measurements:weight", res.What)
	assert.Equal(t, "all", res.WindowStart)

	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Equal(t, "2026-07-05T12:00:00Z\tmeasurements:weight\tall..2026-07-05\t"+res.Path, log[0])

	// The filter is an exact, case-sensitive match: a different case or an
	// unknown slug is a header-only file, not an error.
	for _, slug := range []string{"Weight", "no-such-metric"} {
		res, err = a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", slug)
		require.NoError(t, err, slug)
		assert.Equal(t, "logical_date,metric,value,unit,id\n", readMeasurementsFile(t, res), slug)
		assert.Equal(t, "measurements:"+slug, res.What)
	}
}

func TestExportMeasurementsCSV_WindowOverrideAndAll(t *testing.T) {
	a := newObsStore(t)
	appendMeasurementObs(t, a, "2026-07-01", "weight", "70.3", "kg")
	appendMeasurementObs(t, a, "2026-07-02", "weight", "70.5", "kg")
	appendMeasurementObs(t, a, "2026-07-05", "resting-hr", "58", "bpm")
	appendMeasurementObs(t, a, "2026-07-06", "weight", "165.2", "lb") // after today

	t.Run("at_start", func(t *testing.T) {
		res, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "2026-07-02", "")
		require.NoError(t, err)
		assert.Equal(t, "logical_date,metric,value,unit,id\n"+
			"2026-07-02,weight,70.5,kg,obs_2026_07_02_001\n"+
			"2026-07-05,resting-hr,58,bpm,obs_2026_07_05_001\n", readMeasurementsFile(t, res))
		assert.Equal(t, "2026-07-02", res.WindowStart)
		assert.Equal(t, "2026-07-05", res.WindowEnd)
	})

	t.Run("all", func(t *testing.T) {
		res, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
		require.NoError(t, err)
		assert.Equal(t, "logical_date,metric,value,unit,id\n"+
			"2026-07-01,weight,70.3,kg,obs_2026_07_01_001\n"+
			"2026-07-02,weight,70.5,kg,obs_2026_07_02_001\n"+
			"2026-07-05,resting-hr,58,bpm,obs_2026_07_05_001\n", readMeasurementsFile(t, res),
			"a reading dated after today is outside every window")
		assert.Equal(t, "all", res.WindowStart)
	})

	t.Run("future_start", func(t *testing.T) {
		res, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "2026-07-06", "")
		require.NoError(t, err)
		assert.Equal(t, "logical_date,metric,value,unit,id\n", readMeasurementsFile(t, res))
	})

	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	require.Len(t, log, 3)
	path := filepath.Join(a.projectionsDir(), "measurements.csv")
	assert.Equal(t, "2026-07-05T12:00:00Z\tmeasurements\t2026-07-02..2026-07-05\t"+path, log[0])
	assert.Equal(t, "2026-07-05T12:00:00Z\tmeasurements\tall..2026-07-05\t"+path, log[1])
	assert.Equal(t, "2026-07-05T12:00:00Z\tmeasurements\t2026-07-06..2026-07-05\t"+path, log[2])
}

func TestExportMeasurementsCSV_EmptyLedgerHeaderOnly(t *testing.T) {
	a := newObsStore(t)

	res, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
	require.NoError(t, err)
	assert.Equal(t, "logical_date,metric,value,unit,id\n", readMeasurementsFile(t, res))

	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Equal(t, "2026-07-05T12:00:00Z\tmeasurements\tall..2026-07-05\t"+res.Path, log[0])
}

func TestExportMeasurementsCSV_RerunByteStable(t *testing.T) {
	a := newObsStore(t)
	seedMeasurementLedger(t, a)

	first, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
	require.NoError(t, err)
	firstBody := readMeasurementsFile(t, first)

	later := measurementsNow().Add(time.Hour)
	second, err := a.ExportMeasurementsCSV(later, time.UTC, "", "")
	require.NoError(t, err)

	assert.Equal(t, first.Path, second.Path, "the projection has one fixed name")
	assert.Equal(t, firstBody, readMeasurementsFile(t, second), "a rerun writes the same bytes")
	assert.Equal(t, wantMeasurementsCSV, firstBody)

	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	require.Len(t, log, 2, "each run appends its own disclosure line")
	assert.Equal(t, "2026-07-05T13:00:00Z\tmeasurements\tall..2026-07-05\t"+second.Path, log[1])
}

func TestExportMeasurementsCSV_ReadsWhileKindDisabled(t *testing.T) {
	a := newObsStore(t)
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = []observations.Kind{observations.KindPain}
	require.NoError(t, a.SaveObservationsConfig(cfg))
	require.False(t, cfg.KindEnabled(observations.KindMeasurement))

	seedMeasurementLedger(t, a)

	res, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
	require.NoError(t, err)
	assert.Equal(t, wantMeasurementsCSV, readMeasurementsFile(t, res),
		"enabling gates capture, not projection")
}

func TestExportMeasurementsCSV_SameDayVoidKeepsSurvivor(t *testing.T) {
	a := newObsStore(t)
	first := appendMeasurementObs(t, a, "2026-07-02", "bp-systolic", "121", "mmHg")
	appendMeasurementObs(t, a, "2026-07-02", "bp-systolic", "119", "mmHg")
	appendMeasurementVoidObs(t, a, "2026-07-04", first.ID)

	res, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
	require.NoError(t, err)
	assert.Equal(t, "logical_date,metric,value,unit,id\n"+
		"2026-07-02,bp-systolic,119,mmHg,obs_2026_07_02_002\n", readMeasurementsFile(t, res))
}

func TestExportMeasurementsCSV_NotesAndTagsNeverExported(t *testing.T) {
	a := newObsStore(t)
	_, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindMeasurement,
		RecordedAt: "2026-07-02T12:00:00Z", OccurredAt: "2026-07-02T12:00:00Z",
		OccurredAtPrecision: observations.PrecisionExact, LogicalDate: "2026-07-02",
		Source: observations.SourceMicrolog,
		Payload: map[string]any{
			"metric": "weight", "value": "70.5", "unit": "kg", "note": "SENTINEL-MEAS-NOTE",
		},
		Tags: []string{"sentinel-meas-tag"},
	})
	require.NoError(t, err)

	res, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
	require.NoError(t, err)
	body := readMeasurementsFile(t, res)
	assert.Equal(t, "logical_date,metric,value,unit,id\n"+
		"2026-07-02,weight,70.5,kg,obs_2026_07_02_001\n", body)
	assert.NotContains(t, body, "SENTINEL-MEAS-NOTE")
	assert.NotContains(t, body, "sentinel-meas-tag")
}

func TestExportMeasurementsCSV_WriteError(t *testing.T) {
	a := newObsStore(t)
	// A directory sitting where measurements.csv should be forces the write to
	// fail, and no disclosure line is appended for a projection never written.
	mkdirAt(t, filepath.Join(a.projectionsDir(), measurementsFileName))
	_, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
	require.Error(t, err)

	log, err := a.ReadExportsLog()
	require.NoError(t, err)
	assert.Empty(t, log)
}

func TestExportMeasurementsCSV_ExportsLogWriteError(t *testing.T) {
	a := newObsStore(t)
	mkdirAt(t, a.exportsLogPath())
	_, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
	require.Error(t, err)
}

func TestExportMeasurementsCSV_ReadError(t *testing.T) {
	skipIfRoot(t)
	a := newObsStore(t)
	sub := filepath.Join(a.observationsDir(), "2026")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

	_, err := a.ExportMeasurementsCSV(measurementsNow(), time.UTC, "", "")
	require.Error(t, err, "an unreadable Ledger is surfaced, never exported as empty")
	_, statErr := os.Stat(filepath.Join(a.projectionsDir(), measurementsFileName))
	assert.True(t, os.IsNotExist(statErr), "no projection is written")
}
