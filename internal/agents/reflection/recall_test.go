package reflection

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// recallWindow is a two-insight window: one plain, one ruled.
func recallWindow() []InsightView {
	return []InsightView{
		{ID: "i_2026_05_07_a", Statement: "I go quiet in groups.", Rule: ""},
		{ID: "i_2026_05_05_a", Statement: "I test an idea once and back off.", Rule: "Finish the sentence."},
	}
}

// recallExchange builds a valid recall completion surfacing the given ids.
func recallExchange(pairs ...[2]string) provider.Exchange {
	entries := make([]string, 0, len(pairs))
	for _, p := range pairs {
		entries = append(entries, fmt.Sprintf(`{"id":%q,"surface_text":%q}`, p[0], p[1]))
	}
	return provider.Exchange{Content: fmt.Sprintf(`{"outcome":"recall","ordered_insights":[%s]}`, strings.Join(entries, ","))}
}

// TestSurfaceForRecall_HappyPath surfaces both window insights with the model's
// wording, carries the rule through, and makes exactly one model call.
func TestSurfaceForRecall_HappyPath(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{recallExchange(
		[2]string{"i_2026_05_07_a", "Does 'I go quiet in groups' still fit?"},
		[2]string{"i_2026_05_05_a", "Does testing an idea once still fit?"},
	)}}
	res := SurfaceForRecall(context.Background(), RecallInput{Scope: ScopeWeek, Window: recallWindow()}, p)

	assert.Equal(t, OutcomeRecall, res.Outcome)
	assert.False(t, res.Fallback)
	require.Len(t, res.Ordered, 2)
	assert.Equal(t, "Does 'I go quiet in groups' still fit?", res.Ordered[0].Resonance)
	assert.False(t, res.Ordered[0].Ruled)
	assert.True(t, res.Ordered[1].Ruled, "the second insight carries a rule")
	assert.Equal(t, "Finish the sentence.", res.Ordered[1].Rule)
	assert.Equal(t, 1, p.Calls())
}

// TestSurfaceForRecall_EmptyWindow short-circuits with no model call.
func TestSurfaceForRecall_EmptyWindow(t *testing.T) {
	p := &provider.Fake{}
	res := SurfaceForRecall(context.Background(), RecallInput{Scope: ScopeWeek, Window: nil}, p)
	assert.True(t, res.NoLLM)
	assert.Empty(t, res.Ordered)
	assert.Equal(t, 0, p.Calls(), "an empty window never calls the model")
}

// TestSurfaceForRecall_MalformedTwiceFallsBackVerbatim is the §R-8 path: two
// malformed replies degrade to surfacing every insight verbatim, in window
// order, with no novel framing.
func TestSurfaceForRecall_MalformedTwiceFallsBackVerbatim(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{{Content: "not json"}, {Content: "{still bad"}}}
	res := SurfaceForRecall(context.Background(), RecallInput{Scope: ScopeWeek, Window: recallWindow()}, p)

	assert.True(t, res.Fallback)
	require.Len(t, res.Ordered, 2)
	assert.Equal(t, VerbatimResonance("I go quiet in groups."), res.Ordered[0].Resonance)
	assert.Equal(t, VerbatimResonance("I test an idea once and back off."), res.Ordered[1].Resonance)
	assert.True(t, res.Ordered[1].Ruled)
	assert.Equal(t, 2, p.Calls(), "one attempt plus one retry")
}

// TestSurfaceForRecall_RejectsNovelIDThenFallsBack rejects a reply citing an id
// outside the window and degrades to the verbatim fallback.
func TestSurfaceForRecall_RejectsNovelIDThenFallsBack(t *testing.T) {
	novel := recallExchange([2]string{"i_9999_99_99_z", "not in the window"})
	p := &provider.Fake{Script: []provider.Exchange{novel, novel}}
	res := SurfaceForRecall(context.Background(), RecallInput{Scope: ScopeWeek, Window: recallWindow()}, p)
	assert.True(t, res.Fallback, "a novel id is never surfaced")
	require.Len(t, res.Ordered, 2)
}

// TestSurfaceForRecall_EmptySurfaceTextInvalid rejects a reply with a blank
// surface_text, then a valid retry is accepted.
func TestSurfaceForRecall_EmptySurfaceTextInvalid(t *testing.T) {
	bad := recallExchange([2]string{"i_2026_05_07_a", ""})
	good := recallExchange(
		[2]string{"i_2026_05_07_a", "Still fits?"},
		[2]string{"i_2026_05_05_a", "Still fits?"},
	)
	p := &provider.Fake{Script: []provider.Exchange{bad, good}}
	res := SurfaceForRecall(context.Background(), RecallInput{Scope: ScopeWeek, Window: recallWindow()}, p)
	assert.False(t, res.Fallback, "the valid retry is used")
	require.Len(t, res.Ordered, 2)
	assert.Equal(t, 2, p.Calls())
}

// TestSurfaceForRecall_TransportErrorFallsBack degrades when the model is
// unreachable on both attempts.
func TestSurfaceForRecall_TransportErrorFallsBack(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Err: provider.ErrTimeout}, {Err: provider.ErrUnavailable},
	}}
	res := SurfaceForRecall(context.Background(), RecallInput{Scope: ScopeGate, Window: recallWindow()}, p)
	assert.True(t, res.Fallback)
	require.Len(t, res.Ordered, 2)
}

// TestSurfaceForRecall_EmptyOrderedInvalid rejects a well-formed reply that
// surfaces nothing, then degrades.
func TestSurfaceForRecall_EmptyOrderedInvalid(t *testing.T) {
	empty := provider.Exchange{Content: `{"outcome":"recall","ordered_insights":[]}`}
	p := &provider.Fake{Script: []provider.Exchange{empty, empty}}
	res := SurfaceForRecall(context.Background(), RecallInput{Scope: ScopeWeek, Window: recallWindow()}, p)
	assert.True(t, res.Fallback)
}

// TestVerbatimResonance covers the fixed line format.
func TestVerbatimResonance(t *testing.T) {
	assert.Equal(t, "Earlier you saved: 'X'. Still resonating?", VerbatimResonance("  X  "))
}

// TestRecallSystem_GateMentionsGate confirms the gate scope frames the prompt
// and the prompt stays clean of a novel-pattern instruction.
func TestRecallSystem_GateMentionsGate(t *testing.T) {
	sys := recallSystem(RecallInput{Scope: ScopeGate, Window: recallWindow()}, false)
	assert.Contains(t, sys, "gate review")
	strict := recallSystem(RecallInput{Scope: ScopeWeek, Window: recallWindow()}, true)
	assert.Contains(t, strict, "not valid")
}
