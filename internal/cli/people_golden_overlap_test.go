package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goldenOverlapAddedText is the documented first-name overlap for Ledger B: a
// bare "Sam" goes to the old "Sam" while "Sam Rivera" also has the first name
// Sam. The two are already a merge pair, so it attaches to that pair.
const goldenOverlapAddedText = `  Or keep them apart: a bare "Sam" goes to "Sam" (person_g-rye, first seen 2026-06-01), and "Sam Rivera" (person_c-carp) also has the first name "Sam".
    → lucid person claim person_c-carp "Sam"
    → lucid person dormant person_g-rye`

// goldenOverlapLine reports whether a reconcile line belongs to a first-name
// overlap — the only lines an overlap Ledger may add to what reconcile printed
// before dormant status and name claims.
func goldenOverlapLine(line string) bool {
	return strings.Contains(line, "also has the first name") ||
		strings.HasPrefix(line, "    → lucid person claim ") ||
		strings.HasPrefix(line, "    → lucid person dormant ") ||
		strings.HasPrefix(line, "First-name overlaps")
}

// goldenCanonicalJSON decodes a reconcile --json payload, drops every key the
// identity findings add — held, overlaps, dormant_shares, and each candidate's
// overlap / dormant_share — and re-marshals it with sorted keys, so two
// payloads compare on everything reconcile emitted before.
func goldenCanonicalJSON(t *testing.T, payload string) string {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(payload), &doc))
	for _, k := range []string{"overlaps", "held", "dormant_shares"} {
		delete(doc, k)
	}
	if cands, ok := doc["candidates"].([]any); ok {
		for _, c := range cands {
			if m, isMap := c.(map[string]any); isMap {
				delete(m, "overlap")
				delete(m, "dormant_share")
			}
		}
	}
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return string(out)
}

// TestPeopleGolden_OverlapReconcileOnlyAddsOverlapLines rebuilds the overlap
// Ledger ("Sam", seen first and mentioned more, and "Sam Rivera", both
// current) and proves `lucid person reconcile` differs from what it printed
// before dormant status and name claims only by the first-name overlap: with
// the overlap lines dropped the human output is the pre-change golden byte for
// byte, the dropped lines are exactly the documented overlap, and with the
// identity keys deleted the --json payload equals the pre-change golden.
func TestPeopleGolden_OverlapReconcileOnlyAddsOverlapLines(t *testing.T) {
	home := goldenBuildOverlapLedger(t)
	files := goldenTree(t, home)

	text, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "reconcile")
	require.NoErrorf(t, err, "reconcile: %s", stderr)
	assert.Empty(t, stderr)
	goldenAssertReadOnly(t, home, files, "reconcile/text")

	var kept, dropped []string
	for _, line := range strings.SplitAfter(text, "\n") {
		if goldenOverlapLine(line) {
			dropped = append(dropped, line)
			continue
		}
		kept = append(kept, line)
	}
	assert.Equal(t, goldenOverlapReconcileText, strings.Join(kept, ""),
		"without the overlap lines reconcile prints exactly what it did before")
	assert.Equal(t, goldenOverlapAddedText+"\n", strings.Join(dropped, ""),
		"the only added lines are the documented overlap")

	payload, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "reconcile", "--json")
	require.NoErrorf(t, err, "reconcile --json: %s", stderr)
	assert.Empty(t, stderr)
	goldenAssertReadOnly(t, home, files, "reconcile/json")
	assert.Contains(t, payload, `"overlap": {`, "the overlap attaches to the pair")
	assert.Equal(t, goldenCanonicalJSON(t, goldenOverlapReconcileJSON), goldenCanonicalJSON(t, payload),
		"without the identity keys the --json payload is what it was before")
}
