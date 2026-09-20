package workout

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// TestComposeTemplateReadErrorIsLoud proves the template read stays loud: an empty
// template path fails the compose after the system prompt reads cleanly, rather
// than composing over an empty operator voice.
func TestComposeTemplateReadErrorIsLoud(t *testing.T) {
	t.Parallel()

	deps := baseDeps(t, &fakeObs{}, fakeInjuries{}, &provider.Fake{})
	deps.Workout.Template = ""
	_, err := New(deps).Compose(context.Background(), mustTime(t, mondayNoon))
	require.Error(t, err)
}

// TestComposeProviderBuildErrorIsLoud proves a backend that fails to build is a
// loud error — the phrasing call cannot proceed without a provider, and the
// compose never falls silently to an empty message.
func TestComposeProviderBuildErrorIsLoud(t *testing.T) {
	t.Parallel()

	deps := baseDeps(t, &fakeObs{}, fakeInjuries{}, &provider.Fake{})
	deps.Build = func(config.ProviderConfig) (provider.Provider, error) {
		return nil, errors.New("bad backend")
	}
	_, err := New(deps).Compose(context.Background(), mustTime(t, mondayNoon))
	require.Error(t, err)
}

// TestComposeNonSentinelProviderErrorIsLoud proves a model error that is neither a
// timeout nor an unavailable sentinel is a loud failure, not the deterministic
// fallback — only the two outage sentinels degrade quietly.
func TestComposeNonSentinelProviderErrorIsLoud(t *testing.T) {
	t.Parallel()

	fake := &provider.Fake{Script: []provider.Exchange{{Err: errors.New("boom")}}}
	_, err := New(baseDeps(t, &fakeObs{}, fakeInjuries{}, fake)).Compose(context.Background(), mustTime(t, mondayNoon))
	require.Error(t, err)
}

// TestComposeNilEnrichmentReadersComposeCleanly proves an unwired
// observations/injury seam is not a degrade: with nil readers the message
// composes on the plain calendar and EnrichmentDegraded stays false (a nil reader
// is "no enrichment configured", not "enrichment failed").
func TestComposeNilEnrichmentReadersComposeCleanly(t *testing.T) {
	t.Parallel()

	deps := baseDeps(t, &fakeObs{}, fakeInjuries{}, &provider.Fake{Script: []provider.Exchange{{Content: "note"}}})
	deps.Observations = nil
	deps.Injuries = nil

	res, err := New(deps).Compose(context.Background(), mustTime(t, mondayNoon))
	require.NoError(t, err)
	assert.False(t, res.EnrichmentDegraded, "an unwired reader is not a degraded read")
	assert.Equal(t, "legs", res.Recommendation.Primary.ID)
}

// TestComposeInsightFlowsIntoProgress proves the per-part next-day pain-response
// insight computed from the recent slice reaches both the rendered progress panel
// and the model's grounding digest — three loaded sessions with rising next-day
// readings surface the part's trend on the card and in the digest the model grounds
// on without restating.
func TestComposeInsightFlowsIntoProgress(t *testing.T) {
	t.Parallel()

	obs := &fakeObs{events: []observations.Event{
		sessionOn("2026-06-29", "legs", 8),
		sessionOn("2026-07-03", "legs", 8),
		sessionOn("2026-07-07", "legs", 8),
		bodyStateOn("2026-06-30", "legs", 3, -1),
		bodyStateOn("2026-07-04", "legs", 5, -1),
		bodyStateOn("2026-07-08", "legs", 7, -1),
	}}
	fake := &provider.Fake{Script: []provider.Exchange{{Content: "note"}}}
	res, err := New(baseDeps(t, obs, fakeInjuries{}, fake)).Compose(context.Background(), mustTime(t, mondayNoon))
	require.NoError(t, err)

	assert.Contains(t, res.Text, "legs — next-day pain/soreness rising", "the insight renders in the progress panel")
	require.Equal(t, 1, fake.Calls())
	assert.Contains(t, fake.Requests[0].Messages[0].Content, "next-day pain/soreness rising",
		"the model grounds on the same insight it never restates")
}

// TestRenderWithNoteEmptyNoteFallsBackToSpine proves the note-prepend helper
// renders the pure deterministic spine when handed a blank note — the defensive
// guard that keeps a whitespace-only model reply from prefixing a hollow coach
// emoji.
func TestRenderWithNoteEmptyNoteFallsBackToSpine(t *testing.T) {
	t.Parallel()

	now := mustTime(t, mondayNoon)
	prog := ExampleProgram()
	rec := Recommend(RecommendInput{Program: prog, Now: now, Loc: time.UTC})
	tr := BuildTrend(TrendInput{Now: now, Loc: time.UTC})
	anchor := BuildAnchor(prog, now, time.UTC)

	assert.Equal(t, Render(rec, tr, anchor, now), renderWithNote("   ", rec, tr, anchor, now),
		"a blank note renders exactly the deterministic spine")
}
