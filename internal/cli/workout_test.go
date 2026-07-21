package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
	"github.com/mrz1836/lucid/internal/workout"
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

// enableWorkoutSurface sets up an isolated home with the workout feature fully
// configured: the workout + body_state kinds enabled, a synthetic program and the
// two opaque prompt files written to disk, and an enabled workout block in
// lucid.json pointing at them — so the on-demand `lucid workout` command composes
// against a real (empty) Ledger.
func enableWorkoutSurface(t *testing.T) {
	t.Helper()
	home := enableWorkoutKinds(t) // isolated home + workout/body_state kinds enabled

	dir := t.TempDir()
	prog := filepath.Join(dir, "program.json")
	b, err := json.Marshal(workout.ExampleProgram())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(prog, b, 0o600))
	sys := filepath.Join(dir, "system_prompt.md")
	tmpl := filepath.Join(dir, "daily_template.md")
	require.NoError(t, os.WriteFile(sys, []byte("SYSTEM VOICE\n"), 0o600))
	require.NoError(t, os.WriteFile(tmpl, []byte("TEMPLATE BODY\n"), 0o600))

	a := storage.New(home)
	cfg, err := a.LoadConfig()
	require.NoError(t, err)
	cfg.Workout = config.WorkoutConfig{
		Enabled: true, Program: prog, SlotTime: "12:00", SystemPrompt: sys, Template: tmpl,
	}
	require.NoError(t, a.SaveConfig(cfg))
}

// TestWorkout_OnDemand_RendersRecommendation proves the bare `lucid workout` verb
// composes today's recommendation: the model phrases a note and the message still
// carries the deterministic header, options, and safety line (AC-7 command, AC-9).
func TestWorkout_OnDemand_RendersRecommendation(t *testing.T) {
	enableWorkoutSurface(t)
	withScriptedProvider(t, provider.Exchange{Content: "Great to see you here today."})

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "workout")
	require.NoError(t, err)
	assert.Contains(t, out, "Great to see you here today.", "the model note is rendered")
	assert.Contains(t, out, "**Workout**", "the deterministic header is present")
	assert.Contains(t, out, "Today's options")
	assert.Contains(t, out, "not medical advice", "the safety line is always present")
	assert.NotContains(t, errOut, "deterministic fallback", "the model path is not a fallback")
}

// TestWorkout_OnDemand_JSON proves --json emits the decided Recommendation/Trend
// projection rather than the rendered message (AC-8).
func TestWorkout_OnDemand_JSON(t *testing.T) {
	enableWorkoutSurface(t)
	withScriptedProvider(t, provider.Exchange{Content: "note"})

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "--json")
	require.NoError(t, err)

	var payload struct {
		Recommendation workout.Recommendation `json:"recommendation"`
		Trend          workout.Trend          `json:"trend"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.NotEmpty(t, payload.Recommendation.Primary.Name, "the decided pick is projected")
	assert.NotEmpty(t, payload.Recommendation.Fallback.Name, "the easier door is projected")
}

// TestWorkout_OnDemand_ProviderDownStillRenders proves the on-demand surface
// renders deterministically when the provider is unreachable: the message stands
// (safety line present) and the fallback is noted on stderr (AC-9 degrade).
func TestWorkout_OnDemand_ProviderDownStillRenders(t *testing.T) {
	enableWorkoutSurface(t)
	withScriptedProvider(t, provider.Exchange{Err: provider.ErrUnavailable})

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "workout")
	require.NoError(t, err)
	assert.Contains(t, out, "**Workout**")
	assert.Contains(t, out, "not medical advice")
	assert.Contains(t, errOut, "deterministic fallback")
}

// TestWorkout_OnDemand_DisabledWarns proves a Ledger without a workout block warns
// on stderr but does not crash (it still errors loudly on the absent program,
// surfacing the misconfiguration rather than composing a synthetic program).
func TestWorkout_OnDemand_DisabledWarns(t *testing.T) {
	isolatedHome(t)
	withScriptedProvider(t, provider.Exchange{Content: "note"})

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "workout")
	require.Error(t, err, "an unconfigured program is a loud error")
	assert.Contains(t, errOut, "workout.enabled is false")
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

// --- workout fire (on-demand daily-slot verb) -------------------------------

// TestWorkoutFire_CommandRegistered proves the `fire` child is wired under
// `workout` and carries the deliver/dry-run flags (AC-7 slot, on-demand).
func TestWorkoutFire_CommandRegistered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	fireCmd, _, err := root.Find([]string{"workout", "fire"})
	require.NoError(t, err)
	assert.Equal(t, "fire", fireCmd.Name())
	assert.NotNil(t, fireCmd.Flags().Lookup(workoutFlagDeliver), "fire exposes --deliver")
	assert.NotNil(t, fireCmd.Flags().Lookup(workoutFlagDryRun), "fire exposes --dry-run")
}

// TestWorkoutFire_DryRun_ComposesNoDeliver: a bare `workout fire` is a dry-run —
// it composes and prints today's message (the deterministic scaffold plus the
// model's phrasing) and touches nothing. No Discord env is read on this path.
func TestWorkoutFire_DryRun_ComposesNoDeliver(t *testing.T) {
	enableWorkoutSurface(t)
	withScriptedProvider(t, provider.Exchange{Content: "Good to see you today."})

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "fire")
	require.NoError(t, err)
	assert.Contains(t, out, "dry-run — not delivered")
	assert.Contains(t, out, "Good to see you today.", "the model note is rendered")
	assert.Contains(t, out, "**Workout**", "the deterministic header is present")
	assert.Contains(t, out, "not medical advice", "the safety line is always present")
}

// TestWorkoutFire_DryRun_ProviderDownStillRenders: the dry-run renders
// deterministically when the provider is unreachable and names the fallback.
func TestWorkoutFire_DryRun_ProviderDownStillRenders(t *testing.T) {
	enableWorkoutSurface(t)
	withScriptedProvider(t, provider.Exchange{Err: provider.ErrTimeout})

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "fire")
	require.NoError(t, err)
	assert.Contains(t, out, "dry-run — not delivered")
	assert.Contains(t, out, "deterministic fallback")
	assert.Contains(t, out, "not medical advice")
}

// TestWorkoutFire_DeliverAndDryRunMutuallyExclusive: --deliver and --dry-run
// cannot both be set, so an unambiguous script never asks for both.
func TestWorkoutFire_DeliverAndDryRunMutuallyExclusive(t *testing.T) {
	enableWorkoutSurface(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "fire", "--deliver", "--dry-run")
	require.Error(t, err)
}

// TestWorkoutFire_RejectsArgs: fire is a no-args verb — a stray positional is a
// usage error caught before any compose or delivery.
func TestWorkoutFire_RejectsArgs(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	root.SetArgs([]string{"workout", "fire", "extra"})
	require.Error(t, root.Execute())
}
