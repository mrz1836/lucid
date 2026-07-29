package flynode

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFlynodeImportsNoModel is the agent-free guard for the shared daemon
// scaffolding (P9 / ADR-0004: no LLM in a write path). flynode hosts the teeth's
// boot spine as well as the message daemons', so it must stay reachable from the
// agent-free write path — send/verify/compose reach it only as injected
// closures, never as a named provider/agent type. It parses every non-test .go
// file here and asserts none imports a provider, an agent package, or a vendor
// LLM SDK.
//
// Like internal/schedrun's guard (and unlike internal/scheduler's) it does NOT
// forbid the bare substring "model": flynode hosts the go-flywheel wiring, and
// flywheel legitimately imports go-foundation/models. Forbidding "model" here
// would false-fail on that dependency.
func TestFlynodeImportsNoModel(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	forbidden := []string{"internal/provider", "internal/agents", "openai", "anthropic", "/llm"}
	fset := token.NewFileSet()
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		require.NoError(t, perr)
		for _, imp := range f.Imports {
			for _, bad := range forbidden {
				require.NotContainsf(t, strings.ToLower(imp.Path.Value), bad,
					"flynode file %s imports %s — no model may sit in a daemon write path", name, imp.Path.Value)
			}
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one flynode source file")
}
