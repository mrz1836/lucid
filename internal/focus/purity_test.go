package focus

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFocusIsPure_NoIONoLLM enforces the record family's binding constraints by
// construction (focus.md §1: agent-free, no LLM in any path, and no filesystem
// access — the storage adapter is the only code that touches ~/.lucid/). It
// parses every non-test .go file in this package and asserts each import is on a
// tight pure allowlist — so no provider/agent/model package, and no os/net
// access, can ever reach the deterministic core. The only non-stdlib import
// permitted is the observations layer, itself vetted-pure by its own purity test,
// whose logical-day rollover primitives this package reuses so the focus rotation
// and the day-view join stay aligned.
func TestFocusIsPure_NoIONoLLM(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		`"encoding/json"`: true,
		`"fmt"`:           true,
		`"strconv"`:       true,
		`"strings"`:       true,
		`"time"`:          true,
		// observations is the vetted-pure observation core (its own purity test
		// bars io/net/provider imports); reusing its logical-day rollover keeps
		// "no io, no LLM" intact while aligning the focus day key with the join.
		`"github.com/mrz1836/lucid/internal/observations"`: true,
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
				"focus file %s imports %s — the record family must be pure (no io, no LLM/provider/agent)",
				name, imp.Path.Value)
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one focus source file")
}
