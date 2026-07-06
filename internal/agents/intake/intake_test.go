package intake_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/intake"
	"github.com/mrz1836/lucid/internal/provider"
)

// scriptResponder replays a fixed list of user turns, one per Answer call,
// recording the questions it was asked. It stands in for the chat thread.
type scriptResponder struct {
	turns []intake.Turn
	asked []string
	i     int
}

func (s *scriptResponder) Answer(question string) (intake.Turn, error) {
	s.asked = append(s.asked, question)
	if s.i >= len(s.turns) {
		return intake.Turn{}, fmt.Errorf("responder: no scripted turn for question %q", question)
	}
	t := s.turns[s.i]
	s.i++
	return t, nil
}

// answer builds an ordinary answer turn.
func answer(text string) intake.Turn { return intake.Turn{Text: text} }

// ask is a decide reply that asks a question.
func ask(q string) provider.Exchange {
	return provider.Exchange{Content: fmt.Sprintf(`{"done": false, "question": %q}`, q)}
}

// done is a decide reply that stops asking.
func done() provider.Exchange { return provider.Exchange{Content: `{"done": true}`} }

// bundle is a bundling reply carrying the given body.
func bundle(text string) provider.Exchange {
	return provider.Exchange{Content: fmt.Sprintf(`{"bundled_text": %q}`, text)}
}

// input is the standard synthetic Intake input (cap 4).
func input(opening string) intake.Input {
	return intake.Input{OpeningMessage: opening, MaxQuestions: 4, AgentVersion: "intake-2026.05.0"}
}

// TestGather_ThinOpeningTwoAnswers is acceptance case 3.1: a thin opening
// plus two answers stops "satisfied" with exactly two questions.
func TestGather_ThinOpeningTwoAnswers(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		ask("What happened?"),
		ask("How did it land?"),
		done(),
		bundle("Rough dinner tonight.\n\nI tried to push back and then dropped it.\n\nAnnoyed and embarrassed."),
	}}
	r := &scriptResponder{turns: []intake.Turn{
		answer("I tried to push back and then dropped it."),
		answer("Annoyed and embarrassed."),
	}}

	res, err := intake.Gather(context.Background(), input("Rough dinner tonight."), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopSatisfied, res.StopReason)
	assert.Len(t, res.QuestionsAsked, 2)
	assert.Len(t, res.Answers, 2)
	assert.False(t, res.LLMFailed)
	assert.NotEmpty(t, res.BundledText)
}

// TestGather_MaxQuestionsCap is acceptance case 3.2: four answers hit the
// cap and stop "max_questions_reached" with exactly four questions.
func TestGather_MaxQuestionsCap(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		ask("q1"), ask("q2"), ask("q3"), ask("q4"),
		bundle("a1. a2. a3. a4."),
	}}
	r := &scriptResponder{turns: []intake.Turn{
		answer("a1"), answer("a2"), answer("a3"), answer("a4"),
	}}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopMaxQuestions, res.StopReason)
	assert.Len(t, res.QuestionsAsked, 4)
	assert.Equal(t, 5, p.Calls(), "four decides plus one bundle")
}

// TestGather_CancelFirstQuestion is acceptance case 3.3: /cancel before
// answering leaves zero answers and a user_exit — nothing to save.
func TestGather_CancelFirstQuestion(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{ask("What happened?")}}
	r := &scriptResponder{turns: []intake.Turn{{Control: intake.ControlCancel}}}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopUserExit, res.StopReason)
	assert.Empty(t, res.Answers)
	assert.Empty(t, res.QuestionsAsked)
	assert.False(t, res.LLMFailed)
	assert.Empty(t, res.BundledText)
	assert.Equal(t, 1, p.Calls(), "no bundling call when there is nothing to bundle")
}

// TestGather_DoneAfterTwoAnswers is acceptance case 3.4: /done after two
// answers returns a user_exit with the partial bundle intact.
func TestGather_DoneAfterTwoAnswers(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		ask("q1"), ask("q2"), ask("q3"),
		bundle("first answer. second answer."),
	}}
	r := &scriptResponder{turns: []intake.Turn{
		answer("first answer"), answer("second answer"), {Control: intake.ControlDone},
	}}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopUserExit, res.StopReason)
	assert.Len(t, res.Answers, 2)
	assert.False(t, res.LLMFailed)
	assert.NotEmpty(t, res.BundledText)
}

// TestGather_MalformedTwice is acceptance case 3.5 / error-states.md §I-2:
// a malformed decision twice downgrades to a user_exit with no answers and
// the LLMFailed flag set, and no bundling is attempted.
func TestGather_MalformedTwice(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Content: "not json"},
		{Content: "still not json"},
	}}
	r := &scriptResponder{}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopUserExit, res.StopReason)
	assert.True(t, res.LLMFailed)
	assert.Empty(t, res.Answers)
	assert.Equal(t, 2, p.Calls(), "one decide plus one stricter retry, then downgrade")
	assert.Empty(t, r.asked, "the user is never asked when the model fails to open")
}

// TestGather_MalformedThenValidRetry covers the §I-1 success path: the
// first decide is malformed, the stricter retry parses, and Intake
// continues normally.
func TestGather_MalformedThenValidRetry(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Content: "oops"},       // first decide malformed
		ask("What happened?"),   // stricter retry parses
		ask("How did it land?"), // second question
		done(),                  // stop
		bundle("a1. a2."),       // bundle
	}}
	r := &scriptResponder{turns: []intake.Turn{answer("a1"), answer("a2")}}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopSatisfied, res.StopReason)
	assert.Len(t, res.QuestionsAsked, 2)
}

// TestGather_BundleFailsAuthorship is acceptance case 3.6 / §I-6→§I-2: a
// bundle that fails the ≥90% user-authored check is retried stricter, and a
// second failure downgrades to a user_exit with LLMFailed set — no entry.
func TestGather_BundleFailsAuthorship(t *testing.T) {
	editorialized := "The user felt unable to advocate for themselves, leading to a familiar avoidant pattern of folding, and reported annoyance afterward per the assistant summary."
	p := &provider.Fake{Script: []provider.Exchange{
		ask("q1"), ask("q2"), done(),
		bundle(editorialized), // fails authorship
		bundle(editorialized), // stricter retry still fails
	}}
	r := &scriptResponder{turns: []intake.Turn{
		answer("I pushed back then agreed."), answer("Annoyed."),
	}}

	res, err := intake.Gather(context.Background(), input("Dinner went sideways."), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopUserExit, res.StopReason)
	assert.True(t, res.LLMFailed)
	assert.Empty(t, res.BundledText)
	assert.Equal(t, 5, p.Calls(), "three decides plus two rejected bundles")
}

// TestGather_FloorForcesSecondQuestion covers the two-question floor: when
// the model tries to stop after one question, Intake asks a deterministic
// follow-up so it never completes on a single question.
func TestGather_FloorForcesSecondQuestion(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		ask("What happened?"),
		done(), // tries to stop at one question — below the floor
		done(), // after the forced follow-up, stopping at two is allowed
		bundle("first. second."),
	}}
	r := &scriptResponder{turns: []intake.Turn{answer("first"), answer("second")}}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopSatisfied, res.StopReason)
	assert.Len(t, res.QuestionsAsked, 2, "the floor forces a second question")
	assert.Len(t, r.asked, 2)
}

// TestGather_OpeningSufficientNoQuestions covers the zero-question path:
// when the opening is enough, Intake asks nothing, makes no bundle call,
// and returns the opening verbatim as the body.
func TestGather_OpeningSufficientNoQuestions(t *testing.T) {
	opening := "Long, rich reflection that already stands on its own."
	p := &provider.Fake{Script: []provider.Exchange{done()}}
	r := &scriptResponder{}

	res, err := intake.Gather(context.Background(), input(opening), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopSatisfied, res.StopReason)
	assert.Empty(t, res.QuestionsAsked)
	assert.Equal(t, opening, res.BundledText)
	assert.Equal(t, 1, p.Calls(), "no bundling call when there are no answers to combine")
}

// TestGather_SingleAnswerExitSavesNothing covers the sub-floor user_exit:
// one answer then /done is below the two-question floor, so Intake bundles
// nothing and the answer count stays under the write threshold — this is
// what keeps a one-question raw entry from ever existing.
func TestGather_SingleAnswerExitSavesNothing(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{ask("q1"), ask("q2")}}
	r := &scriptResponder{turns: []intake.Turn{answer("only one"), {Control: intake.ControlDone}}}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopUserExit, res.StopReason)
	assert.Len(t, res.Answers, 1)
	assert.Empty(t, res.BundledText, "a single-answer exit is not bundled")
	assert.False(t, res.LLMFailed)
}

// TestGather_ResponderErrorSurfaces confirms an infrastructure fault in the
// responder (not a user control) surfaces as an error, not a silent exit.
func TestGather_ResponderErrorSurfaces(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{ask("q1")}}
	r := &scriptResponder{} // no scripted turns → Answer errors

	_, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read user answer")
}

// TestGather_NeverAsksMoreThanCap and never exactly one: an exhaustive
// sweep of the invariant across every stop reason it can reach.
func TestGather_QuestionCountInvariant(t *testing.T) {
	cases := []struct {
		name   string
		script []provider.Exchange
		turns  []intake.Turn
	}{
		{
			name:   "satisfied at two",
			script: []provider.Exchange{ask("q1"), ask("q2"), done(), bundle("a1. a2.")},
			turns:  []intake.Turn{answer("a1"), answer("a2")},
		},
		{
			name:   "satisfied at three",
			script: []provider.Exchange{ask("q1"), ask("q2"), ask("q3"), done(), bundle("a1. a2. a3.")},
			turns:  []intake.Turn{answer("a1"), answer("a2"), answer("a3")},
		},
		{
			name:   "cap at four",
			script: []provider.Exchange{ask("q1"), ask("q2"), ask("q3"), ask("q4"), bundle("a1. a2. a3. a4.")},
			turns:  []intake.Turn{answer("a1"), answer("a2"), answer("a3"), answer("a4")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &provider.Fake{Script: tc.script}
			r := &scriptResponder{turns: tc.turns}
			res, err := intake.Gather(context.Background(), input("thin"), p, r)
			require.NoError(t, err)
			n := len(res.QuestionsAsked)
			assert.NotEqual(t, 1, n, "an entry never has exactly one question")
			assert.LessOrEqual(t, n, 4, "never more than the cap")
			assert.GreaterOrEqual(t, n, 2, "at least the floor when any asked")
		})
	}
}

// TestGather_ProviderTimeoutDowngrades confirms a transport timeout is
// handled like a malformed reply: retried once, then downgraded to a
// user_exit with no write (capture can be blocked on Intake).
func TestGather_ProviderTimeoutDowngrades(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Err: provider.ErrTimeout},
		{Err: provider.ErrTimeout},
	}}
	r := &scriptResponder{}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopUserExit, res.StopReason)
	assert.True(t, res.LLMFailed)
}

// TestGather_CapClampSubFloorConfig guards the effective cap: a sub-floor
// config is lifted to the two-question floor.
func TestGather_CapClampSubFloorConfig(t *testing.T) {
	// A misconfigured cap of 1 must still permit the two-question floor.
	in := intake.Input{OpeningMessage: "thin", MaxQuestions: 1, AgentVersion: "intake-2026.05.0"}
	p := &provider.Fake{Script: []provider.Exchange{ask("q1"), ask("q2"), done(), bundle("a1. a2.")}}
	r := &scriptResponder{turns: []intake.Turn{answer("a1"), answer("a2")}}

	res, err := intake.Gather(context.Background(), in, p, r)
	require.NoError(t, err)
	assert.Len(t, res.QuestionsAsked, 2)
}

// TestGather_CapDefaultsWhenUnset covers the unset-cap branch: a zero cap
// falls back to the documented four, so four answers reach the cap.
func TestGather_CapDefaultsWhenUnset(t *testing.T) {
	in := intake.Input{OpeningMessage: "thin", AgentVersion: "intake-2026.05.0"} // MaxQuestions 0
	p := &provider.Fake{Script: []provider.Exchange{
		ask("q1"), ask("q2"), ask("q3"), ask("q4"), bundle("a1. a2. a3. a4."),
	}}
	r := &scriptResponder{turns: []intake.Turn{answer("a1"), answer("a2"), answer("a3"), answer("a4")}}

	res, err := intake.Gather(context.Background(), in, p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopMaxQuestions, res.StopReason)
	assert.Len(t, res.QuestionsAsked, 4)
}

// TestGather_ContinueWithoutQuestionIsMalformed covers the semantic guard:
// a decision that continues (done=false) but supplies no question is
// treated as malformed and retried, then succeeds.
func TestGather_ContinueWithoutQuestionIsMalformed(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Content: `{"done": false, "question": ""}`}, // continues with no question → malformed
		ask("What happened?"),                        // stricter retry parses
		ask("How did it land?"),
		done(),
		bundle("a1. a2."),
	}}
	r := &scriptResponder{turns: []intake.Turn{answer("a1"), answer("a2")}}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopSatisfied, res.StopReason)
	assert.Len(t, res.QuestionsAsked, 2)
}

// TestGather_BundleMalformedThenEmptyDowngrades covers bundleOnce's parse
// and empty-body guards: a malformed bundle then an empty one both fail,
// downgrading to a user_exit with no write.
func TestGather_BundleMalformedThenEmptyDowngrades(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		ask("q1"), ask("q2"), done(),
		{Content: "not json"},                // malformed bundle
		{Content: `{"bundled_text": "   "}`}, // empty bundle
	}}
	r := &scriptResponder{turns: []intake.Turn{answer("a1"), answer("a2")}}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopUserExit, res.StopReason)
	assert.True(t, res.LLMFailed)
}

// TestGather_BundleTransportErrorDowngrades covers bundleOnce's transport
// error branch: a bundle call that errors twice downgrades cleanly.
func TestGather_BundleTransportErrorDowngrades(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		ask("q1"), ask("q2"), done(),
		{Err: provider.ErrUnavailable},
		{Err: provider.ErrUnavailable},
	}}
	r := &scriptResponder{turns: []intake.Turn{answer("a1"), answer("a2")}}

	res, err := intake.Gather(context.Background(), input("thin"), p, r)
	require.NoError(t, err)
	assert.True(t, res.LLMFailed)
}

// TestGather_EmptyOpeningImmediateDoneDowngrades covers buildBundle's
// empty-opening guard: a zero-question satisfied turn with an empty opening
// has nothing to save and downgrades.
func TestGather_EmptyOpeningImmediateDoneDowngrades(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{done()}}
	r := &scriptResponder{}

	res, err := intake.Gather(context.Background(), input(""), p, r)
	require.NoError(t, err)
	assert.Equal(t, intake.StopUserExit, res.StopReason)
	assert.True(t, res.LLMFailed)
	assert.Empty(t, res.BundledText)
}
