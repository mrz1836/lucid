package router

import (
	"context"
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// ResponseKind is how the user answered a surfaced proposal.
type ResponseKind string

// The four proposal responses (agent-contracts.md §3 "How contracts compose").
const (
	// RespAccepted — the user accepts the pattern as stated.
	RespAccepted ResponseKind = "accepted"
	// RespNuanced — the user refines it; the refinement becomes canonical.
	RespNuanced ResponseKind = "nuanced"
	// RespRejected — the user rejects it; a rejection is recorded, no insight.
	RespRejected ResponseKind = "rejected"
	// RespUnanswered — the user lets it pass; the shape is suppressed and the
	// pause counter advances.
	RespUnanswered ResponseKind = "unanswered"
)

// proposalFallback is the fixed copy the router surfaces when Safety blocks a
// reflection output (agent-contracts.md §4; error-states.md §Sf-2). It is calm
// and earns no status the system did not (error-states.md cross-cutting).
const proposalFallback = "I held that response — let me ask differently."

// rulePrompt is the fixed, once-per-insight prompt asked after an accepted or
// nuanced validation (agent-contracts.md §3 "Rules"). Skipping is first-class.
const rulePrompt = "Want to attach a rule — one line, what you'll try? Skipping is fine."

// ProposalResponse is the user's answer to a surfaced proposal. Text is the
// user's verbatim words: the refinement for a nuance (which becomes the
// canonical statement), the affirmation for an accept, or the reason for a
// rejection. It is ignored for an unanswered response.
type ProposalResponse struct {
	Kind ResponseKind
	Text string
}

// RuleResponse is the user's answer to the fixed rule prompt. Answered false
// (a skip) leaves rule null and the prompt never returns for that insight.
type RuleResponse struct {
	Answered bool
	Rule     string
}

// ValidationResponder supplies the user's turns in the validation flow: the
// response to a surfaced proposal, then — only on accept/nuance — the answer
// to the fixed rule prompt. In a real harness it blocks on the chat thread; in
// tests it replays a fixed script.
type ValidationResponder interface {
	RespondToProposal(message string) (ProposalResponse, error)
	RespondToRule(prompt string) (RuleResponse, error)
}

// ValidateRequest carries the inputs for one validation turn over an already-
// structured processed artifact (agent-contracts.md §"How contracts compose":
// this is the propose → Safety → await → persist tail of a /checkin). The
// artifact must already be on disk; Structuring ran upstream.
type ValidateRequest struct {
	ProcessedID string
	Now         time.Time
	Bootstrap   bool
	Provider    provider.Provider
	Responder   ValidationResponder
}

// ValidateResult reports what a validation turn did. Outcome is Reflection's
// proposal outcome (empty when propose was skipped). Message is what the user
// saw (post-Safety, or the fallback). The remaining flags record the durable
// effect so a caller and the tests can assert exactly one of the accept /
// reject / unanswered paths fired.
type ValidateResult struct {
	Outcome        reflection.Outcome
	Message        string
	Decision       safety.Decision
	ReasonCode     safety.ReasonCode
	ProposalPaused bool
	InsightID      string
	Wrote          bool
	Rejected       bool
	Unanswered     bool
	RuleSet        bool
}

// Validate runs the insight-validation flow for one processed artifact:
// build the recent window (off-limits redacted), run reflection.propose unless
// bootstrap or an active pause suppresses it, gate the output through Safety,
// await the user, and persist the outcome — an insight (accept/nuance), a
// rejection, or an unanswered shape — advancing the silent proposal pause on
// consecutive silence and resetting it on any answer (agent-contracts.md §3;
// acceptance-criteria.md Phase 5).
func (r *Router) Validate(ctx context.Context, req ValidateRequest) (ValidateResult, error) {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}

	current, err := r.store.ReadProcessed(req.ProcessedID)
	if err != nil {
		return ValidateResult{}, fmt.Errorf("validate: read processed %q: %w", req.ProcessedID, err)
	}

	// Bootstrap and an active pause both suppress propose deterministically —
	// no model is called (error-states.md §R-6; agent-contracts.md §3 pause).
	if req.Bootstrap {
		return ValidateResult{Outcome: reflection.OutcomeNoPattern}, nil
	}
	paused, pauseState, err := r.proposalPauseActive(now)
	if err != nil {
		return ValidateResult{}, err
	}
	if paused {
		return ValidateResult{Outcome: reflection.OutcomeNoPattern, ProposalPaused: true}, nil
	}

	in, err := r.buildProposeInput(current, req.Bootstrap)
	if err != nil {
		return ValidateResult{}, err
	}
	prop := reflection.Propose(ctx, in, req.Provider)

	switch prop.Outcome {
	case reflection.OutcomeNoPattern, reflection.OutcomeRecall, reflection.OutcomeAnswer, reflection.OutcomeInsufficient:
		// recall/answer/insufficient are /reflect and /ask outcomes; propose never
		// yields them, so treat any of them as no_pattern for safety.
		return ValidateResult{Outcome: reflection.OutcomeNoPattern, Message: prop.MessageText}, nil
	case reflection.OutcomeSoftContradiction:
		return r.surfaceSoftContradiction(ctx, prop, req)
	case reflection.OutcomeProposal:
		return r.handleProposal(ctx, prop, current, req, now, pauseState)
	default:
		return ValidateResult{Outcome: reflection.OutcomeNoPattern, Message: prop.MessageText}, nil
	}
}

// surfaceSoftContradiction gates a soft-contradiction question through Safety
// and surfaces it. A soft contradiction invites a later look; it writes no
// insight and does not participate in the proposal pause.
func (r *Router) surfaceSoftContradiction(ctx context.Context, prop reflection.ProposeResult, req ValidateRequest) (ValidateResult, error) {
	dec := safety.Evaluate(ctx, safety.Candidate{
		FromAgent:          safety.FromReflection,
		Intent:             safety.IntentSoftContradiction,
		Text:               prop.MessageText,
		SupportingEntryIDs: prop.SupportingEntryIDs,
	}, r.safetySession(req.Bootstrap), req.Provider)

	res := ValidateResult{Outcome: reflection.OutcomeSoftContradiction, Decision: dec.Decision, ReasonCode: dec.ReasonCode}
	res.Message = dec.Text // pass returns the original; rewrite the softened text
	return res, nil
}

// handleProposal gates a proposal through Safety, awaits the user, and
// persists the answer. A Safety block ends the turn with the fallback and no
// write (nothing is stored — error-states.md §Sf-2/§Sf-3). Otherwise the
// user's response routes to write_insight, append_rejected_proposal, or
// append_unanswered_proposal, and the pause counter is updated accordingly.
func (r *Router) handleProposal(
	ctx context.Context, prop reflection.ProposeResult, current storage.ProcessedArtifact,
	req ValidateRequest, now time.Time, pauseState storage.ProposalPauseState,
) (ValidateResult, error) {
	dec := safety.Evaluate(ctx, safety.Candidate{
		FromAgent:          safety.FromReflection,
		Intent:             safety.IntentProposePattern,
		Text:               prop.ProposalText,
		ShapeTag:           prop.ShapeTag,
		SupportingEntryIDs: prop.SupportingEntryIDs,
	}, r.safetySession(req.Bootstrap), req.Provider)

	res := ValidateResult{Outcome: reflection.OutcomeProposal, Decision: dec.Decision, ReasonCode: dec.ReasonCode}
	if dec.Decision == safety.Block {
		// Nothing stored — not even a rejection or an unanswered shape
		// (error-states.md §Sf-3). The proposal never reached the user.
		res.Message = proposalFallback
		return res, nil
	}

	message := dec.Text // pass returns the original; rewrite the softened text
	res.Message = message
	resp, err := req.Responder.RespondToProposal(message)
	if err != nil {
		return ValidateResult{}, fmt.Errorf("validate: await proposal response: %w", err)
	}

	switch resp.Kind {
	case RespAccepted, RespNuanced:
		return r.persistInsight(res, message, prop, current, resp, req, now)
	case RespRejected:
		return r.persistRejection(res, message, prop, current, resp, now)
	case RespUnanswered:
		return r.persistUnanswered(res, prop, current, now, pauseState)
	default:
		return r.persistUnanswered(res, prop, current, now, pauseState)
	}
}

// persistInsight writes the accepted/nuanced insight with full provenance,
// resets the proposal pause (an answered proposal), then asks the fixed rule
// prompt once and records a rule only if the user answers (agent-contracts.md
// §3; acceptance-criteria.md 5.1/5.2/5.10/5.11).
func (r *Router) persistInsight(
	res ValidateResult, message string, prop reflection.ProposeResult,
	current storage.ProcessedArtifact, resp ProposalResponse, req ValidateRequest, now time.Time,
) (ValidateResult, error) {
	nuanced := resp.Kind == RespNuanced
	body := message
	kind := storage.ResponseAccepted
	if nuanced {
		body = resp.Text // the user's refinement is canonical
		kind = storage.ResponseNuanced
	}

	writeRes, err := r.store.WriteInsight(storage.Insight{
		CreatedAt:           now,
		Status:              storage.InsightStatusAccepted,
		NuancedFromProposal: nuanced,
		Provenance: storage.InsightProvenance{
			RawEntryIDs:             supportingOrCurrent(prop.SupportingEntryIDs, current.ID),
			ProcessedArtifactID:     current.ID,
			ReflectionPromptVersion: r.cfg.AgentVersions.Reflection,
			Framework:               prop.Framework,
			UserResponseKind:        kind,
			UserResponseText:        resp.Text,
		},
		Body: body,
	})
	if err != nil {
		return ValidateResult{}, fmt.Errorf("validate: write insight: %w", err)
	}
	if err = r.resetProposalPause(); err != nil {
		return ValidateResult{}, err
	}
	res.Wrote = true
	res.InsightID = writeRes.InsightID

	ruleRes, err := req.Responder.RespondToRule(rulePrompt)
	if err != nil {
		return ValidateResult{}, fmt.Errorf("validate: await rule response: %w", err)
	}
	if ruleRes.Answered {
		if err := r.store.SetInsightRule(writeRes.InsightID, ruleRes.Rule, now); err != nil {
			return ValidateResult{}, fmt.Errorf("validate: set insight rule: %w", err)
		}
		res.RuleSet = true
	}
	return res, nil
}

// persistRejection records the rejection on the processed artifact and resets
// the pause counter (a rejection is an answer). No insight is written
// (acceptance-criteria.md 5.3).
func (r *Router) persistRejection(
	res ValidateResult, message string, prop reflection.ProposeResult,
	current storage.ProcessedArtifact, resp ProposalResponse, now time.Time,
) (ValidateResult, error) {
	if err := r.store.AppendRejectedProposal(current.ID, storage.RejectedProposal{
		At:                      now,
		ReflectionPromptVersion: r.cfg.AgentVersions.Reflection,
		ProposalText:            message,
		UserResponseText:        resp.Text,
		ShapeTag:                prop.ShapeTag,
	}); err != nil {
		return ValidateResult{}, fmt.Errorf("validate: append rejected proposal: %w", err)
	}
	if err := r.resetProposalPause(); err != nil {
		return ValidateResult{}, err
	}
	res.Rejected = true
	return res, nil
}

// persistUnanswered records the unanswered shape on the processed artifact and
// advances the pause counter, arming the 14-day silent pause once the
// unanswered threshold is reached (agent-contracts.md §3; acceptance-criteria
// 5.8/5.9).
func (r *Router) persistUnanswered(
	res ValidateResult, prop reflection.ProposeResult, current storage.ProcessedArtifact,
	now time.Time, pauseState storage.ProposalPauseState,
) (ValidateResult, error) {
	if err := r.store.AppendUnansweredProposal(current.ID, storage.UnansweredProposal{
		ShapeTag:   prop.ShapeTag,
		ProposedAt: now,
	}); err != nil {
		return ValidateResult{}, fmt.Errorf("validate: append unanswered proposal: %w", err)
	}

	pauseState.ConsecutiveUnanswered++
	if pauseState.ConsecutiveUnanswered >= r.cfg.ProposalPause.UnansweredThreshold {
		until := now.Add(time.Duration(r.cfg.ProposalPause.PauseDays) * 24 * time.Hour)
		pauseState.PausedUntil = &until
	}
	if err := r.store.WriteProposalPauseState(pauseState); err != nil {
		return ValidateResult{}, fmt.Errorf("validate: write proposal pause state: %w", err)
	}
	res.Unanswered = true
	return res, nil
}

// buildProposeInput assembles Reflection's authorized slice: the current
// artifact and the recent window (both off-limits redacted), plus the rejected
// and unanswered shape-tag unions over the same set. It reads the off-limits
// registry once and fails the turn if it cannot (fail closed — a partial
// redaction is a contract violation, not a judgment call).
func (r *Router) buildProposeInput(current storage.ProcessedArtifact, bootstrap bool) (reflection.ProposeInput, error) {
	offLimits, err := r.store.ReadOffLimitsPersonKeys()
	if err != nil {
		return reflection.ProposeInput{}, fmt.Errorf("validate: read off-limits registry: %w", err)
	}
	deny := toSet(offLimits)

	window, err := r.recentWindow(current.ID)
	if err != nil {
		return reflection.ProposeInput{}, err
	}

	rejected := storage.RejectedShapeTags(current)
	unanswered := storage.UnansweredShapeTags(current)
	views := make([]reflection.ProcessedView, 0, len(window))
	for _, art := range window {
		rejected = append(rejected, storage.RejectedShapeTags(art)...)
		unanswered = append(unanswered, storage.UnansweredShapeTags(art)...)
		views = append(views, toView(art, deny))
	}

	return reflection.ProposeInput{
		Current:             toView(current, deny),
		RecentWindow:        views,
		RejectedShapeTags:   dedupe(rejected),
		UnansweredShapeTags: dedupe(unanswered),
		AgentVersion:        r.cfg.AgentVersions.Reflection,
		Bootstrap:           bootstrap,
	}, nil
}

// recentWindow reads the most recent processed artifacts before the current
// one, up to the configured recent_window size (already clipped to the max at
// boot). The current artifact is excluded — Reflection receives it separately.
func (r *Router) recentWindow(currentID string) ([]storage.ProcessedArtifact, error) {
	ids, err := r.store.ListProcessedIDs()
	if err != nil {
		return nil, fmt.Errorf("validate: list processed: %w", err)
	}
	// ids are chronological; drop the current one and take the newest N.
	prior := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != currentID {
			prior = append(prior, id)
		}
	}
	n := r.cfg.RecentWindow
	if n <= 0 || n > len(prior) {
		n = len(prior)
	}
	prior = prior[len(prior)-n:]

	out := make([]storage.ProcessedArtifact, 0, len(prior))
	for _, id := range prior {
		art, err := r.store.ReadProcessed(id)
		if err != nil {
			return nil, fmt.Errorf("validate: read window artifact %q: %w", id, err)
		}
		out = append(out, art)
	}
	return out, nil
}

// toView renders a processed artifact as Reflection's slice view, dropping any
// person whose key is in the off-limits set. The on-disk artifact is never
// touched — the redaction is a copy-time transform on the agent-bound slice
// only (agent-contracts.md cross-cutting; acceptance-criteria 5.12).
func toView(art storage.ProcessedArtifact, offLimits map[string]bool) reflection.ProcessedView {
	view := reflection.ProcessedView{ID: art.ID, Notes: notesString(art.Notes)}
	for _, e := range art.Emotions {
		view.Emotions = append(view.Emotions, e.Name)
	}
	for _, th := range art.Themes {
		view.Themes = append(view.Themes, th.Name)
	}
	for _, p := range art.People {
		if offLimits[p.PersonKey] {
			continue // redacted from the agent-bound copy, fail closed
		}
		view.People = append(view.People, p.DisplayName)
	}
	return view
}

// proposalPauseActive reports whether the silent proposal pause is currently
// in effect. An expired pause is cleared here so the next propose resumes with
// a fresh counter (agent-contracts.md §3).
func (r *Router) proposalPauseActive(now time.Time) (bool, storage.ProposalPauseState, error) {
	st, err := r.store.ReadProposalPauseState()
	if err != nil {
		return false, storage.ProposalPauseState{}, fmt.Errorf("validate: read proposal pause state: %w", err)
	}
	if st.PausedUntil == nil {
		return false, st, nil
	}
	if now.Before(*st.PausedUntil) {
		return true, st, nil
	}
	// The pause has expired: clear it and resume with a zeroed counter.
	st = storage.ProposalPauseState{}
	if err := r.store.WriteProposalPauseState(st); err != nil {
		return false, st, fmt.Errorf("validate: clear expired pause: %w", err)
	}
	return false, st, nil
}

// resetProposalPause clears the pause counter after any answered proposal
// (accepted, nuanced, or rejected — agent-contracts.md §3).
func (r *Router) resetProposalPause() error {
	if err := r.store.WriteProposalPauseState(storage.ProposalPauseState{}); err != nil {
		return fmt.Errorf("validate: reset proposal pause state: %w", err)
	}
	return nil
}

// safetySession builds the Safety session context for this turn.
func (r *Router) safetySession(bootstrap bool) safety.SessionContext {
	return safety.SessionContext{Command: commandCheckin, BootstrapMode: bootstrap}
}

// supportingOrCurrent returns the proposal's supporting entry ids, or the
// current artifact id when the proposal cited none, so provenance always names
// at least one raw entry.
func supportingOrCurrent(supporting []string, currentID string) []string {
	if len(supporting) == 0 {
		return []string{currentID}
	}
	return supporting
}

// notesString collapses a nullable notes pointer to a plain string.
func notesString(notes *string) string {
	if notes == nil {
		return ""
	}
	return *notes
}

// toSet builds a lookup set from a slice.
func toSet(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	set := make(map[string]bool, len(xs))
	for _, x := range xs {
		set[x] = true
	}
	return set
}

// dedupe returns xs with duplicates and empty strings removed, order-stable.
func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
