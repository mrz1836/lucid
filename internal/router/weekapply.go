package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// ApplyWeekProposalRequest carries one weekly-deep-dive apply turn: the
// Safety-gated candidate the read-only `lucid reflect week` surfaced (its text,
// shape_tag, and raw-entry-id citations), the "<id> v<version>" lens label that
// framed it (empty for the baseline voice), the user's response, and — on an
// accept/nuance — the optional rule answer. Provider is the model boundary the
// resonance gate re-evaluates the candidate through at persist time (the same
// gate `/checkin` runs), so nothing is written that Safety would block.
type ApplyWeekProposalRequest struct {
	Now       time.Time
	Provider  provider.Provider
	Candidate ReflectWeekPattern
	Framework string
	Response  ProposalResponse
	Rule      RuleResponse
}

// ApplyWeekProposal routes a weekly deep-dive candidate + the user's response
// through the EXISTING resonance/persist machinery — it is a thin wrapper, not a
// parallel writer. It rebuilds the candidate as a reflection proposal (stamped
// with the active lens label), anchors it to the most recent processed artifact
// (the same recent window the deep-dive's shape-tag denylist reads), and hands
// it to the router's handleProposal: safety.Evaluate re-gates the candidate,
// then an accept/nuance persists a tracked insight via persistInsight (setting
// provenance.framework), a rejection appends a rejected_proposal and resets the
// pause, and an unanswered response appends an unanswered_proposal and advances
// the 3-unanswered→14-day pause. One hypothesis at a time is inherent: the
// deep-dive surfaces at most one candidate.
//
// It requires at least one processed artifact to anchor the insight's provenance
// and carry the shape-tag bookkeeping. In the real Sunday flow the reflection
// loop has already produced processed artifacts; a week that never reached a
// `/checkin` has no processed context to hang a persisted pattern on, and this
// surfaces that honestly rather than writing an orphan insight.
func (r *Router) ApplyWeekProposal(ctx context.Context, req ApplyWeekProposalRequest) (ValidateResult, error) {
	now := whenOr(req.Now)

	current, ok, err := r.latestProcessedArtifact()
	if err != nil {
		return ValidateResult{}, err
	}
	if !ok {
		return ValidateResult{}, fmt.Errorf("weekapply: no processed artifact to anchor the weekly insight — run a check-in first")
	}

	// Read the pause state (clearing an expired one) so persistUnanswered advances
	// the same counter /checkin does; an explicit answer here still persists — the
	// pause suppresses UNPROMPTED proposals, not a user's own confirmation.
	_, pauseState, err := r.proposalPauseActive(now)
	if err != nil {
		return ValidateResult{}, err
	}

	prop := reflection.ProposeResult{
		Outcome:            reflection.OutcomeProposal,
		ProposalText:       req.Candidate.ProposalText,
		ShapeTag:           req.Candidate.ShapeTag,
		SupportingEntryIDs: req.Candidate.SupportingEntryIDs,
		Framework:          frameworkLabel(req.Framework),
	}
	vreq := ValidateRequest{
		ProcessedID: current.ID,
		Now:         now,
		Provider:    req.Provider,
		Responder:   scriptedApplyResponder{proposal: req.Response, rule: req.Rule},
	}
	res, err := r.handleProposal(ctx, prop, current, vreq, now, pauseState)
	if err != nil {
		return res, err
	}

	// An answered apply is engagement with the reflection, so it advances the
	// reflected-through cursor as a convenience — the user need not also close.
	// A cursor write that fails must NOT mask a successful persist: the insight
	// is already on disk, and reporting a failure here would tell the user their
	// accepted pattern was lost when it was not. It degrades to a surfaced flag
	// instead, and the worst case is that the next reflection re-reads days the
	// user already sat with — the same cheap, visible cost the whole cursor
	// design prefers over a silently dropped day.
	if reflectionEngaged(req.Response.Kind) {
		if cerr := r.advanceReflectionCursor(now, reflectionSourceApply); cerr != nil {
			res.CursorStalled = true
		}
	}
	return res, nil
}

// reflectionEngaged reports whether a proposal response counts as engaging with
// the reflection, and so advances the reflected-through cursor. Accepting,
// refining, and rejecting are all answers — the user read the pattern and said
// something about it. Letting it pass is the abandoned case and advances
// nothing, so those days are re-read rather than marked reflected.
//
// It switches on the response kind rather than reading the persist flags:
// ValidateResult.Wrote is false for a rejection, which is emphatically still an
// answer.
func reflectionEngaged(kind ResponseKind) bool {
	switch kind {
	case RespAccepted, RespNuanced, RespRejected:
		return true
	case RespUnanswered:
		return false
	default:
		return false
	}
}

// frameworkLabel normalizes the request's lens label to a nullable pointer: a
// blank label is the baseline voice (nil → provenance.framework null), a present
// one threads through to the persisted insight. The label's shape is validated
// downstream by storage.ValidateInsight before any write.
func frameworkLabel(label string) *string {
	if l := strings.TrimSpace(label); l != "" {
		return &l
	}
	return nil
}

// latestProcessedArtifact reads the most recent processed artifact (ids are
// chronological, so the last is newest). ok is false with no error when none
// exists yet, so the caller can decide how to degrade.
func (r *Router) latestProcessedArtifact() (storage.ProcessedArtifact, bool, error) {
	ids, err := r.store.ListProcessedIDs()
	if err != nil {
		return storage.ProcessedArtifact{}, false, fmt.Errorf("weekapply: list processed: %w", err)
	}
	if len(ids) == 0 {
		return storage.ProcessedArtifact{}, false, nil
	}
	art, err := r.store.ReadProcessed(ids[len(ids)-1])
	if err != nil {
		return storage.ProcessedArtifact{}, false, fmt.Errorf("weekapply: read latest processed %q: %w", ids[len(ids)-1], err)
	}
	return art, true, nil
}

// scriptedApplyResponder replays the already-known apply turn into the shared
// handleProposal flow: the user's proposal response, then — only reached on an
// accept/nuance — their rule answer. Unlike the interactive validation
// responder it never blocks; the driver collected both answers before invoking
// apply, so this only hands them back.
type scriptedApplyResponder struct {
	proposal ProposalResponse
	rule     RuleResponse
}

// RespondToProposal hands back the user's already-collected proposal answer.
func (s scriptedApplyResponder) RespondToProposal(string) (ProposalResponse, error) {
	return s.proposal, nil
}

// RespondToRule hands back the user's already-collected rule answer (reached
// only on an accept/nuance).
func (s scriptedApplyResponder) RespondToRule(string) (RuleResponse, error) {
	return s.rule, nil
}
