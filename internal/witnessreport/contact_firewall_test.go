package witnessreport

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
)

// TestWitnessReport_NeverReadsContactData pins the contact check-in firewall
// (ADR-0014 §10): the friend-facing weekly report never reads contact data,
// and v1 has no setting to make it. It walks every non-test .go file in this
// package and asserts none imports the contact core or the Monday contact
// node and none so much as names "contact"; and it asserts the
// witness_report config block carries no field — by Go name or JSON key — that
// mentions contact, so no opt-in exists to flip.
func TestWitnessReport_NeverReadsContactData(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	forbiddenImports := []string{
		`"github.com/mrz1836/lucid/internal/contact"`,
		`"github.com/mrz1836/lucid/internal/contactweekly"`,
	}
	fset := token.NewFileSet()
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(".", name)
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		require.NoError(t, perr)
		for _, imp := range f.Imports {
			for _, bad := range forbiddenImports {
				require.NotEqualf(t, bad, imp.Path.Value,
					"witnessreport file %s imports %s — the witness report never reads contact data", name, imp.Path.Value)
			}
		}
		src, rerr := os.ReadFile(path)
		require.NoError(t, rerr)
		require.NotContainsf(t, strings.ToLower(string(src)), "contact",
			"witnessreport file %s names contact — the witness report never reads contact data", name)
		scanned++
	}
	require.Positive(t, scanned, "expected to scan the witnessreport source files")

	cfg := reflect.TypeOf(config.WitnessReportConfig{})
	for i := range cfg.NumField() {
		field := cfg.Field(i)
		require.NotContainsf(t, strings.ToLower(field.Name+" "+field.Tag.Get("json")), "contact",
			"witness_report.%s mentions contact — v1 has no setting to include contact data", field.Tag.Get("json"))
	}
}
