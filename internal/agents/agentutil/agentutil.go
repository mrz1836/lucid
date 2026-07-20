// Package agentutil holds the small mechanics every LLM-backed agent shares.
// It sits under internal/agents and reaches a model only through the
// [provider.Provider] seam (ADR-0006) — it wires no SDK and touches no storage.
//
// Its one job today is [CompleteJSON]: the "call the model, decode its JSON
// reply" dance the intake, structuring, and reflection agents each hand-copied.
// Semantic validation of the decoded value stays with the caller, which knows
// its own contract; this package owns only the transport-and-decode step so the
// trimming and error convention live in exactly one place.
package agentutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mrz1836/lucid/internal/provider"
)

// ErrParse marks a reply that reached the agent but did not decode as the
// expected JSON shape — the "model answered with garbage" failure, distinct
// from a transport error the provider returns. Agents that must retry or
// degrade differently for "no model reached" versus "unusable reply" branch on
// errors.Is(err, ErrParse) (e.g. reflection's grounded-answer §N-3 vs §R-12).
var ErrParse = errors.New("agentutil: reply is not valid JSON")

// CompleteJSON runs one bounded completion and decodes the model's reply as
// JSON into a T. It trims surrounding whitespace before decoding, since models
// routinely wrap JSON in stray newlines. On any failure it returns the zero T:
// a transport error is returned verbatim so provider.ErrTimeout /
// provider.ErrUnavailable stay comparable with errors.Is, and a malformed reply
// is wrapped with [ErrParse]. The caller adds its own agent-scoped wrapping (or
// translates to its ok bool) and performs its own semantic validation.
func CompleteJSON[T any](ctx context.Context, p provider.Provider, req provider.Request) (T, error) {
	var zero T
	resp, err := p.Complete(ctx, req)
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &v); err != nil {
		return zero, fmt.Errorf("%w: %w", ErrParse, err)
	}
	return v, nil
}
