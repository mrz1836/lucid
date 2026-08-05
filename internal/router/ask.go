package router

import (
	"context"
	"fmt"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// commandAsk is the verb for the /ask session context handed to Safety.
const commandAsk = "/ask"

// askFallback is the fixed copy the router surfaces when Safety blocks a
// grounded answer — an out-of-slice citation (§Sf-7) or advice (§Sf-8). It is
// the same calm, status-free fallback the proposal path uses (error-states.md
// cross-cutting).
const askFallback = proposalFallback

// AskRequest carries the inputs for one /ask turn. Provider is the model
// boundary the grounded-answer agent reaches through; the router owns every
// Ledger read and builds the two authorized slices.
type AskRequest struct {
	Question string
	Provider provider.Provider
}

// AskResult reports what an /ask turn surfaced. Outcome is Reflection's
// answer_grounded outcome (answer | insufficient). Message is what the user
// saw: the grounded answer on pass, the Safety-softened text on rewrite, or the
// fixed fallback on block. Citations carries the grounded references on an
// answer. Blocked records that Safety held the answer. /ask never writes, so
// there is no id or wrote flag.
type AskResult struct {
	Outcome    reflection.Outcome
	Message    string
	Decision   safety.Decision
	ReasonCode safety.ReasonCode
	Citations  []reflection.Citation
	Blocked    bool
}

// Ask executes /ask: it builds the insights slice (accepted, cap
// ask_insights_cap, most recent by the last status transition), the reflections
// slice (cap ask_reflections_cap, most recent ISO-week), and the self-facts
// slice (active only, cap self_facts_cap, ordered by namespace priority then
// key), runs reflection.answer_grounded over them and the question, and gates an
// answer through Safety. It is strictly read-only — nothing under ~/.lucid/
// changes (agent-contracts.md §3; acceptance-criteria.md Phase 7; S-6), which is
// why the self-facts read tolerates an absent file rather than scaffolding one.
// Every slice empty short-circuits to the insufficient message with no model
// call (error-states.md §R-10). An answer citing an id outside the slice, or
// giving advice, is blocked by Safety and the fallback is surfaced
// (§Sf-7/§Sf-8).
func (r *Router) Ask(ctx context.Context, req AskRequest) (AskResult, error) {
	insights, err := r.store.ReadAcceptedInsights(r.cfg.AskInsightsCap)
	if err != nil {
		return AskResult{}, fmt.Errorf("ask: read insights slice: %w", err)
	}
	reflections, err := r.store.ReadReflections(r.cfg.AskReflectionsCap)
	if err != nil {
		return AskResult{}, fmt.Errorf("ask: read reflections slice: %w", err)
	}
	selfLog, err := r.store.ReadSelfFacts()
	if err != nil {
		return AskResult{}, fmt.Errorf("ask: read self facts slice: %w", err)
	}
	facts := engine.GroundingSelfFacts(engine.LatestSelfFacts(selfLog), r.cfg.SelfFactsCap)

	ans := reflection.AnswerGrounded(ctx, reflection.AnswerInput{
		Question:     req.Question,
		Insights:     toInsightViews(insights),
		Reflections:  toWeeklyReflectionViews(reflections),
		SelfFacts:    toSelfFactViews(facts),
		AgentVersion: r.cfg.AgentVersions.Reflection,
	}, req.Provider)

	// insufficient is fixed, blocklist-clean router/agent copy — like the
	// no_pattern path, it is surfaced without a Safety round-trip.
	if ans.Outcome == reflection.OutcomeInsufficient {
		return AskResult{Outcome: ans.Outcome, Message: ans.AnswerText}, nil
	}

	dec := safety.Evaluate(ctx, safety.Candidate{
		FromAgent:     safety.FromReflection,
		Intent:        safety.IntentAnswer,
		Text:          ans.AnswerText,
		Citations:     citationIDs(ans.Citations),
		AuthorizedIDs: sliceIDs(insights, reflections, facts),
	}, safety.SessionContext{Command: commandAsk}, req.Provider)

	res := AskResult{Outcome: ans.Outcome, Decision: dec.Decision, ReasonCode: dec.ReasonCode, Citations: ans.Citations}
	if dec.Decision == safety.Block {
		res.Message = askFallback
		res.Blocked = true
		return res, nil
	}
	res.Message = dec.Text // pass returns the original; rewrite the softened text
	return res, nil
}

// toWeeklyReflectionViews maps stored reflections to the agent's answer slice: id and
// one-line summary. Reflection sees nothing else of a reflection record.
func toWeeklyReflectionViews(recs []storage.Reflection) []reflection.WeeklyReflectionView {
	out := make([]reflection.WeeklyReflectionView, 0, len(recs))
	for _, rec := range recs {
		out = append(out, reflection.WeeklyReflectionView{ID: rec.ID, Summary: rec.Summary})
	}
	return out
}

// toSelfFactViews maps folded self facts to the agent's answer slice: id, key
// and value. Reflection sees nothing else of a fact — no origin date, no note,
// no recorded-at stamp — and never a path, because the router composes the
// values itself (Sanctuary; the projection shape the companion panel uses).
func toSelfFactViews(facts []engine.SelfFact) []reflection.SelfFactView {
	out := make([]reflection.SelfFactView, 0, len(facts))
	for _, f := range facts {
		out = append(out, reflection.SelfFactView{ID: f.ID, Key: f.Key, Value: f.Value})
	}
	return out
}

// citationIDs flattens the answer's citations to their ids for the Safety
// out-of-slice check (§Sf-7).
func citationIDs(cites []reflection.Citation) []string {
	out := make([]string, 0, len(cites))
	for _, c := range cites {
		out = append(out, c.ID)
	}
	return out
}

// sliceIDs is the authorized-id set Safety checks citations against: every
// insight id, every reflection id, and every self-fact id the router put in
// front of the agent. The self-fact arm is the whole reason a fact carries a
// stable minted id rather than being addressed by its (mutable) key — without
// it Safety would block a correctly grounded self-fact citation as
// out-of-slice (§Sf-7) and the grounding would be inert.
func sliceIDs(insights []storage.Insight, reflections []storage.Reflection, facts []engine.SelfFact) []string {
	out := make([]string, 0, len(insights)+len(reflections)+len(facts))
	for _, ins := range insights {
		out = append(out, ins.ID)
	}
	for _, rec := range reflections {
		out = append(out, rec.ID)
	}
	for _, f := range facts {
		out = append(out, f.ID)
	}
	return out
}
