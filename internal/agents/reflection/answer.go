package reflection

import (
	"context"
	"errors"
	"strings"

	"github.com/mrz1836/lucid/internal/agents/agentutil"
	"github.com/mrz1836/lucid/internal/provider"
)

// The two answer_grounded outcomes (agent-contracts.md §3 Outputs): a grounded
// answer that cites only in-slice records, or an honest insufficient.
const (
	// OutcomeAnswer — a grounded answer with a non-empty citations set, every
	// cited id present in the supplied slice.
	OutcomeAnswer Outcome = "answer"
	// OutcomeInsufficient — the slice cannot answer the question; citations is
	// empty and the message points the user back at /checkin or /log.
	OutcomeInsufficient Outcome = "insufficient"
)

// Citation kinds (agent-contracts.md §3 answer_grounded Outputs). A citation is
// either a validated insight or a weekly reflection record.
const (
	CitationInsight    = "insight"
	CitationReflection = "reflection"
)

// The fixed answer_grounded fallback copy. Each is deliberately clean of every
// blocklist phrase so it passes the "zero hits" sweep (acceptance-criteria.md
// Phase 7). The router surfaces these verbatim; none is agent-authored.
const (
	// answerEmptyStore is the no-material message returned without a model call
	// when both slices are empty (error-states.md §R-10; §E-4).
	answerEmptyStore = "I don't have anything validated yet — try `/checkin` or `/log` first."
	// answerModelInsufficient is surfaced when the model itself reports it
	// cannot answer from a non-empty slice (agent-contracts.md §3 example (b)).
	answerModelInsufficient = "I don't have enough validated material to answer that yet — want to capture one?"
	// answerMalformedFallback is the short fallback after two malformed model
	// replies (error-states.md §R-12).
	answerMalformedFallback = "I had trouble pulling that together — want to ask it differently?"
	// answerTransportFallback is the transient-transport fallback after a
	// timeout / unreachable model persists past one retry (error-states.md §N-3).
	answerTransportFallback = "I'm having trouble reaching my model right now — want to try again?"
)

// WeeklyReflectionView is the slice of one weekly reflection record answer_grounded
// sees: its id and its one-line summary. Reflection receives nothing else — no
// per-insight breakdown, no raw bodies.
type WeeklyReflectionView struct {
	ID      string
	Summary string
}

// Citation is one grounded reference the model returned: the kind of record and
// its id. Every citation's id must appear in the supplied slice of that kind
// (agent-contracts.md §3 forbidden behavior; error-states.md §Sf-7).
type Citation struct {
	Kind string
	ID   string
}

// AnswerInput is the authorized slice for one answer_grounded call
// (agent-contracts.md §3 Inputs): the user's verbatim question, the validated
// insights the router included, the weekly reflections it included, and the
// agent version. Reflection has no access to raw entries, processed artifacts,
// or people records — only this slice.
type AnswerInput struct {
	Question     string
	Insights     []InsightView
	Reflections  []WeeklyReflectionView
	AgentVersion string
}

// AnswerResult is Reflection's answer_grounded payload. For an answer,
// AnswerText and Citations are set; for insufficient, AnswerText carries the
// fallback and Citations is empty. NoLLM records that the outcome was reached
// deterministically (both slices empty — no model call). Fallback records that
// a degrade fired: two malformed / transport failures, or an answer whose
// citations stayed out-of-slice after the retry (which the router then lets
// Safety block per §Sf-7).
type AnswerResult struct {
	Outcome    Outcome
	AnswerText string
	Citations  []Citation
	NoLLM      bool
	Fallback   bool
}

// answerReply is the parsed model reply for one answer_grounded call.
type answerReply struct {
	Outcome    string          `json:"outcome"`
	AnswerText string          `json:"answer_text"`
	Citations  []answerCiteYML `json:"citations"`
}

// answerCiteYML is one citation as the model returns it.
type answerCiteYML struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// AnswerGrounded runs one reflection.answer_grounded turn. Like the other
// sub-modes it never returns an error: an empty slice short-circuits with no
// model call (error-states.md §R-10), and every other failure degrades to
// insufficient (§R-11/§R-12/§N-3). It is read-only — it returns data, and the
// router writes nothing on /ask (agent-contracts.md §3; acceptance-criteria.md
// Phase 7).
//
// Citation grounding is defense in depth: an answer whose citations fall
// outside the slice is retried once with the slice ids restated (§R-13); if it
// is still out-of-slice, the answer is returned unchanged so the router's Safety
// gate blocks it (§Sf-7) rather than the agent silently swallowing the miss.
func AnswerGrounded(ctx context.Context, in AnswerInput, p provider.Provider) AnswerResult {
	if len(in.Insights) == 0 && len(in.Reflections) == 0 {
		return AnswerResult{Outcome: OutcomeInsufficient, AnswerText: answerEmptyStore, NoLLM: true}
	}

	var (
		haveAnswer bool
		last       answerReply
		transport  bool
	)
	for _, strict := range []bool{false, true} {
		reply, ok, transportErr := answerOnce(ctx, in, p, strict)
		if !ok {
			transport = transportErr
			continue
		}
		transport = false
		if Outcome(reply.Outcome) == OutcomeInsufficient {
			return AnswerResult{Outcome: OutcomeInsufficient, AnswerText: answerModelInsufficient}
		}
		haveAnswer = true
		last = reply
		if citationsInSlice(reply, in) {
			return fromAnswerReply(reply)
		}
	}

	if haveAnswer {
		// Structurally an answer, but its citations stayed out-of-slice across
		// the retry (§R-13). Return it so Safety blocks it (§Sf-7).
		res := fromAnswerReply(last)
		res.Fallback = true
		return res
	}

	msg := answerMalformedFallback
	if transport {
		msg = answerTransportFallback
	}
	return AnswerResult{Outcome: OutcomeInsufficient, AnswerText: msg, Fallback: true}
}

// answerOnce performs a single answer_grounded completion, parses it, and
// checks its structural validity. It returns transportErr=true when the model
// call itself failed (a timeout / unreachable transport — retry, then §N-3),
// distinct from a well-formed transport that returned unusable content (a
// parse / shape failure — retry, then §R-12). A well-formed answer that merely
// cites out-of-slice is reported ok=true here; the slice check happens in the
// caller so such an answer can reach Safety.
func answerOnce(ctx context.Context, in AnswerInput, p provider.Provider, strict bool) (reply answerReply, ok, transportErr bool) {
	parsed, err := agentutil.CompleteJSON[answerReply](ctx, p, provider.Request{
		Intent:   "reflection.answer_grounded",
		System:   answerSystem(in, strict),
		Messages: answerSlice(in),
	})
	if err != nil {
		// A parse failure is a well-formed transport that returned garbage
		// (retry, then §R-12); anything else is a transport failure that did
		// not reach a usable model (retry, then §N-3).
		return answerReply{}, false, !errors.Is(err, agentutil.ErrParse)
	}
	if !validAnswer(parsed) {
		return answerReply{}, false, false
	}
	return parsed, true, false
}

// validAnswer enforces the structural §3 rules independent of slice membership:
// the outcome is answer or insufficient; an answer has non-empty text and at
// least one citation; an insufficient has non-empty text and no citation.
func validAnswer(reply answerReply) bool {
	switch Outcome(reply.Outcome) {
	case OutcomeAnswer:
		return strings.TrimSpace(reply.AnswerText) != "" && len(reply.Citations) > 0
	case OutcomeInsufficient:
		return strings.TrimSpace(reply.AnswerText) != "" && len(reply.Citations) == 0
	case OutcomeProposal, OutcomeNoPattern, OutcomeSoftContradiction, OutcomeRecall:
		return false // a propose/recall outcome is never a valid answer_grounded reply
	default:
		return false
	}
}

// citationsInSlice reports whether every citation names a record in the
// supplied slice of its kind — insight ids in Insights, reflection ids in
// Reflections. An unknown kind is out-of-slice by construction.
func citationsInSlice(reply answerReply, in AnswerInput) bool {
	insightIDs := make(map[string]bool, len(in.Insights))
	for _, v := range in.Insights {
		insightIDs[v.ID] = true
	}
	reflectionIDs := make(map[string]bool, len(in.Reflections))
	for _, v := range in.Reflections {
		reflectionIDs[v.ID] = true
	}
	for _, c := range reply.Citations {
		switch c.Kind {
		case CitationInsight:
			if !insightIDs[c.ID] {
				return false
			}
		case CitationReflection:
			if !reflectionIDs[c.ID] {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// fromAnswerReply maps a validated answer reply into an AnswerResult (the model
// path, so NoLLM is false).
func fromAnswerReply(reply answerReply) AnswerResult {
	cites := make([]Citation, 0, len(reply.Citations))
	for _, c := range reply.Citations {
		cites = append(cites, Citation(c))
	}
	return AnswerResult{Outcome: OutcomeAnswer, AnswerText: reply.AnswerText, Citations: cites}
}

// answerSlice renders the question and the two slices as the single user
// message in the authorized context — the entirety of what answer_grounded
// sees. Each record is shown id-anchored so the model can cite it by id.
func answerSlice(in AnswerInput) []provider.Message {
	var b strings.Builder
	b.WriteString("QUESTION\n")
	b.WriteString(oneLine(in.Question) + "\n\n")
	b.WriteString("VALIDATED INSIGHTS\n")
	for _, v := range in.Insights {
		b.WriteString("- id: " + v.ID + "\n")
		b.WriteString("  statement: " + oneLine(v.Statement) + "\n")
	}
	b.WriteString("\nWEEKLY REFLECTIONS\n")
	for _, v := range in.Reflections {
		b.WriteString("- id: " + v.ID + "\n")
		b.WriteString("  summary: " + oneLine(v.Summary) + "\n")
	}
	return []provider.Message{{Role: provider.RoleUser, Content: b.String()}}
}

// answerSystem is the instruction for an answer_grounded call. It is kept clean
// of every blocklist phrase so the prompt itself passes the "zero hits" sweep
// (acceptance-criteria.md Phase 7). strict adds the retry emphasis and restates
// the citable ids so a second attempt can correct an out-of-slice citation
// (error-states.md §R-13).
func answerSystem(in AnswerInput, strict bool) string {
	var b strings.Builder
	b.WriteString("You are Lucid's Reflection agent answering a question from validated material only. ")
	b.WriteString("Quote or paraphrase the insights and weekly reflections below to answer; cite every ")
	b.WriteString("record you lean on by its exact id. Introduce no new pattern, add no advice, and use ")
	b.WriteString("tentative framing. If the material cannot answer the question, say so honestly. ")
	b.WriteString("Reply ONLY with JSON, one of:\n")
	b.WriteString(`{"outcome":"answer","answer_text":"...","citations":[{"kind":"insight","id":"i_..."}]}` + "\n")
	b.WriteString(`{"outcome":"insufficient","answer_text":"...","citations":[]}` + "\n")
	b.WriteString("A citation kind is \"insight\" or \"reflection\"; every cited id must be one shown below. ")
	if strict {
		b.WriteString("Your previous reply was not valid. Cite only these ids: " + citableIDs(in) + ". ")
		b.WriteString("Reply with one JSON object and nothing else.")
	}
	return b.String()
}

// citableIDs joins every id in the slice for the strict-retry prompt, insights
// then reflections, so a corrected attempt has the exact allow-list in front of
// it (error-states.md §R-13).
func citableIDs(in AnswerInput) string {
	ids := make([]string, 0, len(in.Insights)+len(in.Reflections))
	for _, v := range in.Insights {
		ids = append(ids, v.ID)
	}
	for _, v := range in.Reflections {
		ids = append(ids, v.ID)
	}
	return strings.Join(ids, ", ")
}
