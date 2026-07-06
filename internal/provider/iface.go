// Package provider is the single seam through which every Lucid agent
// reaches a model (ADR-0006). Model access goes through a locally-
// authenticated vendor CLI, a local model runtime, or (in tests) a
// scripted fake — never a raw API key — but the agents never see that
// choice: they call one [Provider] interface and the concrete backend is
// per-instance configuration. ADR-0001's "HTTP calls behind interfaces"
// generalizes here to "process or HTTP behind the same interface".
//
// The package is deliberately tiny and dependency-free: it owns the
// request/response shape and the small set of transport error sentinels,
// nothing else. It performs no filesystem or network access itself — a
// real backend lives behind this interface, and tests use [Fake] so no
// test ever requires live vendor auth (ADR-0006 §Consequences).
package provider

import (
	"context"
	"errors"
)

// Role names who authored a message in the bounded slice handed to a
// model. Only these three are valid; an agent builds the slice from the
// current turn's authorized context, never from the full history.
type Role string

// The three message roles. The system instruction is carried separately
// on [Request.System]; RoleSystem exists for backends that model it as a
// message rather than a field.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in the authorized slice: who said it and what they
// said. Agents pass only the narrow slice the router authorized for the
// current step — never the whole conversation, never the Ledger.
type Message struct {
	Role    Role
	Content string
}

// Request is one bounded completion call. Intent is an audit label naming
// the router plan that authorized the slice (e.g. "intake.decide"); System
// is the instruction that frames the task; Messages is the authorized
// slice. A backend must send only what is in this struct — the interface
// gives it no other reach.
type Request struct {
	Intent   string
	System   string
	Messages []Message
}

// Response is a completion result: the model's raw text output. Parsing
// and validating that text (JSON shape, schema, phrase blocklist) is the
// calling agent's job, not the provider's — the provider is a dumb pipe.
type Response struct {
	Content string
}

// Provider is the sole model boundary. Implementations may spawn a vendor
// CLI, call a local daemon, or replay a script; the contract is identical
// and swappable per instance (ADR-0006). A backend that cannot answer
// returns one of the sentinels below (or a wrapped form) so callers can
// branch on the failure class without string-matching.
type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Transport error sentinels. A backend returns these (directly or wrapped
// with %w) so an agent can distinguish "the model is momentarily
// unreachable" (retry) from "the model answered with garbage" (a parse
// error the agent raises itself). Callers compare with errors.Is.
var (
	// ErrTimeout means the backend did not answer within its deadline.
	// It maps to the "transport timeout" branch agents retry once
	// (error-states.md §N-2).
	ErrTimeout = errors.New("provider: request timed out")
	// ErrUnavailable means no model was reachable at all (expired OAuth,
	// offline daemon) — the designed "no model reachable" degradation
	// (ADR-0006 §Consequences; architecture P9).
	ErrUnavailable = errors.New("provider: no model reachable")
)
