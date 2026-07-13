package ollama_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOllama_ImportAllowlist enforces the Sanctuary rule (architecture P3) by
// construction: the Ollama backend may reach only the standard library and the
// provider boundary — never the Ledger (internal/storage), the Engine,
// observations, or registries. A model backend that could import those trees
// would have reach beyond the bounded slice the router authorized, so the seam
// is kept honest by parsing every non-test source file's imports and asserting
// each is on a tight allowlist (mirrors the claudecli/intake/structuring access
// guards).
func TestOllama_ImportAllowlist(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		`"bytes"`:         true,
		`"context"`:       true,
		`"encoding/json"`: true,
		`"errors"`:        true,
		`"fmt"`:           true,
		`"net"`:           true,
		`"net/http"`:      true,
		`"strings"`:       true,
		`"time"`:          true,
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
				"ollama file %s imports %s — the model backend must reach only stdlib and the provider boundary, never the Ledger/Engine/observations/registries",
				name, imp.Path.Value)
		}
		scanned++
	}
	require.Positive(t, scanned, "expected to scan at least one ollama source file")
}
