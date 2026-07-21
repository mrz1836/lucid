package workout

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// --- test doubles -----------------------------------------------------------

// fakeMetrics is a scripted MetricsReader: it returns a fixed engine fold (or a
// read error) so a compose test drives the streak and the loud live-number path
// without a live engine tree.
type fakeMetrics struct {
	m   engine.Metrics
	err error
}

func (f fakeMetrics) Metrics(time.Time) (engine.Metrics, error) { return f.m, f.err }

// fakeObs is a scripted ObservationsReader: it returns a fixed slice (or a read
// error) and records the window it was asked for and how many times, so a compose
// test asserts the composer passes the full trend window and degrades non-fatally
// on a read error.
type fakeObs struct {
	events []observations.Event
	err    error
	calls  int
	window int
}

func (f *fakeObs) RecentObservations(_ time.Time, windowDays int) ([]observations.Event, error) {
	f.calls++
	f.window = windowDays
	return f.events, f.err
}

// fakeInjuries is a scripted InjuryReader: a fixed registry slice (or a read
// error) so a compose test proves the injury guardrail is wired and degrades on
// a read error.
type fakeInjuries struct {
	recs []observations.Registry
	err  error
}

func (f fakeInjuries) ReadRegistryKind(string) ([]observations.Registry, error) {
	return f.recs, f.err
}

// --- fixtures ---------------------------------------------------------------

const (
	sysContent  = "SYSTEM VOICE PROMPT"
	tmplContent = "TEMPLATE BODY"
)

// writeWorkoutConfig writes the synthetic program + the two opaque prompt files
// under a temp dir and returns an enabled workout config whose explicit per-file
// paths point at them. The program is the synthetic ExampleProgram — no personal
// content ever touches a test (product-principles §9).
func writeWorkoutConfig(t *testing.T) config.WorkoutConfig {
	t.Helper()
	dir := t.TempDir()
	progPath := filepath.Join(dir, "program.json")
	b, err := json.Marshal(ExampleProgram())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(progPath, b, 0o600))

	sys := filepath.Join(dir, "system_prompt.md")
	tmpl := filepath.Join(dir, "daily_template.md")
	require.NoError(t, os.WriteFile(sys, []byte(sysContent+"\n"), 0o600))
	require.NoError(t, os.WriteFile(tmpl, []byte(tmplContent+"\n"), 0o600))

	return config.WorkoutConfig{
		Enabled:      true,
		Program:      progPath,
		SlotTime:     "12:00",
		SystemPrompt: sys,
		Template:     tmpl,
	}
}

// fakeBuilder returns a ProviderBuilder that always yields the given fake, so the
// compose runs fully offline (ADR-0006).
func fakeBuilder(f *provider.Fake) ProviderBuilder {
	return func(config.ProviderConfig) (provider.Provider, error) { return f, nil }
}

// baseDeps assembles compose dependencies from a workout config, a streak-3
// metrics fold, empty enrichment readers, and the given provider fake — the
// happy-path baseline each test tweaks.
func baseDeps(t *testing.T, obs *fakeObs, inj fakeInjuries, f *provider.Fake) Deps {
	t.Helper()
	return Deps{
		Workout:      writeWorkoutConfig(t),
		Metrics:      fakeMetrics{m: engine.Metrics{CurrentStreak: 3}},
		Observations: obs,
		Injuries:     inj,
		Build:        fakeBuilder(f),
	}
}

// --- LLM path ---------------------------------------------------------------

// TestComposeLLMPathPhrasesTheDecidedPick proves the happy path: the deterministic
// core picks Monday's legs card, the model phrases a bounded note, and the
// rendered message leads with that note and still carries the whole deterministic
// scaffold (header, options, progress). The one model call is the phrasing
// intent, framed by the system prompt, and grounded on the plan digest.
func TestComposeLLMPathPhrasesTheDecidedPick(t *testing.T) {
	t.Parallel()

	const note = "Nice work showing up today — take the option that matches your energy."
	fake := &provider.Fake{Script: []provider.Exchange{{Content: note}}}
	res, err := New(baseDeps(t, &fakeObs{}, fakeInjuries{}, fake)).Compose(context.Background(), mustTime(t, mondayNoon))
	require.NoError(t, err)

	assert.True(t, res.UsedLLM, "the model filled the phrasing slot")
	assert.False(t, res.Fallback)
	assert.Equal(t, "legs", res.Recommendation.Primary.ID, "Monday resolves the legs card")

	// The message leads with the model's note, then the full deterministic card.
	assert.Contains(t, res.Text, emojiCoach+" "+note)
	assert.Contains(t, res.Text, "**Workout**", "the deterministic header is present")
	assert.Contains(t, res.Text, "Legs + hips", "the decided card is rendered")
	assert.NotContains(t, res.Text, "not medical advice")

	// Exactly one phrasing call, correctly framed and grounded.
	require.Equal(t, 1, fake.Calls())
	req := fake.Requests[0]
	assert.Equal(t, intentDaily, req.Intent)
	assert.Contains(t, req.System, sysContent)
	require.Len(t, req.Messages, 1)
	assert.Contains(t, req.Messages[0].Content, tmplContent, "the operator template frames the call")
	assert.Contains(t, req.Messages[0].Content, "Today's pick:", "the decided plan is handed to the model")
}

// TestComposeModelNeverChangesPick proves the invariant: legs loaded yesterday
// forces the deterministic recovery veto to rotate to push, and the model's reply
// (which even names the wrong card) cannot move the pick — the Recommendation and
// the grounding digest are the deterministic core's, not the model's.
func TestComposeModelNeverChangesPick(t *testing.T) {
	t.Parallel()

	obs := &fakeObs{events: []observations.Event{workoutEvent("2026-07-19T18:00:00Z", "legs", nil, 0)}}
	fake := &provider.Fake{Script: []provider.Exchange{{Content: "Let's crush legs again!"}}}
	res, err := New(baseDeps(t, obs, fakeInjuries{}, fake)).Compose(context.Background(), mustTime(t, mondayNoon))
	require.NoError(t, err)

	assert.Equal(t, "push", res.Recommendation.Primary.ID, "legs is still recovering — the deterministic core rotated to push")
	require.NotEmpty(t, res.Recommendation.Vetoes)
	// The digest handed to the model carries the decided (push) pick, not legs.
	assert.Contains(t, fake.Requests[0].Messages[0].Content, "Push")
}

// TestComposePassesInjuriesToHardStop proves the injury reader is wired into the
// guardrail: an active injury naming a targeted part emits a hard stop and
// downshifts the primary, even with no logged pain.
func TestComposePassesInjuriesToHardStop(t *testing.T) {
	t.Parallel()

	inj := fakeInjuries{recs: []observations.Registry{{
		Kind: observations.RegistryInjury, Status: observations.StatusActive,
		DisplayName: "left knee", Fields: map[string]any{"body_part": "legs"},
	}}}
	fake := &provider.Fake{Script: []provider.Exchange{{Content: "Easy does it today."}}}
	res, err := New(baseDeps(t, &fakeObs{}, inj, fake)).Compose(context.Background(), mustTime(t, mondayNoon))
	require.NoError(t, err)

	require.NotNil(t, res.Recommendation.HardStop, "an active injury on a targeted part hard-stops")
	assert.Equal(t, "recovery", res.Recommendation.Primary.ID)
}

// --- fallback paths ---------------------------------------------------------

// TestComposeFallsBackOnProviderDown proves the provider-outage sentinels render
// the deterministic scaffold verbatim — the pick stands, only the leading note
// is lost, and the text is exactly Render's output.
func TestComposeFallsBackOnProviderDown(t *testing.T) {
	t.Parallel()

	for name, sentinel := range map[string]error{
		"timeout":     provider.ErrTimeout,
		"unavailable": provider.ErrUnavailable,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := &provider.Fake{Script: []provider.Exchange{{Err: sentinel}}}
			now := mustTime(t, mondayNoon)
			res, err := New(baseDeps(t, &fakeObs{}, fakeInjuries{}, fake)).Compose(context.Background(), now)
			require.NoError(t, err)

			assert.True(t, res.Fallback)
			assert.False(t, res.UsedLLM)
			assert.NotContains(t, res.Text, emojiCoach, "no model note on the deterministic path")
			assert.NotContains(t, res.Text, "not medical advice")
			assert.Equal(t, Render(res.Recommendation, res.Trend, now), res.Text, "the fallback is exactly the deterministic Render")
		})
	}
}

// TestComposeFallsBackOnEmptyReply proves an empty model reply is treated as
// unusable and renders the deterministic scaffold rather than an empty note.
func TestComposeFallsBackOnEmptyReply(t *testing.T) {
	t.Parallel()

	fake := &provider.Fake{Script: []provider.Exchange{{Content: "   "}}}
	now := mustTime(t, mondayNoon)
	res, err := New(baseDeps(t, &fakeObs{}, fakeInjuries{}, fake)).Compose(context.Background(), now)
	require.NoError(t, err)

	assert.True(t, res.Fallback)
	assert.False(t, res.UsedLLM)
	assert.Equal(t, Render(res.Recommendation, res.Trend, now), res.Text)
}

// --- enrichment + windows ---------------------------------------------------

// TestComposeReadsFullTrendWindow proves the composer asks for the four-week trend
// window exactly once, so both the recovery guardrail and the trend see enough
// history.
func TestComposeReadsFullTrendWindow(t *testing.T) {
	t.Parallel()

	obs := &fakeObs{}
	fake := &provider.Fake{Script: []provider.Exchange{{Content: "note"}}}
	_, err := New(baseDeps(t, obs, fakeInjuries{}, fake)).Compose(context.Background(), mustTime(t, mondayNoon))
	require.NoError(t, err)

	assert.Equal(t, 1, obs.calls)
	assert.Equal(t, defaultTrendWindowDays, obs.window)
}

// TestComposeEnrichmentDegradesToCalendar proves a recent-observation read error
// is non-fatal: the message still composes, EnrichmentDegraded is flagged, and the
// recommendation follows the plain program calendar (the missing-data rule).
func TestComposeEnrichmentDegradesToCalendar(t *testing.T) {
	t.Parallel()

	obs := &fakeObs{err: assertAnError()}
	fake := &provider.Fake{Script: []provider.Exchange{{Content: "note"}}}
	res, err := New(baseDeps(t, obs, fakeInjuries{}, fake)).Compose(context.Background(), mustTime(t, mondayNoon))
	require.NoError(t, err)

	assert.True(t, res.EnrichmentDegraded, "a recent-observation read error is recorded, not fatal")
	assert.Equal(t, "legs", res.Recommendation.Primary.ID, "the pick falls to the plain calendar")
	assert.Empty(t, res.Recommendation.Vetoes)
}

// TestComposeInjuryReadDegradesNonFatally proves an injury-registry read error is
// likewise non-fatal — the message composes and only the flag is raised.
func TestComposeInjuryReadDegradesNonFatally(t *testing.T) {
	t.Parallel()

	fake := &provider.Fake{Script: []provider.Exchange{{Content: "note"}}}
	res, err := New(baseDeps(t, &fakeObs{}, fakeInjuries{err: assertAnError()}, fake)).Compose(context.Background(), mustTime(t, mondayNoon))
	require.NoError(t, err)

	assert.True(t, res.EnrichmentDegraded)
	assert.True(t, res.UsedLLM, "the message still composes")
}

// --- loud failures ----------------------------------------------------------

// TestComposeMissingProgramIsLoud proves an unreadable program path is a loud
// error — the surface needs a program.
func TestComposeMissingProgramIsLoud(t *testing.T) {
	t.Parallel()

	deps := baseDeps(t, &fakeObs{}, fakeInjuries{}, &provider.Fake{})
	deps.Workout.Program = filepath.Join(t.TempDir(), "missing.json")
	_, err := New(deps).Compose(context.Background(), mustTime(t, mondayNoon))
	require.Error(t, err)
}

// TestComposeMissingPromptIsLoud proves an empty system-prompt path is a loud
// configuration error rather than an empty read.
func TestComposeMissingPromptIsLoud(t *testing.T) {
	t.Parallel()

	deps := baseDeps(t, &fakeObs{}, fakeInjuries{}, &provider.Fake{})
	deps.Workout.SystemPrompt = ""
	_, err := New(deps).Compose(context.Background(), mustTime(t, mondayNoon))
	require.Error(t, err)
}

// TestComposeMetricsErrorIsLoud proves the live-number read stays loud — a metrics
// failure fails the compose rather than fabricating a streak.
func TestComposeMetricsErrorIsLoud(t *testing.T) {
	t.Parallel()

	deps := baseDeps(t, &fakeObs{}, fakeInjuries{}, &provider.Fake{})
	deps.Metrics = fakeMetrics{err: assertAnError()}
	_, err := New(deps).Compose(context.Background(), mustTime(t, mondayNoon))
	require.Error(t, err)
}

// TestComposeNilBuildDefaultsFactory proves New defaults the provider builder when
// Deps.Build is nil, so a caller that forgets to wire it still constructs a valid
// Composer (the factory path is exercised only in production).
func TestComposeNilBuildDefaultsFactory(t *testing.T) {
	t.Parallel()

	c := New(Deps{Workout: writeWorkoutConfig(t), Metrics: fakeMetrics{}})
	assert.NotNil(t, c.build)
}

// assertAnError returns a non-nil error for the degrade tests without importing a
// sentinel — any error drives the non-fatal path.
func assertAnError() error { return os.ErrClosed }
