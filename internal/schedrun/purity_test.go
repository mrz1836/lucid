package schedrun

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSchedrunImportsNoModel is the agent-free guard for the scheduler write
// path (P9 / ADR-0004: no LLM in the tripwire path). It parses every non-test
// .go file in this package and asserts none imports a provider, an agent
// package, or a vendor LLM SDK.
//
// Unlike internal/scheduler's guard it deliberately does NOT forbid the bare
// substring "model": this package hosts the go-flywheel wiring, and flywheel
// legitimately imports go-foundation/models. Forbidding "model" here would
// false-fail on that dependency — which is exactly why the flywheel driver lives
// in this sibling package instead of internal/scheduler.
func TestSchedrunImportsNoModel(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	forbidden := []string{"internal/provider", "internal/agents", "openai", "anthropic", "/llm"}
	fset := token.NewFileSet()
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
					"schedrun file %s imports %s — no model may sit in the scheduler write path", name, imp.Path.Value)
			}
		}
	}
}
