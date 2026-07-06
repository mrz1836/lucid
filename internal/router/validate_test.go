package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// isRoot reports whether the test runs as root (where chmod bits are a no-op).
func isRoot() bool { return os.Geteuid() == 0 }

// TestValidateHelpers covers the small pure helpers' branches directly.
func TestValidateHelpers(t *testing.T) {
	assert.Empty(t, notesString(nil))
	n := "a note"
	assert.Equal(t, "a note", notesString(&n))
	assert.Equal(t, []string{"raw_x"}, supportingOrCurrent(nil, "raw_x"))
	assert.Equal(t, []string{"a", "b"}, supportingOrCurrent([]string{"a", "b"}, "raw_x"))
}

// TestValidate_PauseStateReadErrorSurfaces proves a corrupt pause-state file
// fails the turn rather than silently proposing.
func TestValidate_PauseStateReadErrorSurfaces(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedWindow(t, a)
	require.NoError(t, writeFileHelper(home, "proposal_pause.json", "{not json"))
	_, err := r.Validate(context.Background(), validateReq(proposalFake(), acceptResp(), false))
	require.Error(t, err)
}

// writeFileHelper writes content to home/rel, creating parent dirs.
func writeFileHelper(home, rel, content string) error {
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// chmodPath chmods home/rel and restores it on cleanup.
func chmodPath(t *testing.T, home, rel string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(home, rel)
	require.NoError(t, os.Chmod(path, mode))
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
}

// Synthetic ids used across the validation-flow tests.
const (
	priorID = "raw_2026_05_03_21_10"
	curr    = "raw_2026_05_05_19_42"
)

// scriptValidation is a fixed-script ValidationResponder: one proposal answer,
// then one rule answer. It records how many times each was asked and the last
// message it was shown, so tests can assert single-prompt semantics and what
// the user actually saw.
type scriptValidation struct {
	proposal      ProposalResponse
	proposalErr   error
	rule          RuleResponse
	ruleErr       error
	proposalCalls int
	ruleCalls     int
	lastMessage   string
}

func (s *scriptValidation) RespondToProposal(msg string) (ProposalResponse, error) {
	s.proposalCalls++
	s.lastMessage = msg
	return s.proposal, s.proposalErr
}

func (s *scriptValidation) RespondToRule(string) (RuleResponse, error) {
	s.ruleCalls++
	return s.rule, s.ruleErr
}

// seedProc writes a processed artifact directly (standing in for a prior
// Structuring pass) with a non-empty emotion so it is never the empty-artifact
// short-circuit.
func seedProc(t *testing.T, a *storage.Adapter, id string, at time.Time, people []storage.ProcessedPerson, themes ...string) {
	t.Helper()
	items := make([]storage.ProcessedItem, 0, len(themes))
	for _, th := range themes {
		items = append(items, storage.ProcessedItem{Name: th, Rationale: "grounded in the entry"})
	}
	require.NoError(t, a.WriteProcessed(storage.ProcessedArtifact{
		ID: id, EntryID: id, ProducedAt: at, AgentVersion: "structuring-2026.05.0",
		Emotions: []storage.ProcessedItem{{Name: "annoyed", Rationale: "user said 'annoyed'"}},
		Themes:   items, People: people,
	}))
}

// seedWindow seeds a prior + current artifact so a proposal can fire (a
// non-empty recent window over a non-empty current artifact).
func seedWindow(t *testing.T, a *storage.Adapter) {
	t.Helper()
	seedProc(t, a, priorID, time.Date(2026, time.May, 3, 21, 10, 0, 0, time.UTC), nil, "voice-not-heard")
	seedProc(t, a, curr, time.Date(2026, time.May, 5, 19, 42, 0, 0, time.UTC), nil, "voice-not-heard")
}

// proposeReply builds a proposal completion.
func proposeReply(text, tag string, ids ...string) provider.Exchange {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	body := fmt.Sprintf(`{"outcome":"proposal","proposal_text":%q,"shape_tag":%q,"supporting_entry_ids":[%s]}`,
		text, tag, strings.Join(quoted, ","))
	return provider.Exchange{Content: body}
}

// validateReq wires a validation request over the current artifact.
func validateReq(p provider.Provider, resp ValidationResponder, bootstrap bool) ValidateRequest {
	return ValidateRequest{ProcessedID: curr, Now: fixedNow(), Bootstrap: bootstrap, Provider: p, Responder: resp}
}

// TestValidate_5_1_ProposalAcceptedWritesInsight is acceptance case 5.1.
func TestValidate_5_1_ProposalAcceptedWritesInsight(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	p := &provider.Fake{Script: []provider.Exchange{
		proposeReply("One possible pattern: you test an idea once, then back off.", "voice-fold-when-m", curr, priorID),
	}}
	resp := &scriptValidation{proposal: ProposalResponse{Kind: RespAccepted, Text: "Yes, that fits."}, rule: RuleResponse{}}

	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	assert.Equal(t, reflection.OutcomeProposal, res.Outcome)
	assert.Equal(t, safety.Pass, res.Decision)
	require.True(t, res.Wrote)
	require.NotEmpty(t, res.InsightID)

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.Equal(t, storage.InsightStatusAccepted, ins.Status)
	assert.Equal(t, storage.ResponseAccepted, ins.Provenance.UserResponseKind)
	assert.Equal(t, curr, ins.Provenance.ProcessedArtifactID)
	assert.Equal(t, []string{curr, priorID}, ins.Provenance.RawEntryIDs)
	assert.Equal(t, "reflection-2026.05.0", ins.Provenance.ReflectionPromptVersion)
}

// TestValidate_5_2_NuancedCanonicalIsUserRefinement is acceptance case 5.2.
func TestValidate_5_2_NuancedCanonicalIsUserRefinement(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	p := &provider.Fake{Script: []provider.Exchange{
		proposeReply("One possible pattern: you go quiet in groups.", "voice-fold", curr),
	}}
	refine := "Mostly yes — it's more when M. is in the room."
	resp := &scriptValidation{proposal: ProposalResponse{Kind: RespNuanced, Text: refine}}

	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	require.True(t, res.Wrote)

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.True(t, ins.NuancedFromProposal)
	assert.Equal(t, storage.ResponseNuanced, ins.Provenance.UserResponseKind)
	assert.Equal(t, refine, ins.Body, "canonical statement is the user's refinement")
}

// TestValidate_5_3_RejectedRecordsNoInsight is acceptance case 5.3.
func TestValidate_5_3_RejectedRecordsNoInsight(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedWindow(t, a)
	p := &provider.Fake{Script: []provider.Exchange{
		proposeReply("One possible pattern: defensiveness when family is the topic.", "family-defensiveness-default", curr),
	}}
	resp := &scriptValidation{proposal: ProposalResponse{Kind: RespRejected, Text: "No — that doesn't fit."}}

	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.False(t, res.Wrote)
	assert.Equal(t, 0, countFiles(t, home, "insights"))

	art, err := a.ReadProcessed(curr)
	require.NoError(t, err)
	tags := storage.RejectedShapeTags(art)
	assert.Equal(t, []string{"family-defensiveness-default"}, tags)
}

// TestValidate_5_4_RejectedTagSuppressedNextWindow is acceptance case 5.4: a
// rejected shape_tag on the current artifact flows into the denylist, so a
// proposal reusing it (even after a strict retry) is prevented — degrading to
// no_pattern.
func TestValidate_5_4_RejectedTagSuppressedNextWindow(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	require.NoError(t, a.AppendRejectedProposal(curr, storage.RejectedProposal{
		At: fixedNow(), ReflectionPromptVersion: "reflection-2026.05.0",
		ProposalText: "x", UserResponseText: "no", ShapeTag: "family-defensiveness-default",
	}))
	// The model insists on the rejected tag on both attempts.
	reuse := proposeReply("One possible pattern.", "family-defensiveness-default", curr)
	p := &provider.Fake{Script: []provider.Exchange{reuse, reuse}}
	resp := &scriptValidation{}

	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	assert.Equal(t, reflection.OutcomeNoPattern, res.Outcome, "a rejected shape is not re-proposed")
	assert.Equal(t, 0, resp.proposalCalls, "no proposal reaches the user")
}

// TestValidate_5_5_BlocklistHitRewritten is acceptance case 5.5: an overclaim
// is rewritten by Safety, the user sees only the rewrite, and the accepted
// insight stores the clean text.
func TestValidate_5_5_BlocklistHitRewritten(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	rewritten := "I noticed a possible pattern: when M. is in the room, you tend to fold. Does this resonate?"
	p := &provider.Fake{Script: []provider.Exchange{
		proposeReply("You always fold when M. is in the room.", "voice-fold-when-m", curr),
		{Content: rewritten},
	}}
	resp := &scriptValidation{proposal: ProposalResponse{Kind: RespAccepted, Text: "Yes."}}

	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	assert.Equal(t, safety.Rewrite, res.Decision)
	assert.Equal(t, rewritten, res.Message)
	assert.Equal(t, rewritten, resp.lastMessage, "the user sees only the rewrite")

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.Equal(t, rewritten, ins.Body)
	assert.False(t, safety.MatchesBlocklist(ins.Body), "the stored insight carries no blocklist phrase")
}

// TestValidate_5_6_ExternalActionBlockedNothingStored is acceptance case 5.6.
func TestValidate_5_6_ExternalActionBlockedNothingStored(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedWindow(t, a)
	p := &provider.Fake{Script: []provider.Exchange{
		proposeReply("I'll send M. a follow-up message tonight.", "voice-fold", curr),
	}}
	resp := &scriptValidation{}

	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	assert.Equal(t, safety.Block, res.Decision)
	assert.Equal(t, safety.ReasonExternalActionAttempt, res.ReasonCode)
	assert.Equal(t, proposalFallback, res.Message)
	assert.False(t, res.Wrote)
	assert.False(t, res.Rejected)
	assert.False(t, res.Unanswered)
	assert.Equal(t, 0, resp.proposalCalls, "the blocked proposal never reached the user")
	assert.Equal(t, 0, countFiles(t, home, "insights"))

	// Nothing was appended to the artifact — not even a rejection (§Sf-3).
	art, err := a.ReadProcessed(curr)
	require.NoError(t, err)
	assert.Empty(t, storage.RejectedShapeTags(art))
	assert.Empty(t, storage.UnansweredShapeTags(art))
}

// TestValidate_5_7_BootstrapSuppressesProposal is acceptance case 5.7.
func TestValidate_5_7_BootstrapSuppressesProposal(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedWindow(t, a)
	p := &provider.Fake{} // no scripted calls — propose must not run
	resp := &scriptValidation{}

	res, err := r.Validate(context.Background(), validateReq(p, resp, true))
	require.NoError(t, err)
	assert.Equal(t, reflection.OutcomeNoPattern, res.Outcome)
	assert.Equal(t, 0, p.Calls(), "no model call during bootstrap")
	assert.Equal(t, 0, countFiles(t, home, "insights"))
}

// TestValidate_5_8_UnansweredRecorded is acceptance case 5.8.
func TestValidate_5_8_UnansweredRecorded(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedWindow(t, a)
	p := &provider.Fake{Script: []provider.Exchange{
		proposeReply("One possible pattern: quiet days read flat.", "quiet-day-flatness", curr),
	}}
	resp := &scriptValidation{proposal: ProposalResponse{Kind: RespUnanswered}}

	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	assert.True(t, res.Unanswered)
	assert.False(t, res.Wrote)
	assert.Equal(t, 0, countFiles(t, home, "insights"))

	art, err := a.ReadProcessed(curr)
	require.NoError(t, err)
	assert.Equal(t, []string{"quiet-day-flatness"}, storage.UnansweredShapeTags(art))

	st, err := a.ReadProposalPauseState()
	require.NoError(t, err)
	assert.Equal(t, 1, st.ConsecutiveUnanswered)
	assert.Nil(t, st.PausedUntil)
}

// TestValidate_5_9_ThreeUnansweredPausesSilently is acceptance case 5.9: three
// consecutive unanswered proposals arm the 14-day pause, the next /checkin
// skips propose without a word about the pause, and any answered proposal
// resets the counter.
func TestValidate_5_9_ThreeUnansweredPausesSilently(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedProc(t, a, priorID, time.Date(2026, time.May, 3, 21, 10, 0, 0, time.UTC), nil, "voice-not-heard")

	// Three distinct current artifacts, each left unanswered.
	ids := []string{"raw_2026_05_05_10_00", "raw_2026_05_05_11_00", "raw_2026_05_05_12_00"}
	for i, id := range ids {
		seedProc(t, a, id, time.Date(2026, time.May, 5, 10+i, 0, 0, 0, time.UTC), nil, "voice-not-heard")
		tag := fmt.Sprintf("shape-%d", i)
		p := &provider.Fake{Script: []provider.Exchange{proposeReply("One possible pattern.", tag, id)}}
		resp := &scriptValidation{proposal: ProposalResponse{Kind: RespUnanswered}}
		res, err := r.Validate(context.Background(), ValidateRequest{ProcessedID: id, Now: fixedNow(), Provider: p, Responder: resp})
		require.NoError(t, err)
		require.True(t, res.Unanswered)
	}

	st, err := a.ReadProposalPauseState()
	require.NoError(t, err)
	assert.Equal(t, 3, st.ConsecutiveUnanswered)
	require.NotNil(t, st.PausedUntil, "the pause is armed after three consecutive unanswered proposals")
	assert.True(t, st.PausedUntil.After(fixedNow()))

	// The next /checkin skips propose entirely — no model call, no message.
	seedProc(t, a, "raw_2026_05_05_13_00", time.Date(2026, time.May, 5, 13, 0, 0, 0, time.UTC), nil, "voice-not-heard")
	pausedP := &provider.Fake{}
	res, err := r.Validate(context.Background(), ValidateRequest{ProcessedID: "raw_2026_05_05_13_00", Now: fixedNow(), Provider: pausedP, Responder: &scriptValidation{}})
	require.NoError(t, err)
	assert.True(t, res.ProposalPaused)
	assert.Equal(t, 0, pausedP.Calls(), "no model call while paused")
	assert.Empty(t, res.Message, "the pause is silent — no copy mentions it")
}

// TestValidate_5_9_AnsweredResetsCounter proves any answered proposal resets
// the consecutive-unanswered counter before the pause arms.
func TestValidate_5_9_AnsweredResetsCounter(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedProc(t, a, priorID, time.Date(2026, time.May, 3, 21, 10, 0, 0, time.UTC), nil, "voice-not-heard")
	require.NoError(t, a.WriteProposalPauseState(storage.ProposalPauseState{ConsecutiveUnanswered: 2}))

	seedProc(t, a, curr, time.Date(2026, time.May, 5, 19, 42, 0, 0, time.UTC), nil, "voice-not-heard")
	p := &provider.Fake{Script: []provider.Exchange{proposeReply("One possible pattern.", "voice-fold", curr)}}
	resp := &scriptValidation{proposal: ProposalResponse{Kind: RespAccepted, Text: "Yes."}}
	_, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)

	st, err := a.ReadProposalPauseState()
	require.NoError(t, err)
	assert.Equal(t, 0, st.ConsecutiveUnanswered, "an answered proposal resets the counter")
	assert.Nil(t, st.PausedUntil)
}

// TestValidate_PauseExpiryResumes proves an expired pause is cleared and
// propose resumes.
func TestValidate_PauseExpiryResumes(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	past := fixedNow().Add(-time.Hour)
	require.NoError(t, a.WriteProposalPauseState(storage.ProposalPauseState{ConsecutiveUnanswered: 3, PausedUntil: &past}))

	p := &provider.Fake{Script: []provider.Exchange{proposeReply("One possible pattern.", "voice-fold", curr)}}
	resp := &scriptValidation{proposal: ProposalResponse{Kind: RespUnanswered}}
	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	assert.False(t, res.ProposalPaused, "an expired pause no longer suppresses propose")
	assert.Equal(t, reflection.OutcomeProposal, res.Outcome)
}

// TestValidate_5_10_RuleAnswered is acceptance case 5.10.
func TestValidate_5_10_RuleAnswered(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	p := &provider.Fake{Script: []provider.Exchange{proposeReply("One possible pattern.", "voice-fold", curr)}}
	rule := "When I catch myself folding mid-sentence, finish the sentence."
	resp := &scriptValidation{
		proposal: ProposalResponse{Kind: RespAccepted, Text: "Yes."},
		rule:     RuleResponse{Answered: true, Rule: rule},
	}

	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	require.True(t, res.RuleSet)
	assert.Equal(t, 1, resp.ruleCalls, "the rule prompt is asked exactly once")

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	require.NotNil(t, ins.Rule)
	assert.Equal(t, rule, *ins.Rule)
	require.Len(t, ins.RuleHistory, 1)
	assert.Equal(t, storage.RuleStated, ins.RuleHistory[0].Kind)
}

// TestValidate_5_11_RuleSkipped is acceptance case 5.11.
func TestValidate_5_11_RuleSkipped(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	p := &provider.Fake{Script: []provider.Exchange{proposeReply("One possible pattern.", "voice-fold", curr)}}
	resp := &scriptValidation{proposal: ProposalResponse{Kind: RespAccepted, Text: "Yes."}, rule: RuleResponse{Answered: false}}

	res, err := r.Validate(context.Background(), validateReq(p, resp, false))
	require.NoError(t, err)
	require.True(t, res.Wrote)
	assert.False(t, res.RuleSet)
	assert.Equal(t, 1, resp.ruleCalls, "the prompt is offered once, then never re-asked")

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.Nil(t, ins.Rule)
	assert.Empty(t, ins.RuleHistory)
}

// TestValidate_5_12_OffLimitsRedactedFromSlice is acceptance case 5.12: an
// off-limits person in the recent window is redacted from the agent slice and
// referenced nowhere, while the on-disk artifact is untouched.
func TestValidate_5_12_OffLimitsRedactedFromSlice(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	offKey := "person_p-hazel"
	// The prior artifact names the off-limits person; the current one does not.
	seedProc(t, a, priorID, time.Date(2026, time.May, 3, 21, 10, 0, 0, time.UTC),
		[]storage.ProcessedPerson{{DisplayName: "M.", PersonKey: offKey, FirstMention: true}}, "voice-not-heard")
	seedProc(t, a, curr, time.Date(2026, time.May, 5, 19, 42, 0, 0, time.UTC), nil, "voice-not-heard")
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{offKey}))

	p := &provider.Fake{Script: []provider.Exchange{{Content: `{"outcome":"no_pattern","message_text":"nothing yet"}`}}}
	res, err := r.Validate(context.Background(), validateReq(p, &scriptValidation{}, false))
	require.NoError(t, err)
	assert.Equal(t, reflection.OutcomeNoPattern, res.Outcome)

	// The slice the model was shown carries no trace of the off-limits person.
	require.Len(t, p.Requests, 1)
	assert.NotContains(t, p.Requests[0].Messages[0].Content, "M.", "the off-limits person is redacted from the agent slice")

	// The on-disk artifact is untouched — the person still appears there.
	art, err := a.ReadProcessed(priorID)
	require.NoError(t, err)
	require.Len(t, art.People, 1)
	assert.Equal(t, "M.", art.People[0].DisplayName)
}

// TestValidate_SoftContradictionSurfaced covers the soft_contradiction branch:
// it is Safety-gated and surfaced, and writes no insight.
func TestValidate_SoftContradictionSurfaced(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedWindow(t, a)
	reply := fmt.Sprintf(`{"outcome":"soft_contradiction","message_text":"Earlier X; today Y. Want to look at the gap?","supporting_entry_ids":[%q,%q]}`, priorID, curr)
	p := &provider.Fake{Script: []provider.Exchange{{Content: reply}}}

	res, err := r.Validate(context.Background(), validateReq(p, &scriptValidation{}, false))
	require.NoError(t, err)
	assert.Equal(t, reflection.OutcomeSoftContradiction, res.Outcome)
	assert.Contains(t, res.Message, "look at the gap?")
	assert.Equal(t, 0, countFiles(t, home, "insights"))
}

// TestValidate_NoPatternSurfacesMessage covers the plain no_pattern outcome.
func TestValidate_NoPatternSurfacesMessage(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	p := &provider.Fake{Script: []provider.Exchange{{Content: `{"outcome":"no_pattern","message_text":"not yet"}`}}}
	res, err := r.Validate(context.Background(), validateReq(p, &scriptValidation{}, false))
	require.NoError(t, err)
	assert.Equal(t, reflection.OutcomeNoPattern, res.Outcome)
	assert.NotEmpty(t, res.Message)
}

// TestValidate_MissingProcessedErrors surfaces a read error for an absent
// current artifact.
func TestValidate_MissingProcessedErrors(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	_, err := r.Validate(context.Background(), ValidateRequest{ProcessedID: "raw_nope", Now: fixedNow(), Provider: &provider.Fake{}, Responder: &scriptValidation{}})
	require.Error(t, err)
}

// acceptResp returns a responder that accepts and answers the rule.
func acceptResp() *scriptValidation {
	return &scriptValidation{proposal: ProposalResponse{Kind: RespAccepted, Text: "Yes."}, rule: RuleResponse{Answered: true, Rule: "finish the sentence"}}
}

// proposalFake returns a provider that answers propose with one clean proposal.
func proposalFake() *provider.Fake {
	return &provider.Fake{Script: []provider.Exchange{proposeReply("One possible pattern.", "voice-fold", curr)}}
}

// TestValidate_ResponderErrorsSurface proves a responder failure at either turn
// is surfaced as an error.
func TestValidate_ResponderErrorsSurface(t *testing.T) {
	// Proposal-turn responder error.
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	resp := &scriptValidation{proposalErr: fmt.Errorf("thread closed")}
	_, err := r.Validate(context.Background(), validateReq(proposalFake(), resp, false))
	require.Error(t, err)

	// Rule-turn responder error (after a successful accept).
	r2, a2, _ := newBootedRouter(t)
	seedWindow(t, a2)
	resp2 := &scriptValidation{proposal: ProposalResponse{Kind: RespAccepted, Text: "Yes."}, ruleErr: fmt.Errorf("thread closed")}
	_, err = r2.Validate(context.Background(), validateReq(proposalFake(), resp2, false))
	require.Error(t, err)
}

// TestValidate_OffLimitsReadErrorSurfaces proves a corrupt off-limits registry
// fails the turn closed rather than leaking.
func TestValidate_OffLimitsReadErrorSurfaces(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedWindow(t, a)
	require.NoError(t, writeFileHelper(home, "off_limits.json", "{not json"))
	_, err := r.Validate(context.Background(), validateReq(proposalFake(), acceptResp(), false))
	require.Error(t, err)
}

// TestValidate_RecentWindowReadErrorSurfaces proves a malformed window artifact
// surfaces a read error.
func TestValidate_RecentWindowReadErrorSurfaces(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedWindow(t, a)
	// Corrupt the prior (window) artifact on disk.
	require.NoError(t, writeFileHelper(home, "processed/"+priorID+".json", "{not json"))
	_, err := r.Validate(context.Background(), validateReq(proposalFake(), acceptResp(), false))
	require.Error(t, err)
}

// TestValidate_PersistErrorsSurface proves each persistence failure is surfaced:
// an unwritable insights dir (accept), an unwritable artifact (reject and
// unanswered).
func TestValidate_PersistErrorsSurface(t *testing.T) {
	if isRoot() {
		t.Skip("chmod permission bits are a no-op as root")
	}

	t.Run("accept write_insight fails", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedWindow(t, a)
		chmodPath(t, home, "insights", 0o500)
		_, err := r.Validate(context.Background(), validateReq(proposalFake(), acceptResp(), false))
		require.Error(t, err)
	})

	t.Run("reject append fails", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedWindow(t, a)
		chmodPath(t, home, "processed/"+curr+".json", 0o400)
		resp := &scriptValidation{proposal: ProposalResponse{Kind: RespRejected, Text: "no"}}
		_, err := r.Validate(context.Background(), validateReq(proposalFake(), resp, false))
		require.Error(t, err)
	})

	t.Run("unanswered append fails", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedWindow(t, a)
		chmodPath(t, home, "processed/"+curr+".json", 0o400)
		resp := &scriptValidation{proposal: ProposalResponse{Kind: RespUnanswered}}
		_, err := r.Validate(context.Background(), validateReq(proposalFake(), resp, false))
		require.Error(t, err)
	})
}
