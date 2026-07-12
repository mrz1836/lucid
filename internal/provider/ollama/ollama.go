// Package ollama implements [provider.Provider] over a local Ollama daemon
// — the full-sovereignty backend (ADR-0006 §"Pinned invocation contracts").
// Each Complete makes a non-streaming `POST <endpoint>/api/chat` with the
// System instruction mapped to a leading `system` message and the role-tagged
// Messages passed through; the reply's `.message.content` becomes the response
// text. Every completion is one bounded call — nothing persists between them.
//
// The local model is a first-class configuration, not a degraded mode, and
// this package holds no credential and touches no API key — auth is the local
// daemon's. It reaches only the provider boundary and the standard library —
// no Ledger, engine, observations, or registries — so the Sanctuary import
// allowlist (access_test.go) holds by construction.
//
// Every call is deadline-bounded against the known binary-skew failure class:
// a stale `ollama serve` leaves `/api/chat` hanging while `/api/tags` stays
// healthy, so a hung daemon must map to [provider.ErrTimeout] rather than an
// unbounded wait. The base URL and *http.Client are injectable so tests hit an
// httptest.Server and no test requires a running daemon (ADR-0006 §Testing).
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/provider"
)

// chatPath is the daemon endpoint every Complete posts to, appended to the
// configured base URL (default "http://localhost:11434").
const chatPath = "/api/chat"

// Provider is a local-Ollama [provider.Provider]. Construct it with [New]; it
// is safe for sequential use (each Complete is an independent bounded request).
type Provider struct {
	baseURL string
	model   string
	client  *http.Client
	timeout time.Duration
}

// Option configures a [Provider] at construction (see [WithHTTPClient],
// [WithTimeout]).
type Option func(*Provider)

// WithHTTPClient overrides the transport — tests use it to point at an
// httptest.Server so no live daemon is contacted. A nil client is ignored so a
// mis-wired option cannot drop the default transport.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) {
		if c != nil {
			p.client = c
		}
	}
}

// WithTimeout bounds every call: Complete wraps the caller's context with this
// deadline so a hung daemon degrades to [provider.ErrTimeout] rather than
// waiting forever, even when the caller passes a context without a deadline of
// its own. It also becomes the default *http.Client's own Timeout. A
// non-positive duration leaves the caller's context unmodified. The factory
// sets this from lucid.json provider.timeout_seconds.
func WithTimeout(d time.Duration) Option {
	return func(p *Provider) { p.timeout = d }
}

// New builds an Ollama backend for the given base URL (e.g.
// "http://localhost:11434") and model (e.g. "qwen2.5:14b"). By default it uses
// an *http.Client whose Timeout is the configured deadline; pass
// [WithHTTPClient] to stub the transport in tests and [WithTimeout] to bound
// each call.
func New(baseURL, model string, opts ...Option) *Provider {
	p := &Provider{
		baseURL: baseURL,
		model:   model,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.client == nil {
		// The default client honors the config timeout too, so even a caller
		// context without a deadline stays bounded (belt-and-suspenders with
		// the per-call context wrap in Complete).
		p.client = &http.Client{Timeout: p.timeout}
	}
	return p
}

// chatMessage is one turn in the /api/chat request (and the reply's message):
// a role name plus its text.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest is the non-streaming /api/chat payload: the model, the mapped
// message slice, and stream:false so the daemon returns a single object.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// chatResponse is the subset of the /api/chat reply this backend reads: the
// assistant message. Timing and token fields are intentionally ignored — the
// provider is a dumb pipe.
type chatResponse struct {
	Message chatMessage `json:"message"`
}

// Complete makes one non-streaming `POST <baseURL>/api/chat` and returns the
// reply's `.message.content` as the response. The System instruction is mapped
// to a leading `system` message and the Messages are passed through role-tagged.
// A context deadline or a client timeout (a hung daemon) maps to
// [provider.ErrTimeout]; a refused connection, DNS failure, non-2xx status, or
// an unpulled/missing model maps to [provider.ErrUnavailable] (the designed
// "no model reachable" degradation, ADR-0006 §Consequences).
func (p *Provider) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	body, err := json.Marshal(chatRequest{
		Model:    p.model,
		Messages: toMessages(req),
		Stream:   false,
	})
	if err != nil {
		return provider.Response{}, fmt.Errorf("ollama: marshal request: %v: %w", err, provider.ErrUnavailable)
	}

	url := strings.TrimRight(p.baseURL, "/") + chatPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return provider.Response{}, fmt.Errorf("ollama: build request: %v: %w", err, provider.ErrUnavailable)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		// A deadline — the caller's or this backend's WithTimeout bound — is the
		// known binary-skew hang: a stale daemon leaves /api/chat hanging. Map it
		// to ErrTimeout, never an unbounded wait. An explicit caller cancel stays
		// the caller's. Anything else (connection refused, DNS) is "no daemon
		// reachable".
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || isTimeout(err) {
			return provider.Response{}, fmt.Errorf("ollama: %w", provider.ErrTimeout)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return provider.Response{}, fmt.Errorf("ollama: %w", ctxErr)
		}
		return provider.Response{}, fmt.Errorf("ollama: request failed: %v: %w", err, provider.ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// A reachable daemon that cannot serve (e.g. 404 "model not found, try
		// pulling it first") is an unavailable model, not a timeout.
		return provider.Response{}, fmt.Errorf("ollama: unexpected status %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}

	var cr chatResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&cr); decErr != nil {
		return provider.Response{}, fmt.Errorf("ollama: unparseable response: %v: %w", decErr, provider.ErrUnavailable)
	}
	if cr.Message.Content == "" {
		return provider.Response{}, fmt.Errorf("ollama: empty message content: %w", provider.ErrUnavailable)
	}
	return provider.Response{Content: cr.Message.Content}, nil
}

// toMessages maps the shared Request onto the /api/chat message slice: the
// System instruction becomes a leading `system` message (Ollama models it as a
// message, not a field) and the authorized Messages pass through role-tagged.
func toMessages(req provider.Request) []chatMessage {
	msgs := make([]chatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, chatMessage{Role: string(provider.RoleSystem), Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, chatMessage{Role: string(m.Role), Content: m.Content})
	}
	return msgs
}

// isTimeout reports whether err is a transport timeout (a client Timeout that
// fired, or a deadline surfaced through the net stack) so a hung daemon maps to
// ErrTimeout even when the context error is not itself the trigger.
func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

// interface guard: *Provider must satisfy provider.Provider.
var _ provider.Provider = (*Provider)(nil)
