package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/agents/intake"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/provider/factory"
	"github.com/mrz1836/lucid/internal/router"
)

// buildProvider is the seam that turns the resolved lucid.json provider block
// into a concrete model backend. Production goes through the factory (a single
// default backend serves every agent role this pillar); the serve/reflect/ask
// tests override it to inject a scripted [provider.Fake] so no test spawns a
// real vendor CLI (ADR-0006), mirroring the clockNow clock seam.
//
//nolint:gochecknoglobals // one injected provider seam so the provider-backed verbs stay testable offline
var buildProvider = func(cfg config.ProviderConfig) (provider.Provider, error) {
	return factory.Build(cfg)
}

// The serve protocol frame types. The server emits question/proposal/rule to
// ask the user something, and ack/error to close a turn; the client answers
// with answer/resonance/rule_answer and opens a session with control(start).
// It is a small, line-oriented JSON protocol — one object per line — so a
// harness can drive the interactive /checkin without an in-process binding
// (harness-integration.md §D).
const (
	frameQuestion   = "question"
	frameProposal   = "proposal"
	frameRule       = "rule"
	frameAck        = "ack"
	frameError      = "error"
	frameControl    = "control"
	frameAnswer     = "answer"
	frameResonance  = "resonance"
	frameRuleAnswer = "rule_answer"

	// controlStart opens a new /checkin session, carrying the opening message.
	controlStart = "start"
	// controlDone / controlCancel are the intake exit turns a client may carry
	// on an answer (or a bare control frame) to end the follow-up loop.
	controlDone   = "done"
	controlCancel = "cancel"
)

// serveIn is one client → server frame. A single flat shape carries every
// client turn; the server keys on Type and reads only the fields that turn
// uses (Opening for control/start, Text/Control for an answer, Kind/Text for a
// resonance, Answered/Rule for a rule answer).
type serveIn struct {
	Type     string `json:"type"`
	Command  string `json:"command,omitempty"`
	Opening  string `json:"opening,omitempty"`
	Text     string `json:"text,omitempty"`
	Control  string `json:"control,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Answered bool   `json:"answered,omitempty"`
	Rule     string `json:"rule,omitempty"`
}

// serveOut is one server → client frame. Text carries a question/proposal/rule
// prompt; the ack fields carry the durable result of the session (what was
// written, its ids, and the copy the user should see). Wrote is always
// serialized so a harness can branch on it without a presence check.
type serveOut struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	Message    string `json:"message,omitempty"`
	RawID      string `json:"raw_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	InsightID  string `json:"insight_id,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
	Wrote      bool   `json:"wrote"`
}

// serveSession is the stdin/JSON transport for one serve process. It backs
// both the intake [intake.Responder] and the [router.ValidationResponder], so
// a single decoder/encoder pair carries the whole interactive /checkin — the
// Intake follow-ups, the resonance confirmation, and the rule prompt — over
// one ordered protocol. It is intentionally single-threaded: the router calls
// its methods sequentially within a session.
type serveSession struct {
	dec *json.Decoder
	enc *json.Encoder
}

// Answer implements [intake.Responder]: it surfaces the follow-up question and
// blocks on the next client frame, mapping a control token (or a bare control
// frame) to the intake exit signals so /done and /cancel end the loop.
func (s *serveSession) Answer(question string) (intake.Turn, error) {
	if err := s.send(serveOut{Type: frameQuestion, Text: question}); err != nil {
		return intake.Turn{}, err
	}
	in, err := s.recv()
	if err != nil {
		return intake.Turn{}, err
	}
	switch controlToken(in) {
	case controlDone:
		return intake.Turn{Control: intake.ControlDone}, nil
	case controlCancel:
		return intake.Turn{Control: intake.ControlCancel}, nil
	default:
		return intake.Turn{Text: in.Text}, nil
	}
}

// RespondToProposal implements [router.ValidationResponder]: it surfaces the
// Safety-gated proposal the user should see and returns their resonance answer.
// It is called only when a proposal actually reaches the user — a Safety block
// ends the turn upstream without a prompt, so nothing is surfaced there.
func (s *serveSession) RespondToProposal(message string) (router.ProposalResponse, error) {
	if err := s.send(serveOut{Type: frameProposal, Text: message}); err != nil {
		return router.ProposalResponse{}, err
	}
	in, err := s.recv()
	if err != nil {
		return router.ProposalResponse{}, err
	}
	return router.ProposalResponse{Kind: resonanceKind(in.Kind), Text: in.Text}, nil
}

// RespondToRule implements [router.ValidationResponder]: it asks the fixed
// once-per-insight rule prompt and returns the answer. A skip (Answered false)
// leaves the insight rule-less and the prompt never returns.
func (s *serveSession) RespondToRule(prompt string) (router.RuleResponse, error) {
	if err := s.send(serveOut{Type: frameRule, Text: prompt}); err != nil {
		return router.RuleResponse{}, err
	}
	in, err := s.recv()
	if err != nil {
		return router.RuleResponse{}, err
	}
	return router.RuleResponse{Answered: in.Answered, Rule: in.Rule}, nil
}

// send writes one server frame as a line of JSON.
func (s *serveSession) send(f serveOut) error {
	if err := s.enc.Encode(f); err != nil {
		return fmt.Errorf("serve: encode frame: %w", err)
	}
	return nil
}

// recv reads the next client frame. It returns [io.EOF] verbatim so callers
// can distinguish a clean end of input from a malformed frame.
func (s *serveSession) recv() (serveIn, error) {
	var f serveIn
	if err := s.dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return serveIn{}, io.EOF
		}
		return serveIn{}, fmt.Errorf("serve: decode client frame: %w", err)
	}
	return f, nil
}

// controlToken resolves the intake exit signal a client turn carries, from
// either an answer's `control` field or a bare `control` frame's command.
func controlToken(in serveIn) string {
	if in.Control == controlDone || in.Control == controlCancel {
		return in.Control
	}
	if in.Type == frameControl {
		return in.Command
	}
	return ""
}

// resonanceKind maps a resonance frame's kind to the router response kind. An
// empty or unrecognized kind is treated as unanswered — the user let the
// proposal pass — so a malformed turn never silently accepts a pattern.
func resonanceKind(kind string) router.ResponseKind {
	switch kind {
	case string(router.RespAccepted):
		return router.RespAccepted
	case string(router.RespNuanced):
		return router.RespNuanced
	case string(router.RespRejected):
		return router.RespRejected
	default:
		return router.RespUnanswered
	}
}

// newServeCmd wires `lucid serve`: it drives the interactive /checkin flow over
// a line-oriented stdin/JSON protocol so a chat harness can run the full
// Intake → Structuring → Validate pipeline — follow-ups, resonance gate, and
// rule prompt — behind Lucid's own router and Safety gate (harness-integration
// §D). Each session opens with a control(start) frame carrying the opening
// message; the process reads sessions until stdin closes.
func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Drive the interactive /checkin flow over a stdin/JSON protocol",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetEscapeHTML(false)
			sess := &serveSession{dec: json.NewDecoder(cmd.InOrStdin()), enc: enc}
			return serveLoop(cmd.Context(), r, sess)
		},
	}
}

// serveLoop reads sessions until stdin closes. Each session begins with a
// control(start) frame; a clean EOF ends serve, a malformed frame ends it with
// an error, and an out-of-place frame is reported so the harness can resend a
// proper start.
func serveLoop(ctx context.Context, r *router.Router, sess *serveSession) error {
	for {
		frame, err := sess.recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			_ = sess.send(serveOut{Type: frameError, Message: err.Error()})
			return err
		}
		if frame.Type != frameControl || frame.Command != controlStart {
			if err := sess.send(serveOut{Type: frameError, Message: `expected {"type":"control","command":"start"} to begin a session`}); err != nil {
				return err
			}
			continue
		}
		if err := runCheckinSession(ctx, r, sess, frame.Opening); err != nil {
			return err
		}
	}
}

// runCheckinSession orchestrates one interactive /checkin: Intake gathers the
// bundled entry (2–4 follow-ups), Structuring passes over the capture, and
// Validate surfaces the resonance-gated proposal through Safety. Every path
// emits exactly one terminal ack. A nil error means the session closed with an
// ack (including the honest no-write paths); a non-nil error is an
// infrastructure fault the caller surfaces and stops on.
func runCheckinSession(ctx context.Context, r *router.Router, sess *serveSession, opening string) error {
	p, err := buildProvider(r.Config().Provider)
	if err != nil {
		_ = sess.send(serveOut{Type: frameError, Message: err.Error()})
		return err
	}

	ci, err := r.Checkin(ctx, router.CheckinRequest{
		Opening:   opening,
		Now:       clockNow(),
		Source:    sourceCLI,
		Harness:   sourceCLI,
		ChannelID: sourceCLI,
		Provider:  p,
		Responder: sess,
	})
	if err != nil {
		_ = sess.send(serveOut{Type: frameError, Message: err.Error()})
		return err
	}
	// Nothing captured (a bare cancel, a sub-floor exit, or an honest model
	// failure): the terminal ack is the /checkin copy — no structure or
	// validate runs on an entry that was never written.
	if !ci.Wrote {
		return sess.send(serveOut{Type: frameAck, StopReason: ci.StopReason, Message: ci.Ack})
	}

	st, err := r.Structure(ctx, router.StructureRequest{RawID: ci.RawID, Now: clockNow(), Provider: p})
	if err != nil {
		_ = sess.send(serveOut{Type: frameError, Message: err.Error()})
		return err
	}
	// Structuring degraded honestly (people routine or write failed): the raw
	// entry is captured but unprocessed, so there is nothing to validate. Ack
	// the capture and stop (error-states.md §S-5/§St-3).
	if !st.Wrote {
		return sess.send(serveOut{Type: frameAck, Wrote: true, RawID: ci.RawID, SessionID: ci.SessionID, Message: st.Ack})
	}

	v, err := r.Validate(ctx, router.ValidateRequest{
		ProcessedID: st.ProcessedID,
		Now:         clockNow(),
		Provider:    p,
		Responder:   sess,
	})
	if err != nil {
		_ = sess.send(serveOut{Type: frameError, Message: err.Error()})
		return err
	}

	return sess.send(serveOut{
		Type:      frameAck,
		Wrote:     v.Wrote,
		RawID:     ci.RawID,
		SessionID: ci.SessionID,
		InsightID: v.InsightID,
		Outcome:   string(v.Outcome),
		Message:   v.Message,
	})
}
