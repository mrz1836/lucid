package reflection

import (
	"context"
	"strings"

	"github.com/mrz1836/lucid/internal/agents/agentutil"
	"github.com/mrz1836/lucid/internal/provider"
)

// OutcomeRecall is the surface_for_recall outcome (agent-contracts.md §3
// Outputs): an ordered set of validated insights re-surfaced for the user to
// confirm, soften, or retire. surface_for_recall never proposes a new pattern.
const OutcomeRecall Outcome = "recall"

// RecallScope selects the recall window (agent-contracts.md §3): the past week
// for `/reflect`, or every accepted insight for `/reflect gate`. The agent uses
// it only to frame the instruction; the router builds the window.
type RecallScope string

// The two recall scopes.
const (
	ScopeWeek RecallScope = "week"
	ScopeGate RecallScope = "gate"
)

// InsightView is the slice of one validated insight Reflection sees for recall
// (agent-contracts.md §3 surface_for_recall input): its id, its canonical
// statement, and the attached rule if any. Reflection receives nothing else —
// no provenance, no raw bodies, no people records.
type InsightView struct {
	ID        string
	Statement string
	Rule      string // "" when the insight carries no rule
}

// SurfacedInsight is one recall surface the agent returns. Resonance is the
// statement-resonance question (model-authored, or the verbatim fallback);
// Statement is carried through so the router can rebuild the verbatim line if
// Safety blocks the model's wording. Ruled/Rule let the router append the fixed
// rule question verbatim after the Safety gate — the rule is the user's own
// testimony, surfaced without judgment (agent-contracts.md §3 "Rules").
type SurfacedInsight struct {
	ID        string
	Statement string
	Resonance string
	Ruled     bool
	Rule      string
}

// RecallInput is the authorized slice for one surface_for_recall call: the
// scope, the window of validated insights, and the agent version to stamp.
type RecallInput struct {
	Scope        RecallScope
	Window       []InsightView
	AgentVersion string
}

// RecallResult is Reflection's recall payload. Outcome is always recall.
// Ordered is a subset of the window by id (no novel ids). NoLLM records that
// the result was reached deterministically (an empty window). Fallback records
// that the verbatim degrade fired after two malformed model replies
// (error-states.md §R-8) — the router surfaces the insights unchanged.
type RecallResult struct {
	Outcome  Outcome
	Ordered  []SurfacedInsight
	NoLLM    bool
	Fallback bool
}

// recallReply is the parsed model reply for one surface_for_recall call.
type recallReply struct {
	Outcome         string             `json:"outcome"`
	OrderedInsights []recallReplyEntry `json:"ordered_insights"`
}

// recallReplyEntry is one model-authored surface: an in-window id and the
// resonance question for it.
type recallReplyEntry struct {
	ID          string `json:"id"`
	SurfaceText string `json:"surface_text"`
}

// SurfaceForRecall runs one reflection.surface_for_recall turn. Like propose it
// never returns an error: an empty window short-circuits with no model call,
// and two malformed replies degrade to surfacing every insight verbatim
// (agent-contracts.md §3 failure handling; error-states.md §R-8). The rule
// question is never part of the model's job — the router appends it
// deterministically from Ruled/Rule, so a rule always surfaces verbatim
// regardless of what the model returns.
func SurfaceForRecall(ctx context.Context, in RecallInput, p provider.Provider) RecallResult {
	if len(in.Window) == 0 {
		return RecallResult{Outcome: OutcomeRecall, NoLLM: true}
	}

	for _, strict := range []bool{false, true} {
		reply, ok := recallOnce(ctx, in, p, strict)
		if ok {
			return RecallResult{Outcome: OutcomeRecall, Ordered: fromRecallReply(reply, in)}
		}
	}
	return RecallResult{Outcome: OutcomeRecall, Ordered: verbatimSurfaces(in), Fallback: true}
}

// recallOnce performs a single surface_for_recall completion, parses it, and
// validates it: the outcome must be recall, the ordered_insights non-empty,
// every id in the window, and every surface_text non-empty. Any failure —
// transport error, malformed JSON, or a rule break — reports ok=false so the
// caller retries or degrades.
func recallOnce(ctx context.Context, in RecallInput, p provider.Provider, strict bool) (recallReply, bool) {
	reply, err := agentutil.CompleteJSON[recallReply](ctx, p, provider.Request{
		Intent:   "reflection.surface_for_recall",
		System:   recallSystem(in, strict),
		Messages: recallSlice(in),
	})
	if err != nil {
		return recallReply{}, false
	}
	if !validRecall(reply, in) {
		return recallReply{}, false
	}
	return reply, true
}

// validRecall enforces the §3 recall rules: the recall outcome, a non-empty
// ordered set, and every entry naming an in-window id with a non-empty surface.
func validRecall(reply recallReply, in RecallInput) bool {
	if Outcome(reply.Outcome) != OutcomeRecall {
		return false
	}
	if len(reply.OrderedInsights) == 0 {
		return false
	}
	known := windowIDs(in)
	for _, e := range reply.OrderedInsights {
		if !known[e.ID] {
			return false
		}
		if strings.TrimSpace(e.SurfaceText) == "" {
			return false
		}
	}
	return true
}

// fromRecallReply maps a validated model reply into ordered surfaces, carrying
// each insight's statement and rule through from the window by id so the router
// can gate the resonance and append the rule question.
func fromRecallReply(reply recallReply, in RecallInput) []SurfacedInsight {
	byID := make(map[string]InsightView, len(in.Window))
	for _, v := range in.Window {
		byID[v.ID] = v
	}
	out := make([]SurfacedInsight, 0, len(reply.OrderedInsights))
	for _, e := range reply.OrderedInsights {
		v := byID[e.ID]
		out = append(out, surfaced(v, e.SurfaceText))
	}
	return out
}

// verbatimSurfaces surfaces every window insight with the fixed resonance line,
// in window order — the deterministic R-8 fallback (no novel framing).
func verbatimSurfaces(in RecallInput) []SurfacedInsight {
	out := make([]SurfacedInsight, 0, len(in.Window))
	for _, v := range in.Window {
		out = append(out, surfaced(v, VerbatimResonance(v.Statement)))
	}
	return out
}

// surfaced builds one SurfacedInsight from a view and a resonance line.
func surfaced(v InsightView, resonance string) SurfacedInsight {
	return SurfacedInsight{
		ID:        v.ID,
		Statement: v.Statement,
		Resonance: resonance,
		Ruled:     strings.TrimSpace(v.Rule) != "",
		Rule:      v.Rule,
	}
}

// VerbatimResonance is the fixed statement-resonance line used on the R-8
// fallback and whenever Safety blocks the model's wording (error-states.md
// §R-8; agent-contracts.md §3). It quotes the user's canonical statement and
// asks a single hypothesis-safe question — no novel framing.
func VerbatimResonance(statement string) string {
	return "Earlier you saved: '" + strings.TrimSpace(statement) + "'. Still resonating?"
}

// windowIDs builds the in-window id set for the citation check.
func windowIDs(in RecallInput) map[string]bool {
	ids := make(map[string]bool, len(in.Window))
	for _, v := range in.Window {
		ids[v.ID] = true
	}
	return ids
}

// recallSlice renders the window as the single user message in the authorized
// slice. It shows the model each insight's id and canonical statement — the
// entirety of what recall sees. The rule is deliberately omitted: the model
// never authors the rule question.
func recallSlice(in RecallInput) []provider.Message {
	var b strings.Builder
	b.WriteString("VALIDATED INSIGHTS (surface each, most recent first)\n")
	for _, v := range in.Window {
		b.WriteString("- id: " + v.ID + "\n")
		b.WriteString("  statement: " + oneLine(v.Statement) + "\n")
	}
	return []provider.Message{{Role: provider.RoleUser, Content: b.String()}}
}

// oneLine collapses a multi-line statement to a single line for the slice.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// recallSystem is the instruction for a surface_for_recall call. It is kept
// clean of every blocklist phrase so the prompt itself passes the "zero hits"
// sweep (acceptance-criteria.md Phase 6). strict adds the retry emphasis.
func recallSystem(in RecallInput, strict bool) string {
	var b strings.Builder
	b.WriteString("You are Lucid's Reflection agent in recall mode. For each validated insight ")
	b.WriteString("below, write one short, warm question asking whether it still fits — quote or ")
	b.WriteString("paraphrase the user's own statement, use tentative framing, and never add advice, ")
	b.WriteString("a label, or a new pattern. Surface only the ids you were shown. ")
	if in.Scope == ScopeGate {
		b.WriteString("This is a gate review across everything the user has validated. ")
	}
	b.WriteString("Reply ONLY with JSON:\n")
	b.WriteString(`{"outcome":"recall","ordered_insights":[{"id":"i_...","surface_text":"..."}]}` + "\n")
	if strict {
		b.WriteString("Your previous reply was not valid. Reply with one JSON object and nothing else, ")
		b.WriteString("one entry per insight id shown.")
	}
	return b.String()
}
