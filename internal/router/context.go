// Package router is the command/router layer: it translates a slash
// command into an explicit, named Lucid intent and decides which
// agent(s) handle a turn, in what order, against which storage scope
// (architecture.md §2). It is the only place that constructs an
// [AgentContext] and the only place that grows when a new command
// ships. This Phase-1 foundation carries the typed context-slice gate,
// the sanctuary denylist, and the boot routine that clips an
// out-of-range lucid.json.
package router

import (
	"path"
	"strings"
)

// SanctuaryDenylist returns the runtime subtrees no agent slice may
// ever include (Sanctuary / P3, enforced by construction in both
// directions). An agent that could read the Engine or observations
// trees would break the boundary the whole product rests on, so slice
// construction fails closed against these prefixes (architecture.md §6
// context-slice gate; plan.md Invariant Rules). The list is grepped at
// Stage 5 and unit-tested here.
func SanctuaryDenylist() []string {
	return []string{
		"engine/",
		"observations/",
		"registries/",
	}
}

// PathAllowedForAgent reports whether a Ledger-relative path may appear
// in an agent's context slice. It fails closed: any path inside a
// sanctuary subtree (engine/, observations/, registries/) — at the root
// or nested — is denied. The check is prefix-based on cleaned,
// slash-normalized path segments so "engine/days/x" and "./engine" are
// both caught and "engineering/" is not.
func PathAllowedForAgent(rel string) bool {
	clean := path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	clean = strings.TrimPrefix(clean, "/")
	for _, deny := range SanctuaryDenylist() {
		d := strings.TrimSuffix(deny, "/")
		if clean == d || strings.HasPrefix(clean, d+"/") {
			return false
		}
	}
	return true
}

// AgentContext is the only channel through which an agent receives
// data. T is the explicit slice schema for one agent sub-mode (e.g.
// AgentContext[IntakeInput]). Because an agent function accepts only an
// AgentContext[T] — there is no global storage handle, no shared
// session map, and no filesystem reader in agent scope — "context
// slice" is enforced by construction, not convention (architecture.md
// §6 "Mechanism"). New agents declare a new T and a new router plan;
// that is the only way to widen what an agent can see.
type AgentContext[T any] struct {
	intent string
	slice  T
}

// NewAgentContext builds the sole context an agent will receive. intent
// names the router plan that authorized this slice (for logging and
// audit); slice carries exactly the data the agent is permitted to see.
func NewAgentContext[T any](intent string, slice T) AgentContext[T] {
	return AgentContext[T]{intent: intent, slice: slice}
}

// Intent returns the router plan that authorized this context.
func (c AgentContext[T]) Intent() string { return c.intent }

// Slice returns the authorized data slice. It is the entirety of what
// the agent may read for this turn.
func (c AgentContext[T]) Slice() T { return c.slice }
