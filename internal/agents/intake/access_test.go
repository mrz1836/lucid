package intake_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntake_ReadsCurrentThreadOnly enforces agent-contracts.md §1's
// "reads the current thread only — never ~/.lucid/raw/, processed/, or any
// other prior session" by construction. It parses every non-test source
// file in this package and asserts each import is on a tight allowlist:
// standard-library helpers plus the provider boundary, and nothing else.
// A storage/Ledger import, an os/filesystem import, or a net import would
// give Intake reach beyond the thread the router authorized — so the seam
// stays honest, not just documented. This is the intake half of the
// bidirectional sanctuary/context-slice guard (plan.md Invariant Rules).
func TestIntake_ReadsCurrentThreadOnly(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		`"context"`:       true,
		`"encoding/json"`: true,
		`"errors"`:        true,
		`"fmt"`:           true,
		`"strings"`:       true,
		`"github.com/mrz1836/lucid/internal/provider"`: true,
		// agentutil is the shared call-and-decode helper; it reaches only the
		// provider boundary + stdlib (guarded by its own access test), so it
		// gives Intake no reach past the provider seam.
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
				"intake file %s imports %s — Intake must reach only the provider boundary and stdlib, never the Ledger",
				name, imp.Path.Value)
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one intake source file")
}
