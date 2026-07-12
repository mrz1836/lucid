package factory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider/claudecli"
	"github.com/mrz1836/lucid/internal/provider/factory"
)

// agentRoles are the four roles a single default backend serves this pillar
// (agent-contracts.md); the per-role lookup must resolve every one of them.
var agentRoles = []string{"intake", "structuring", "reflection", "safety"}

// TestBuild_DefaultBuildsClaudeCLI: the shipped default provider block
// (backend "claude_cli") constructs the Claude CLI backend.
func TestBuild_DefaultBuildsClaudeCLI(t *testing.T) {
	p, err := factory.Build(config.Default().Provider)
	require.NoError(t, err)
	require.NotNil(t, p)
	_, ok := p.(*claudecli.Provider)
	assert.True(t, ok, "default backend must be the Claude CLI provider, got %T", p)
}

// TestBuild_UnknownBackendErrors: a backend name the table does not
// recognize is a clear construction error (also covers a not-yet-shipped
// backend like the Codex CLI).
func TestBuild_UnknownBackendErrors(t *testing.T) {
	cfg := config.Default().Provider
	cfg.Backend = "codex"
	p, err := factory.Build(cfg)
	require.Error(t, err)
	assert.Nil(t, p)
	assert.Contains(t, err.Error(), "unknown provider backend")
}

// TestSelector_ForResolvesDefaultForAllRoles: with no per-role overrides,
// every agent role resolves to the same single default backend.
func TestSelector_ForResolvesDefaultForAllRoles(t *testing.T) {
	sel, err := factory.NewSelector(config.Default().Provider)
	require.NoError(t, err)

	def := sel.For(agentRoles[0])
	require.NotNil(t, def)
	_, ok := def.(*claudecli.Provider)
	assert.True(t, ok, "role backend must be the Claude CLI provider, got %T", def)

	for _, role := range agentRoles {
		assert.Samef(t, def, sel.For(role),
			"role %q must resolve to the single default backend this pillar", role)
	}
}

// TestSelector_HonorsPerRoleOverride: the reserved per-role seam is real —
// a cfg.Roles override resolves to its own backend instance while
// unoverridden roles still share the default (structural wiring).
func TestSelector_HonorsPerRoleOverride(t *testing.T) {
	cfg := config.Default().Provider
	cfg.Roles = map[string]config.ProviderRole{
		"reflection": {Backend: "claude_cli", Model: "sonnet"},
	}

	sel, err := factory.NewSelector(cfg)
	require.NoError(t, err)

	override := sel.For("reflection")
	require.NotNil(t, override)
	assert.NotSame(t, sel.For("intake"), override,
		"an overridden role must resolve to its own backend instance")
	assert.Same(t, sel.For("intake"), sel.For("safety"),
		"unoverridden roles still share the default backend")
}

// TestSelector_PropagatesBadDefaultBackend: a bad default backend fails at
// construction, not at first model call.
func TestSelector_PropagatesBadDefaultBackend(t *testing.T) {
	cfg := config.Default().Provider
	cfg.Backend = "codex"
	sel, err := factory.NewSelector(cfg)
	require.Error(t, err)
	assert.Nil(t, sel)
}

// TestSelector_PropagatesBadOverrideBackend: an unknown per-role override
// backend is a construction error naming the offending role.
func TestSelector_PropagatesBadOverrideBackend(t *testing.T) {
	cfg := config.Default().Provider
	cfg.Roles = map[string]config.ProviderRole{
		"safety": {Backend: "codex"},
	}
	sel, err := factory.NewSelector(cfg)
	require.Error(t, err)
	assert.Nil(t, sel)
	assert.Contains(t, err.Error(), "safety")
}
