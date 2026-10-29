package contactweekly

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestContactWeeklyIsModelFree is the P9 guard for the Monday post (ADR-0014
// §8): the post is a fixed template rendered from the deterministic due view,
// so friend names must never reach a model provider. It parses every non-test
// .go file here and asserts none imports a provider, an agent package, the
// compose kit, or a vendor LLM SDK.
//
// Like flynode's guard it does NOT forbid the bare substring "model": the node
// hosts the go-flywheel wiring, which legitimately imports go-foundation/models.
func TestContactWeeklyIsModelFree(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	forbidden := []string{
		"internal/provider", "internal/agents", "internal/composekit",
		"openai", "anthropic", "ollama", "genai", "/llm",
	}
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
					"contactweekly file %s imports %s — the Monday post must stay model-free", name, imp.Path.Value)
			}
		}
		scanned++
	}
	require.GreaterOrEqual(t, scanned, 2, "expected to scan the package's source files")
}
