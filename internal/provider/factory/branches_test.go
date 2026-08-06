package factory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider/claudecli"
	"github.com/mrz1836/lucid/internal/provider/factory"
)

// TestSelector_OverrideInheritsDefaultBackend: a per-role override that names
// only a model inherits the default backend (and endpoint/timeout), resolving
// to its own instance while unoverridden roles still share the default.
func TestSelector_OverrideInheritsDefaultBackend(t *testing.T) {
	cfg := config.Default().Provider // default backend is claude_cli
	cfg.Roles = map[string]config.ProviderRole{
		"reflection": {Model: "sonnet"}, // Backend left empty ⇒ inherit the default
	}

	sel, err := factory.NewSelector(cfg)
	require.NoError(t, err)

	override := sel.For("reflection")
	require.NotNil(t, override)
	_, ok := override.(*claudecli.Provider)
	assert.True(t, ok, "an override with no backend inherits the default (claude_cli), got %T", override)
	assert.NotSame(t, sel.For("intake"), override,
		"an overridden role resolves to its own instance even when it inherits the backend")
}
