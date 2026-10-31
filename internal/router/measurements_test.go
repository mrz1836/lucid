package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertMeasurementsRejected asserts err is the typed measurement-export
// refusal carrying exactly reason.
func assertMeasurementsRejected(t *testing.T, err error, reason string) {
	t.Helper()
	var rejected *MeasurementsRejectedError
	require.ErrorAs(t, err, &rejected, "a typed refusal")
	assert.Equal(t, reason, rejected.Reason)
	assert.Equal(t, reason, err.Error())
}

func TestMeasurementsExport_WindowArgGrammar(t *testing.T) {
	r := bootedObs(t)
	capture(t, r, "measurement", "weight", "70.5", "kg")

	for _, arg := range []string{"", "all", "@2026-07-02"} {
		out, err := r.MeasurementsExport(arg, "", false, nowEDT())
		require.NoErrorf(t, err, "arg %q", arg)
		assert.Equal(t, "measurements", out.What)
		assert.Equal(t, "2026-07-02", out.WindowEnd)
		if arg == "@2026-07-02" {
			assert.Equal(t, "2026-07-02", out.WindowStart)
		} else {
			assert.Equal(t, "all", out.WindowStart, "arg %q is all history", arg)
		}
	}

	for _, arg := range []string{"@bad", "bogus"} {
		r := bootedObs(t)
		_, err := r.MeasurementsExport(arg, "", false, nowEDT())
		assertMeasurementsRejected(t, err, `measurements window must be @YYYY-MM-DD or all, got "`+arg+`"`)

		log, lerr := r.Store().ReadExportsLog()
		require.NoError(t, lerr)
		assert.Empty(t, log, "a refused window writes no disclosure line")
	}
}

func TestMeasurementsExport_MetricValidation(t *testing.T) {
	for _, metric := range []string{"", "a b", "a\tb", " weight", "a\x00b"} {
		r := bootedObs(t)
		_, err := r.MeasurementsExport("", metric, true, nowEDT())
		assertMeasurementsRejected(t, err, "--metric needs one slug with no spaces")

		log, lerr := r.Store().ReadExportsLog()
		require.NoError(t, lerr)
		assert.Emptyf(t, log, "metric %q: a refused filter writes no disclosure line", metric)
	}

	r := bootedObs(t)
	out, err := r.MeasurementsExport("", "weight", true, nowEDT())
	require.NoError(t, err)
	assert.Equal(t, "measurements:weight", out.What)
	assert.Equal(t, "weight", out.Metric)

	// An unset flag is no filter, whatever value rides along.
	out, err = r.MeasurementsExport("", "", false, nowEDT())
	require.NoError(t, err)
	assert.Equal(t, "measurements", out.What)
	assert.Empty(t, out.Metric)
}

func TestMeasurementsExport_PostsOnlyPath(t *testing.T) {
	r := bootedObs(t)
	capture(t, r, "measurement", "weight", "70.5", "kg")

	out, err := r.MeasurementsExport("", "", false, nowEDT())
	require.NoError(t, err)
	assert.Equal(t, out.Path, out.Message, "only the path is posted")
	assert.Equal(t, "measurements.csv", filepath.Base(out.Path))
	assert.NotContains(t, out.Message, "70.5")

	body, err := os.ReadFile(out.Path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "logical_date,metric,value,unit,id\n")
	assert.Contains(t, string(body), ",weight,70.5,kg,obs_")
}

func TestMeasurementsExport_StoreError(t *testing.T) {
	r := bootedObs(t)
	out, err := r.MeasurementsExport("", "", false, nowEDT())
	require.NoError(t, err)

	// A directory where the projection belongs makes the next write fail; the
	// failure surfaces as itself, never as a usage refusal.
	require.NoError(t, os.Remove(out.Path))
	require.NoError(t, os.Mkdir(out.Path, 0o700))
	_, err = r.MeasurementsExport("", "", false, nowEDT())
	require.Error(t, err)
	var rejected *MeasurementsRejectedError
	assert.NotErrorAs(t, err, &rejected)
}
