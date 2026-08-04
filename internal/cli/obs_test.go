package cli

import (
	"encoding/json"
	"fmt"
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
	cfg.KindsEnabled = []observations.Kind{
		observations.KindPain, observations.KindElimination, observations.KindMood,
		observations.KindIntake, observations.KindSleep, observations.KindLocation,
	}
	require.NoError(t, a.SaveObservationsConfig(cfg))
	return home
}

// readObsEvents walks the observations tree under home and returns every
// appended event, so a CLI capture test can assert what actually landed on
// disk (including payload.provenance).
func readObsEvents(t *testing.T, home string) []observations.Event {
	t.Helper()
	var events []observations.Event
	err := filepath.WalkDir(filepath.Join(home, "observations"), func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			ev, uerr := observations.UnmarshalEventLine([]byte(line))
			if uerr != nil {
				return uerr
			}
			events = append(events, ev)
		}
		return nil
	})
	require.NoError(t, err)
	return events
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

// TestObs_CLI_SourceFlagStampsProvenance: --source/--agent/--model/--channel
// flags land in payload.provenance with the harness normalized (AC-9).
func TestObs_CLI_SourceFlagStampsProvenance(t *testing.T) {
	home := enableAllObsKinds(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"obs", "--source", "Discord", "--agent", "agent-x",
		"--model", "model-y", "--channel", "<channel>", "pain", "6", "knee")
	require.NoError(t, err)

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	prov, ok := events[0].Payload["provenance"].(map[string]any)
	require.True(t, ok, "payload.provenance is stamped from the flags")
	assert.Equal(t, "discord", prov["harness"], "harness normalized through the shared grammar")
	assert.Equal(t, "agent-x", prov["agent"])
	assert.Equal(t, "model-y", prov["model"])
	assert.Equal(t, "<channel>", prov["channel"])
}

// TestObs_CLI_EnvFallbackStampsProvenance: LUCID_* env fills provenance when no
// flag is set (flag > env > default) (AC-9).
func TestObs_CLI_EnvFallbackStampsProvenance(t *testing.T) {
	home := enableAllObsKinds(t)
	t.Setenv("LUCID_SOURCE", "discord")
	t.Setenv("LUCID_AGENT", "agent-x")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "pain", "6", "knee")
	require.NoError(t, err)

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	prov, ok := events[0].Payload["provenance"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "discord", prov["harness"])
	assert.Equal(t, "agent-x", prov["agent"])
}

// TestObs_CLI_FlagBeatsEnvForProvenance: an explicit flag overrides the env
// fallback (AC-9 precedence).
func TestObs_CLI_FlagBeatsEnvForProvenance(t *testing.T) {
	home := enableAllObsKinds(t)
	t.Setenv("LUCID_SOURCE", "env-source")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "--source", "discord", "pain", "6")
	require.NoError(t, err)

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	prov, ok := events[0].Payload["provenance"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "discord", prov["harness"])
}

// TestObs_CLI_BareCaptureOmitsProvenance: a bare `lucid obs` writes no
// provenance key, so the event is byte-identical to the pre-change shape (AC-9).
func TestObs_CLI_BareCaptureOmitsProvenance(t *testing.T) {
	home := enableAllObsKinds(t)
	// Ensure no ambient LUCID_* provenance leaks into the bare path.
	for _, env := range []string{"LUCID_SOURCE", "LUCID_HARNESS", "LUCID_AGENT", "LUCID_MODEL", "LUCID_CHANNEL", "LUCID_THREAD"} {
		t.Setenv(env, "")
	}

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "pain", "6", "knee")
	require.NoError(t, err)

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	_, has := events[0].Payload["provenance"]
	assert.False(t, has, "a bare CLI obs writes no provenance key")
}

// TestObs_CLI_MalformedSourceRejected: a malformed source token errors and
// leaves nothing on disk — never silently coerced (AC-8/AC-9).
func TestObs_CLI_MalformedSourceRejected(t *testing.T) {
	home := enableAllObsKinds(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "--source", "bad token!", "pain", "6", "knee")
	require.Error(t, err)
	assert.Empty(t, readObsEvents(t, home), "a malformed source leaves nothing on disk")
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

// TestObs_CLI_DayFlagBeatsInlineToken: obs carries both backdating tiers, and
// the deliberate flag wins over the token parsed out of the prose — with the
// consumed token still stripped from the captured text.
func TestObs_CLI_DayFlagBeatsInlineToken(t *testing.T) {
	home := enableAllObsKinds(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"obs", "--day", "2026-06-01", "ate", "eggs,", "toast", "@yesterday")
	require.NoError(t, err)

	events := readObsEvents(t, home)
	require.Len(t, events, 1)
	assert.Equal(t, "2026-06-01", events[0].LogicalDate)
	assert.NotContains(t, fmt.Sprint(events[0].Payload), "@yesterday")
}

// TestObs_CLI_DayFlagStrictRejects: the flag is strict, so a typo is a clean
// refusal that names the accepted forms and writes nothing — where the same
// token inside the prose would have been kept as text and captured. The reason
// has to reach stderr, not just the returned error: Execute renders none, so a
// refusal nobody prints is a bare non-zero exit the user has to guess at.
func TestObs_CLI_DayFlagStrictRejects(t *testing.T) {
	home := enableAllObsKinds(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "--day", "@yesterdya", "pain", "6", "knee")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
	assert.Contains(t, err.Error(), "nothing was saved")
	assert.Empty(t, out)
	assert.Contains(t, errOut, "could not read the day", "the reason reaches the user, not just the exit code")
	assert.Contains(t, errOut, "nothing was saved")
	assert.Empty(t, readObsEvents(t, home), "a refused day writes nothing")
}

// TestObs_CLI_DayFlagFutureSurfacesReason names the refused day on stderr, so a
// mistyped year is legible rather than a silent non-zero exit.
func TestObs_CLI_DayFlagFutureSurfacesReason(t *testing.T) {
	home := enableAllObsKinds(t)

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "--day", "2030-01-01", "pain", "6", "knee")
	require.Error(t, err)
	assert.Contains(t, errOut, "2030-01-01")
	assert.Contains(t, errOut, "has not happened yet")
	assert.Empty(t, readObsEvents(t, home), "a refused day writes nothing")
}
