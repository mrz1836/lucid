package cli

import (
	"bytes"
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
// carries the deterministic header, options, and progress panel (AC-7 command, AC-9).
func TestWorkout_OnDemand_RendersRecommendation(t *testing.T) {
	enableWorkoutSurface(t)
	withScriptedProvider(t, provider.Exchange{Content: "Great to see you here today."})

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "workout")
	require.NoError(t, err)
	assert.Contains(t, out, "Great to see you here today.", "the model note is rendered")
	assert.Contains(t, out, "**Workout**", "the deterministic header is present")
	assert.Contains(t, out, "Today's options")
	assert.NotContains(t, out, "not medical advice")
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
// and the fallback is noted on stderr (AC-9 degrade).
func TestWorkout_OnDemand_ProviderDownStillRenders(t *testing.T) {
	enableWorkoutSurface(t)
	withScriptedProvider(t, provider.Exchange{Err: provider.ErrUnavailable})

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "workout")
	require.NoError(t, err)
	assert.Contains(t, out, "**Workout**")
	assert.NotContains(t, out, "not medical advice")
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

// --- The daily anchor -------------------------------------------------------

// singleWorkout reads back the one workout event a capture wrote.
func singleWorkout(t *testing.T, home string) observations.Event {
	t.Helper()
	workouts := eventsOfKind(readObsEvents(t, home), observations.KindWorkout)
	require.Len(t, workouts, 1)
	return workouts[0]
}

// TestWorkout_Log_AnchorCLI: --anchor alone logs today's daily floor — one
// workout event carrying the marker, no session fields, no body parts.
func TestWorkout_Log_AnchorCLI(t *testing.T) {
	home := enableWorkoutKinds(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--anchor")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged workout as `")

	w := singleWorkout(t, home)
	assert.Equal(t, true, w.Payload["anchor"])
	assert.NotContains(t, w.Payload, "body_parts")
	assert.NotContains(t, w.Payload, "parse", "an anchor is content, not a partial capture")
}

// TestWorkout_Log_AnchorItemsCLI: repeatable --anchor-item records the counts the
// user has, a bare item records no count, and the flag implies the marker.
func TestWorkout_Log_AnchorItemsCLI(t *testing.T) {
	home := enableWorkoutKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log",
		"--anchor-item", "squats:55", "--anchor-item", "core:50", "--anchor-item", "easy push-ups")
	require.NoError(t, err)

	w := singleWorkout(t, home)
	assert.Equal(t, true, w.Payload["anchor"], "naming items reports the floor was done")
	raw, ok := w.Payload["anchor_items"].([]any)
	require.True(t, ok)
	require.Len(t, raw, 3)

	first, ok := raw[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "squats", first["name"])
	assert.EqualValues(t, 55, first["count"])

	third, ok := raw[2].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "easy push-ups", third["name"], "a multi-word item is one value, never comma-split")
	assert.NotContains(t, third, "count")
}

// TestWorkout_Log_AnchorMixedWithSpokenRejected: --anchor is a content flag, so
// combining it with a spoken drop is rejected exactly like any other — spoken or
// structured, never both.
func TestWorkout_Log_AnchorMixedWithSpokenRejected(t *testing.T) {
	enableWorkoutKinds(t)
	for _, args := range [][]string{
		{"workout", "log", "--anchor", "did", "my", "anchor"},
		{"workout", "log", "--anchor-item", "squats:55", "--text", "did my anchor"},
	} {
		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not both")
	}
}

// TestWorkout_Log_AnchorItemBadCountRejected: a non-numeric or negative count is
// a usage error, never clamped and never silently dropped.
func TestWorkout_Log_AnchorItemBadCountRejected(t *testing.T) {
	enableWorkoutKinds(t)
	cases := map[string]string{
		"squats:many": "must be a number",
		"squats:-5":   "must be zero or more",
		":55":         "is missing an item name",
	}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--anchor-item", value)
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
		})
	}
}

// TestParseAnchorItemFlags covers the splitter directly: a bare name carries no
// count, a repeated name merges under its first spelling with the last stated
// count winning, blanks are dropped, and an explicit zero is a stated count. A
// count of 55 is an ordinary tally, not an out-of-range 0-10 reading.
func TestParseAnchorItemFlags(t *testing.T) {
	got, err := parseAnchorItemFlags([]string{"squats:55", " core ", "", "Squats:60", "planks:0"})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, router.AnchorCount{Name: "squats", Count: 60, HasCount: true}, got[0],
		"a repeated item merges under its first spelling, last stated count winning")
	assert.Equal(t, router.AnchorCount{Name: "core"}, got[1])
	assert.Equal(t, router.AnchorCount{Name: "planks", Count: 0, HasCount: true}, got[2],
		"an explicit zero is a stated count, not an absent one")

	kept, err := parseAnchorItemFlags([]string{"squats:55", "squats"})
	require.NoError(t, err)
	require.Len(t, kept, 1)
	assert.Equal(t, router.AnchorCount{Name: "squats", Count: 55, HasCount: true}, kept[0],
		"a bare re-entry never erases a count already given")
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
	assert.NotContains(t, out, "not medical advice")
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
	assert.NotContains(t, out, "not medical advice")
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

// TestWorkout_Log_DayFlagStructuredCLI is the case that started this: a session
// done on a prior day, logged today. The session and every derived body_state
// reading must land on that day with the same instant and precision, because
// the recovery guardrail reads occurred_at and the progress trend reads
// logical_date.
func TestWorkout_Log_DayFlagStructuredCLI(t *testing.T) {
	home := enableWorkoutKinds(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log",
		"--type", "push", "--pain", "knee:7", "--day", "2026-06-01")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged workout as `")

	events := readObsEvents(t, home)
	workouts := eventsOfKind(events, observations.KindWorkout)
	states := eventsOfKind(events, observations.KindBodyState)
	require.Len(t, workouts, 1)
	require.Len(t, states, 1)

	assert.Equal(t, "2026-06-01", workouts[0].LogicalDate)
	assert.Equal(t, workouts[0].LogicalDate, states[0].LogicalDate)
	assert.Equal(t, workouts[0].OccurredAt, states[0].OccurredAt)
	assert.Equal(t, workouts[0].OccurredAtPrecision, states[0].OccurredAtPrecision)
}

// --- render helpers ---------------------------------------------------------

// TestRenderWorkoutDryRun_NamesDegrades proves the dry-run preview names the
// deterministic-fallback and enrichment-degraded paths when they fired, and
// names neither on a clean compose.
func TestRenderWorkoutDryRun_NamesDegrades(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, renderWorkoutDryRun(&buf, router.WorkoutResult{
		Fallback: true, EnrichmentDegraded: true, Text: "SESSION",
	}))
	out := buf.String()
	assert.Contains(t, out, "dry-run — not delivered")
	assert.Contains(t, out, "deterministic fallback")
	assert.Contains(t, out, "enrichment degraded")
	assert.Contains(t, out, "SESSION")

	buf.Reset()
	require.NoError(t, renderWorkoutDryRun(&buf, router.WorkoutResult{Text: "CLEAN"}))
	assert.Contains(t, buf.String(), "CLEAN")
	assert.NotContains(t, buf.String(), "fallback")
}

// TestRenderWorkoutFire_ReportsOutcome proves the delivery reporter maps each
// outcome to its honest line: a skip names the reason, a delivery names the
// message id, and a late or fallback delivery is annotated.
func TestRenderWorkoutFire_ReportsOutcome(t *testing.T) {
	cases := []struct {
		name    string
		outcome workout.Outcome
		want    string
	}{
		{"skipped", workout.Outcome{Skipped: true, SkipReason: "already delivered"}, "workout slot skipped (already delivered)."},
		{"delivered", workout.Outcome{Delivered: true, MessageID: "42"}, "workout delivered — message 42."},
		{"late", workout.Outcome{Delivered: true, Late: true, MessageID: "42"}, "(late note prepended)"},
		{"fallback", workout.Outcome{Delivered: true, Fallback: true, MessageID: "7"}, "(deterministic fallback)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, renderWorkoutFire(&buf, tc.outcome))
			assert.Contains(t, buf.String(), tc.want)
		})
	}
}

// TestWorkoutFire_DeliverWithoutEnv_Errors proves the --deliver path fails
// loudly when the Discord transport cannot be built from the environment.
func TestWorkoutFire_DeliverWithoutEnv_Errors(t *testing.T) {
	enableWorkoutSurface(t)
	withScriptedProvider(t, provider.Exchange{Content: "note"})
	t.Setenv("LUCID_HARNESS_TOKEN", "")
	t.Setenv("LUCID_USER_CHANNEL_ID", "")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "fire", "--deliver")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lucid workout fire:")
}

// TestParsePartLevel covers the splitter's branches directly: an empty value is
// dropped, a bare part is allowed only when a level is not required, and a
// present level must be a 0-10 integer.
func TestParsePartLevel(t *testing.T) {
	cases := []struct {
		raw          string
		requireLevel bool
		wantPart     string
		wantLevel    int
		wantHas      bool
		wantErr      string
	}{
		{raw: "  ", requireLevel: false},
		{raw: "knee", requireLevel: false, wantPart: "knee"},
		{raw: "knee", requireLevel: true, wantErr: "needs a 0-10 level"},
		{raw: ":5", requireLevel: true, wantErr: "missing a body part"},
		{raw: "knee:abc", requireLevel: false, wantErr: "must be a number"},
		{raw: "knee:12", requireLevel: false, wantErr: "must be 0-10"},
		{raw: "knee:5", requireLevel: true, wantPart: "knee", wantLevel: 5, wantHas: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw+"/"+boolName(tc.requireLevel), func(t *testing.T) {
			part, level, has, err := parsePartLevel(tc.raw, flagWSoreness, tc.requireLevel)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantPart, part)
			assert.Equal(t, tc.wantLevel, level)
			assert.Equal(t, tc.wantHas, has)
		})
	}
}

// TestParseBodyStateFlags_MergesPerPart proves per-part soreness and pain fold
// into one reading for the same part, an empty flag value is skipped, and a bare
// pain part records its own reading at the flag level.
func TestParseBodyStateFlags_MergesPerPart(t *testing.T) {
	states, err := parseBodyStateFlags([]string{"knee:4", ""}, []string{"knee:7", "shoulder"})
	require.NoError(t, err)
	require.Len(t, states, 2, "knee merges soreness+pain; shoulder is its own reading")

	// order preserves first appearance: knee (from soreness), then shoulder.
	knee := states[0]
	assert.Equal(t, "knee", knee.Part)
	require.NotNil(t, knee.Soreness)
	require.NotNil(t, knee.Pain)
	assert.Equal(t, 4, *knee.Soreness)
	assert.Equal(t, 7, *knee.Pain)

	shoulder := states[1]
	assert.Equal(t, "shoulder", shoulder.Part)
	assert.Nil(t, shoulder.Soreness, "a bare pain flag records no soreness")
	require.NotNil(t, shoulder.Pain)
	assert.Equal(t, router.PainFlagLevel, *shoulder.Pain, "a bare part records at the flag level")
}

// boolName labels a subtest by its requireLevel flag.
func boolName(b bool) string {
	if b {
		return "requireLevel"
	}
	return "bareOK"
}

// TestParseBodyStateFlags_PainErrorAndEmptySkip covers the pain loop's two
// remaining branches: a malformed pain value surfaces the parse error, and an
// empty value is skipped rather than recorded as a blank part.
func TestParseBodyStateFlags_PainErrorAndEmptySkip(t *testing.T) {
	_, err := parseBodyStateFlags(nil, []string{"knee:99"})
	require.Error(t, err, "an out-of-range pain level is a usage error")
	assert.Contains(t, err.Error(), "must be 0-10")

	states, err := parseBodyStateFlags(nil, []string{""})
	require.NoError(t, err)
	assert.Empty(t, states, "an empty pain value is skipped, not a blank reading")
}

// TestWorkout_OnDemand_BootError proves the bare on-demand verb surfaces a boot
// failure before it composes anything.
func TestWorkout_OnDemand_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout")
	require.Error(t, err)
}

// TestWorkout_OnDemand_ProviderBuildErrorSurfaces proves a provider that cannot
// be built is a loud failure on the on-demand surface (the message is composed
// through the model, so no backend is fatal here — unlike the scheduled slot's
// deterministic fallback).
func TestWorkout_OnDemand_ProviderBuildErrorSurfaces(t *testing.T) {
	enableWorkoutSurface(t)
	withFailingProvider(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider backend unavailable")
}

// TestWorkoutFire_BootError proves the fire verb surfaces a boot failure before
// it composes or delivers.
func TestWorkoutFire_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "fire")
	require.Error(t, err)
}

// TestWorkoutFire_DryRun_ProviderBuildErrorSurfaces proves the dry-run compose
// fails loudly when the provider cannot be built.
func TestWorkoutFire_DryRun_ProviderBuildErrorSurfaces(t *testing.T) {
	enableWorkoutSurface(t)
	withFailingProvider(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "fire")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider backend unavailable")
}

// TestWorkoutLog_BootError proves the log verb surfaces a boot failure before it
// parses or writes.
func TestWorkoutLog_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--type", "push")
	require.Error(t, err)
}

// TestWorkoutLog_NegativeDurationRejected proves a negative --duration is a usage
// error rather than a clamped or silently-dropped value.
func TestWorkoutLog_NegativeDurationRejected(t *testing.T) {
	enableWorkoutKinds(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "--type", "push", "--duration", "-5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be zero or more")
}

// TestWorkoutLog_SpokenProviderBuildErrorSurfaces proves the spoken capture path
// surfaces a provider-build failure (the extraction needs the model).
func TestWorkoutLog_SpokenProviderBuildErrorSurfaces(t *testing.T) {
	enableWorkoutKinds(t)
	withFailingProvider(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log", "did pull today")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider backend unavailable")
}

// TestWorkout_Log_DayComposesWithSpokenDrop: --day says *when*, not *what*, so
// it is not a content flag — a spoken drop plus --day is an ordinary backdated
// capture, not a mixed-form usage error.
func TestWorkout_Log_DayComposesWithSpokenDrop(t *testing.T) {
	home := enableWorkoutKinds(t)
	fake := withScriptedProvider(t, provider.Exchange{Content: workoutCLIReply})
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log",
		"--day", "2026-06-01", "did pull, shoulder felt fine, 50 min")
	require.NoError(t, err)
	assert.Equal(t, 1, fake.Calls())

	workouts := eventsOfKind(readObsEvents(t, home), observations.KindWorkout)
	require.Len(t, workouts, 1)
	assert.Equal(t, "2026-06-01", workouts[0].LogicalDate)
}

// TestWorkout_Log_DayStrictRejectedCLI: a day that has not happened yet is a
// clean refusal on both capture forms, with nothing written.
func TestWorkout_Log_DayStrictRejectedCLI(t *testing.T) {
	home := enableWorkoutKinds(t)
	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "workout", "log",
		"--type", "push", "--pain", "knee:7", "--day", "2099-01-01")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not happened yet")
	// The reason reaches the user, not just the exit code — Execute renders no
	// returned error, so an unprinted refusal is a bare non-zero exit.
	assert.Contains(t, errOut, "2099-01-01")
	assert.Contains(t, errOut, "has not happened yet")
	assert.Empty(t, readObsEvents(t, home), "a refused day writes neither session nor reading")
}
