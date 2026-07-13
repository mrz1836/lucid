// Package claudecli implements [provider.Provider] over the Claude Code
// CLI — the zero-setup default backend (ADR-0006 §"Pinned invocation
// contracts"). Each Complete spawns a fresh one-shot
// `claude -p --output-format json --model <model>`: the System instruction
// is passed via `--system-prompt`, the role-tagged Messages are flattened
// onto stdin, and the JSON envelope's `.result` becomes the response text.
// Every completion is one stateless bounded slice — nothing persists
// between calls.
//
// Auth is the Claude CLI's own on-host subscription OAuth; Lucid holds no
// credential, and this package never touches an API key. It reaches only
// the provider boundary and the standard library — no Ledger, engine,
// observations, or registries — so the Sanctuary import allowlist
// (access_test.go) holds by construction.
//
// The process seam is injectable: tests supply a [WithRunner] stub so no
// test ever spawns the real CLI or requires live vendor auth (ADR-0006
// §Testing).
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/provider"
)

// binaryName is the Claude Code CLI executable spawned per call. It is
// resolved on PATH by the default runner; tests bypass the process
// entirely via [WithRunner].
const binaryName = "claude"

// Runner spawns one CLI invocation: it runs name with args, feeds stdin to
// the process, and returns its stdout. It is the single seam the backend
// uses to reach a process, so a test can replay a fixed stdout / error
// without a real `claude` on PATH. The default (see [New]) is a thin
// wrapper over exec.CommandContext honoring ctx cancellation.
type Runner func(ctx context.Context, name string, args []string, stdin []byte) (stdout []byte, err error)

// Provider is a Claude Code CLI [provider.Provider]. Construct it with
// [New]; it is safe for sequential use (each Complete is an independent
// one-shot process).
type Provider struct {
	model   string
	runner  Runner
	timeout time.Duration
}

// Option configures a [Provider] at construction (see [WithRunner],
// [WithTimeout]).
type Option func(*Provider)

// WithRunner overrides the process seam — tests use it to stub the CLI so
// no live `claude` is spawned.
func WithRunner(r Runner) Option {
	return func(p *Provider) {
		if r != nil {
			p.runner = r
		}
	}
}

// WithTimeout bounds every call: Complete wraps the caller's context with
// this deadline so a hung CLI degrades to [provider.ErrTimeout] rather than
// waiting forever, even when the caller passes a context without a deadline
// of its own. A non-positive duration leaves the caller's context
// unmodified. The factory sets this from lucid.json provider.timeout_seconds.
func WithTimeout(d time.Duration) Option {
	return func(p *Provider) { p.timeout = d }
}

// New builds a Claude CLI backend for the given model (e.g. "opus"). By
// default it spawns the real `claude` binary via exec.CommandContext;
// pass [WithRunner] to stub the process in tests and [WithTimeout] to bound
// each call.
func New(model string, opts ...Option) *Provider {
	p := &Provider{
		model:  model,
		runner: defaultRunner,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// defaultRunner spawns `claude` under ctx, feeds stdin, and returns stdout.
// cmd.Output surfaces a non-zero exit as *exec.ExitError (carrying stderr),
// which Complete maps to the transport sentinels.
func defaultRunner(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name is the fixed "claude" binary and args are constructed locally from the pinned invocation contract (ADR-0006)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	return cmd.Output()
}

// envelope is the subset of the `claude -p --output-format json` result
// object this backend reads: the assistant text plus the error flag. Usage
// and cost fields are intentionally ignored — the provider is a dumb pipe.
type envelope struct {
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// Complete spawns one `claude -p --output-format json --model <model>` call
// and returns its `.result` as the response. The System instruction is
// passed via `--system-prompt` and the Messages are flattened role-prefixed
// onto stdin. A deadline maps to [provider.ErrTimeout]; a spawn failure,
// non-zero exit, unparseable/`is_error` envelope, or an empty result maps
// to [provider.ErrUnavailable] (the designed "no model reachable"
// degradation, ADR-0006 §Consequences).
func (p *Provider) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	args := []string{"-p", "--output-format", "json", "--model", p.model}
	if req.System != "" {
		args = append(args, "--system-prompt", req.System)
	}

	stdout, err := p.runner(ctx, binaryName, args, flatten(req.Messages))
	if err != nil {
		// A context deadline that killed the process surfaces here as a
		// generic exec error, so branch on ctx.Err() first: a deadline is a
		// timeout (retryable), an explicit cancel is the caller's, and any
		// other spawn/exit failure is "no model reachable".
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return provider.Response{}, fmt.Errorf("claudecli: %w", provider.ErrTimeout)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return provider.Response{}, fmt.Errorf("claudecli: %w", ctxErr)
		}
		return provider.Response{}, fmt.Errorf("claudecli: spawn/exit failed: %w: %w", err, provider.ErrUnavailable)
	}

	var env envelope
	if jsonErr := json.Unmarshal(stdout, &env); jsonErr != nil {
		return provider.Response{}, fmt.Errorf("claudecli: unparseable envelope: %w: %w", jsonErr, provider.ErrUnavailable)
	}
	if env.IsError {
		return provider.Response{}, fmt.Errorf("claudecli: cli reported is_error: %w", provider.ErrUnavailable)
	}
	if env.Result == "" {
		return provider.Response{}, fmt.Errorf("claudecli: empty result: %w", provider.ErrUnavailable)
	}
	return provider.Response{Content: env.Result}, nil
}

// flatten renders the authorized Messages slice as role-prefixed lines fed
// to the CLI on stdin (System travels separately via --system-prompt). An
// empty slice yields empty stdin.
func flatten(msgs []provider.Message) []byte {
	if len(msgs) == 0 {
		return nil
	}
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	return []byte(b.String())
}

// interface guard: *Provider must satisfy provider.Provider.
var _ provider.Provider = (*Provider)(nil)
