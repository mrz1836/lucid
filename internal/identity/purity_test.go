package identity

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIdentityIsPure_NoIONoLLM enforces the identity core's binding constraints
// by construction (ADR-0016: deterministic, no LLM in any path, and no
// filesystem access — the storage adapter is the only code that touches
// ~/.lucid/). It parses every non-test .go file in this package and asserts each
// import is on a tight pure allowlist, so no provider/agent/model package and no
// os/net access can ever reach resolution's facts. The non-stdlib imports
// permitted are the pure key normalization (forms compare as person_keys do)
// and the engine (the shared per-day slot grammar for event ids).
func TestIdentityIsPure_NoIONoLLM(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		`"encoding/json"`: true,
		`"errors"`:        true,
		`"fmt"`:           true,
		`"slices"`:        true,
		`"sort"`:          true,
		`"strconv"`:       true,
		`"strings"`:       true,
		`"time"`:          true,
		`"github.com/mrz1836/lucid/internal/keyderive"`: true,
		`"github.com/mrz1836/lucid/internal/engine"`:    true,
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
				"identity file %s imports %s — the identity core must be pure (no io, no LLM/provider/agent)",
				name, imp.Path.Value)
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one identity source file")
}
