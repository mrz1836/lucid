package witnessreport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
)

// wellFormedReply is a canonical four-slot model reply the compose tests script
// the fake with — clean, witness-safe prose plus two grounded asks.
const wellFormedReply = `%%FAULTS%%
Two days slipped midweek — a wobble, not a collapse.

%%PROGRESS%%
The back half of the week closed strong.

%%NARRATIVE%%
A mixed week, honestly read. The numbers above tell it straight; a midweek nudge would help.

%%ASKS%%
- Check in on me Wednesday.
- Ask how the back half went.`

// fixtureReaders returns the honest-numbers and day-record seams for a synthetic
// fixture week, folded through the real engine projection exactly as the
// CLI/scheduler will — only the readers are fakes, the numbers are honest.
func fixtureReaders(t *testing.T, name string) (fakeNumbers, fakeRecords) {
	t.Helper()
	recs := loadFixture(t, name)
	clocks, err := engine.DefaultChain().ClocksFor(engine.DefaultProfile)
	require.NoError(t, err)
	m := engine.BuildMetrics(engine.MetricsInput{
		Records: recs,
		Chain:   engine.DefaultChain(),
		Now:     reportNow(),
		Clocks:  clocks,
		Loc:     reportNow().Location(),
	})
	return fakeNumbers{result: router.MetricsResult{Metrics: m}}, fakeRecords{days: recs}
}

// writeFile writes content to a temp file and returns its path — the opaque
// prompt/curated-asks path the composer reads.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

// composerDeps builds a Composer over a fixture week and a scripted provider
// fake, with real (temp) system-prompt and template files. asksFile is the
// optional curated-asks path ("" leaves it unconfigured). It returns the fake so
// a test can assert what request reached the model.
func composerDeps(t *testing.T, fixture, asksFile string, script ...provider.Exchange) (*Composer, *provider.Fake) {
	t.Helper()
	dir := t.TempDir()
	numbers, records := fixtureReaders(t, fixture)
	fake := &provider.Fake{Script: script}
	c := New(Deps{
		SystemPrompt: writeFile(t, dir, "system.md", "You are the witness-report voice."),
		Template:     writeFile(t, dir, "template.md", "Weekly witness report template."),
		AsksFile:     asksFile,
		Provider:     config.ProviderConfig{Backend: "fake"},
		Numbers:      numbers,
		Records:      records,
		Build:        func(config.ProviderConfig) (provider.Provider, error) { return fake, nil },
	})
	return c, fake
}

// TestCompose_NormalFillsSlots: a well-formed reply fills the narrative slots,
// the refined asks land (the week has real signal), and the honest numbers are
// untouched. UsedLLM is set; neither fallback flag is.
func TestCompose_NormalFillsSlots(t *testing.T) {
	c, fake := composerDeps(t, "miss-heavy-week.json", "", provider.Exchange{Content: wellFormedReply})

	r, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	assert.True(t, r.UsedLLM)
	assert.False(t, r.Fallback)
	assert.False(t, r.SafetyTripped)
	assert.Equal(t, "Two days slipped midweek — a wobble, not a collapse.", r.Faults)
	assert.Equal(t, "The back half of the week closed strong.", r.Progress)
	assert.Contains(t, r.Narrative, "A mixed week")
	assert.Equal(t, []string{"Check in on me Wednesday.", "Ask how the back half went."}, r.Asks)

	// The honest numbers survive the compose pass unchanged.
	assert.Equal(t, 1, r.Streak)
	assert.Equal(t, 4, r.WeekMisses)
	assert.Equal(t, 1, fake.Calls(), "exactly one model call")
}

// TestCompose_RequestCarriesOnlyWitnessSafeInput is the input-restriction guard:
// the single message that reaches the model is exactly composeBody over the
// deterministic Report — the template, the numbers digest, the watch-outs, and
// the drafted asks — and carries no private-data field, because no reader is
// wired to produce one. The intent + system prompt are the audit-attributable
// companion shape.
func TestCompose_RequestCarriesOnlyWitnessSafeInput(t *testing.T) {
	c, fake := composerDeps(t, "miss-heavy-week.json", "", provider.Exchange{Content: wellFormedReply})

	numbers, records := fixtureReaders(t, "miss-heavy-week.json")
	r, err := BuildReport(reportNow(), numbers, records)
	require.NoError(t, err)

	_, composeErr := c.Compose(context.Background(), reportNow())
	require.NoError(t, composeErr)

	require.Len(t, fake.Requests, 1)
	req := fake.Requests[0]
	assert.Equal(t, intentWitnessReport, req.Intent)
	assert.Equal(t, "You are the witness-report voice.", req.System)
	require.Len(t, req.Messages, 1)
	assert.Equal(t, provider.RoleUser, req.Messages[0].Role)
	// The body is exactly the witness-safe composeBody — nothing else can reach
	// the model, because the only inputs the composer holds are the numbers and
	// day-record readers.
	assert.Equal(t, composeBody("Weekly witness report template.", r), req.Messages[0].Content)
	assert.NotContains(t, req.Messages[0].Content, "raw/", "no ledger path in the model input")
}

// TestCompose_NoSignalKeepsGenericAsk is the AC-4 guard through compose: on a
// signal-free week the composer keeps its single honest generic ask even when
// the model returns specific asks — it never fabricates friend-facing specifics.
func TestCompose_NoSignalKeepsGenericAsk(t *testing.T) {
	c, _ := composerDeps(t, "strong-week.json", "", provider.Exchange{Content: wellFormedReply})

	r, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	assert.True(t, r.UsedLLM, "the prose still lands")
	assert.Equal(t, []string{askGeneric}, r.Asks, "a signal-free week never grows fabricated specifics")
}

// TestCompose_WitnessSafeTripFailsClosed is the AC-6 fail-closed guard: a reply
// whose narrative plants a private-detail marker is discarded entirely — the
// prose never lands — and the composer returns the deterministic metrics-only
// report with SafetyTripped set.
func TestCompose_WitnessSafeTripFailsClosed(t *testing.T) {
	unsafeReply := `%%FAULTS%%
Two days slipped midweek.

%%NARRATIVE%%
This ties back to [[the-hard-monday]] and my therapist's read on it.

%%ASKS%%
- Check in on me Wednesday.`
	c, _ := composerDeps(t, "miss-heavy-week.json", "", provider.Exchange{Content: unsafeReply})

	r, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	assert.True(t, r.SafetyTripped, "the planted private detail trips the scan")
	assert.True(t, r.Fallback, "a tripped scan falls back to the metrics-only report")
	assert.False(t, r.UsedLLM)
	assert.Empty(t, r.Faults, "no model prose survives a trip")
	assert.Empty(t, r.Narrative)
	// The deterministic report still lands whole.
	assert.Equal(t, 4, r.WeekMisses)
	assert.Equal(t, []string{askMidweekCheckIn, askChainHeld}, r.Asks, "the deterministic asks stand")
}

// TestCompose_TwoWeeksDifferAfterCompose is the AC-8 guard through the full
// path: two different weeks composed with the same reply still render materially
// different reports, because the numbers — which the model never owns — differ.
func TestCompose_TwoWeeksDifferAfterCompose(t *testing.T) {
	cStrong, _ := composerDeps(t, "strong-week.json", "", provider.Exchange{Content: wellFormedReply})
	cHeavy, _ := composerDeps(t, "miss-heavy-week.json", "", provider.Exchange{Content: wellFormedReply})

	strong, err := cStrong.Compose(context.Background(), reportNow())
	require.NoError(t, err)
	heavy, err := cHeavy.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	assert.NotEqual(t, RenderEmbed(strong), RenderEmbed(heavy), "the rendered reports differ materially")
	assert.NotEqual(t, strong.WatchOuts, heavy.WatchOuts)
}

// TestCompose_MissingPromptIsLoud: a missing/empty required prompt path is a
// loud configuration error, never a silent unwarm send.
func TestCompose_MissingPromptIsLoud(t *testing.T) {
	numbers, records := fixtureReaders(t, "strong-week.json")
	c := New(Deps{
		SystemPrompt: "", // unset
		Template:     "",
		Provider:     config.ProviderConfig{Backend: "fake"},
		Numbers:      numbers,
		Records:      records,
		Build: func(config.ProviderConfig) (provider.Provider, error) {
			return &provider.Fake{}, nil
		},
	})
	_, err := c.Compose(context.Background(), reportNow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "system prompt")
}

// TestCompose_UnreadableTemplateIsLoud: a set-but-unreadable required template
// path is loud too — the prose is the report, never silently dropped.
func TestCompose_UnreadableTemplateIsLoud(t *testing.T) {
	dir := t.TempDir()
	numbers, records := fixtureReaders(t, "strong-week.json")
	c := New(Deps{
		SystemPrompt: writeFile(t, dir, "system.md", "voice"),
		Template:     filepath.Join(dir, "does-not-exist.md"),
		Provider:     config.ProviderConfig{Backend: "fake"},
		Numbers:      numbers,
		Records:      records,
		Build: func(config.ProviderConfig) (provider.Provider, error) {
			return &provider.Fake{}, nil
		},
	})
	_, err := c.Compose(context.Background(), reportNow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template")
}

// TestCompose_ModelOverride: a set witness-report Model overrides provider.model
// for the compose call; an empty override inherits the provider default. New's
// default builder is bypassed so the resolved config is observable.
func TestCompose_ModelOverride(t *testing.T) {
	dir := t.TempDir()
	numbers, records := fixtureReaders(t, "strong-week.json")
	var gotModel string
	c := New(Deps{
		SystemPrompt: writeFile(t, dir, "system.md", "voice"),
		Template:     writeFile(t, dir, "template.md", "tmpl"),
		Model:        "sonnet",
		Provider:     config.ProviderConfig{Backend: "fake", Model: "opus"},
		Numbers:      numbers,
		Records:      records,
		Build: func(pc config.ProviderConfig) (provider.Provider, error) {
			gotModel = pc.Model
			return &provider.Fake{Script: []provider.Exchange{{Content: wellFormedReply}}}, nil
		},
	})
	_, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)
	assert.Equal(t, "sonnet", gotModel, "the witness-report model override wins over provider.model")
}

// TestReadCuratedAsks_BoundsAndSkipsComments: the curated-asks reader skips
// comment/header lines, strips bullets, and bounds the result to three so the
// friend-facing section never becomes a wall.
func TestReadCuratedAsks_BoundsAndSkipsComments(t *testing.T) {
	p := writeFile(t, t.TempDir(), "asks.md",
		"# my asks\n<!-- draft -->\n- first\n* second\n• third\n- fourth\n")
	got := readCuratedAsks(p)
	assert.Equal(t, []string{"first", "second", "third"}, got, "bounded to three, bullets stripped, comments skipped")
}
