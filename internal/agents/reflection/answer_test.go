package reflection

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// oneInsight is the standard single-insight slice the answer tests ground on.
func oneInsight() []InsightView {
	return []InsightView{{ID: "i_2026_05_05_a", Statement: "I go quiet in groups."}}
}

// answerExchange builds a valid answer completion citing the given insight id.
func answerExchange(id string) provider.Exchange {
	return provider.Exchange{Content: fmt.Sprintf(
		`{"outcome":"answer","answer_text":"Based on what you validated, %s.","citations":[{"kind":"insight","id":%q}]}`,
		"you tend to go quiet", id)}
}

// TestAnswer_EmptySlice_NoLLM is acceptance case 7.2 / §R-10: an empty store
// short-circuits to insufficient with no model call.
func TestAnswer_EmptySlice_NoLLM(t *testing.T) {
	p := &provider.Fake{}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "what have I learned?"}, p)

	assert.Equal(t, OutcomeInsufficient, res.Outcome)
	assert.True(t, res.NoLLM, "empty slice is answered without a model call")
	assert.Equal(t, answerEmptyStore, res.AnswerText)
	assert.Equal(t, 0, p.Calls(), "no completion is made")
	assert.Empty(t, res.Citations)
}

// TestAnswer_ValidInSlice is acceptance case 7.1: a populated store yields an
// answer whose citations are all in the supplied slice.
func TestAnswer_ValidInSlice(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{answerExchange("i_2026_05_05_a")}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "how do I act in groups?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeAnswer, res.Outcome)
	require.Len(t, res.Citations, 1)
	assert.Equal(t, Citation{Kind: CitationInsight, ID: "i_2026_05_05_a"}, res.Citations[0])
	assert.False(t, res.Fallback)
	assert.Equal(t, 1, p.Calls())
}

// TestAnswer_ReflectionCitation confirms a reflection-kind citation is grounded
// against the reflections slice, not the insights slice.
func TestAnswer_ReflectionCitation(t *testing.T) {
	content := `{"outcome":"answer","answer_text":"Your week 18 recall noted it.",` +
		`"citations":[{"kind":"reflection","id":"reflection_2026_w18"}]}`
	p := &provider.Fake{Script: []provider.Exchange{{Content: content}}}
	in := AnswerInput{
		Question:    "anything from my recalls?",
		Insights:    oneInsight(),
		Reflections: []WeeklyReflectionView{{ID: "reflection_2026_w18", Summary: "Confirmed one insight."}},
	}
	res := AnswerGrounded(context.Background(), in, p)

	assert.Equal(t, OutcomeAnswer, res.Outcome)
	require.Len(t, res.Citations, 1)
	assert.Equal(t, CitationReflection, res.Citations[0].Kind)
	assert.False(t, res.Fallback)
}

// TestAnswer_OutOfSlice_RetriesThenReturns is acceptance case 7.3 / §R-13: an
// answer citing an id outside the slice is retried once, and if still
// out-of-slice is returned unchanged so the router's Safety gate blocks it.
func TestAnswer_OutOfSlice_RetriesThenReturns(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		answerExchange("i_2026_99_99_z"),
		answerExchange("i_2026_99_99_z"),
	}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeAnswer, res.Outcome, "still an answer so Safety can block it (Sf-7)")
	assert.True(t, res.Fallback, "the out-of-slice degrade is marked")
	require.Len(t, res.Citations, 1)
	assert.Equal(t, "i_2026_99_99_z", res.Citations[0].ID)
	assert.Equal(t, 2, p.Calls(), "one retry with the slice ids restated")
}

// TestAnswer_OutOfSliceThenInSlice confirms the retry recovers when the second
// attempt cites a valid id (§R-13 happy retry).
func TestAnswer_OutOfSliceThenInSlice(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		answerExchange("i_2026_99_99_z"),
		answerExchange("i_2026_05_05_a"),
	}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeAnswer, res.Outcome)
	assert.False(t, res.Fallback)
	require.Len(t, res.Citations, 1)
	assert.Equal(t, "i_2026_05_05_a", res.Citations[0].ID)
	assert.Equal(t, 2, p.Calls())
}

// TestAnswer_ModelInsufficient confirms a model-reported insufficient over a
// non-empty slice surfaces the fixed insufficient copy (agent-contracts §3 (b)).
func TestAnswer_ModelInsufficient(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Content: `{"outcome":"insufficient","answer_text":"nope","citations":[]}`},
	}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeInsufficient, res.Outcome)
	assert.Equal(t, answerModelInsufficient, res.AnswerText)
	assert.Empty(t, res.Citations)
	assert.Equal(t, 1, p.Calls())
}

// TestAnswer_MalformedTwice_Insufficient is §R-12: two malformed replies degrade
// to insufficient with the malformed fallback and no partial output.
func TestAnswer_MalformedTwice_Insufficient(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{{Content: "not json"}, {Content: `{"outcome":"answer"}`}}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeInsufficient, res.Outcome)
	assert.Equal(t, answerMalformedFallback, res.AnswerText)
	assert.True(t, res.Fallback)
	assert.Empty(t, res.Citations)
	assert.Equal(t, 2, p.Calls(), "one retry before degrading")
}

// TestAnswer_MalformedThenValid is §R-11: a first malformed reply is retried and
// the valid second attempt is used.
func TestAnswer_MalformedThenValid(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{{Content: "not json"}, answerExchange("i_2026_05_05_a")}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeAnswer, res.Outcome)
	assert.Equal(t, 2, p.Calls())
}

// TestAnswer_TransportPersists_N3 is acceptance case 7.6 / §N-2/§N-3: a transport
// failure is retried once and, when it persists, surfaces the transient message
// with no partial output.
func TestAnswer_TransportPersists_N3(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Err: fmt.Errorf("net: %w", provider.ErrTimeout)},
		{Err: fmt.Errorf("net: %w", provider.ErrTimeout)},
	}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeInsufficient, res.Outcome)
	assert.Equal(t, answerTransportFallback, res.AnswerText)
	assert.True(t, res.Fallback)
	assert.Empty(t, res.Citations, "no partial output")
	assert.Equal(t, 2, p.Calls(), "exactly one retry")
}

// TestAnswer_TransportThenValid confirms a transient first failure recovers on
// the retry (§N-2 happy path).
func TestAnswer_TransportThenValid(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Err: fmt.Errorf("net: %w", provider.ErrUnavailable)},
		answerExchange("i_2026_05_05_a"),
	}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeAnswer, res.Outcome)
	assert.Equal(t, 2, p.Calls())
}

// TestAnswer_AnswerWithEmptyCitations_IsInvalid confirms an answer with no
// citations is structurally invalid and degrades (validAnswer rule).
func TestAnswer_AnswerWithEmptyCitations_IsInvalid(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Content: `{"outcome":"answer","answer_text":"grounded in nothing","citations":[]}`},
		{Content: `{"outcome":"answer","answer_text":"grounded in nothing","citations":[]}`},
	}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeInsufficient, res.Outcome)
	assert.Equal(t, 2, p.Calls())
}

// TestAnswer_UnknownCitationKind_IsOutOfSlice confirms a citation with an
// unrecognized kind is treated as out-of-slice.
func TestAnswer_UnknownCitationKind_IsOutOfSlice(t *testing.T) {
	content := `{"outcome":"answer","answer_text":"a","citations":[{"kind":"raw","id":"i_2026_05_05_a"}]}`
	p := &provider.Fake{Script: []provider.Exchange{{Content: content}, {Content: content}}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeAnswer, res.Outcome)
	assert.True(t, res.Fallback, "unknown-kind citation is out-of-slice, returned for Safety to block")
}

// TestAnswer_OutOfSliceReflection confirms a reflection citation absent from the
// reflections slice is out-of-slice (the reflection-kind branch of the check).
func TestAnswer_OutOfSliceReflection(t *testing.T) {
	bad := `{"outcome":"answer","answer_text":"x","citations":[{"kind":"reflection","id":"reflection_2026_w99"}]}`
	p := &provider.Fake{Script: []provider.Exchange{{Content: bad}, {Content: bad}}}
	in := AnswerInput{
		Question:    "q?",
		Insights:    oneInsight(),
		Reflections: []WeeklyReflectionView{{ID: "reflection_2026_w18", Summary: "s"}},
	}
	res := AnswerGrounded(context.Background(), in, p)

	assert.Equal(t, OutcomeAnswer, res.Outcome)
	assert.True(t, res.Fallback)
	assert.Equal(t, 2, p.Calls())
}

// TestAnswer_UnknownOutcome_IsInvalid confirms a reply carrying a non-answer,
// non-insufficient outcome is structurally invalid and degrades.
func TestAnswer_UnknownOutcome_IsInvalid(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Content: `{"outcome":"mystery","answer_text":"x","citations":[]}`}, // off-enum → default branch
		{Content: `{"outcome":"proposal","answer_text":"x","citations":[]}`},
	}}
	res := AnswerGrounded(context.Background(), AnswerInput{Question: "q?", Insights: oneInsight()}, p)

	assert.Equal(t, OutcomeInsufficient, res.Outcome)
	assert.Equal(t, 2, p.Calls())
}

// TestAnswer_StrictRetryRestatesReflectionIDs exercises the strict-retry prompt
// path with a reflection in the slice, so citableIDs enumerates both kinds.
func TestAnswer_StrictRetryRestatesReflectionIDs(t *testing.T) {
	in := AnswerInput{
		Question:    "anything?",
		Insights:    oneInsight(),
		Reflections: []WeeklyReflectionView{{ID: "reflection_2026_w18", Summary: "Confirmed one."}},
	}
	reflectionAnswer := `{"outcome":"answer","answer_text":"per your recall","citations":[{"kind":"reflection","id":"reflection_2026_w18"}]}`
	p := &provider.Fake{Script: []provider.Exchange{answerExchange("i_2026_99_99_z"), {Content: reflectionAnswer}}}
	res := AnswerGrounded(context.Background(), in, p)

	assert.Equal(t, OutcomeAnswer, res.Outcome)
	assert.False(t, res.Fallback)
	// The strict retry's prompt restated every citable id, both kinds.
	require.Len(t, p.Requests, 2)
	strict := p.Requests[1].System
	assert.Contains(t, strict, "i_2026_05_05_a")
	assert.Contains(t, strict, "reflection_2026_w18")
}

// TestAnswer_SliceIsBounded confirms the agent sends only the question and the
// two slices — never a Ledger handle (the slice is the whole authorized input).
func TestAnswer_SliceIsBounded(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{answerExchange("i_2026_05_05_a")}}
	AnswerGrounded(context.Background(), AnswerInput{Question: "how do I act in groups?", Insights: oneInsight()}, p)

	require.Len(t, p.Requests, 1)
	require.Len(t, p.Requests[0].Messages, 1)
	body := p.Requests[0].Messages[0].Content
	assert.Contains(t, body, "how do I act in groups?")
	assert.Contains(t, body, "i_2026_05_05_a")
	assert.Equal(t, "reflection.answer_grounded", p.Requests[0].Intent)
}
