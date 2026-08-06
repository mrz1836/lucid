package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStatefulVerbs_BootErrorSurfaces guards the shared bootedRouter contract at
// the spine: every stateful verb resolves and scaffolds the Ledger before it
// does any work, so an unscaffoldable home surfaces the failure rather than
// running against a half-open store. Each verb is given just enough valid args
// to pass cobra's positional validation and reach its RunE, where bootedRouter
// is the first call. Verbs with their own dedicated *_BootError test (closeout,
// recall, self, …) are deliberately omitted to avoid duplicating that guard.
func TestStatefulVerbs_BootErrorSurfaces(t *testing.T) {
	cases := map[string][]string{
		"day":      {"day"},
		"era":      {"era", "wild summer"},
		"excavate": {"excavate"},
		"export":   {"export"},
		"injury":   {"injury", "left knee"},
		"obs":      {"obs"},
		"stats":    {"stats"},
		"thread":   {"thread", "writing"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			unscaffoldableHome(t)
			_, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
			require.Error(t, err, "a stateful verb surfaces the boot failure")
		})
	}
}
