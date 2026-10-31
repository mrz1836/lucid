package cli

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

func TestExport_Series_CLI(t *testing.T) {
	enableAllObsKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "pain", "6", "knee")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "series")
	require.NoError(t, err)
	path := strings.TrimSpace(out)
	assert.True(t, strings.HasSuffix(path, "series.csv"), "the path is posted: %q", path)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "logical_date,pain,mood,capacity")
}

func TestExport_PacketClinician_CLI(t *testing.T) {
	enableAllObsKinds(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "packet", "clinician")
	require.NoError(t, err)
	path := strings.TrimSpace(out)
	assert.Contains(t, path, "packet_clinician_")

	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "the packet file exists at the posted path")
}

func TestExport_PacketClinician_JSON(t *testing.T) {
	enableAllObsKinds(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "packet", "clinician", "--json")
	require.NoError(t, err)

	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, "export", payload["command"])
	assert.Equal(t, "clinician", payload["what"])
	assert.Contains(t, payload["path"], "packet_clinician_")
	assert.NotEmpty(t, payload["window_end"])
}

func TestExport_UsageErrors(t *testing.T) {
	enableAllObsKinds(t)
	cases := [][]string{
		{"export"},
		{"export", "bogus"},
		{"export", "packet"},
		{"export", "packet", "bogus"},
		{"export", "packet", "clinician", "@bad"}, // a malformed window override propagates
	}
	for _, args := range cases {
		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
		assert.Errorf(t, err, "args %v should error", args)
	}
}

// TestExport_Series_AckAndJSONUnchanged pins the series export's two stdout
// contracts: the human ack is the path alone, and --json carries exactly the
// five metadata keys. Both must stay byte-identical as new export forms land.
func TestExport_Series_AckAndJSONUnchanged(t *testing.T) {
	home := enableAllObsKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "pain", "6", "knee")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "series")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "projections", "series.csv")+"\n", out)

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "export", "series", "--json")
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	keys := slices.Sorted(maps.Keys(payload))
	assert.Equal(t, []string{"command", "path", "what", "window_end", "window_start"}, keys)
	assert.Equal(t, "series", payload["what"])
	assert.Equal(t, "all", payload["window_start"])
}

// enableMeasurementCapture is enableAllObsKinds plus the off-by-default
// measurement kind, so a test can capture readings through the CLI before it
// exports them. It returns the isolated home.
func enableMeasurementCapture(t *testing.T) string {
	t.Helper()
	home := enableAllObsKinds(t)
	a := storage.New(home)
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = append(cfg.KindsEnabled, observations.KindMeasurement)
	require.NoError(t, a.SaveObservationsConfig(cfg))
	return home
}

// captureMeasurement logs one reading through `lucid obs measurement`.
func captureMeasurement(t *testing.T, tokens ...string) {
	t.Helper()
	args := append([]string{"obs", "measurement"}, tokens...)
	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoError(t, err, stderr)
}

func TestExport_Measurements_PrintsOnlyPath(t *testing.T) {
	home := enableMeasurementCapture(t)
	captureMeasurement(t, "weight", "70.5", "kg")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "measurements")
	require.NoError(t, err)
	path := filepath.Join(home, "projections", "measurements.csv")
	assert.Equal(t, path+"\n", out, "only the path is posted")

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "logical_date,metric,value,unit,id", lines[0])
	assert.Contains(t, lines[1], ",weight,70.5,kg,obs_")
}

// TestExport_Measurements_JSONMetadataOnly proves --json carries only the
// export metadata — the five keys, plus metric when filtered — and never a
// reading: the sentinel value stays in the file.
func TestExport_Measurements_JSONMetadataOnly(t *testing.T) {
	enableMeasurementCapture(t)
	captureMeasurement(t, "weight", "987.65", "kg")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "measurements", "--json")
	require.NoError(t, err)
	assert.NotContains(t, out, "987.65")
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, []string{"command", "path", "what", "window_end", "window_start"}, slices.Sorted(maps.Keys(payload)))
	assert.Equal(t, "export", payload["command"])
	assert.Equal(t, "measurements", payload["what"])
	assert.Equal(t, "all", payload["window_start"])
	assert.Equal(t, "measurements.csv", filepath.Base(payload["path"].(string)))

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "export", "measurements", "@2026-03-01", "--metric", "weight", "--json")
	require.NoError(t, err)
	assert.NotContains(t, out, "987.65")
	payload = map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, []string{"command", "metric", "path", "what", "window_end", "window_start"}, slices.Sorted(maps.Keys(payload)))
	assert.Equal(t, "weight", payload["metric"])
	assert.Equal(t, "measurements:weight", payload["what"])
	assert.Equal(t, "2026-03-01", payload["window_start"])

	body, err := os.ReadFile(payload["path"].(string))
	require.NoError(t, err)
	assert.Contains(t, string(body), ",weight,987.65,kg,obs_", "the reading lives in the file, not on stdout")
}

// TestExport_Measurements_UsageErrors covers error-states.md Mx-1..Mx-4: each
// is a usage error (exit 2) whose fixed reason lands on stderr, and nothing is
// written — no projection and no disclosure line.
func TestExport_Measurements_UsageErrors(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		reason string
	}{
		{"mx1_bad_date", []string{"export", "measurements", "@bad"}, `measurements window must be @YYYY-MM-DD or all, got "@bad"`},
		{"mx1_unknown_word", []string{"export", "measurements", "bogus"}, `measurements window must be @YYYY-MM-DD or all, got "bogus"`},
		{"mx2_two_windows", []string{"export", "measurements", "all", "@2026-07-01"}, "lucid export measurements takes at most one window argument"},
		{"mx3_empty_metric", []string{"export", "measurements", "--metric", ""}, "--metric needs one slug with no spaces"},
		{"mx3_spaced_metric", []string{"export", "measurements", "--metric", "a b"}, "--metric needs one slug with no spaces"},
		{"mx3_tab_metric", []string{"export", "measurements", "--metric", "a\tb"}, "--metric needs one slug with no spaces"},
		{"mx4_series", []string{"export", "series", "--metric", "weight"}, "--metric applies only to `lucid export measurements`"},
		{"mx4_packet", []string{"export", "packet", "clinician", "--metric", "weight"}, "--metric applies only to `lucid export measurements`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := enableMeasurementCapture(t)
			captureMeasurement(t, "weight", "70.5", "kg")

			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, tc.args...)
			require.Error(t, err)
			assert.Equal(t, ExitUsage, exitCodeForError(err))
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "lucid export: "+tc.reason+"\n")
			assert.Contains(t, stderr, "Run 'lucid export --help' for usage.")

			projections := filepath.Join(home, "projections")
			for _, name := range []string{"measurements.csv", "series.csv", "exports.log"} {
				_, statErr := os.Stat(filepath.Join(projections, name))
				assert.Truef(t, os.IsNotExist(statErr), "%s must not be written", name)
			}
			matches, globErr := filepath.Glob(filepath.Join(projections, "packet_clinician_*"))
			require.NoError(t, globErr)
			assert.Empty(t, matches, "no packet is written")
		})
	}
}
