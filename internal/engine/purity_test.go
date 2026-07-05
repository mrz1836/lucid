package engine

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEngineIsPure_NoIONoLLM enforces the Engine module's two binding
// constraints by construction (engine-module.md §"Design constraints
// inherited"): agent-free (no LLM anywhere) and no filesystem access (the
// storage adapter is the only code that touches disk). It parses every
// non-test .go file in this package and asserts each import is on a tight
// allowlist — so no provider/agent/model package, and no os/net, can ever
// reach the deterministic core. This is the phase's "assert no LLM call
// anywhere in the phase's code paths" guard for the Engine core.
func TestEngineIsPure_NoIONoLLM(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		`"fmt"`:     true,
		`"sort"`:    true,
		`"strconv"`: true,
		`"strings"`: true,
		`"time"`:    true,
	}
	fset := token.NewFileSet()
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		require.NoError(t, parseErr)
		for _, imp := range f.Imports {
			require.Truef(t, allowed[imp.Path.Value],
				"engine file %s imports %s — the Engine core must be pure (no io, no LLM/provider/agent)",
				name, imp.Path.Value)
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one engine source file")
}
