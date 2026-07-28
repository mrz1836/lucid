package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// applyCiteID is the raw-entry-id citation the weekly candidate carries into the
// apply turn (stored verbatim as the insight's provenance.raw_entry_ids).
const applyCiteID = "raw_2026_07_01_09_00"

// applyReq builds a weekly-apply request over a candidate the deep-dive
// surfaced, with the given lens label and the user's response.
func applyReq(text, tag, framework string, resp ProposalResponse, rule RuleResponse, p provider.Provider) ApplyWeekProposalRequest {
	return ApplyWeekProposalRequest{
		Now:      fixedNow(),
		Provider: p,
		Candidate: ReflectWeekPattern{
			ProposalText:       text,
			ShapeTag:           tag,
			SupportingEntryIDs: []string{applyCiteID},
		},
		Framework: framework,
		Response:  resp,
		Rule:      rule,
	}
}

// cleanCandidate is a hypothesis-framed candidate Safety passes unchanged.
const cleanCandidate = "One possible pattern: preparation as a way to feel safe."

// TestApplyWeekProposal_AcceptTracksInsightWithFramework is the Rock 1 DoD path
// (A2): an accepted weekly candidate persists a tracked insight through the
// existing gate, stamped with provenance.framework and its raw-entry citations
// (AC-6, AC-8).
func TestApplyWeekProposal_AcceptTracksInsightWithFramework(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedProc(t, a, curr, fixedNow(), nil, "prep")

	res, err := r.ApplyWeekProposal(context.Background(), applyReq(
		cleanCandidate, "prep-as-safety", "stoicism v1",
		ProposalResponse{Kind: RespAccepted, Text: "Yes, that fits."}, RuleResponse{}, &provider.Fake{},
	))
	require.NoError(t, err)
	assert.Equal(t, safety.Pass, res.Decision)
	require.True(t, res.Wrote)
	require.NotEmpty(t, res.InsightID)

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.Equal(t, storage.InsightStatusAccepted, ins.Status)
	assert.Equal(t, storage.ResponseAccepted, ins.Provenance.UserResponseKind)
	require.NotNil(t, ins.Provenance.Framework)
	assert.Equal(t, "stoicism v1", *ins.Provenance.Framework, "the lens label is persisted")
	assert.Equal(t, []string{applyCiteID}, ins.Provenance.RawEntryIDs, "the candidate's raw citations back the insight")
	assert.Equal(t, curr, ins.Provenance.ProcessedArtifactID, "the insight anchors to the recent processed artifact")
}

// TestApplyWeekProposal_AcceptBaselineVoiceNilFramework proves an unlensed
// accept persists a framework-null insight (the baseline voice, AC-6).
func TestApplyWeekProposal_AcceptBaselineVoiceNilFramework(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedProc(t, a, curr, fixedNow(), nil, "prep")

	res, err := r.ApplyWeekProposal(context.Background(), applyReq(
		cleanCandidate, "prep-as-safety", "",
		ProposalResponse{Kind: RespAccepted, Text: "Yes."}, RuleResponse{}, &provider.Fake{},
	))
	require.NoError(t, err)
	require.True(t, res.Wrote)

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.Nil(t, ins.Provenance.Framework)
}

// TestApplyWeekProposal_NuancedCanonicalIsRefinement proves a nuance persists
// the user's refinement as the canonical body (mirrors /checkin 5.2).
func TestApplyWeekProposal_NuancedCanonicalIsRefinement(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedProc(t, a, curr, fixedNow(), nil, "prep")
	refine := "Closer to: I over-prepare when the week felt out of control."

	res, err := r.ApplyWeekProposal(context.Background(), applyReq(
		cleanCandidate, "prep-as-safety", "stoicism v1",
		ProposalResponse{Kind: RespNuanced, Text: refine}, RuleResponse{}, &provider.Fake{},
	))
	require.NoError(t, err)
	require.True(t, res.Wrote)

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.True(t, ins.NuancedFromProposal)
	assert.Equal(t, storage.ResponseNuanced, ins.Provenance.UserResponseKind)
	assert.Equal(t, refine, ins.Body)
}

// TestApplyWeekProposal_RuleAnswered persists the one-line rule after an accept.
func TestApplyWeekProposal_RuleAnswered(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedProc(t, a, curr, fixedNow(), nil, "prep")
	rule := "When I feel the urge to over-prepare, name the fear first."

	res, err := r.ApplyWeekProposal(context.Background(), applyReq(
		cleanCandidate, "prep-as-safety", "stoicism v1",
		ProposalResponse{Kind: RespAccepted, Text: "Yes."},
		RuleResponse{Answered: true, Rule: rule}, &provider.Fake{},
	))
	require.NoError(t, err)
	require.True(t, res.RuleSet)

	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	require.NotNil(t, ins.Rule)
	assert.Equal(t, rule, *ins.Rule)
}

// TestApplyWeekProposal_RejectedRecordsNoInsight proves a rejection writes no
// insight, records the rejected shape on the anchor artifact, and resets the
// pause (AC-8 reuse of persistRejection).
func TestApplyWeekProposal_RejectedRecordsNoInsight(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedProc(t, a, curr, fixedNow(), nil, "prep")
	require.NoError(t, a.WriteProposalPauseState(storage.ProposalPauseState{ConsecutiveUnanswered: 2}))

	res, err := r.ApplyWeekProposal(context.Background(), applyReq(
		cleanCandidate, "prep-as-safety", "stoicism v1",
		ProposalResponse{Kind: RespRejected, Text: "No — that's not it."}, RuleResponse{}, &provider.Fake{},
	))
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.False(t, res.Wrote)
	assert.Equal(t, 0, countFiles(t, home, "insights"))

	art, err := a.ReadProcessed(curr)
	require.NoError(t, err)
	assert.Equal(t, []string{"prep-as-safety"}, storage.RejectedShapeTags(art))

	st, err := a.ReadProposalPauseState()
	require.NoError(t, err)
	assert.Equal(t, 0, st.ConsecutiveUnanswered, "a rejection is an answer — the counter resets")
}

// TestApplyWeekProposal_UnansweredAdvancesPause proves an unanswered response
// records the unanswered shape and advances the silent proposal pause (AC-8).
func TestApplyWeekProposal_UnansweredAdvancesPause(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedProc(t, a, curr, fixedNow(), nil, "prep")

	res, err := r.ApplyWeekProposal(context.Background(), applyReq(
		cleanCandidate, "prep-as-safety", "stoicism v1",
		ProposalResponse{Kind: RespUnanswered}, RuleResponse{}, &provider.Fake{},
	))
	require.NoError(t, err)
	assert.True(t, res.Unanswered)
	assert.False(t, res.Wrote)
	assert.Equal(t, 0, countFiles(t, home, "insights"))

	art, err := a.ReadProcessed(curr)
	require.NoError(t, err)
	assert.Equal(t, []string{"prep-as-safety"}, storage.UnansweredShapeTags(art))

	st, err := a.ReadProposalPauseState()
	require.NoError(t, err)
	assert.Equal(t, 1, st.ConsecutiveUnanswered)
	assert.Nil(t, st.PausedUntil)
}

// TestApplyWeekProposal_ThreeUnansweredArmsPause proves the 3-unanswered→14-day
// pause arms through the weekly apply path exactly as /checkin does (AC-8).
func TestApplyWeekProposal_ThreeUnansweredArmsPause(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedProc(t, a, curr, fixedNow(), nil, "prep")

	for i := range 3 {
		res, err := r.ApplyWeekProposal(context.Background(), applyReq(
			cleanCandidate, fmt.Sprintf("prep-shape-%d", i), "stoicism v1",
			ProposalResponse{Kind: RespUnanswered}, RuleResponse{}, &provider.Fake{},
		))
		require.NoError(t, err)
		require.True(t, res.Unanswered)
	}

	st, err := a.ReadProposalPauseState()
	require.NoError(t, err)
	assert.Equal(t, 3, st.ConsecutiveUnanswered)
	require.NotNil(t, st.PausedUntil, "three consecutive unanswered arm the silent pause")
	assert.True(t, st.PausedUntil.After(fixedNow()))
}

// TestApplyWeekProposal_SafetyBlockPersistsNothing proves a diagnostic candidate
// is blocked at the persist gate and nothing is written (AC-8 reuse of the
// resonance gate).
func TestApplyWeekProposal_SafetyBlockPersistsNothing(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedProc(t, a, curr, fixedNow(), nil, "prep")

	res, err := r.ApplyWeekProposal(context.Background(), applyReq(
		"You have an avoidant attachment style.", "avoidant-label", "stoicism v1",
		ProposalResponse{Kind: RespAccepted, Text: "Yes."}, RuleResponse{}, &provider.Fake{},
	))
	require.NoError(t, err)
	assert.Equal(t, safety.Block, res.Decision)
	assert.False(t, res.Wrote)
	assert.Equal(t, proposalFallback, res.Message)
	assert.Equal(t, 0, countFiles(t, home, "insights"))
}

// TestApplyWeekProposal_NoProcessedArtifactErrors proves apply surfaces an honest
// error rather than writing an unanchored insight when the week never reached a
// check-in.
func TestApplyWeekProposal_NoProcessedArtifactErrors(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	_, err := r.ApplyWeekProposal(context.Background(), applyReq(
		cleanCandidate, "prep-as-safety", "stoicism v1",
		ProposalResponse{Kind: RespAccepted, Text: "Yes."}, RuleResponse{}, &provider.Fake{},
	))
	require.Error(t, err)
}

// TestApplyWeekProposal_CursorAdvancesOnlyWhenAnswered is the guard on the
// abandoned case. Accepting, refining, and rejecting are all answers — the user
// read the pattern and said something — so each advances the reflected-through
// cursor as a convenience, sparing an explicit close. Letting the pattern pass is
// NOT an answer: it advances nothing, so the next reflection re-reads those days
// instead of marking them done. Getting this backwards would reintroduce the
// silent-drop this whole window exists to kill, which is why the unanswered case
// asserts the file's ABSENCE rather than some differing field.
func TestApplyWeekProposal_CursorAdvancesOnlyWhenAnswered(t *testing.T) {
	tests := []struct {
		name    string
		kind    ResponseKind
		text    string
		advance bool
	}{
		{name: "accepted", kind: RespAccepted, text: "Yes, that fits.", advance: true},
		{name: "nuanced", kind: RespNuanced, text: "Closer to: I over-prepare when the week felt loose.", advance: true},
		{name: "rejected", kind: RespRejected, text: "No — that's not it.", advance: true},
		{name: "unanswered", kind: RespUnanswered, advance: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, a, home := newBootedRouter(t)
			seedProc(t, a, curr, fixedNow(), nil, "prep")

			res, err := r.ApplyWeekProposal(context.Background(), applyReq(
				cleanCandidate, "prep-as-safety", "stoicism v1",
				ProposalResponse{Kind: tc.kind, Text: tc.text}, RuleResponse{}, &provider.Fake{},
			))
			require.NoError(t, err)
			assert.False(t, res.CursorStalled, "the cursor write succeeds on a healthy Ledger")

			receipt, ok, err := a.ReadReflectionReceipt()
			require.NoError(t, err)
			path := filepath.Join(home, "engine", "reflection", "receipt.json")

			if !tc.advance {
				assert.False(t, ok, "an unanswered proposal leaves no cursor")
				assert.NoFileExists(t, path, "the abandoned case writes nothing at all")
				return
			}

			require.True(t, ok, "an answered proposal advances the cursor")
			assert.FileExists(t, path)
			assert.Equal(t, "apply", receipt.Source, "the apply path stamps its own provenance")

			through, perr := time.Parse(time.RFC3339, receipt.ReflectedThrough)
			require.NoError(t, perr)
			assert.True(t, through.Equal(fixedNow()), "the cursor is stamped at the apply instant")
		})
	}
}

// TestApplyWeekProposal_CursorFailureNeverMasksThePersist proves the degrade
// ordering. By the time the cursor is advanced the user's insight is already on
// disk; returning a cursor write's error would tell them their accepted pattern
// failed when it did not. It degrades to a surfaced flag instead, and the whole
// cost is that the next reflection re-reads days already sat with — the same
// trade the cursor makes everywhere else.
func TestApplyWeekProposal_CursorFailureNeverMasksThePersist(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedProc(t, a, curr, fixedNow(), nil, "prep")

	// A plain file where the cursor's directory belongs: the write cannot
	// succeed, and the persist that already happened must survive it.
	require.NoError(t, os.MkdirAll(filepath.Join(home, "engine"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home, "engine", "reflection"), []byte("not a directory"), 0o600))

	res, err := r.ApplyWeekProposal(context.Background(), applyReq(
		cleanCandidate, "prep-as-safety", "stoicism v1",
		ProposalResponse{Kind: RespAccepted, Text: "Yes."}, RuleResponse{}, &provider.Fake{},
	))
	require.NoError(t, err, "a cursor failure is a degrade, never an error")
	assert.True(t, res.CursorStalled, "and it is surfaced, never swallowed")

	require.True(t, res.Wrote, "the insight still persisted")
	ins, err := a.ReadInsight(res.InsightID)
	require.NoError(t, err)
	assert.Equal(t, storage.InsightStatusAccepted, ins.Status)
}

// TestApplyWeekProposal_NoArtifactLeavesCursorUntouched proves the early
// precondition failure advances nothing. Apply errors before the proposal ever
// reaches the user, so there was no engagement to record — stamping the cursor
// here would mark days reflected that nobody read.
func TestApplyWeekProposal_NoArtifactLeavesCursorUntouched(t *testing.T) {
	r, a, home := newBootedRouter(t)

	_, err := r.ApplyWeekProposal(context.Background(), applyReq(
		cleanCandidate, "prep-as-safety", "stoicism v1",
		ProposalResponse{Kind: RespAccepted, Text: "Yes."}, RuleResponse{}, &provider.Fake{},
	))
	require.Error(t, err)

	_, ok, rerr := a.ReadReflectionReceipt()
	require.NoError(t, rerr)
	assert.False(t, ok)
	assert.NoFileExists(t, filepath.Join(home, "engine", "reflection", "receipt.json"))
}
