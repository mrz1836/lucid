package router

import (
	"context"
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/agents/intake"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// commandCheckin is the verb stamped on /checkin raw entries and sessions.
const commandCheckin = "/checkin"

// minCheckinQuestions is the number of answered questions below which a
// user_exit produces no durable entry. The floor is two: Intake never
// completes a check-in on one question (agent-contracts.md §1), and a raw
// entry with an intake_questions length of exactly one is forbidden
// (acceptance-criteria.md Phase 3 definition of done). A bare cancel (zero
// answers) and a single stray answer both stop with "nothing saved".
const minCheckinQuestions = 2

// CheckinRequest carries the inputs for one /checkin turn. Provider and
// Responder are the capabilities the router hands Intake: the model
// boundary and the channel that supplies the user's answers. The router
// owns everything Ledger-facing — Intake sees neither the store nor the
// clock.
type CheckinRequest struct {
	Opening   string
	Now       time.Time
	Source    string
	Harness   string
	ChannelID string
	ThreadID  string
	Bootstrap bool
	Provider  provider.Provider
	Responder intake.Responder
}

// CheckinResult reports what a /checkin turn wrote and the ack to show the
// user. Wrote is false for every path that persists nothing (a bare
// cancel, or an honest model failure).
type CheckinResult struct {
	RawID      string
	SessionID  string
	StopReason string
	Questions  []string
	Wrote      bool
	Ack        string
}

// Checkin executes the /checkin command: it runs Intake to gather a
// bundled entry from 2–4 follow-ups, then persists exactly one raw entry
// (capture only — Structuring lands in a later phase) and its session
// record, or nothing, per the stop reason. The ack copy is fixed
// verbatim from the Intake contract (agent-contracts.md §1) and the error
// table (error-states.md §I-2/§I-3). A raw entry is written only with two
// or more answered questions, so no /checkin entry ever carries an
// intake_questions length of one.
func (r *Router) Checkin(ctx context.Context, req CheckinRequest) (CheckinResult, error) {
	res, err := intake.Gather(ctx, intake.Input{
		OpeningMessage: req.Opening,
		MaxQuestions:   r.cfg.IntakeMaxQuestions,
		AgentVersion:   r.cfg.AgentVersions.Intake,
	}, req.Provider, req.Responder)
	if err != nil {
		return CheckinResult{}, fmt.Errorf("checkin: intake: %w", err)
	}

	switch res.StopReason {
	case intake.StopSatisfied:
		return r.writeCheckin(req, res, ackSatisfied)
	case intake.StopMaxQuestions:
		return r.writeCheckin(req, res, ackMaxQuestions)
	case intake.StopUserExit:
		return r.checkinUserExit(req, res)
	default:
		return CheckinResult{}, fmt.Errorf("checkin: unknown stop_reason %q", res.StopReason)
	}
}

// checkinUserExit resolves the three user_exit acks: an honest model
// failure apologizes (§I-2), two-or-more answers save a partial bundle
// (§I-3), and anything less saves nothing.
func (r *Router) checkinUserExit(req CheckinRequest, res intake.Result) (CheckinResult, error) {
	switch {
	case res.LLMFailed:
		return CheckinResult{StopReason: res.StopReason, Questions: res.QuestionsAsked, Ack: ackModelFailed}, nil
	case len(res.Answers) >= minCheckinQuestions:
		return r.writeCheckin(req, res, ackPartial)
	default:
		return CheckinResult{StopReason: res.StopReason, Questions: res.QuestionsAsked, Ack: ackNothingSaved}, nil
	}
}

// writeCheckin persists the bundled raw entry and its session record, then
// builds the result with the supplied ack. The raw entry is written first,
// so a write failure leaves nothing dangling and is safe to retry
// (error-states.md cross-cutting: capture is honored before structure).
func (r *Router) writeCheckin(req CheckinRequest, res intake.Result, ack func(string) string) (CheckinResult, error) {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	version := r.cfg.AgentVersions.Intake

	rawRes, err := r.store.WriteRaw(storage.RawEntry{
		RecordedAt:          now,
		OccurredAt:          now,
		OccurredAtPrecision: storage.PrecisionExact,
		Source:              orDefaultSource(req.Source),
		Command:             commandCheckin,
		IntakeQuestions:     res.QuestionsAsked,
		IntakeVersion:       &version,
		Bootstrap:           req.Bootstrap,
		Body:                res.BundledText,
	})
	if err != nil {
		return CheckinResult{}, fmt.Errorf(
			"checkin: could not write the raw entry (out of disk space or permission denied?); nothing was saved: %w", err)
	}

	if _, err := r.store.WriteSession(storage.Session{
		ID:            rawRes.SessionID,
		StartedAt:     now,
		EndedAt:       now,
		Harness:       orDefaultSource(req.Harness),
		ChannelID:     req.ChannelID,
		ThreadID:      req.ThreadID,
		Command:       commandCheckin,
		RawEntryIDs:   []string{rawRes.RawID},
		AgentVersions: r.cfg.AgentVersions,
	}); err != nil {
		return CheckinResult{}, fmt.Errorf("checkin: could not write the session record for %s: %w", rawRes.RawID, err)
	}

	return CheckinResult{
		RawID:      rawRes.RawID,
		SessionID:  rawRes.SessionID,
		StopReason: res.StopReason,
		Questions:  res.QuestionsAsked,
		Wrote:      true,
		Ack:        ack(rawRes.RawID),
	}, nil
}

// The fixed /checkin acks. The three id-bearing forms are the verbatim
// contract copy (agent-contracts.md §1 Outputs); the two static forms are
// the error-table copy (error-states.md §I-2/§I-3).
const (
	ackNothingSaved = "Stopped — nothing saved."
	ackModelFailed  = "I held that — let me try a different opening another time. Nothing saved."
)

// ackSatisfied is the standard save ack.
func ackSatisfied(rawID string) string { return fmt.Sprintf("Saved as `%s`.", rawID) }

// ackMaxQuestions acknowledges the question cap before the id.
func ackMaxQuestions(rawID string) string {
	return fmt.Sprintf("I've got what I need — saved as `%s`.", rawID)
}

// ackPartial acknowledges a partial bundle saved after a user exit.
func ackPartial(rawID string) string { return fmt.Sprintf("Saved what we had as `%s`.", rawID) }
