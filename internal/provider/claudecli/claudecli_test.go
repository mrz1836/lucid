package claudecli_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/provider/claudecli"
)

// okEnvelope is a well-formed success envelope carrying result text.
func okEnvelope(result string) []byte {
	return []byte(fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"result":%q,"total_cost_usd":0.01}`, result))
}

// TestComplete_Success drives the happy path and asserts the invocation
// contract: the pinned flags, the model, the System via --system-prompt,
// the role-flattened Messages on stdin, and .result parsed into Content.
func TestComplete_Success(t *testing.T) {
	var gotName string
	var gotArgs []string
	var gotStdin []byte

	p := claudecli.New("opus", claudecli.WithRunner(
		func(_ context.Context, name string, args []string, stdin []byte) ([]byte, error) {
			gotName, gotArgs, gotStdin = name, args, stdin
			return okEnvelope("a grounded reflection"), nil
		},
	))

	resp, err := p.Complete(context.Background(), provider.Request{
		Intent: "reflection.propose",
		System: "You are a careful mirror.",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "what stuck with me?"},
			{Role: provider.RoleAssistant, Content: "tell me more"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "a grounded reflection", resp.Content)

	assert.Equal(t, "claude", gotName)
	assert.Equal(t, []string{
		"-p", "--output-format", "json", "--model", "opus",
		"--system-prompt", "You are a careful mirror.",
	}, gotArgs)
	assert.Equal(t, "user: what stuck with me?\n\nassistant: tell me more", string(gotStdin))
}

// TestComplete_NoSystemOmitsFlag confirms --system-prompt is only supplied
// when a System instruction is present, and empty Messages yield empty stdin.
func TestComplete_NoSystemOmitsFlag(t *testing.T) {
	var gotArgs []string
	var gotStdin []byte
	p := claudecli.New("sonnet", claudecli.WithRunner(
		func(_ context.Context, _ string, args []string, stdin []byte) ([]byte, error) {
			gotArgs, gotStdin = args, stdin
			return okEnvelope("ok"), nil
		},
	))

	_, err := p.Complete(context.Background(), provider.Request{Intent: "intake.decide"})
	require.NoError(t, err)
	assert.Equal(t, []string{"-p", "--output-format", "json", "--model", "sonnet"}, gotArgs)
	assert.NotContains(t, strings.Join(gotArgs, " "), "--system-prompt")
	assert.Empty(t, gotStdin)
}

// TestComplete_TimeoutFromDeadline: when the option-set deadline elapses and
// the runner returns a killed-process error, ctx.Err() is DeadlineExceeded
// so the backend maps to ErrTimeout (the known Ollama/CLI hang class for
// the CLI's own process).
func TestComplete_TimeoutFromDeadline(t *testing.T) {
	p := claudecli.New(
		"opus",
		claudecli.WithTimeout(10*time.Millisecond),
		claudecli.WithRunner(func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
			<-ctx.Done() // simulate a hung CLI killed by the deadline
			return nil, errors.New("signal: killed")
		}),
	)

	_, err := p.Complete(context.Background(), provider.Request{Intent: "intake.decide"})
	require.ErrorIs(t, err, provider.ErrTimeout)
	assert.NotErrorIs(t, err, provider.ErrUnavailable)
}

// TestComplete_CanceledContext: an explicit caller cancel surfaces as the
// context error, not a transport sentinel — a canceled call is the caller's,
// not "no model reachable".
func TestComplete_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := claudecli.New("opus", claudecli.WithRunner(
		func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
			return nil, ctx.Err()
		},
	))

	_, err := p.Complete(ctx, provider.Request{})
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, provider.ErrUnavailable)
	assert.NotErrorIs(t, err, provider.ErrTimeout)
}

// TestComplete_UnavailableMappings covers every failure that means "no model
// reachable": a spawn/exit error with no ctx error, an is_error envelope, an
// empty result, and unparseable stdout.
func TestComplete_UnavailableMappings(t *testing.T) {
	cases := []struct {
		name   string
		stdout []byte
		runErr error
	}{
		{name: "spawn or non-zero exit", runErr: errors.New("exit status 1: not logged in")},
		{name: "is_error envelope", stdout: []byte(`{"is_error":true,"result":"quota"}`)},
		{name: "empty result", stdout: okEnvelope("")},
		{name: "unparseable envelope", stdout: []byte("not json — a stray notice line")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := claudecli.New("opus", claudecli.WithRunner(
				func(_ context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
					return tc.stdout, tc.runErr
				},
			))
			_, err := p.Complete(context.Background(), provider.Request{})
			require.ErrorIs(t, err, provider.ErrUnavailable)
			assert.NotErrorIs(t, err, provider.ErrTimeout)
		})
	}
}

// notLoggedInEnvelope is the synthetic stdout the CLI prints when it cannot
// find its credential: an is_error envelope whose result names the problem.
const notLoggedInEnvelope = `{"is_error":true,"result":"Not logged in · Please run /login","subtype":"success"}`

// stubRunner returns a Runner replaying a fixed stdout and error.
func stubRunner(stdout string, runErr error) claudecli.Runner {
	return func(_ context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
		return []byte(stdout), runErr
	}
}

// TestComplete_IsErrorEnvelopeOnNonZeroExitKeepsResult pins the production
// failure shape: the CLI exits 1 with its is_error envelope on stdout. The
// result text must survive into the error (it used to be discarded with the
// stdout), the outage classification must be unchanged, and the bare reason
// must be recoverable through provider.ReportedReason.
func TestComplete_IsErrorEnvelopeOnNonZeroExitKeepsResult(t *testing.T) {
	p := claudecli.New("opus", claudecli.WithRunner(stubRunner(notLoggedInEnvelope, errors.New("exit status 1"))))

	_, err := p.Complete(context.Background(), provider.Request{})
	require.Error(t, err)
	assert.Equal(t,
		`claudecli: cli reported is_error (exit status 1): "Not logged in · Please run /login": provider: no model reachable`,
		err.Error())
	require.ErrorIs(t, err, provider.ErrUnavailable)
	require.NotErrorIs(t, err, provider.ErrTimeout)
	assert.True(t, provider.IsOutage(err))
	assert.Equal(t, "Not logged in · Please run /login", provider.ReportedReason(err))
}

// TestComplete_IsErrorEnvelopeOnZeroExitKeepsResult covers the exit-0 shape
// of the same envelope: the result text is kept and attached as the reason.
func TestComplete_IsErrorEnvelopeOnZeroExitKeepsResult(t *testing.T) {
	p := claudecli.New("opus", claudecli.WithRunner(stubRunner(notLoggedInEnvelope, nil)))

	_, err := p.Complete(context.Background(), provider.Request{})
	require.Error(t, err)
	assert.Equal(t,
		`claudecli: cli reported is_error: "Not logged in · Please run /login": provider: no model reachable`,
		err.Error())
	require.ErrorIs(t, err, provider.ErrUnavailable)
	assert.True(t, provider.IsOutage(err))
	assert.Equal(t, "Not logged in · Please run /login", provider.ReportedReason(err))
}

// TestComplete_IsErrorResultIsCollapsedAndCapped: a multi-line, oversized
// result is collapsed to one line and capped at 200 runes plus an ellipsis,
// on both exit shapes; a blank result keeps the reason-free wording.
func TestComplete_IsErrorResultIsCollapsedAndCapped(t *testing.T) {
	long := "  first line\n\n\tsecond   line " + strings.Repeat("é", 300) + "\n"
	stdout := fmt.Sprintf(`{"is_error":true,"result":%q}`, long)
	want := "first line second line " + strings.Repeat("é", 200-len([]rune("first line second line "))) + "…"

	for _, runErr := range []error{nil, errors.New("exit status 1")} {
		p := claudecli.New("opus", claudecli.WithRunner(stubRunner(stdout, runErr)))
		_, err := p.Complete(context.Background(), provider.Request{})
		require.ErrorIs(t, err, provider.ErrUnavailable)
		reason := provider.ReportedReason(err)
		assert.Equal(t, want, reason)
		assert.Len(t, []rune(reason), 201)
		assert.NotContains(t, err.Error(), "\n")
		assert.Contains(t, err.Error(), fmt.Sprintf("%q", want))
	}

	t.Run("blank result keeps the reason-free wording", func(t *testing.T) {
		blank := `{"is_error":true,"result":"  \n "}`
		for runErr, want := range map[error]string{
			errors.New("exit status 1"): "claudecli: cli reported is_error (exit status 1): provider: no model reachable",
			nil:                         "claudecli: cli reported is_error: provider: no model reachable",
		} {
			p := claudecli.New("opus", claudecli.WithRunner(stubRunner(blank, runErr)))
			_, err := p.Complete(context.Background(), provider.Request{})
			require.ErrorIs(t, err, provider.ErrUnavailable)
			assert.Equal(t, want, err.Error())
			assert.Empty(t, provider.ReportedReason(err))
		}
	})
}

// TestComplete_NonZeroExitUnparseableStdoutKeepsSpawnWording: a non-zero
// exit whose stdout is not an is_error envelope (garbage, empty, or a
// success-shaped envelope) keeps today's spawn/exit wording and carries no
// reported reason.
func TestComplete_NonZeroExitUnparseableStdoutKeepsSpawnWording(t *testing.T) {
	for _, stdout := range []string{"", "not json — a stray notice line", string(okEnvelope("partial"))} {
		p := claudecli.New("opus", claudecli.WithRunner(stubRunner(stdout, errors.New("exit status 1"))))
		_, err := p.Complete(context.Background(), provider.Request{})
		require.ErrorIs(t, err, provider.ErrUnavailable)
		assert.Equal(t, "claudecli: spawn/exit failed: exit status 1: provider: no model reachable", err.Error())
		assert.Empty(t, provider.ReportedReason(err))
	}
}

// TestNew_NilRunnerKeepsDefault: WithRunner(nil) is a no-op so a mis-wired
// option can't drop the default process seam.
func TestNew_NilRunnerKeepsDefault(t *testing.T) {
	p := claudecli.New("opus", claudecli.WithRunner(nil))
	// With no stub and a bogus binary path this would try the real CLI; we
	// only assert construction succeeds and the value satisfies the seam.
	var _ provider.Provider = p
	require.NotNil(t, p)
}
