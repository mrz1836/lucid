package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPeopleGolden_OverlapReconcilePre pins `lucid person reconcile` over the
// overlap Ledger ("Sam", seen first and mentioned more, and "Sam Rivera", both
// current) to what it prints today: one merge pair, found by the diminutive
// prefix rule. It is the one golden assertion a later change to reconcile is
// expected to replace, with a test proving that the only difference is the
// lines that change adds.
func TestPeopleGolden_OverlapReconcilePre(t *testing.T) {
	home := goldenBuildOverlapLedger(t)
	files := goldenTree(t, home)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"reconcile/text", []string{"person", "reconcile"}, goldenOverlapReconcileText},
		{"reconcile/json", []string{"person", "reconcile", "--json"}, goldenOverlapReconcileJSON},
	} {
		stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, tc.args...)
		require.NoErrorf(t, err, "%s: %s", tc.name, stderr)
		assert.Emptyf(t, stderr, "%s stderr", tc.name)
		assert.Equalf(t, tc.want, stdout, "golden %q", tc.name)
		goldenAssertReadOnly(t, home, files, tc.name)
	}
}
