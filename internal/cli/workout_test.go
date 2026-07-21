package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// enableWorkoutKinds points LUCID_HOME at an isolated home, scaffolds it, and
// enables the workout + body_state kinds so the workout-log CLI tests exercise
// the full capture surface. It returns the home path.
func enableWorkoutKinds(t *testing.T) string {
	t.Helper()
	home := isolatedHome(t)
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = append(cfg.KindsEnabled, observations.KindWorkout, observations.KindBodyState)
	require.NoError(t, a.SaveObservationsConfig(cfg))
	return home
}

// withScriptedProvider injects a buildProvider seam returning a fake that
// replays script, so the spoken workout-log path runs fully offline. It returns
// the fake for call-count assertions.
func withScriptedProvider(t *testing.T, script ...provider.Exchange) *provider.Fake {
	t.Helper()
	fake := &provider.Fake{Script: script}
	prev := buildProvider
	buildProvider = func(config.ProviderConfig) (provider.Provider, error) { return fake, nil }
	t.Cleanup(func() { buildProvider = prev })
	return fake
}

// eventsOfKind filters read-back events to one kind.
func eventsOfKind(events []observations.Event, kind observations.Kind) []observations.Event {
	var out []observations.Event
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

const workoutCLIReply = `{
  "type": "pull",
  "duration_min": 50,
  "rpe": 6,
  "body_parts": ["back"],
  "soreness": [],
  "pain_flags": [],
  "notes": "shoulder felt fine"
}`

// TestWorkout_CommandRegistered proves the verb is wired into the root spine
// (the AC-6 checkpoint: grep newWorkoutCmd in root.go).
func TestWorkout_CommandRegistered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	cmd, _, err := root.Find([]string{"workout", "log"})
	require.NoError(t, err)
	assert.Equal(t, "log", cmd.Name())
}

// TestWorkout_Log_StructuredCLI: the structured flags write one workout event
// and one body_state event per reading, and the ack is inventory-only.
func TestWorkout_Log_StructuredCLI(t *testing.T) {
	home := enableWorkoutKinds(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log",
		"--type", "push", "--duration", "45", "--rpe", "7",
		"--parts", "chest,shoulders", "--soreness", "shoulder:4", "--pain", "knee:7", "--notes", "felt strong")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged workout as `")

	events := readObsEvents(t, home)
	workouts := eventsOfKind(events, observations.KindWorkout)
	require.Len(t, workouts, 1)
	assert.Equal(t, "push", workouts[0].Payload["type"])
	assert.EqualValues(t, 7, workouts[0].Payload["rpe"])

	states := eventsOfKind(events, observations.KindBodyState)
	require.Len(t, states, 2)
}

// TestWorkout_Log_SpokenCLI: a positional drop is extracted by the injected fake
// provider and captured as a workout event.
func TestWorkout_Log_SpokenCLI(t *testing.T) {
	home := enableWorkoutKinds(t)
	fake := withScriptedProvider(t, provider.Exchange{Content: workoutCLIReply})
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "did pull, shoulder felt fine, 50 min")
	require.NoError(t, err)
	assert.Equal(t, 1, fake.Calls())
	assert.Contains(t, out, "Logged workout as `")

	workouts := eventsOfKind(readObsEvents(t, home), observations.KindWorkout)
	require.Len(t, workouts, 1)
	assert.Equal(t, "pull", workouts[0].Payload["type"])
}

// TestWorkout_Log_TextFlagSpoken: the --text flag drives the same spoken path as
// a positional drop.
func TestWorkout_Log_TextFlagSpoken(t *testing.T) {
	home := enableWorkoutKinds(t)
	fake := withScriptedProvider(t, provider.Exchange{Content: workoutCLIReply})
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--text", "did pull today")
	require.NoError(t, err)
	assert.Equal(t, 1, fake.Calls())
	assert.Len(t, eventsOfKind(readObsEvents(t, home), observations.KindWorkout), 1)
}

// TestWorkout_Log_MixedFormsRejected: a spoken drop combined with structured
// flags is rejected rather than silently dropping one form.
func TestWorkout_Log_MixedFormsRejected(t *testing.T) {
	enableWorkoutKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--type", "push", "some", "spoken", "text")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

// TestWorkout_Log_RPEOutOfRangeRejected: an out-of-range rpe is a usage error,
// never clamped.
func TestWorkout_Log_RPEOutOfRangeRejected(t *testing.T) {
	enableWorkoutKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--type", "push", "--rpe", "12")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--rpe must be 0-10")
}

// TestWorkout_Log_SorenessNeedsLevel: --soreness requires a part:level pair.
func TestWorkout_Log_SorenessNeedsLevel(t *testing.T) {
	enableWorkoutKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--type", "push", "--soreness", "shoulder")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a 0-10 level")
}

// TestWorkout_Log_BarePainFlag: --pain accepts a bare part, recorded at
// PainFlagLevel so the recommender can protect it.
func TestWorkout_Log_BarePainFlag(t *testing.T) {
	home := enableWorkoutKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--type", "legs", "--pain", "knee")
	require.NoError(t, err)
	states := eventsOfKind(readObsEvents(t, home), observations.KindBodyState)
	require.Len(t, states, 1)
	assert.Equal(t, "knee", states[0].Payload["body_part"])
	assert.EqualValues(t, router.PainFlagLevel, states[0].Payload["pain"])
}

// TestWorkout_Log_DisabledKindRejectedCLI: on a fresh Ledger (workout kind not
// enabled) the CLI surfaces the enable hint and writes nothing.
func TestWorkout_Log_DisabledKindRejectedCLI(t *testing.T) {
	home := isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--type", "push")
	require.NoError(t, err)
	assert.Contains(t, out, "isn't enabled")
	assert.Empty(t, eventsOfKind(readObsEvents(t, home), observations.KindWorkout))
}
