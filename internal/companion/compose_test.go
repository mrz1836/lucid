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
// read error) so a compose test asserts the status panel equals exactly the
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

// sampleNumbers is a decided-day projection (streak 5, 83% over 20/24 decided,
// 2/3 error budget) so the status panel takes the decided-day branch; no ambient
// signals hold.
func sampleNumbers() fakeNumbers {
	return fakeNumbers{
		metrics: router.MetricsResult{
			Metrics: engine.Metrics{
				CurrentStreak: 5,
				LongestStreak: 12,
				Adherence:     engine.Window{Length: 30, Adherence: 0.83, Completed: 20, Decided: 24, DaysAccounted: 26},
				ErrorBudget:   engine.ErrorBudget{Budget: 3, Burn: 1, Remaining: 2},
			},
		},
		status: router.StatusResult{Status: engine.Status{ConsecutiveMisses: 0, StormState: engine.StormNone}},
	}
}

// wantSamplePanel is the compact status panel sampleNumbers renders to — the
// streak+adherence hero line and the error-budget line. Built literally so the
// assertion is independent of the code under test.
func wantSamplePanel() []string {
	return []string{
		"⛓️ 5-day streak · 83% adherence (20/24 decided)",
		"📊 Error budget · 2/3 isolated misses left",
	}
}

// slotReply is a well-formed two-slot model reply — an interpretation block and
// two action bullets under the delimiter labels the renderer parses.
func slotReply(interp string, actions ...string) string {
	lines := make([]string, 0, 3+len(actions))
	lines = append(lines, interpDelim, interp, actionsDelim)
	for _, a := range actions {
		lines = append(lines, "- "+a)
	}
	return strings.Join(lines, "\n")
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

// TestCompose_NormalDayMorning_RendersScaffold is the happy path: a completed
// day (empty verdict) composes through the model, the model's prose fills the
// interpretation slot, and the delivered text is the deterministic scaffold —
// header, status panel, and the read — never the raw numbers dump. The compose
// body hands the model the template, the panel context, and the slot
// instruction.
func TestCompose_NormalDayMorning_RendersScaffold(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "  WARM MORNING MESSAGE  "}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	assert.True(t, res.UsedLLM)
	assert.False(t, res.Fallback)
	assert.False(t, res.MissDay)

	// The delivered text is the scaffold: header, panel, and the model's prose in
	// the read slot — no verbatim metric dump.
	assert.Contains(t, res.Text, "☀️ **Morning** · ", "the header leads")
	for _, line := range wantSamplePanel() {
		assert.Contains(t, res.Text, line, "the compact status panel renders")
	}
	assert.Contains(t, res.Text, "🧭 **The read**\nWARM MORNING MESSAGE", "the model prose fills the read slot, trimmed")

	require.Equal(t, 1, p.Calls())
	req := p.Requests[0]
	assert.Equal(t, intentMorning, req.Intent)
	assert.Equal(t, sysContent+"\n", req.System, "system prompt is the opaque file verbatim")
	require.Len(t, req.Messages, 1)
	body := req.Messages[0].Content
	assert.True(t, strings.HasPrefix(body, morningContent), "body leads with the per-mode template")
	assert.Contains(t, body, interpDelim, "the body instructs the two-slot contract")
	assert.Contains(t, body, actionsDelim)
	assert.Contains(t, body, wantSamplePanel()[0], "the panel is handed to the model as context")
	assert.NotContains(t, body, "LIVE NUMBERS", "the old verbatim numbers dump is gone from the prompt")
}

// TestCompose_NightUsesNightTemplate confirms the night window reads the night
// template, stamps the night intent, and renders the compact close-out framing.
func TestCompose_NightUsesNightTemplate(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("WARM NIGHT", "Close the chain.")}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeNight, time.Now())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(res.Text, "🌙 **Night** · "), "night header leads")
	assert.NotContains(t, res.Text, "🕯️ **Examen**", "night suppresses the separate examen section")
	assert.NotContains(t, res.Text, "WARM NIGHT", "night does not render the interpretation slot")
	assert.Contains(t, res.Text, "🌒 **Close-out**", "night uses the close-out framing")
	require.Equal(t, 1, p.Calls())
	assert.Equal(t, intentNight, p.Requests[0].Intent)
	assert.True(t, strings.HasPrefix(p.Requests[0].Messages[0].Content, nightContent))
}

// TestCompose_SlotSuccess_RendersInterpAndActions parses both model slots and
// renders them into the scaffold's read and next regions.
func TestCompose_SlotSuccess_RendersInterpAndActions(t *testing.T) {
	comp := writePrompts(t)
	reply := slotReply("Steady week. The streak holds.", "Run the morning chain.", "Log a mood check at noon.")
	p := &provider.Fake{Script: []provider.Exchange{{Content: reply}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.True(t, res.UsedLLM)
	assert.Contains(t, res.Text, "🧭 **The read**\nSteady week. The streak holds.")
	assert.Contains(t, res.Text, "▶️ **Next**\n• Run the morning chain.\n• Log a mood check at noon.")
}

// TestCompose_MissDay_RendersVerdictAsFinalGroup: on a miss-day the model
// composes the warmth and the Engine's user verdict is rendered byte-for-byte as
// the final scaffold group (trailing newline trimmed, internal newlines
// preserved) — the teeth are never reworded.
func TestCompose_MissDay_RendersVerdictAsFinalGroup(t *testing.T) {
	comp := writePrompts(t)
	verdict := "last night was a miss. Tonight is a must.\n— the form letter, pre-committed at Day 0.\n"
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("Own it and reset.", "Tonight is a must.")}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: verdict}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	wantVerdict := "last night was a miss. Tonight is a must.\n— the form letter, pre-committed at Day 0."
	assert.True(t, strings.HasSuffix(res.Text, "\n\n"+dividerLine+"\n\n"+wantVerdict),
		"the verdict is the final group, divider-separated and verbatim")
	assert.Contains(t, res.Text, "Own it and reset.", "the warm interpretation still renders above the verdict")
	assert.True(t, res.MissDay)
	assert.True(t, res.UsedLLM)
	assert.False(t, res.Fallback)
}

// TestCompose_MissDayNight_RendersVerdict confirms the verdict-as-final-group is
// not morning-only — the night window renders it too.
func TestCompose_MissDayNight_RendersVerdict(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("A hard night.", "Close it out.")}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: "VERDICT LINE"}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeNight, time.Now())
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(res.Text, "\n\nVERDICT LINE"))
	assert.NotContains(t, res.Text, dividerLine, "night verdict stays compact without divider chrome")
	assert.True(t, res.MissDay)
}

// TestCompose_ProviderDown_NormalMorning_FallsBackToScaffold: on ErrUnavailable
// the morning renders the deterministic fallback scaffold — the panel, the
// deterministic read, and a start-small action — so a valid message always lands
// and only warmth is lost.
func TestCompose_ProviderDown_NormalMorning_FallsBackToScaffold(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("cli offline: %w", provider.ErrUnavailable)}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	assert.True(t, res.Fallback)
	assert.False(t, res.UsedLLM)
	assert.False(t, res.MissDay)
	assert.Contains(t, res.Text, "☀️ **Morning** · ")
	assert.Contains(t, res.Text, wantSamplePanel()[0], "the panel still renders on the fallback path")
	assert.Contains(t, res.Text, "🧭 **The read**\n"+fallbackInterpMorning)
	assert.Contains(t, res.Text, "▶️ **Next**\n• "+fallbackActionMorning)
}

// TestCompose_ProviderDown_NormalNight_FallsBackToBellCloseout confirms the
// night fallback scaffold's close-out action is the Engine's deterministic
// evening Bell naming the chain.
func TestCompose_ProviderDown_NormalNight_FallsBackToBellCloseout(t *testing.T) {
	comp := writePrompts(t)
	chain := fakeChain{chain: engine.ChainConfig{Label: "Journal. Dock. Read."}}
	p := &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("timeout: %w", provider.ErrTimeout)}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, chain, p)

	res, err := c.Compose(context.Background(), ModeNight, time.Now())
	require.NoError(t, err)

	assert.True(t, res.Fallback)
	assert.False(t, res.UsedLLM)
	assert.Contains(t, res.Text, "🌙 **Night** · ")
	assert.NotContains(t, res.Text, "🕯️ **Examen**", "night fallback suppresses the separate examen section")
	assert.NotContains(t, res.Text, fallbackInterpNight, "night fallback does not render interpretation copy")
	assert.Contains(t, res.Text, "🌒 **Close-out**\n• "+templates.Bell("Journal. Dock. Read."))
}

// TestCompose_ProviderDown_MissDay_FallsBackWithVerdict confirms the teeth still
// land when the model is unreachable on a miss-day: the fallback scaffold renders
// with the Engine's verdict as its final group, verbatim.
func TestCompose_ProviderDown_MissDay_FallsBackWithVerdict(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("x: %w", provider.ErrUnavailable)}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: "you missed — the floor: one line."}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	assert.True(t, res.Fallback)
	assert.True(t, res.MissDay)
	assert.True(t, strings.HasSuffix(res.Text, "\n\n"+dividerLine+"\n\nyou missed — the floor: one line."),
		"the verdict lands verbatim as the final group even on the fallback path")
	assert.Contains(t, res.Text, wantSamplePanel()[0], "the fallback still carries the panel")
}

// TestCompose_EmptyModelReply_FallsBack: a model reply with no usable text (only
// whitespace) is treated as a fallback rather than delivering an empty
// interpretation.
func TestCompose_EmptyModelReply_FallsBack(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "   \n  "}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.True(t, res.Fallback, "an empty reply falls back")
	assert.False(t, res.UsedLLM)
	assert.Contains(t, res.Text, fallbackInterpMorning)
}

// TestCompose_MissingDelimiter_UsesProseAsInterp: a model reply with no slot
// delimiters is a valid scaffold, not a fallback — the whole prose becomes the
// interpretation.
func TestCompose_MissingDelimiter_UsesProseAsInterp(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "A quiet, steady paragraph with no delimiters."}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.True(t, res.UsedLLM, "plain prose is still the model's warmth, not a fallback")
	assert.False(t, res.Fallback)
	assert.Contains(t, res.Text, "🧭 **The read**\nA quiet, steady paragraph with no delimiters.")
}

// TestCompose_ModelOverride confirms companion.model overrides provider.model on
// the compose call, and an empty override inherits provider.model.
func TestCompose_ModelOverride(t *testing.T) {
	comp := writePrompts(t)
	comp.Model = "sonnet"
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("x", "y")}}}
	c, got := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p)
	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "sonnet", got.Model, "companion.model overrides provider.model")
	assert.Equal(t, "claude_cli", got.Backend, "backend is inherited unchanged")

	comp2 := writePrompts(t)
	p2 := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("x", "y")}}}
	c2, got2 := newComposer(t, comp2, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p2)
	_, err = c2.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "opus", got2.Model, "empty companion.model inherits provider.model")
}

// TestCompose_EarlyRamp_PanelFramesBuilding confirms the status panel handed to
// the model takes the building branch (no hollow percentage) when no day is
// decided yet.
func TestCompose_EarlyRamp_PanelFramesBuilding(t *testing.T) {
	comp := writePrompts(t)
	nums := fakeNumbers{
		metrics: router.MetricsResult{
			Metrics: engine.Metrics{CurrentStreak: 0, Adherence: engine.Window{Length: 30, Completed: 2, Decided: 0, DaysAccounted: 3}},
		},
		status: router.StatusResult{Status: engine.Status{}},
	}
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("Building.", "Begin.")}}}
	c, _ := newComposer(t, comp, defaultProvider(), nums, fakeVerdict{}, fakeChain{}, p)
	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	body := p.Requests[0].Messages[0].Content
	assert.Contains(t, body, "⛓️ Building · 2 completed of 3 accounted — no decided day yet")
	assert.NotContains(t, body, "adherence (", "no decided-day framing before a day is decided")
}

// TestCompose_StatusAmbient_SurfacedInPanel confirms the consecutive-miss and
// storm ambient signals are rendered into the panel handed to the model when
// they hold.
func TestCompose_StatusAmbient_SurfacedInPanel(t *testing.T) {
	comp := writePrompts(t)
	nums := sampleNumbers()
	nums.status = router.StatusResult{Status: engine.Status{ConsecutiveMisses: 2, StormState: engine.StormStandingState}}
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("Ambient.", "Steady.")}}}
	c, _ := newComposer(t, comp, defaultProvider(), nums, fakeVerdict{}, fakeChain{}, p)
	_, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)

	body := p.Requests[0].Messages[0].Content
	assert.Contains(t, body, "⚠️ Consecutive misses · 2")
	assert.Contains(t, body, "🌩️ Storm standing — the stake is stayed")
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

// TestCompose_NightFallback_ChainReadError_IsLoud confirms a night fallback that
// cannot read the chain (for the Bell close-out) surfaces the error rather than
// delivering a bell-less close-out.
func TestCompose_NightFallback_ChainReadError_IsLoud(t *testing.T) {
	comp := writePrompts(t)
	ch := fakeChain{err: errors.New("chain read boom")}
	p := &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("down: %w", provider.ErrUnavailable)}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{text: ""}, ch, p)
	_, err := c.Compose(context.Background(), ModeNight, time.Now())
	require.Error(t, err)
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
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("ok", "go")}}}
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

// TestCompose_Sections_RenderWithFreshness confirms the recent observations
// render into their context sections with the "as logged <date>" freshness stamp
// (and a stale flag past the threshold), covering the body-state, change /
// withdrawal, and commitment kinds.
func TestCompose_Sections_RenderWithFreshness(t *testing.T) {
	now := time.Date(2026, 7, 20, 6, 0, 0, 0, time.UTC)
	obs := &fakeObservations{events: []observations.Event{
		{ID: "obs_2026_07_19_001", Kind: observations.KindMood, LogicalDate: "2026-07-19", Payload: map[string]any{"level": 7, "word": "steady"}},
		{ID: "obs_2026_07_19_002", Kind: observations.KindSleep, LogicalDate: "2026-07-19", Payload: map[string]any{"quality": 8}},
		{ID: "obs_2026_07_18_003", Kind: observations.KindWithdrawal, LogicalDate: "2026-07-18", Payload: map[string]any{"severity": 6, "note": "rough morning"}},
		{ID: "obs_2026_07_17_004", Kind: observations.KindHabitChange, LogicalDate: "2026-07-17", Payload: map[string]any{"load": 7, "note": "cut coffee"}},
		{ID: "obs_2026_07_15_005", Kind: observations.KindCommitment, LogicalDate: "2026-07-15", Payload: map[string]any{"what": "call the dentist"}},
	}}
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("Read.", "Act.")}}}
	c := newComposerWithObs(t, obs, p)

	res, err := c.Compose(context.Background(), ModeMorning, now)
	require.NoError(t, err)

	// Body & state: newest event is 2026-07-19 (two days back → fresh, no flag).
	assert.Contains(t, res.Text, "🫀 **Body & state** · as logged 2026-07-19")
	assert.Contains(t, res.Text, "• mood 7 — steady")
	assert.Contains(t, res.Text, "• sleep · quality 8")
	// Change & withdrawal: newest is the withdrawal at 2026-07-18 (still fresh).
	assert.Contains(t, res.Text, "🔄 **Change & withdrawal** · as logged 2026-07-18")
	assert.Contains(t, res.Text, "• withdrawal 6 — rough morning")
	assert.Contains(t, res.Text, "• habit change 7 — cut coffee")
	// Commitments: the only event is five days back → stale flag appended.
	assert.Contains(t, res.Text, "📌 **Commitments** · as logged 2026-07-15 · stale")
	assert.Contains(t, res.Text, "• call the dentist")
}

// TestCompose_AbsentKinds_OmitSections confirms a section with no logged event
// is omitted — only the body-state section renders when just a mood is logged.
func TestCompose_AbsentKinds_OmitSections(t *testing.T) {
	now := time.Date(2026, 7, 20, 6, 0, 0, 0, time.UTC)
	obs := &fakeObservations{events: []observations.Event{
		{ID: "obs_2026_07_20_001", Kind: observations.KindMood, LogicalDate: "2026-07-20", Payload: map[string]any{"level": 6}},
	}}
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("ok", "go")}}}
	c := newComposerWithObs(t, obs, p)

	res, err := c.Compose(context.Background(), ModeMorning, now)
	require.NoError(t, err)
	assert.Contains(t, res.Text, "🫀 **Body & state**")
	assert.NotContains(t, res.Text, "Change & withdrawal", "an absent change signal leaves no stray section")
	assert.NotContains(t, res.Text, "Commitments", "an absent commitment leaves no stray section")
}

// TestCompose_RecentReadError_DegradesNonFatally confirms an enrichment read
// error never fails the life-critical send: the message still composes, no
// events are surfaced, and the degradation is recorded for a dry-run to show.
func TestCompose_RecentReadError_DegradesNonFatally(t *testing.T) {
	obs := &fakeObservations{err: errors.New("ledger read boom")}
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("WARM MORNING", "go")}}}
	c := newComposerWithObs(t, obs, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err, "an enrichment read error never fails the send")

	assert.True(t, res.EnrichmentDegraded, "the degraded read is recorded so a dry-run surfaces it")
	assert.Empty(t, res.Recent, "no events are surfaced on a degraded read")
	assert.Contains(t, res.Text, "WARM MORNING", "the message is still composed")
	assert.True(t, res.UsedLLM)
}

// TestCompose_NilObservationsReader_NotDegraded confirms an unconfigured reader
// (a Deps built without Observations) leaves the message unenriched without
// flagging a degradation.
func TestCompose_NilObservationsReader_NotDegraded(t *testing.T) {
	comp := writePrompts(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("WARM", "go")}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.False(t, res.EnrichmentDegraded, "an unconfigured reader is not a degradation")
	assert.Empty(t, res.Recent)
}

// TestCompose_RoutineInjected confirms a configured, readable routine file is
// injected into the compose body as grounding context (never rendered verbatim
// into the delivered message), and RoutineDegraded stays false.
func TestCompose_RoutineInjected(t *testing.T) {
	comp := writePrompts(t)
	routine := filepath.Join(t.TempDir(), "morning-routine.md")
	require.NoError(t, os.WriteFile(routine, []byte("MORNING ROUTINE ANCHOR\n"), 0o600))
	comp.MorningRoutine = routine
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("grounded", "step")}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err)
	assert.False(t, res.RoutineDegraded)
	body := p.Requests[0].Messages[0].Content
	assert.Contains(t, body, "Intended morning routine:\nMORNING ROUTINE ANCHOR", "the routine is context for the model")
	assert.NotContains(t, res.Text, "MORNING ROUTINE ANCHOR", "the routine doc is never dumped into the delivered message")
}

// TestCompose_RoutineUnreadable_DegradesNonFatally confirms a set-but-unreadable
// routine path is omitted and recorded, never failing the send.
func TestCompose_RoutineUnreadable_DegradesNonFatally(t *testing.T) {
	comp := writePrompts(t)
	comp.MorningRoutine = filepath.Join(t.TempDir(), "missing-routine.md")
	p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("ok", "go")}}}
	c, _ := newComposer(t, comp, defaultProvider(), sampleNumbers(), fakeVerdict{}, fakeChain{}, p)

	res, err := c.Compose(context.Background(), ModeMorning, time.Now())
	require.NoError(t, err, "an unreadable routine never fails the send")
	assert.True(t, res.RoutineDegraded, "the unreadable routine is recorded")
	assert.True(t, res.UsedLLM)
	body := p.Requests[0].Messages[0].Content
	assert.NotContains(t, body, "Intended morning routine:", "an unreadable routine is omitted from the context")
}

// TestObservationLine covers the per-kind bullet rendering, including the
// partial (note-only) path and the pain side/site composition.
func TestObservationLine(t *testing.T) {
	cases := []struct {
		name string
		ev   observations.Event
		want string
	}{
		{"mood scale+word", observations.Event{Kind: observations.KindMood, Payload: map[string]any{"level": 7, "word": "steady"}}, "mood 7 — steady"},
		{"mood partial note", observations.Event{Kind: observations.KindMood, Payload: map[string]any{"note": "wired feeling"}}, "mood — wired feeling"},
		{"mood float round-trip", observations.Event{Kind: observations.KindMood, Payload: map[string]any{"level": float64(4)}}, "mood 4"},
		{"pain side+site", observations.Event{Kind: observations.KindPain, Payload: map[string]any{"intensity": 4, "site": "back", "side": "left"}}, "pain 4 — left back"},
		{"pain note fallback", observations.Event{Kind: observations.KindPain, Payload: map[string]any{"intensity": 3, "note": "dull ache"}}, "pain 3 — dull ache"},
		{"sleep quality", observations.Event{Kind: observations.KindSleep, Payload: map[string]any{"quality": 8}}, "sleep · quality 8"},
		{"symptom name+severity", observations.Event{Kind: observations.KindSymptom, Payload: map[string]any{"name": "headache", "severity": 6}}, "headache 6"},
		{"withdrawal scale+note", observations.Event{Kind: observations.KindWithdrawal, Payload: map[string]any{"severity": 6, "note": "rough morning"}}, "withdrawal 6 — rough morning"},
		{"habit_change scale+note", observations.Event{Kind: observations.KindHabitChange, Payload: map[string]any{"load": 7, "note": "cut coffee"}}, "habit change 7 — cut coffee"},
		{"commitment what", observations.Event{Kind: observations.KindCommitment, Payload: map[string]any{"what": "call the dentist"}}, "call the dentist"},
		{"commitment partial note", observations.Event{Kind: observations.KindCommitment, Payload: map[string]any{"note": "email the landlord"}}, "email the landlord"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, observationLine(tc.ev))
		})
	}
}
