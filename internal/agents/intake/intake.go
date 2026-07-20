// Package intake is the Intake agent (agent-contracts.md §1): it turns one
// /checkin into a single bundled raw-entry body by asking 2–4 follow-up
// questions only when the opening is too thin, then bundling the user's
// answers in their own words. Intake is a scribe, not Reflection — it adds
// no interpretation and rewords nothing.
//
// It reads the current thread only. By construction it holds no storage
// handle and imports no Ledger package, so "reads current thread only,
// never raw/ or processed/ or other sessions" (agent-contracts.md §1) is
// enforced structurally, not by convention — the router hands Intake the
// opening plus a [provider.Provider] and a [Responder], nothing else. All
// model access is behind the provider boundary (ADR-0006), so tests drive
// Intake with a fake and never touch a real model.
package intake

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrz1836/lucid/internal/agents/agentutil"
	"github.com/mrz1836/lucid/internal/provider"
)

// Stop reasons Intake returns (agent-contracts.md §1 Outputs). They are
// plain string constants — not a closed enum — so the router can switch on
// them without an exhaustiveness obligation and forward them verbatim into
// records and acks.
const (
	// StopSatisfied — Intake has enough; the bundle is ready.
	StopSatisfied = "satisfied"
	// StopMaxQuestions — the 4-question cap was reached; bundle what's there.
	StopMaxQuestions = "max_questions_reached"
	// StopUserExit — the user typed /done or /cancel, went silent, or the
	// model failed twice; Intake returns whatever it has (possibly nothing).
	StopUserExit = "user_exit"
)

// floorQuestions is the minimum number of questions asked once Intake asks
// any at all (agent-contracts.md §1: "fewer than two questions when any
// are asked" is forbidden). defaultMaxQuestions is the documented cap used
// when the router passes an unset value.
const (
	floorQuestions      = 2
	defaultMaxQuestions = 4
)

// errIntakeDowngrade is the internal sentinel for the I-1 → I-2 path: the
// model returned malformed output (or an unusable bundle) twice, so Intake
// gives up on this turn. The router surfaces the fixed apology and writes
// nothing.
var errIntakeDowngrade = errors.New("intake: model output unusable after retry")

// Control is an out-of-band signal a user turn can carry instead of (or
// alongside) an answer: the user typed /done or /cancel, or the thread
// timed out. It drives the user_exit path (error-states.md §I-3/§I-4).
type Control int

// The control signals a [Turn] may carry.
const (
	// ControlNone is an ordinary answer.
	ControlNone Control = iota
	// ControlDone is the user typing /done — stop, keep what we have.
	ControlDone
	// ControlCancel is the user typing /cancel — stop, discard.
	ControlCancel
)

// Turn is one user reply to a follow-up question: the answer text and any
// control signal. A control signal ends the gather loop; Text is ignored
// when a control is set.
type Turn struct {
	Text    string
	Control Control
}

// Responder supplies the user's answers as Intake asks its questions. In a
// real harness this blocks on the chat thread; in tests it replays a fixed
// script. It is the only channel through which Intake sees user input
// after the opening — Intake never reads stored history.
type Responder interface {
	Answer(question string) (Turn, error)
}

// Input is the current-thread slice the router authorizes for one /checkin
// (agent-contracts.md §1 Inputs). It carries the opening message, the
// question cap from lucid.json, and the intake agent version to stamp —
// and nothing that would let Intake see prior sessions.
type Input struct {
	OpeningMessage string
	MaxQuestions   int
	AgentVersion   string
}

// Result is Intake's structured payload (agent-contracts.md §1 Outputs)
// plus one internal flag. QuestionsAsked and Answers are parallel and
// equal-length: each element is a question the user actually answered.
type Result struct {
	QuestionsAsked []string
	Answers        []string
	BundledText    string
	StopReason     string
	// LLMFailed marks the I-2 downgrade (model unusable twice). It is a
	// user_exit whose router copy is the apology, not "nothing saved" — so
	// the router can tell an honest model failure from a plain cancel.
	LLMFailed bool
}

// decision is the parsed model reply for one gather step: keep asking
// (Done=false, Question set) or stop asking (Done=true).
type decision struct {
	Done     bool   `json:"done"`
	Question string `json:"question"`
}

// bundleReply is the parsed model reply for the bundling step.
type bundleReply struct {
	BundledText string `json:"bundled_text"`
}

// cap returns the effective question cap: the router's value, floored at
// [floorQuestions] and defaulted when unset, so a misconfigured cap can
// never drop below the two-question floor or vanish entirely.
func (in Input) cap() int {
	if in.MaxQuestions <= 0 {
		return defaultMaxQuestions
	}
	if in.MaxQuestions < floorQuestions {
		return floorQuestions
	}
	return in.MaxQuestions
}

// Gather runs one Intake turn end to end: it drives the follow-up loop via
// the provider and responder, enforces the 2–4 bounds, then bundles the
// user's answers. It never returns an error for a model failure — those
// become a user_exit with LLMFailed set (error-states.md §I-2). A non-nil
// error means an infrastructure fault (the responder itself broke), which
// the router surfaces as a capture failure.
func Gather(ctx context.Context, in Input, p provider.Provider, r Responder) (Result, error) {
	questions, answers, stop, err := gatherLoop(ctx, in, p, r)
	if err != nil {
		if errors.Is(err, errIntakeDowngrade) {
			// Malformed twice: discard and apologize (§I-2).
			return Result{StopReason: StopUserExit, LLMFailed: true}, nil
		}
		return Result{}, err
	}

	// Nothing durable to bundle: a user exit below the two-question floor
	// (a bare cancel, or a single stray answer) writes nothing. Bundling is
	// skipped so no sub-floor partial is ever composed — the router turns
	// this into "nothing saved" (a raw entry with one question is forbidden,
	// acceptance-criteria.md Phase 3).
	if stop == StopUserExit && len(answers) < floorQuestions {
		return Result{QuestionsAsked: questions, Answers: answers, StopReason: StopUserExit}, nil
	}

	text, ok := buildBundle(ctx, in, p, questions, answers)
	if !ok {
		// The model was unusable across its retry, or there was nothing to
		// save — the §I-2 downgrade: no write, apology.
		return Result{QuestionsAsked: questions, Answers: answers, StopReason: StopUserExit, LLMFailed: true}, nil
	}

	return Result{
		QuestionsAsked: questions,
		Answers:        answers,
		BundledText:    text,
		StopReason:     stop,
	}, nil
}

// gatherLoop runs the ask/answer loop and returns the answered questions,
// the answers, and the stop reason. It enforces the cap (never asks more
// than Input.cap()) and the floor (never accepts "satisfied" at exactly
// one question). A malformed decision twice returns [errIntakeDowngrade].
func gatherLoop(ctx context.Context, in Input, p provider.Provider, r Responder) (questions, answers []string, stop string, err error) {
	maxQ := in.cap()
	for len(questions) < maxQ {
		dec, decErr := decide(ctx, p, in, questions, answers)
		if decErr != nil {
			return nil, nil, "", decErr
		}
		if dec.Done && canStop(len(questions)) {
			return questions, answers, StopSatisfied, nil
		}

		question := nextQuestion(dec)
		turn, ansErr := r.Answer(question)
		if ansErr != nil {
			return nil, nil, "", fmt.Errorf("intake: read user answer: %w", ansErr)
		}
		if turn.Control != ControlNone {
			return questions, answers, StopUserExit, nil
		}
		questions = append(questions, question)
		answers = append(answers, turn.Text)
	}
	// Fell out because the cap was hit (error-states.md §I-5).
	return questions, answers, StopMaxQuestions, nil
}

// canStop reports whether Intake may accept a "satisfied" signal at the
// given number of asked questions: it may stop at zero (the opening was
// enough) or at the floor or above, but never at exactly one.
func canStop(asked int) bool {
	return asked == 0 || asked >= floorQuestions
}

// nextQuestion picks the question text to ask. Normally it is the model's
// proposed question. When the model signaled "done" one question short of
// the floor it supplies no question; Intake then asks a single
// deterministic follow-up so it never stops one question short — asking a
// generic follow-up is a scribe's job, not authorship. (nextQuestion is
// only reached below the floor when exactly one question has been asked;
// canStop makes a zero-question fall-through unreachable.)
func nextQuestion(dec decision) string {
	if q := strings.TrimSpace(dec.Question); q != "" {
		return q
	}
	return floorFollowup
}

// floorFollowup is the deterministic question Intake falls back to when it
// must meet the two-question floor but the model offered none.
const floorFollowup = "Is there anything else that stood out?"

// decide makes one gather-step model call with a retry: a malformed reply
// is retried once with a stricter instruction (§I-1); a second failure
// returns [errIntakeDowngrade] (§I-2). A well-behaved reply that continues
// without a question, or continues with an empty question, is treated as
// malformed so the loop can never spin on an unusable decision.
func decide(ctx context.Context, p provider.Provider, in Input, questions, answers []string) (decision, error) {
	dec, err := decideOnce(ctx, p, in, questions, answers, false)
	if err == nil {
		return dec, nil
	}
	dec, err = decideOnce(ctx, p, in, questions, answers, true)
	if err == nil {
		return dec, nil
	}
	return decision{}, errIntakeDowngrade
}

// decideOnce performs a single decision completion and parses it.
func decideOnce(ctx context.Context, p provider.Provider, in Input, questions, answers []string, strict bool) (decision, error) {
	dec, err := agentutil.CompleteJSON[decision](ctx, p, provider.Request{
		Intent:   "intake.decide",
		System:   decideSystem(in, strict),
		Messages: threadSlice(in, questions, answers),
	})
	if err != nil {
		return decision{}, fmt.Errorf("intake: decide: %w", err)
	}
	if !dec.Done && strings.TrimSpace(dec.Question) == "" {
		return decision{}, errors.New("intake: decision continues but supplies no question")
	}
	return dec, nil
}

// buildBundle produces the raw-entry body from the user's answers, with a
// retry gated on the ≥90% user-authored floor: a malformed or over-edited
// bundle is retried once stricter (§I-6/§I-1). It reports ok=false when the
// model is still unusable after the retry (§I-2) or there is nothing to
// save. When no questions were asked (the opening was sufficient) the
// bundle is the opening verbatim — nothing to combine, so no model call is
// made and authorship is trivially 100%.
func buildBundle(ctx context.Context, in Input, p provider.Provider, questions, answers []string) (text string, ok bool) {
	if len(answers) == 0 {
		body := strings.TrimSpace(in.OpeningMessage)
		if body == "" {
			return "", false
		}
		return body, true
	}

	for _, strict := range []bool{false, true} {
		candidate, err := bundleOnce(ctx, p, in, questions, answers, strict)
		if err != nil {
			continue
		}
		if bundleIsUserAuthored(candidate, in.OpeningMessage, questions, answers) {
			return candidate, true
		}
	}
	return "", false
}

// bundleOnce performs a single bundling completion and parses it.
func bundleOnce(ctx context.Context, p provider.Provider, in Input, questions, answers []string, strict bool) (string, error) {
	reply, err := agentutil.CompleteJSON[bundleReply](ctx, p, provider.Request{
		Intent:   "intake.bundle",
		System:   bundleSystem(strict),
		Messages: threadSlice(in, questions, answers),
	})
	if err != nil {
		return "", fmt.Errorf("intake: bundle: %w", err)
	}
	if strings.TrimSpace(reply.BundledText) == "" {
		return "", errors.New("intake: empty bundled_text")
	}
	return reply.BundledText, nil
}

// threadSlice renders the current-thread slice as provider messages: the
// opening as the user's first message, then each asked question (assistant)
// with its answer (user). This is the entirety of what Intake shows a
// model — no Ledger, no other session.
func threadSlice(in Input, questions, answers []string) []provider.Message {
	msgs := make([]provider.Message, 0, 1+len(questions)+len(answers))
	if opening := strings.TrimSpace(in.OpeningMessage); opening != "" {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: opening})
	}
	for i, q := range questions {
		msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: q})
		if i < len(answers) {
			msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: answers[i]})
		}
	}
	return msgs
}

// decideSystem is the instruction for a gather-step call. strict adds the
// retry emphasis after a malformed reply (§I-1).
func decideSystem(in Input, strict bool) string {
	var b strings.Builder
	b.WriteString("You are Lucid's Intake scribe. Decide whether to ask one more short ")
	b.WriteString("follow-up question about the user's check-in, or stop. Ask between ")
	_, _ = fmt.Fprintf(&b, "%d and %d questions only when the opening is too thin; ", floorQuestions, in.cap())
	b.WriteString("never interpret, advise, or spot patterns. Reply ONLY with JSON: ")
	b.WriteString(`{"done": false, "question": "..."} to ask, or {"done": true} to stop.`)
	if strict {
		b.WriteString(" Your previous reply was not valid JSON. Reply with the JSON object and nothing else.")
	}
	return b.String()
}

// bundleSystem is the instruction for the bundling call. strict adds the
// retry emphasis after a rejected or malformed bundle (§I-6).
func bundleSystem(strict bool) string {
	var b strings.Builder
	b.WriteString("You are Lucid's Intake scribe. Combine the user's own words into one ")
	b.WriteString("raw journal entry. Preserve their wording; add only paragraph breaks or a ")
	b.WriteString("short question prefix. Do not reword, interpret, summarize, or add anything ")
	b.WriteString(`the user did not say. Reply ONLY with JSON: {"bundled_text": "..."}.`)
	if strict {
		b.WriteString(" Your previous bundle changed the user's words. Keep at least 90% of the")
		b.WriteString(" user's exact wording; connective tissue must stay invisible.")
	}
	return b.String()
}
