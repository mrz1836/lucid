package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzParseCompact asserts the one-line compact closeout parser is robust
// against arbitrary user input: it never panics, and a successful parse always
// yields one link status per chain link. Seeds cover the well-formed forms plus
// the wrong-length, unknown-char, and empty edges.
func FuzzParseCompact(f *testing.F) {
	for _, s := range []string{
		"dfx 3/wrist Long day but the chain ran.",
		"ddd 5 all done",
		"", "d", "ddd", "zzz 3 x", "ddd 99 x", "dfx 3",
	} {
		f.Add(s)
	}
	chain := DefaultChain()
	f.Fuzz(func(t *testing.T, s string) {
		links, _, _, _, err := ParseCompact(chain, s)
		if err != nil {
			return
		}
		require.Len(t, links, len(chain.LinkKeys()), "a parsed compact form covers every link")
	})
}
