// Package factory turns a lucid.json provider block ([config.ProviderConfig])
// into a concrete [provider.Provider], and reserves the per-role selection
// seam ADR-0006 mandates. It is the one place that knows which backend name
// maps to which implementation, so the CLI verbs and `lucid serve` depend
// only on the [provider.Provider] interface and never on a concrete backend
// package.
//
// This pillar ships a single default backend that serves all four agent
// roles (intake, structuring, reflection, safety). The [Selector]
// per-role lookup already reads cfg.Roles[role], so a future per-role
// override drops in without a contract change; today the map is empty and
// every role resolves to the default. A backend name the factory does not
// recognize is a construction error — a mistyped or not-yet-shipped backend
// fails at Boot, not at first model call.
package factory

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/provider/claudecli"
)

// Build constructs the single default backend named by cfg.Backend. It is
// the composition root the CLI verbs call to obtain one provider for every
// role this pillar. An unknown backend name returns a clear error; the
// Codex CLI and any future backend register here (a new case) without
// changing this signature or the provider interface.
func Build(cfg config.ProviderConfig) (provider.Provider, error) {
	return buildBackend(cfg.Backend, cfg.Model, cfg.Endpoint, timeoutOf(cfg))
}

// buildBackend maps a resolved (backend, model, endpoint, timeout) tuple to
// a concrete provider. It is shared by [Build] and by [NewSelector]'s
// per-role resolution so both dispatch on exactly the same backend table.
func buildBackend(backend, model, endpoint string, timeout time.Duration) (provider.Provider, error) {
	switch backend {
	case "claude_cli":
		return claudecli.New(model, claudecli.WithTimeout(timeout)), nil
	// The "ollama" backend registers here in a later pillar phase; any name
	// this table does not yet recognize is a clear, early construction error
	// rather than a silent nil provider.
	default:
		return nil, fmt.Errorf("factory: unknown provider backend %q", backend)
	}
}

// timeoutOf converts the config's whole-second per-call bound into a
// duration. Validation guarantees TimeoutSeconds >= 1, so this is always a
// positive, backend-honored deadline.
func timeoutOf(cfg config.ProviderConfig) time.Duration {
	return time.Duration(cfg.TimeoutSeconds) * time.Second
}

// Selector resolves which [provider.Provider] backs a given agent role. It
// is the structural home for ADR-0006's per-role mandate: this pillar builds
// one default backend that answers every role, while any cfg.Roles[role]
// override is honored if present so the seam is real, not just documented.
// Construct it with [NewSelector]; [Selector.For] is a pure lookup.
type Selector struct {
	def   provider.Provider
	roles map[string]provider.Provider
}

// NewSelector builds the default backend plus any per-role overrides declared
// in cfg.Roles. Resolving overrides at construction means a bad backend name
// — default or override — fails here rather than on the first model call, and
// keeps [Selector.For] a non-erroring lookup. An override inherits the
// default's endpoint and timeout; it may narrow only the backend and model.
func NewSelector(cfg config.ProviderConfig) (*Selector, error) {
	def, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	roles := make(map[string]provider.Provider, len(cfg.Roles))
	for role, ov := range cfg.Roles {
		backend := ov.Backend
		if backend == "" {
			backend = cfg.Backend
		}
		model := ov.Model
		if model == "" {
			model = cfg.Model
		}
		p, buildErr := buildBackend(backend, model, cfg.Endpoint, timeoutOf(cfg))
		if buildErr != nil {
			return nil, fmt.Errorf("factory: role %q: %w", role, buildErr)
		}
		roles[role] = p
	}
	return &Selector{def: def, roles: roles}, nil
}

// For returns the provider backing role. Every role resolves to the single
// configured default this pillar; when cfg.Roles named an override for the
// role, that override is returned instead. An unrecognized role falls back
// to the default — role names are the agent-contract set, not validated
// here.
func (s *Selector) For(role string) provider.Provider {
	if p, ok := s.roles[role]; ok {
		return p
	}
	return s.def
}
