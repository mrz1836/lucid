package companion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/engine/templates"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
)

// --- test doubles -----------------------------------------------------------

// fakeNumbers is a scripted NumbersReader: it returns fixed projections (or a
// read error) so a compose test asserts the numbers block equals exactly the
// injected projection, with no live store.
type fakeNumbers struct {
	metrics router.MetricsResult
	status  router.StatusResult
	metErr  error
	statErr error
}

func (f fakeNumbers) Metrics(time.Time) (router.MetricsResult, error) {
	return f.metrics, f.metErr
}

func (f fakeNumbers) Status(time.Time) (router.StatusResult, error) {
	return f.status, f.statErr
}

// fakeVerdict is a scripted VerdictReader: "" is a normal/completed day, a
// non-empty string is a miss-day verdict, and err drives the loud-failure path.
type fakeVerdict struct {
	text string
	err  error
}

func (f fakeVerdict) TripwireUserVerdict(time.Time) (string, error) {
	return f.text, f.err
}

// fakeChain is a scripted ChainReader for the deterministic night fallback.
type fakeChain struct {
	chain engine.ChainConfig
	err   error
}

func (f fakeChain) ReadChainConfig() (engine.ChainConfig, error) {
	return f.chain, f.err
}

// fakeObservations is a scripted ObservationsReader: it returns a fixed slice
// (or a read error) and records how it was called, so a compose test asserts
// the composer filters to the render-relevant kinds, passes the bounded window,
// and degrades non-fatally on a read error.
type fakeObservations struct {
	events []observations.Event
	err    error
	calls  int
	window int
}

func (f *fakeObservations) RecentObservations(_ time.Time, windowDays int) ([]observations.Event, error) {
	f.calls++
	f.window = windowDays
	return f.events, f.err
}

// --- fixtures ---------------------------------------------------------------

const (
	sysContent     = "SYSTEM VOICE PROMPT"
	morningContent = "MORNING TEMPLATE BODY"
	nightContent   = "NIGHT TEMPLATE BODY"
)

// writePrompts writes the three opaque prompt files under a temp dir and returns
// a companion config whose explicit per-file paths point at them.
func writePrompts(t *testing.T) config.CompanionConfig {
	t.Helper()
	dir := t.TempDir()
	sys := filepath.Join(dir, "system_prompt.md")
	morning := filepath.Join(dir, "morning_template.md")
	night := filepath.Join(dir, "night_template.md")
	require.NoError(t, os.WriteFile(sys, []byte(sysContent+"\n"), 0o600))
	require.NoError(t, os.WriteFile(morning, []byte(morningContent+"\n"), 0o600))
	require.NoError(t, os.WriteFile(night, []byte(nightContent+"\n"), 0o600))
	return config.CompanionConfig{
		Enabled:         true,
		SystemPrompt:    sys,
		MorningTemplate: morning,
		NightTemplate:   night,
	}
}

// sampleNumbers is a decided-day projection (streak 5, 20/24 decided over 30) so
// the ramp frame takes the streak branch; no ambient signals hold.
func sampleNumbers() fakeNumbers {
	return fakeNumbers{
		metrics: router.MetricsResult{
			Metrics: engine.Metrics{
				CurrentStreak: 5,
				LongestStreak: 12,
				Adherence:     engine.Window{Length: 30, Completed: 20, Decided: 24, DaysAccounted: 26},
			},
			Lines: []string{
				"Streak: 5 (longest 12).",
				"30-day adherence: 83% (20/24 decided, 26 accounted; floor-days 3, 12% floor).",
			},
		},
		status: router.StatusResult{Status: engine.Status{ConsecutiveMisses: 0, StormState: engine.StormNone}},
	}
}

// wantSampleNumbers is the deterministic numbers block sampleNumbers renders to —
// the ramp-frame line followed by the projection's own honest lines. Built
// literally so the assertion is independent of the code under test.
func wantSampleNumbers() string {
	return strings.Join([]string{
		"Chain: 5-day streak — 20/24 decided days completed over the last 30.",
		"Streak: 5 (longest 12).",
		"30-day adherence: 83% (20/24 decided, 26 accounted; floor-days 3, 12% floor).",
	}, "\n")
}

// newComposer wires a Composer with a captured provider builder so tests can
// assert the resolved model and inspect the fake's recorded requests.
func newComposer(t *testing.T, comp config.CompanionConfig, prov config.ProviderConfig, nums fakeNumbers, vd fakeVerdict, ch fakeChain, p *provider.Fake) (*Composer, *config.ProviderConfig) {
	t.Helper()
	var got config.ProviderConfig
	c := New(Deps{
		Companion: comp,
		Provider:  prov,
		Numbers:   nums,
		Verdict:   vd,
		Chain:     ch,
		Build: func(pc config.ProviderConfig) (provider.Provider, error) {
			got = pc
			return p, nil
		},
	})
	return c, &got
}

func defaultProvider() config.ProviderConfig {
	return config.ProviderConfig{Backend: "claude_cli", Model: "opus", TimeoutSeconds: 120}
}

// --- tests ------------------------------------------------------------------

// TestCompose_NormalDayMorning_ComposesNoVerdict is the happy path: a completed
// day (empty verdict) composes through the model, appends no verdict, and the
// request carries the system prompt + template + the exact injected numbers.
func TestCompose_NormalDayMorning_ComposesNoVerdict(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "  WARM MORNING MESSAGE  "}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	assert.Equal(t, "WARM MORNING MESSAGE", res.Text, "model content is trimmed, no verdict appended")
	assert.True(t, res.UsedLLM)
	assert.False(t, res.Fallback)
	assert.False(t, res.MissDay)

	require.Equal(t, 1, p.Calls())
	req := p.Requests[0]
	assert.Equal(t, intentMorning, req.Intent)
	assert.Equal(t, sysContent+"\n", req.System, "system prompt is the opaque file verbatim")
	require.Len(t, req.Messages, 1)
	body := req.Messages[0].Content
	assert.True(t, strings.HasPrefix(body, morningContent), "body leads with the per-mode template")
	assert.Contains(t, body, numbersHeader)
	assert.Contains(t, body, wantSampleNumbers(), "numbers equal the injected metrics/status projection")
}

// TestCompose_NightUsesNightTemplate confirms the night window reads the night
// template and stamps the night intent.
func TestCompose_NightUsesNightTemplate(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "WARM NIGHT"}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeNight, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "WARM NIGHT", res.Text)
	require.Equal(t, 1, p.Calls())
	assert.Equal(t, intentNight, p.Requests[0].Intent)
	assert.True(t, strings.HasPrefix(p.Requests[0].Messages[0].Content, nightContent))
}

// TestCompose_MissDay_AppendsVerdictVerbatim: on a miss-day the model composes
// the warmth and the Engine's user verdict is appended byte-for-byte below it
// (trailing newline trimmed, internal newlines preserved).
func TestCompose_MissDay_AppendsVerdictVerbatim(t *testing.T) {
	comp := writePrompts(t)
	verdict := "last night was a miss. Tonight is a must.\n— the form letter, pre-committed at Day 0.\n"
	p := &provider.Fake{Script: []provider.Exchange{{Content: "WARM MORNING MESSAGE"}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: verdict}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	want := "WARM MORNING MESSAGE\n\nlast night was a miss. Tonight is a must.\n— the form letter, pre-committed at Day 0."
	assert.Equal(t, want, res.Text)
	assert.True(t, res.MissDay)
	assert.True(t, res.UsedLLM)
	assert.False(t, res.Fallback)
}

// TestCompose_MissDayNight_AppendsVerdict confirms the verdict append is not
// morning-only — the night window appends it too.
func TestCompose_MissDayNight_AppendsVerdict(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "WARM NIGHT"}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: "VERDICT LINE"}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeNight, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "WARM NIGHT\n\nVERDICT LINE", res.Text)
	assert.True(t, res.MissDay)
}

// TestCompose_ProviderDown_NormalMorning_FallsBackToNumbers: on ErrUnavailable
// the morning falls back to the deterministic honest numbers (no bell for the
// morning window) so a message always lands; only warmth is lost.
func TestCompose_ProviderDown_NormalMorning_FallsBackToNumbers(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("cli offline: %w", provider.ErrUnavailable)}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	assert.Equal(t, wantSampleNumbers(), res.Text)
	assert.True(t, res.Fallback)
	assert.False(t, res.UsedLLM)
	assert.False(t, res.MissDay)
}

// TestCompose_ProviderDown_NormalNight_FallsBackToBellPlusNumbers confirms the
// night fallback carries the Engine's deterministic evening Bell above the
// honest numbers.
func TestCompose_ProviderDown_NormalNight_FallsBackToBellPlusNumbers(t *testing.T) {
	comp := writePrompts(t)
	chain := fakeChain{chain: engine.ChainConfig{Label: "Journal. Dock. Read."}}
	p := &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("timeout: %w", provider.ErrTimeout)}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, chain, p)

	res, err := c.Compose(context.Background(), ModeNight, time.Now())
	require.NoError(t, err)

	want := templates.Bell("Journal. Dock. Read.") + "\n\n" + wantSampleNumbers()
	assert.Equal(t, want, res.Text)
	assert.True(t, res.Fallback)
	assert.False(t, res.UsedLLM)
}

// TestCompose_ProviderDown_MissDay_FallsBackToVerdict confirms the teeth still
// land unsoftened when the model is unreachable on a miss-day: the fallback body
// is exactly the Engine's verdict, no bell, no numbers.
func TestCompose_ProviderDown_MissDay_FallsBackToVerdict(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("x: %w", provider.ErrUnavailable)}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: "you missed — the floor: one line."}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	assert.Equal(t, "you missed — the floor: one line.", res.Text)
	assert.True(t, res.Fallback)
	assert.True(t, res.MissDay)
}

// TestCompose_ModelOverride confirms companion.model overrides provider.model on
// the compose call, and an empty override inherits provider.model.
func TestCompose_ModelOverride(t *testing.T) {
	comp := writePrompts(t)
	comp.Model = "sonnet"
	p := &provider.Fake{Script: []provider.Exchange{{Content: "x"}}}
	c, got := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p)
	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "sonnet", got.Model, "companion.model overrides provider.model")
	assert.Equal(t, "claude_cli", got.Backend, "backend is inherited unchanged")

	comp2 := writePrompts(t)
	p2 := &provider.Fake{Script: []provider.Exchange{{Content: "x"}}}
	c2, got2 := newComposer(t, comp2, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p2)
	_, err = c2.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "opus", got2.Model, "empty companion.model inherits provider.model")
}

// TestCompose_EarlyRamp_FramesBuilding confirms the ramp frame takes the
// building branch (no hollow percentage) when no day is decided yet.
func TestCompose_EarlyRamp_FramesBuilding(t *testing.T) {
	comp := writePrompts(t)
	nums := fakeNumbers{
		metrics: router.MetricsResult{
			Metrics: engine.Metrics{CurrentStreak: 0, Adherence: engine.Window{Length: 30, Completed: 2, Decided: 0, DaysAccounted: 3}},
			Lines:   []string{"Streak: 0 (longest 0).", "30-day adherence: no decided days yet (3 accounted)."},
		},
		status: router.StatusResult{Status: engine.Status{}},
	}
	p := &provider.Fake{Script: []provider.Exchange{{Content: "WARM"}}}
	c, _ := newComposer(t, comp, defaultProvider(), nums, fakeVerdict{}, fakeChain{}, p)
	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	body := p.Requests[0].Messages[0].Content
	assert.Contains(t, body, "Building — 2 completed of 3 accounted so far; no decided day yet.")
	assert.NotContains(t, body, "Chain: 0-day streak", "no streak framing before a day is decided")
}

// TestCompose_StatusAmbient_Surfaced confirms the consecutive-miss and storm
// ambient signals from the status projection are rendered when they hold.
func TestCompose_StatusAmbient_Surfaced(t *testing.T) {
	comp := writePrompts(t)
	nums := sampleNumbers()
	nums.status = router.StatusResult{Status: engine.Status{ConsecutiveMisses: 2, StormState: engine.StormStandingState}}
	p := &provider.Fake{Script: []provider.Exchange{{Content: "WARM"}}}
	c, _ := newComposer(t, comp, defaultProvider(), nums, fakeVerdict{}, fakeChain{}, p)
	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	body := p.Requests[0].Messages[0].Content
	assert.Contains(t, body, "Consecutive misses: 2.")
	assert.Contains(t, body, "Storm standing — the stake is stayed; contact continues.")
}

// TestCompose_MissingPromptFile_Errors confirms a misconfigured (missing) prompt
// path is a loud error, not a silent empty send — no model call is made.
func TestCompose_MissingPromptFile_Errors(t *testing.T) {
	comp := writePrompts(t)
	comp.MorningTemplate = filepath.Join(t.TempDir(), "does-not-exist.md")
	p := &provider.Fake{Script: []provider.Exchange{{Content: "x"}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p)

	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.Error(t, err)
	assert.Equal(t, 0, p.Calls(), "no compose is attempted when a prompt file is missing")
}

// TestCompose_UnknownMode_Errors rejects a mode that is neither window.
func TestCompose_UnknownMode_Errors(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p)
	_, err := c.Compose(context.Background(), Mode("noon"), time.Now())
	require.Error(t, err)
	assert.Equal(t, 0, p.Calls())
}

// TestCompose_VerdictReadError_IsLoud confirms a failure reading the life-critical
// verdict surfaces as an error, never a silent send.
func TestCompose_VerdictReadError_IsLoud(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "x"}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{err: errors.New("read boom")}, fakeChain{}, p)
	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.Error(t, err)
	assert.Equal(t, 0, p.Calls())
}

// TestCompose_MetricsReadError_IsLoud confirms an unreadable numbers projection
// is a loud error before any model call.
func TestCompose_MetricsReadError_IsLoud(t *testing.T) {
	comp := writePrompts(t)
	nums := sampleNumbers()
	nums.metErr = errors.New("metrics boom")
	p := &provider.Fake{Script: []provider.Exchange{{Content: "x"}}}
	c, _ := newComposer(t, comp, defaultProvider(), nums, fakeVerdict{}, fakeChain{}, p)
	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.Error(t, err)
	assert.Equal(t, 0, p.Calls())
}

// TestCompose_NonSentinelProviderError_Propagates confirms a provider error that
// is neither timeout nor unavailable is returned, not swallowed by the
// deterministic fallback (which is reserved for the two transport sentinels).
func TestCompose_NonSentinelProviderError_Propagates(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Err: errors.New("garbage envelope")}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)
	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.Error(t, err)
	assert.False(t, res.Fallback, "a non-transport error is not a deterministic fallback")
	assert.Empty(t, res.Text)
}

// newComposerWithObs wires a Composer with a recent-observation reader (the
// path newComposer omits) so the enrichment seam is exercised with a scripted
// fake.
func newComposerWithObs(t *testing.T, obs ObservationsReader, p *provider.Fake) *Composer {
	t.Helper()
	return New(Deps{
		Companion:    writePrompts(t),
		Provider:     defaultProvider(),
		Numbers:      sampleNumbers(),
		Verdict:      fakeVerdict{},
		Chain:        fakeChain{},
		Observations: obs,
		Build:        func(config.ProviderConfig) (provider.Provider, error) { return p, nil },
	})
}

// TestCompose_Recent_FiltersToRenderKinds confirms the composer surfaces the
// bounded recent slice, filtered to the render-relevant kinds (an unrelated
// intake event is dropped), and passes the contract window to the reader.
func TestCompose_Recent_FiltersToRenderKinds(t *testing.T) {
	obs := &fakeObservations{events: []observations.Event{
		{ID: "obs_2026_07_08_001", Kind: observations.KindMood},
		{ID: "obs_2026_07_08_002", Kind: observations.KindIntake}, // not render-relevant
		{ID: "obs_2026_07_08_003", Kind: observations.KindWithdrawal},
		{ID: "obs_2026_07_08_004", Kind: observations.KindCommitment},
	}}
	p := &provider.Fake{Script: []provider.Exchange{{Content: "WARM"}}}
	c := newComposerWithObs(t, obs, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	assert.False(t, res.EnrichmentDegraded)
	require.Len(t, res.Recent, 3, "the intake event is filtered out; the render-relevant kinds are kept")
	assert.Equal(t, []observations.Kind{observations.KindMood, observations.KindWithdrawal, observations.KindCommitment},
		[]observations.Kind{res.Recent[0].Kind, res.Recent[1].Kind, res.Recent[2].Kind})
	assert.Equal(t, 1, obs.calls, "the reader is called exactly once")
	assert.Equal(t, recentWindowDays, obs.window, "the bounded window constant is passed to the reader")
}

// TestCompose_RecentReadError_DegradesNonFatally confirms an enrichment read
// error never fails the life-critical send: the message still composes, no
// events are surfaced, and the degradation is recorded for a dry-run to show.
func TestCompose_RecentReadError_DegradesNonFatally(t *testing.T) {
	obs := &fakeObservations{err: errors.New("ledger read boom")}
	p := &provider.Fake{Script: []provider.Exchange{{Content: "WARM MORNING"}}}
	c := newComposerWithObs(t, obs, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err, "an enrichment read error never fails the send")

	assert.True(t, res.EnrichmentDegraded, "the degraded read is recorded so a dry-run surfaces it")
	assert.Empty(t, res.Recent, "no events are surfaced on a degraded read")
	assert.Equal(t, "WARM MORNING", res.Text, "the message is still composed")
	assert.True(t, res.UsedLLM)
}

// TestCompose_NilObservationsReader_NotDegraded confirms an unconfigured reader
// (the current daemon path, which builds Deps without Observations) leaves the
// message unenriched without flagging a degradation.
func TestCompose_NilObservationsReader_NotDegraded(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "WARM"}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.False(t, res.EnrichmentDegraded, "an unconfigured reader is not a degradation")
	assert.Empty(t, res.Recent)
}
