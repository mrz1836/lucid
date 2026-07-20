package observations

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestObservationsIsPure_NoIONoLLM enforces the module's two binding
// constraints by construction (observations-module.md: "entirely agent-free —
// deterministic parsers, arithmetic, and template output; no LLM in any
// path"). It parses every non-test .go file in this package and asserts each
// import is on a tight pure-stdlib allowlist — so no provider/agent/model
// package, and no os/net filesystem or network access, can ever reach the
// deterministic core. The storage adapter is the only code that touches disk.
// This is the phase's "no LLM in any path" guard for the observation core.
func TestObservationsIsPure_NoIONoLLM(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		`"cmp"`:           true,
		`"crypto/sha256"`: true,
		`"encoding/json"`: true,
		`"fmt"`:           true,
		`"maps"`:          true,
		`"slices"`:        true,
		`"sort"`:          true,
		`"strconv"`:       true,
		`"strings"`:       true,
		`"time"`:          true,
		`"unicode"`:       true,
		// keyderive is the shared, vetted-pure slug-derivation core (only
		// crypto/sha256 + fmt + strings + unicode); permitting it keeps the
		// "no io, no LLM" intent intact while removing duplication with storage.
		`"github.com/mrz1836/lucid/internal/keyderive"`: true,
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
				"observations file %s imports %s — the module must be pure (no io, no LLM/provider/agent)",
				name, imp.Path.Value)
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one observations source file")
}
