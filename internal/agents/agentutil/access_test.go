package agentutil_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAgentutil_ReachesOnlyProviderAndStdlib keeps the shared agent helper as
// honest as the agents that depend on it. Intake and Structuring allow
// agentutil onto their tight import allowlists (access_test.go in each); that
// is only sound while agentutil itself reaches nothing past the provider
// boundary and stdlib. If agentutil ever imported storage/, os, or net it
// would become a transitive Ledger reach the agents' own guards could not see —
// so this test forbids exactly that, closing the hole by construction.
func TestAgentutil_ReachesOnlyProviderAndStdlib(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		`"context"`:       true,
		`"encoding/json"`: true,
		`"errors"`:        true,
		`"fmt"`:           true,
		`"strings"`:       true,
		`"github.com/mrz1836/lucid/internal/provider"`: true,
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
				"agentutil file %s imports %s — the shared agent helper may reach only the provider boundary and stdlib",
				name, imp.Path.Value)
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one agentutil source file")
}
