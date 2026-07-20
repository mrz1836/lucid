package structuring_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStructuring_ReadsSingleEntryOnly enforces agent-contracts.md §2's
// "allowed data access: the single raw entry passed in — nothing else" by
// construction. It parses every non-test source file in this package and
// asserts each import is on a tight allowlist: standard-library helpers plus
// the provider boundary, and nothing else. A storage/Ledger import (which
// would give Structuring reach into people/, processed/, or other raw
// entries), an os/filesystem import, or a net import would break the seam —
// so it stays honest, not just documented. This is the Structuring half of
// the bidirectional context-slice guard (plan.md Invariant Rules); the People
// routine that touches people/ lives in the storage adapter, not here.
func TestStructuring_ReadsSingleEntryOnly(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		`"context"`:       true,
		`"encoding/json"`: true,
		`"regexp"`:        true,
		`"strings"`:       true,
		`"github.com/mrz1836/lucid/internal/provider"`: true,
		// agentutil is the shared call-and-decode helper; it reaches only the
		// provider boundary + stdlib (guarded by its own access test), so it
		// gives Structuring no reach past the provider seam.
		`"github.com/mrz1836/lucid/internal/agents/agentutil"`: true,
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
				"structuring file %s imports %s — Structuring may reach only the provider boundary and stdlib, never the Ledger",
				name, imp.Path.Value)
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one structuring source file")
}
