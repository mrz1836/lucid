package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/intake"
	"github.com/mrz1836/lucid/internal/router"
)

// errWriter fails every write, so a serveSession built over it exercises the
// send-error path of each responder method.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write boom") }

// newSessionOver builds a serveSession whose client frames are the given lines
// and whose server frames are written to a discardable buffer.
func newSessionOver(t *testing.T, clientFrames string, out *strings.Builder) *serveSession {
	t.Helper()
	return &serveSession{
		dec: json.NewDecoder(strings.NewReader(clientFrames)),
		enc: json.NewEncoder(out),
	}
}

func TestServeSession_Answer(t *testing.T) {
	tests := []struct {
		name        string
		clientFrame string
		wantControl intake.Control
		wantText    string
	}{
		{
			name:        "done via answer control field",
			clientFrame: `{"type":"answer","control":"done"}`,
			wantControl: intake.ControlDone,
		},
		{
			name:        "cancel via answer control field",
			clientFrame: `{"type":"answer","control":"cancel"}`,
			wantControl: intake.ControlCancel,
		},
		{
			name:        "done via bare control frame",
			clientFrame: `{"type":"control","command":"done"}`,
			wantControl: intake.ControlDone,
		},
		{
			name:        "plain text answer",
			clientFrame: `{"type":"answer","text":"the knee again"}`,
			wantText:    "the knee again",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			s := newSessionOver(t, tt.clientFrame, &out)
			turn, err := s.Answer("where does it hurt?")
			require.NoError(t, err)
			assert.Equal(t, tt.wantControl, turn.Control)
			assert.Equal(t, tt.wantText, turn.Text)
			// The question is surfaced to the client before blocking on input.
			assert.Contains(t, out.String(), "where does it hurt?")
		})
	}
}

func TestServeSession_AnswerSendError(t *testing.T) {
	s := &serveSession{
		dec: json.NewDecoder(strings.NewReader("")),
		enc: json.NewEncoder(errWriter{}),
	}
	_, err := s.Answer("q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode frame")
}

func TestServeSession_AnswerRecvError(t *testing.T) {
	var out strings.Builder
	s := newSessionOver(t, "{not valid json", &out)
	_, err := s.Answer("q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode client frame")
}

func TestServeSession_RespondToProposal(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		wantKind router.ResponseKind
	}{
		{"accepted", string(router.RespAccepted), router.RespAccepted},
		{"nuanced", string(router.RespNuanced), router.RespNuanced},
		{"rejected", string(router.RespRejected), router.RespRejected},
		{"unknown maps to unanswered", "gibberish", router.RespUnanswered},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			frame := `{"type":"resonance","kind":"` + tt.kind + `","text":"note"}`
			s := newSessionOver(t, frame, &out)
			resp, err := s.RespondToProposal("does this land?")
			require.NoError(t, err)
			assert.Equal(t, tt.wantKind, resp.Kind)
			assert.Equal(t, "note", resp.Text)
			assert.Contains(t, out.String(), "does this land?")
		})
	}
}

func TestServeSession_RespondToProposalErrors(t *testing.T) {
	t.Run("send error", func(t *testing.T) {
		s := &serveSession{
			dec: json.NewDecoder(strings.NewReader("")),
			enc: json.NewEncoder(errWriter{}),
		}
		_, err := s.RespondToProposal("m")
		require.Error(t, err)
	})
	t.Run("recv error", func(t *testing.T) {
		var out strings.Builder
		s := newSessionOver(t, "{bad", &out)
		_, err := s.RespondToProposal("m")
		require.Error(t, err)
	})
}

func TestServeSession_RespondToRule(t *testing.T) {
	var out strings.Builder
	s := newSessionOver(t, `{"type":"rule_answer","answered":true,"rule":"when tired, rest"}`, &out)
	resp, err := s.RespondToRule("name the rule?")
	require.NoError(t, err)
	assert.True(t, resp.Answered)
	assert.Equal(t, "when tired, rest", resp.Rule)
	assert.Contains(t, out.String(), "name the rule?")
}

func TestServeSession_RespondToRuleErrors(t *testing.T) {
	t.Run("send error", func(t *testing.T) {
		s := &serveSession{
			dec: json.NewDecoder(strings.NewReader("")),
			enc: json.NewEncoder(errWriter{}),
		}
		_, err := s.RespondToRule("p")
		require.Error(t, err)
	})
	t.Run("recv error", func(t *testing.T) {
		var out strings.Builder
		s := newSessionOver(t, "{bad", &out)
		_, err := s.RespondToRule("p")
		require.Error(t, err)
	})
}
