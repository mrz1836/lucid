// Package reflection is the Reflection agent (agent-contracts.md §3). It has
// three sub-modes, one per command; this file implements reflection.propose,
// used inside /checkin: given the new processed artifact and a small recent
// window, it returns exactly one of a single hypothesis-framed pattern
// proposal, an honest "no pattern yet", or a soft contradiction.
//
// Like the other agents it holds no storage handle and imports no Ledger
// package — the router hands it the current artifact, the recent window, and
// the rejected/unanswered shape-tag unions, and nothing else (agent-contracts
// §3 allowed access). It is the only place a hypothesis is ever introduced.
// Model access is behind the provider boundary (ADR-0006); several paths are
// deterministic and make no model call at all (architecture P9).
package reflection

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/mrz1836/lucid/internal/provider"
)

// Outcome is the kind of proposal result (agent-contracts.md §3 Outputs).
type Outcome string

// The three propose outcomes.
const (
	// OutcomeProposal — one hypothesis-framed pattern with a shape_tag and
	// supporting entry ids.
	OutcomeProposal Outcome = "proposal"
	// OutcomeNoPattern — nothing useful to say yet.
	OutcomeNoPattern Outcome = "no_pattern"
	// OutcomeSoftContradiction — a gentle "earlier you said X, today reads
	// like Y" question citing exactly two entries.
	OutcomeSoftContradiction Outcome = "soft_contradiction"
)

// noPatternMessage is the fixed generic message for every no_pattern outcome
// (error-states.md §R-2). It is calm and hypothesis-safe.
const noPatternMessage = "I don't have enough yet to say anything useful — want to keep going?"

// shapeTagPattern is the shape_tag grammar (agent-contracts.md §3 validation):
// lowercase kebab, first char alnum, ≤ 41 chars total. The ≤ 6 hyphen-segment
// cap is enforced separately.
var shapeTagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// maxShapeTagSegments is the ceiling on hyphen-separated segments in a
// shape_tag (agent-contracts.md §3: "≤ 6 hyphen-segments").
const maxShapeTagSegments = 6

// ProcessedView is the slice of a processed artifact Reflection sees — the id
// plus the extracted names, already redacted of any off-limits person by the
// router at slice-build (agent-contracts.md cross-cutting rules). Reflection
// never receives raw bodies, people records, or anything outside the window.
type ProcessedView struct {
	ID       string
	Emotions []string
	Themes   []string
	People   []string
	Notes    string
}

// ProposeInput is the authorized slice for one propose call (agent-contracts
// §3 Inputs): the current artifact, the recent window, the rejected and
// unanswered shape-tag unions, the agent version to stamp, and the bootstrap
// flag (which suppresses proposals entirely).
type ProposeInput struct {
	Current             ProcessedView
	RecentWindow        []ProcessedView
	RejectedShapeTags   []string
	UnansweredShapeTags []string
	AgentVersion        string
	Bootstrap           bool
}

// ProposeResult is Reflection's propose payload (agent-contracts.md §3
// Outputs). For a proposal, ProposalText/ShapeTag/SupportingEntryIDs are set;
// for a soft contradiction, MessageText + exactly two SupportingEntryIDs; for
// no_pattern, MessageText only. NoLLM records that the outcome was reached
// deterministically (no model call), which the tests assert on the empty-
// window / empty-artifact / bootstrap paths (error-states.md §R-3/§R-4/§R-6).
type ProposeResult struct {
	Outcome            Outcome
	ProposalText       string
	MessageText        string
	ShapeTag           string
	SupportingEntryIDs []string
	NoLLM              bool
}

// proposalReply is the parsed model reply for one propose call.
type proposalReply struct {
	Outcome            string   `json:"outcome"`
	ProposalText       string   `json:"proposal_text"`
	MessageText        string   `json:"message_text"`
	ShapeTag           string   `json:"shape_tag"`
	SupportingEntryIDs []string `json:"supporting_entry_ids"`
}

// Propose runs one reflection.propose turn. It never returns an error: every
// failure degrades to no_pattern (agent-contracts.md §3 failure handling; the
// loop is never blocked on Reflection). Three paths short-circuit with no
// model call — bootstrap (§R-6), an empty recent window (§R-3), and a current
// artifact with no emotions/themes/people (§R-4). Otherwise the model is asked
// once and, on malformed or invalid output, retried once with the rejected and
// unanswered tags restated as a denylist (§R-1/§R-5) before degrading.
func Propose(ctx context.Context, in ProposeInput, p provider.Provider) ProposeResult {
	if in.Bootstrap {
		return noPattern(true)
	}
	if len(in.RecentWindow) == 0 {
		return noPattern(true)
	}
	if in.Current.isEmpty() {
		return noPattern(true)
	}

	for _, strict := range []bool{false, true} {
		reply, ok := proposeOnce(ctx, in, p, strict)
		if ok {
			return fromReply(reply)
		}
	}
	return noPattern(false)
}

// proposeOnce performs a single propose completion, parses it, and validates
// the payload against the §3 rules for its outcome. It reports ok=false for a
// transport error, malformed JSON, or a payload that breaks a validation rule
// (bad shape_tag, a rejected/unanswered tag, a citation outside the window,
// the wrong soft-contradiction shape) — each of which the caller retries or
// degrades on.
func proposeOnce(ctx context.Context, in ProposeInput, p provider.Provider, strict bool) (proposalReply, bool) {
	resp, err := p.Complete(ctx, provider.Request{
		Intent:   "reflection.propose",
		System:   proposeSystem(in, strict),
		Messages: windowSlice(in),
	})
	if err != nil {
		return proposalReply{}, false
	}
	var reply proposalReply
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &reply); jsonErr != nil {
		return proposalReply{}, false
	}
	if !validReply(reply, in) {
		return proposalReply{}, false
	}
	return reply, true
}

// validReply enforces the §3 validation rules per outcome. A no_pattern is
// always usable (it says nothing). A proposal must be hypothesis-shaped with a
// grammar-valid shape_tag that is not on either denylist and cite at least one
// in-window entry. A soft_contradiction must cite exactly two in-window
// entries and end in a question mark.
func validReply(reply proposalReply, in ProposeInput) bool {
	switch Outcome(reply.Outcome) {
	case OutcomeNoPattern:
		return true
	case OutcomeProposal:
		return validProposal(reply, in)
	case OutcomeSoftContradiction:
		return validSoftContradiction(reply, in)
	case OutcomeRecall, OutcomeAnswer, OutcomeInsufficient:
		return false // recall/answer/insufficient belong to /reflect and /ask, never a propose reply
	default:
		return false
	}
}

// validProposal checks the proposal outcome: non-empty text, ≥ 1 supporting
// id all in the window, and a shape_tag that matches the grammar, stays within
// the segment cap, and is on neither the rejected nor the unanswered denylist.
func validProposal(reply proposalReply, in ProposeInput) bool {
	if strings.TrimSpace(reply.ProposalText) == "" {
		return false
	}
	if len(reply.SupportingEntryIDs) == 0 || !allInWindow(reply.SupportingEntryIDs, in) {
		return false
	}
	return validShapeTag(reply.ShapeTag, in)
}

// validSoftContradiction checks the soft_contradiction outcome: a non-empty
// message ending in "?", citing exactly two entries both in the window.
func validSoftContradiction(reply proposalReply, in ProposeInput) bool {
	msg := strings.TrimSpace(reply.MessageText)
	if msg == "" || !strings.HasSuffix(msg, "?") {
		return false
	}
	if len(reply.SupportingEntryIDs) != 2 || !allInWindow(reply.SupportingEntryIDs, in) {
		return false
	}
	return true
}

// validShapeTag reports whether tag is grammar-valid, within the segment cap,
// and absent from both the rejected and unanswered denylists (silence is not
// rejection, but a shape the user let pass is not re-proposed while it sits in
// the window — agent-contracts.md §3 forbidden behavior).
func validShapeTag(tag string, in ProposeInput) bool {
	if !shapeTagPattern.MatchString(tag) {
		return false
	}
	if strings.Count(tag, "-")+1 > maxShapeTagSegments {
		return false
	}
	if slices.Contains(in.RejectedShapeTags, tag) || slices.Contains(in.UnansweredShapeTags, tag) {
		return false
	}
	return true
}

// allInWindow reports whether every id is the current artifact or a member of
// the recent window — Reflection may only cite entries it was given.
func allInWindow(ids []string, in ProposeInput) bool {
	known := make(map[string]bool, len(in.RecentWindow)+1)
	known[in.Current.ID] = true
	for _, v := range in.RecentWindow {
		known[v.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return false
		}
	}
	return true
}

// fromReply maps a validated model reply into a ProposeResult (the model path,
// so NoLLM is false).
func fromReply(reply proposalReply) ProposeResult {
	switch Outcome(reply.Outcome) {
	case OutcomeProposal:
		return ProposeResult{
			Outcome:            OutcomeProposal,
			ProposalText:       reply.ProposalText,
			ShapeTag:           reply.ShapeTag,
			SupportingEntryIDs: reply.SupportingEntryIDs,
		}
	case OutcomeSoftContradiction:
		return ProposeResult{
			Outcome:            OutcomeSoftContradiction,
			MessageText:        reply.MessageText,
			SupportingEntryIDs: reply.SupportingEntryIDs,
		}
	case OutcomeNoPattern, OutcomeRecall, OutcomeAnswer, OutcomeInsufficient:
		return noPattern(false)
	default:
		return noPattern(false)
	}
}

// noPattern builds the no_pattern result with the fixed message; noLLM records
// whether the outcome was reached without a model call.
func noPattern(noLLM bool) ProposeResult {
	return ProposeResult{Outcome: OutcomeNoPattern, MessageText: noPatternMessage, NoLLM: noLLM}
}

// isEmpty reports whether a processed view carries no emotions, themes, or
// people — the §R-4 "not enough signal" short-circuit.
func (v ProcessedView) isEmpty() bool {
	return len(v.Emotions) == 0 && len(v.Themes) == 0 && len(v.People) == 0
}

// windowSlice renders the current artifact and the recent window as the single
// user message in the authorized slice. This is the entirety of what
// Reflection shows a model — no raw bodies, no Ledger.
func windowSlice(in ProposeInput) []provider.Message {
	var b strings.Builder
	b.WriteString("CURRENT ENTRY\n")
	writeView(&b, in.Current)
	b.WriteString("\nRECENT WINDOW (older to newer)\n")
	for _, v := range in.RecentWindow {
		writeView(&b, v)
	}
	return []provider.Message{{Role: provider.RoleUser, Content: b.String()}}
}

// writeView renders one processed view as a compact, id-anchored block the
// model can cite by id.
func writeView(b *strings.Builder, v ProcessedView) {
	b.WriteString("- id: " + v.ID + "\n")
	if len(v.Emotions) > 0 {
		b.WriteString("  emotions: " + strings.Join(v.Emotions, ", ") + "\n")
	}
	if len(v.Themes) > 0 {
		b.WriteString("  themes: " + strings.Join(v.Themes, ", ") + "\n")
	}
	if len(v.People) > 0 {
		b.WriteString("  people: " + strings.Join(v.People, ", ") + "\n")
	}
	if strings.TrimSpace(v.Notes) != "" {
		b.WriteString("  notes: " + v.Notes + "\n")
	}
}

// proposeSystem is the instruction for a propose call. It is kept clean of
// every blocklist phrase so the prompt itself passes the "zero hits" sweep
// (acceptance-criteria.md Phase 5). strict adds the retry emphasis and, when
// present, restates the rejected and unanswered tags as a "do not propose"
// denylist (error-states.md §R-1/§R-5).
func proposeSystem(in ProposeInput, strict bool) string {
	var b strings.Builder
	b.WriteString("You are Lucid's Reflection agent in propose mode. Read the current entry and ")
	b.WriteString("the recent window and return at most ONE tentative, hypothesis-framed pattern. ")
	b.WriteString("Use humble framing (\"one possible pattern\", \"it sounds like\") — never a label ")
	b.WriteString("or a certainty. Cite only entry ids you were shown. Reply ONLY with JSON, one of:\n")
	b.WriteString(`{"outcome":"proposal","proposal_text":"...","shape_tag":"kebab-case-tag",`)
	b.WriteString(`"supporting_entry_ids":["raw_..."]}` + "\n")
	b.WriteString(`{"outcome":"no_pattern","message_text":"..."}` + "\n")
	b.WriteString(`{"outcome":"soft_contradiction","message_text":"...?","supporting_entry_ids":["raw_a","raw_b"]}` + "\n")
	b.WriteString("A shape_tag is lowercase kebab-case, up to six segments. ")
	b.WriteString("A soft_contradiction cites exactly two ids and ends in a question mark.")
	if denied := denylist(in); denied != "" {
		b.WriteString(" Do not propose any of these shapes again: " + denied + ".")
	}
	if strict {
		b.WriteString(" Your previous reply was not valid. Reply with one JSON object and nothing else.")
	}
	return b.String()
}

// denylist joins the rejected and unanswered shape tags into a single comma
// list for the strict-retry prompt, deduplicated and empty when neither set
// has anything.
func denylist(in ProposeInput) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range slices.Concat(in.RejectedShapeTags, in.UnansweredShapeTags) {
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return strings.Join(out, ", ")
}
