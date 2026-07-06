package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// enableAllObsKinds points LUCID_HOME at an isolated home, scaffolds it, and
// enables every capturable kind so the CLI capture tests exercise the full
// surface. It returns the home path.
func enableAllObsKinds(t *testing.T) string {
	t.Helper()
	home := isolatedHome(t)
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = []string{
		observations.KindPain, observations.KindElimination, observations.KindMood,
		observations.KindIntake, observations.KindSleep, observations.KindLocation,
	}
	require.NoError(t, a.SaveObservationsConfig(cfg))
	return home
}

func TestObs_CLI_CapturesAndAcks(t *testing.T) {
	home := enableAllObsKinds(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "pain", "6", "knee")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged pain as `obs_")

	// One JSONL file was written under observations/.
	var count int
	err = filepath.WalkDir(filepath.Join(home, "observations"), func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			count++
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestObs_CLI_WhereCreatesPlaceRegistry(t *testing.T) {
	home := enableAllObsKinds(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "where", "Lisbon")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged context.location as `obs_")

	entries, err := os.ReadDir(filepath.Join(home, "registries", "places"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, strings.HasPrefix(entries[0].Name(), "place_"))
}

func TestObs_CLI_DisabledKindPrintsHint(t *testing.T) {
	// Default config: sleep is not enabled.
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "slept", "q3")
	require.NoError(t, err)
	assert.Contains(t, out, "isn't enabled")
	assert.Contains(t, out, "observations/config.json")
}

func TestObs_CLI_UnknownKindErrors(t *testing.T) {
	enableAllObsKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "nonsense", "x")
	require.Error(t, err, "an unknown observation kind is a runtime error")
}

func TestDay_CLI_HumanAndJSON(t *testing.T) {
	enableAllObsKinds(t)

	// Capture something today, then read the day view.
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "pain", "6", "knee")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "day")
	require.NoError(t, err)
	assert.Contains(t, out, "Observations:")
	assert.Contains(t, out, "pain")

	// --json emits the assembled view.
	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "day", "--json")
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &payload))
	assert.Contains(t, payload, "Date")
}

func TestDay_CLI_EmptyDay(t *testing.T) {
	enableAllObsKinds(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "day", "2026-06-01")
	require.NoError(t, err)
	assert.Contains(t, out, "No record for 2026-06-01.")
}
