// Package composekit holds the small compose/provider-adjacent helpers the
// message daemons' composers share verbatim: reading an operator's explicit
// prompt file, applying a per-feature model override onto the base provider
// config, the shared [ProviderBuilder] backend-constructor type, and the
// bullet-stripping [StripBullet] each reply parser trims lines with. It is
// deliberately separate from internal/flynode — flynode is provider-free by
// construction, and these helpers name config.ProviderConfig and
// provider.Provider — and it owns nothing of the daemons' distinct
// compose/render domain logic.
package composekit

import (
	"errors"
	"os"
	"strings"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
)

// ProviderBuilder constructs a model backend from a resolved provider config.
// Each composer (companion, workout, witness report) defaults it to
// factory.Build and lets a test inject a builder returning a [provider.Fake], so
// no compose test needs live vendor auth (ADR-0006). It lives here because the
// three composers declared this signature identically.
type ProviderBuilder func(config.ProviderConfig) (provider.Provider, error)

// StripBullet trims one model-reply line to its content: outer whitespace, then
// a leading run of bullet markers (-, *, •), then whitespace again. It is the
// shared body of the composers' ask/action line parsers, which drop a line that
// strips to empty. It only removes framing — the text itself is unchanged.
func StripBullet(line string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*•"))
}

// ReadPromptFile reads one explicit, opaque prompt file for a compose call. It
// opens exactly the operator-configured path — never a directory walk — and
// rejects an empty path rather than reading an accidental default. The three
// composers (companion, workout, witness report) share it byte for byte.
func ReadPromptFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty prompt path")
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-configured, explicit prompt path — read directly, never dir-walked
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WithModelOverride returns base with its Model replaced by model when model is
// non-empty, else base unchanged. base is taken by value, so the caller's
// provider config is never mutated. It is the single place a feature's optional
// model override (companion/workout/witness_report.model) is layered onto the
// shared provider.model.
func WithModelOverride(base config.ProviderConfig, model string) config.ProviderConfig {
	if model != "" {
		base.Model = model
	}
	return base
}
