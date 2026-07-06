// Package safety is the Safety/Consent agent (agent-contracts.md §4): the
// last filter on every agent-authored outbound Lucid message and the gate on
// any proposed external action. It passes safe output through unchanged,
// rewrites soft violations into hypothesis framing, and blocks the rest with
// a flagged reason code. It is the only agent that ever blocks another
// agent's output.
//
// Every pass and block decision is reachable WITHOUT a model call — the gate
// is a compiled phrase blocklist (blocklist.go) plus a few structural checks,
// so it is fast and offline-safe. The single model path is a rewrite, and
// only with the candidate message itself as the slice. A failed rewrite
// downgrades to block; a failed Safety/Consent never falls through to pass
// (agent-contracts.md §4; error-states.md §Sf-4).
package safety

import (
	"context"
	"strings"

	"github.com/mrz1836/lucid/internal/provider"
)

// Decision is the outcome of evaluating one candidate message.
type Decision string

// The three decisions (agent-contracts.md §4 Outputs).
const (
	// Pass returns the candidate text unchanged.
	Pass Decision = "pass"
	// Rewrite returns a corrected text that preserves intent but obeys the
	// rules — used only for soft overclaim / flattening / performance hits.
	Rewrite Decision = "rewrite"
	// Block returns no text; the router substitutes a fixed fallback.
	Block Decision = "block"
)

// ReasonCode names why a decision was reached (agent-contracts.md §4). It is
// recorded for audit and never surfaced to the user.
type ReasonCode string

// The reason codes (agent-contracts.md §4 Outputs).
const (
	ReasonOK                    ReasonCode = "ok"
	ReasonDiagnosticLanguage    ReasonCode = "diagnostic_language"
	ReasonExternalActionAttempt ReasonCode = "external_action_attempt"
	ReasonContextOverrun        ReasonCode = "context_overrun"
	ReasonUnverifiedClaim       ReasonCode = "unverified_claim"
	ReasonAgentSelfAttempt      ReasonCode = "agent_self_attempt"
	ReasonScopeViolation        ReasonCode = "scope_violation"
	ReasonPhraseBlocklist       ReasonCode = "phrase_blocklist"
)

// The agent that authored a candidate (agent-contracts.md §4 Inputs).
const (
	FromReflection          = "reflection"
	FromIntake              = "intake"
	FromStructuringRendered = "structuring_rendered"
)

// Intent is the router plan that produced the candidate (agent-contracts.md
// §4 Inputs). Only propose_pattern and answer carry special structural rules;
// the rest are ordinary text the content checks govern.
type Intent string

// The candidate intents (agent-contracts.md §4 Inputs).
const (
	IntentAskQuestion        Intent = "ask_question"
	IntentAckCapture         Intent = "ack_capture"
	IntentProposePattern     Intent = "propose_pattern"
	IntentNoPattern          Intent = "no_pattern"
	IntentSoftContradiction  Intent = "soft_contradiction"
	IntentRecall             Intent = "recall"
	IntentAnswer             Intent = "answer"
	IntentAnswerInsufficient Intent = "answer_insufficient"
	IntentValidationFollowup Intent = "validation_followup"
)

// Candidate is the message Safety evaluates plus the metadata the rules need
// (agent-contracts.md §4 Inputs). Citations and AuthorizedIDs support the
// answer path's out-of-slice check (§Sf-7); the propose path leaves both
// empty and the check is inert. UserAuthored marks verbatim user text quoted
// back to the user, which is exempt from the external-action and diagnostic
// rules (product-principles.md §6 scope; §4 "verbatim user text is exempt").
type Candidate struct {
	FromAgent          string
	Intent             Intent
	Text               string
	SupportingEntryIDs []string
	ShapeTag           string
	Citations          []string
	AuthorizedIDs      []string
	UserAuthored       bool
}

// SessionContext carries the command and bootstrap flag (agent-contracts.md
// §4 Inputs). bootstrap_mode true blocks any propose_pattern candidate.
type SessionContext struct {
	Command       string
	BootstrapMode bool
}

// Result is the evaluated decision (agent-contracts.md §4 Outputs). Text is
// the candidate on pass, the replacement on rewrite, and empty on block.
type Result struct {
	Decision   Decision
	Text       string
	ReasonCode ReasonCode
	Notes      string
}

// pass, block, and rewriteResult build the three result shapes so the
// decision table below reads as intent, not plumbing.
func pass(text string) Result {
	return Result{Decision: Pass, Text: text, ReasonCode: ReasonOK, Notes: "no violation"}
}

func block(reason ReasonCode, notes string) Result {
	return Result{Decision: Block, Text: "", ReasonCode: reason, Notes: notes}
}

// Evaluate applies the §4 decision table to one candidate. The order encodes
// precedence: empty first, then the structural propose_pattern rules, then the
// user-authored exemption, then content checks from hardest (external action)
// to softest (rewrite), then pass. Only the rewrite branch may call the model;
// every other branch is deterministic and needs no provider.
func Evaluate(ctx context.Context, cand Candidate, sc SessionContext, p provider.Provider) Result {
	text := strings.TrimSpace(cand.Text)
	if text == "" {
		// Nothing to say — block with reason ok (error-states.md §Sf-5).
		return block(ReasonOK, "empty candidate")
	}

	// Structural gate on a pattern proposal (agent-contracts.md §4 validation
	// rules; error-states.md §Sf-6). A propose_pattern must carry a shape_tag,
	// and no proposal is permitted during bootstrap.
	if cand.FromAgent == FromReflection && cand.Intent == IntentProposePattern {
		if sc.BootstrapMode {
			return block(ReasonScopeViolation, "propose_pattern during bootstrap_mode")
		}
		if strings.TrimSpace(cand.ShapeTag) == "" {
			return block(ReasonScopeViolation, "propose_pattern without a shape_tag")
		}
	}

	// Verbatim user text quoted back is testimony, not agent speech: exempt
	// from the external-action-verb and diagnostic-language rules (§6 scope).
	if cand.UserAuthored {
		return pass(cand.Text)
	}

	// External-action verbs always block — the MVP has no send path, so the
	// only valid decision is block (error-states.md §Sf-2). Highest content
	// precedence so an "email M." can never be softened into a pass.
	if matchesAny(externalActionPatterns, text) {
		return block(ReasonExternalActionAttempt, "external-action verb")
	}

	// An answer citing an id outside the authorized slice is unverifiable
	// (error-states.md §Sf-7). Checked before the softer content rules so a
	// fabricated citation is never rewritten into looking grounded.
	if cand.Intent == IntentAnswer && citesOutsideSlice(cand) {
		return block(ReasonUnverifiedClaim, "citation not in the authorized slice")
	}

	// Advice / recommendation phrasing blocks — no agent advises in the MVP
	// (error-states.md §Sf-8).
	if matchesAny(coachingPatterns, text) {
		return block(ReasonAgentSelfAttempt, "advice / recommendation phrasing")
	}

	// A clinical label presented as fact cannot be softened — hard block
	// (error-states.md §Sf-3).
	if matchesAny(diagnosticLabelPatterns, text) {
		return block(ReasonDiagnosticLanguage, "diagnostic / clinical label")
	}

	// Soft overclaim / flattening / performance: rewrite into hypothesis
	// framing (error-states.md §Sf-1). The one model path; a failure here
	// downgrades to block, never pass (§Sf-4).
	if matchesAny(overclaimPatterns, text) {
		return rewriteCandidate(ctx, cand, p)
	}

	return pass(cand.Text)
}

// citesOutsideSlice reports whether any of the candidate's citations is
// absent from its authorized-id set. With no citations it is trivially false,
// so an answer that cites nothing is not blocked on this rule.
func citesOutsideSlice(cand Candidate) bool {
	allowed := make(map[string]bool, len(cand.AuthorizedIDs))
	for _, id := range cand.AuthorizedIDs {
		allowed[id] = true
	}
	for _, c := range cand.Citations {
		if !allowed[c] {
			return true
		}
	}
	return false
}

// rewriteCandidate performs the single model rewrite: it asks the provider to
// soften the flagged wording into hypothesis framing, with the candidate as
// its only slice, then validates the reply. The rewrite must be non-empty,
// carry no blocklist hit, and preserve every supporting_entry_id that appeared
// in the original text. Any failure — a model error, an empty reply, a
// still-flagged reply, or a dropped citation — downgrades to block with
// reason_code phrase_blocklist (agent-contracts.md §4; error-states.md §Sf-4).
func rewriteCandidate(ctx context.Context, cand Candidate, p provider.Provider) Result {
	if p == nil {
		return block(ReasonPhraseBlocklist, "rewrite required but no provider")
	}
	resp, err := p.Complete(ctx, provider.Request{
		Intent:   "safety.rewrite",
		System:   rewriteSystem(),
		Messages: []provider.Message{{Role: provider.RoleUser, Content: cand.Text}},
	})
	if err != nil {
		return block(ReasonPhraseBlocklist, "rewrite completion failed")
	}
	out := strings.TrimSpace(resp.Content)
	if out == "" {
		return block(ReasonPhraseBlocklist, "rewrite produced empty text")
	}
	if MatchesBlocklist(out) {
		return block(ReasonPhraseBlocklist, "rewrite still hit the blocklist")
	}
	if !preservesSupportingIDs(cand, out) {
		return block(ReasonPhraseBlocklist, "rewrite dropped a supporting entry id")
	}
	return Result{Decision: Rewrite, Text: out, ReasonCode: ReasonPhraseBlocklist, Notes: "softened overclaim"}
}

// preservesSupportingIDs reports whether every supporting_entry_id that was
// present in the original candidate text still appears in the rewrite. Ids not
// referenced in the original (the common case — proposals frame in natural
// language, not raw ids) impose no constraint (agent-contracts.md §4
// validation rule on rewrite).
func preservesSupportingIDs(cand Candidate, rewritten string) bool {
	for _, id := range cand.SupportingEntryIDs {
		if strings.Contains(cand.Text, id) && !strings.Contains(rewritten, id) {
			return false
		}
	}
	return true
}

// rewriteSystem is the instruction for the single rewrite completion. It is
// deliberately clean of every blocklist phrase so the prompt itself passes the
// "zero hits across prompt files" sweep (acceptance-criteria.md Phase 5). The
// rewrite may only soften or reframe — it may not add information the
// originating agent did not have (agent-contracts.md §4 forbidden behavior).
func rewriteSystem() string {
	var b strings.Builder
	b.WriteString("You are Lucid's Safety filter. Rewrite the one message below so it uses ")
	b.WriteString("hypothesis framing — tentative, non-labeling, non-performative. Fix only the ")
	b.WriteString("flagged wording; keep the rest, keep it brief, and add no information the ")
	b.WriteString("message did not already carry. Prefer openings like \"I noticed a possible ")
	b.WriteString("pattern:\" or \"it sounds like\". Reply with the rewritten message text only, ")
	b.WriteString("no quotes and no preamble.")
	return b.String()
}
