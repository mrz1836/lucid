package contact

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestContactIsPure_NoIONoLLM enforces the record family's binding constraints by
// construction (ADR-0014: agent-free, no LLM in any path, and no filesystem
// access — the storage adapter is the only code that touches ~/.lucid/). It
// parses every non-test .go file in this package and asserts each import is on a
// tight pure allowlist, so no provider/agent/model package and no os/net access
// can ever reach the deterministic core. The non-stdlib imports permitted are
// the vetted-pure observation core (logical-day rollover), the ISO week helper,
// and the engine (the shared per-day slot grammar for event ids).
func TestContactIsPure_NoIONoLLM(t *testing.T) {
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
		`"github.com/mrz1836/lucid/internal/observations"`: true,
		`"github.com/mrz1836/lucid/internal/isoweek"`:      true,
		`"github.com/mrz1836/lucid/internal/engine"`:       true,
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
				"contact file %s imports %s — the record family must be pure (no io, no LLM/provider/agent)",
				name, imp.Path.Value)
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one contact source file")
}
