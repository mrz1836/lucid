// Package lucidtest holds shared test helpers for building an isolated Ledger.
// Every package that exercises the storage adapter used to re-roll the same
// New(t.TempDir()) + Scaffold() (+ ScaffoldEngine/ScaffoldObservations)
// constructor; [Ledger] is the single, option-driven replacement.
//
// It imports only internal/storage, so any package that already depends on
// storage may use it. The two white-box test packages that cannot (a storage
// or observations package test importing this would form an import cycle) keep
// their own thin local builders.
package lucidtest

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// config accumulates the requested scaffolding; the zero value is a bare
// Ledger rooted directly at t.TempDir().
type config struct {
	engine       bool
	observations bool
	nested       bool
}

// Option tunes what [Ledger] scaffolds and where it roots the home.
type Option func(*config)

// WithEngine additionally scaffolds the Engine subtree (chain.json, profile.json,
// days/, …) — for tests that read or write Engine state.
func WithEngine() Option { return func(c *config) { c.engine = true } }

// WithObservations additionally scaffolds the observations subtree — for tests
// that read or write micro-logs, registries, or the day view.
func WithObservations() Option { return func(c *config) { c.observations = true } }

// NestedHome roots the Ledger at <tempdir>/.lucid rather than <tempdir>,
// matching callers that construct the home the way `lucid init` lays it out
// under a parent directory.
func NestedHome() Option { return func(c *config) { c.nested = true } }

// Ledger returns a freshly scaffolded, fully isolated Ledger under t.TempDir()
// together with its home path. It is the shared replacement for the
// New + Scaffold constructor each package re-implemented; pass options to also
// scaffold the Engine or observations subtrees or to nest the home.
func Ledger(t *testing.T, opts ...Option) (home string, a *storage.Adapter) {
	t.Helper()
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	home = t.TempDir()
	if cfg.nested {
		home = filepath.Join(home, ".lucid")
	}
	a = storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err, "scaffold ledger")
	if cfg.engine {
		require.NoError(t, a.ScaffoldEngine(), "scaffold engine")
	}
	if cfg.observations {
		require.NoError(t, a.ScaffoldObservations(), "scaffold observations")
	}
	return home, a
}
