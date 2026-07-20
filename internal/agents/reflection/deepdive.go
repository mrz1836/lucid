// This file implements reflection.weekly_deepdive, the analysis behind the
// read-only `lucid reflect week` surface. Unlike propose (which reads one new
// artifact and a short window inside /checkin), the deep-dive reads the whole
// week's projection bundle and authors the Sunday narrative — a week summary,
// wins, misses, body/pain context, habit-change signals, and next-week
// adjustments — plus at most one hypothesis-framed candidate pattern with
// raw-entry-id citations, framed through the active consented lens when one is
// set.
//
// Like the other sub-modes it never returns an error and holds no storage
// handle: the router hands it an already-projected, sanctuary-safe slice and
// nothing else. An empty week short-circuits with no model call (P9), and every
// malformed reply degrades to a calm fallback. It introduces a candidate but
// never persists one — the router routes any surfaced candidate through the
// existing resonance gate, and Safety gates every surfaced line downstream, so
// this agent stays within already-licensed, hypothesis-framed vocabulary.
package reflection

import (
	"context"
	"slices"
	"strings"

	"github.com/mrz1836/lucid/internal/agents/agentutil"
	"github.com/mrz1836/lucid/internal/provider"
)

// deepDiveFallback is the fixed, blocklist-clean line the deep-dive degrades to
// after two malformed replies (mirroring the answer_grounded transport
// fallback). The week's entries are untouched — the read simply could not be
// composed this turn.
const deepDiveFallback = "I couldn't pull the week together just now — your entries are all saved; want to try again?"

// DeepEntry is one citable raw entry the deep-dive may reference by id. It is
// the user's own words, read through the sanctioned `/day` projection, so a
// candidate can back a hypothesis with a raw-entry-id citation.
type DeepEntry struct {
	ID   string
	Date string
	Text string
}

// DeepSignal is one body/observation signal folded into the week: the kind and
// the logical day it was logged. It carries no value payload — only that a kind
// (pain, mood, intake, …) was recorded when — enough for the body-pain and
// habit-change reads without reaching past the projection.
type DeepSignal struct {
	Kind string
	Date string
}

// DeepInsight is one already-validated insight carried for continuity, so the
// deep-dive can relate the week to what the user has previously confirmed
// without re-proposing it.
type DeepInsight struct {
	ID        string
	Statement string
}

// DeepLens is the minimal framing view the deep-dive sees of the active lens:
// its human name and the "<id> v<version>" provenance label. The prose is
// framed through the lens by name, and the label stamps any surfaced candidate
// (AC-6). The lens's licensing patterns and composition stay in the consent
// layer — the agent never sees them.
type DeepLens struct {
	Name  string
	Label string
}

// DeepDiveInput is the authorized slice for one weekly deep-dive: the ISO-week
// label, the honest pre-rendered metric lines (the router formats them from the
// projections so the agent never recomputes an Engine number), the week's
// citable raw entries, its body signals, the accepted-insight window for
// continuity, the active lens (nil for the baseline voice), the rejected and
// unanswered shape-tag denylists, and the agent version to stamp.
type DeepDiveInput struct {
	ISOWeek             string
	Numbers             []string
	Entries             []DeepEntry
	Signals             []DeepSignal
	Insights            []DeepInsight
	ActiveLens          *DeepLens
	RejectedShapeTags   []string
	UnansweredShapeTags []string
	AgentVersion        string
}

// DeepDiveCandidate is the single hypothesis-framed pattern the deep-dive may
// surface: its tentative text, a grammar-valid shape_tag, and the raw-entry ids
// that support it. The router routes it through the resonance gate; it is never
// persisted here.
type DeepDiveCandidate struct {
	ProposalText       string
	ShapeTag           string
	SupportingEntryIDs []string
}

// DeepDiveResult is the deep-dive payload. The six narrative sections are the
// Discord-friendly body; Candidate is the optional hypothesis (nil when no
// pattern, or when the model's candidate failed validation and was dropped);
// AppliedLens is the "<id> v<version>" label when a lens framed the run.
// NoLLM records the empty-week short-circuit (no model call); Fallback records
// the two-malformed degrade.
type DeepDiveResult struct {
	Summary     string
	Wins        []string
	Misses      []string
	BodyPain    []string
	HabitChange []string
	NextWeek    []string
	Candidate   *DeepDiveCandidate
	AppliedLens *string
	NoLLM       bool
	Fallback    bool
}

// deepDiveReply is the parsed model reply for one weekly_deepdive call.
type deepDiveReply struct {
	Summary     string                  `json:"summary"`
	Wins        []string                `json:"wins"`
	Misses      []string                `json:"misses"`
	BodyPain    []string                `json:"body_pain"`
	HabitChange []string                `json:"habit_change"`
	NextWeek    []string                `json:"next_week"`
	Candidate   *deepDiveCandidateReply `json:"candidate"`
}

// deepDiveCandidateReply is the optional candidate block as the model returns
// it. It is omitted or null on a "no pattern" week.
type deepDiveCandidateReply struct {
	ProposalText       string   `json:"proposal_text"`
	ShapeTag           string   `json:"shape_tag"`
	SupportingEntryIDs []string `json:"supporting_entry_ids"`
}

// DeepDive runs one reflection.weekly_deepdive turn. It never returns an error:
// an empty week (no entries and no signals) short-circuits to an empty result
// with no model call (P9), and two malformed replies degrade to a calm
// fallback summary. Otherwise the model is asked once and, on a malformed or
// unusable reply, retried once with the denylist and citation rule restated
// before degrading. A candidate that survives validation carries at least one
// in-slice raw-entry-id citation; an invalid candidate is dropped while the
// narrative is kept, so anything surfaced is always cited.
func DeepDive(ctx context.Context, in DeepDiveInput, p provider.Provider) DeepDiveResult {
	if in.isEmpty() {
		return DeepDiveResult{NoLLM: true}
	}

	for _, strict := range []bool{false, true} {
		reply, ok := deepDiveOnce(ctx, in, p, strict)
		if ok {
			return fromDeepDiveReply(reply, in)
		}
	}
	return DeepDiveResult{Summary: deepDiveFallback, Fallback: true}
}

// deepDiveOnce performs a single weekly_deepdive completion, parses it, and
// checks that it is structurally usable (a non-empty summary). A transport
// error, malformed JSON, or an empty summary reports ok=false so the caller
// retries or degrades. Candidate validity is checked separately in
// fromDeepDiveReply so a good narrative is never lost to a bad candidate.
func deepDiveOnce(ctx context.Context, in DeepDiveInput, p provider.Provider, strict bool) (deepDiveReply, bool) {
	reply, err := agentutil.CompleteJSON[deepDiveReply](ctx, p, provider.Request{
		Intent:   "reflection.weekly_deepdive",
		System:   deepDiveSystem(in, strict),
		Messages: deepDiveSlice(in),
	})
	if err != nil {
		return deepDiveReply{}, false
	}
	if strings.TrimSpace(reply.Summary) == "" {
		return deepDiveReply{}, false
	}
	return reply, true
}

// fromDeepDiveReply maps a usable reply into a result. The narrative sections
// are carried through (nil slices normalized to empty); the candidate is
// included only if it independently validates (a grammar-valid shape_tag off
// both denylists and ≥ 1 in-slice citation), so a surfaced pattern is always
// cited and never re-proposes a rejected or let-pass shape. When a lens is
// active the "<id> v<version>" label is stamped on the result (AC-6).
func fromDeepDiveReply(reply deepDiveReply, in DeepDiveInput) DeepDiveResult {
	res := DeepDiveResult{
		Summary:     strings.TrimSpace(reply.Summary),
		Wins:        cleanLines(reply.Wins),
		Misses:      cleanLines(reply.Misses),
		BodyPain:    cleanLines(reply.BodyPain),
		HabitChange: cleanLines(reply.HabitChange),
		NextWeek:    cleanLines(reply.NextWeek),
	}
	if c := reply.Candidate; c != nil && validDeepDiveCandidate(*c, in) {
		res.Candidate = &DeepDiveCandidate{
			ProposalText:       strings.TrimSpace(c.ProposalText),
			ShapeTag:           c.ShapeTag,
			SupportingEntryIDs: c.SupportingEntryIDs,
		}
	}
	if in.ActiveLens != nil {
		label := in.ActiveLens.Label
		res.AppliedLens = &label
	}
	return res
}

// validDeepDiveCandidate enforces the candidate rules: non-empty text, a
// shape_tag that matches the shared grammar and segment cap and is on neither
// denylist, and at least one supporting id that names a raw entry in the slice
// (AC-7 citations). It reuses the propose-mode shape_tag grammar so the two
// hypothesis paths validate identically.
func validDeepDiveCandidate(c deepDiveCandidateReply, in DeepDiveInput) bool {
	if strings.TrimSpace(c.ProposalText) == "" {
		return false
	}
	if !shapeTagPattern.MatchString(c.ShapeTag) {
		return false
	}
	if strings.Count(c.ShapeTag, "-")+1 > maxShapeTagSegments {
		return false
	}
	if slices.Contains(in.RejectedShapeTags, c.ShapeTag) || slices.Contains(in.UnansweredShapeTags, c.ShapeTag) {
		return false
	}
	if len(c.SupportingEntryIDs) == 0 {
		return false
	}
	known := make(map[string]bool, len(in.Entries))
	for _, e := range in.Entries {
		known[e.ID] = true
	}
	for _, id := range c.SupportingEntryIDs {
		if !known[id] {
			return false
		}
	}
	return true
}

// isEmpty reports whether the week carries no material to read — no raw entries
// and no body signals. Accepted insights alone are prior context, not new week
// material, so an insights-only slice is still empty for the deep-dive.
func (in DeepDiveInput) isEmpty() bool {
	return len(in.Entries) == 0 && len(in.Signals) == 0
}

// cleanLines trims each line and drops the empties, normalizing a nil slice to
// an empty one so a section is always a valid (possibly empty) list.
func cleanLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// deepDiveSlice renders the week as the single user message in the authorized
// slice — the entirety of what the deep-dive sees. It shows the honest numbers,
// each raw entry id-anchored so the model can cite it, the body signals, the
// accepted insights for continuity, and, when set, the active lens by name.
func deepDiveSlice(in DeepDiveInput) []provider.Message {
	var b strings.Builder
	b.WriteString("ISO WEEK: " + in.ISOWeek + "\n\n")

	b.WriteString("HONEST NUMBERS\n")
	for _, n := range in.Numbers {
		b.WriteString("- " + n + "\n")
	}

	b.WriteString("\nRAW ENTRIES (cite by id)\n")
	for _, e := range in.Entries {
		b.WriteString("- id: " + e.ID + " (" + e.Date + ")\n")
		b.WriteString("  text: " + oneLine(e.Text) + "\n")
	}

	b.WriteString("\nBODY SIGNALS\n")
	for _, s := range in.Signals {
		b.WriteString("- " + s.Kind + " on " + s.Date + "\n")
	}

	b.WriteString("\nALREADY-VALIDATED INSIGHTS (for continuity — do not re-propose)\n")
	for _, v := range in.Insights {
		b.WriteString("- id: " + v.ID + "\n")
		b.WriteString("  statement: " + oneLine(v.Statement) + "\n")
	}

	if in.ActiveLens != nil {
		b.WriteString("\nACTIVE LENS: " + in.ActiveLens.Name + "\n")
	}
	return []provider.Message{{Role: provider.RoleUser, Content: b.String()}}
}

// deepDiveSystem is the instruction for a weekly_deepdive call. It is kept
// clean of every blocklist phrase so the prompt itself passes the diagnostic
// "zero hits" source sweep (internal/validate). strict adds the retry emphasis
// and, when present, restates the shapes not to re-propose.
func deepDiveSystem(in DeepDiveInput, strict bool) string {
	var b strings.Builder
	b.WriteString("You are Lucid's Reflection agent composing a weekly deep-dive of the past week. ")
	b.WriteString("Read the honest numbers, the raw entries, the body signals, and the already-validated ")
	b.WriteString("insights below, then write a calm, tentative reflection. Ground every observation in ")
	b.WriteString("the material shown, quote or paraphrase the user's own words, use hypothesis framing, ")
	b.WriteString("and add no diagnosis, no label, and no advice. ")
	if in.ActiveLens != nil {
		b.WriteString("Where it fits, frame your reflection through the lens named at the end of the slice. ")
	}
	b.WriteString("You may surface at most ONE tentative pattern as a candidate, citing the raw entry ids ")
	b.WriteString("that support it; omit the candidate when nothing rises to one. Reply ONLY with JSON:\n")
	b.WriteString(`{"summary":"...","wins":["..."],"misses":["..."],"body_pain":["..."],`)
	b.WriteString(`"habit_change":["..."],"next_week":["..."],`)
	b.WriteString(`"candidate":{"proposal_text":"...","shape_tag":"kebab-case-tag","supporting_entry_ids":["raw_..."]}}` + "\n")
	b.WriteString("Every list may be empty. A shape_tag is lowercase kebab-case, up to six segments. ")
	b.WriteString("Cite only raw entry ids shown above. ")
	if denied := deepDiveDenylist(in); denied != "" {
		b.WriteString("Do not surface any of these shapes again: " + denied + ". ")
	}
	if strict {
		b.WriteString("Your previous reply was not valid. Reply with one JSON object and nothing else.")
	}
	return b.String()
}

// deepDiveDenylist joins the rejected and unanswered shape tags into a single
// comma list for the prompt, deduplicated and empty when neither set has
// anything — the same "do not re-propose" hygiene propose mode applies.
func deepDiveDenylist(in DeepDiveInput) string {
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
