package cli

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// person_reconcile_test.go covers the --json projection and the --all flag of
// `lucid person reconcile`: held, overlaps, and dormant_shares appear only when
// there is something to list, the per-candidate dormant marks and dormant_share
// only under --all. Synthetic people only: two people named "Sam" ("Sam" and
// "Sam Rivera"), plus "Jo" and "Jo Lark".

// personReconcileJSON runs `lucid person reconcile [args] --json`, decodes the view,
// and returns its sorted top-level keys and the raw output.
func personReconcileJSON(t *testing.T, args ...string) (personReconcileView, []string, string) {
	t.Helper()
	out := runOK(t, append(append([]string{"person", "reconcile"}, args...), "--json")...)
	var view personReconcileView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	keys := topLevelKeys(t, out)
	slices.Sort(keys)
	return view, keys, out
}

// TestPersonReconcile_CLIJSONKeysAndAll: held mentions project under held,
// a dormant look-alike under dormant_shares (attached to its pair as
// dormant_share under --all, with the pair's dormant mark), and a standalone
// first-name overlap under overlaps — each key left out when empty, and no
// dormant key on a held candidate who is active.
func TestPersonReconcile_CLIJSONKeysAndAll(t *testing.T) {
	home, sam, river, raws := heldSamCLIHome(t)
	pair := []string{sam, river}
	slices.Sort(pair)

	before := identityTreeHash(t, home)
	view, keys, out := personReconcileJSON(t)
	assert.Equal(t, before, identityTreeHash(t, home), "reconcile --json writes nothing")
	assert.Equal(t, []string{"candidates", "held"}, keys)
	require.Len(t, view.Held, 2)
	for i, h := range view.Held {
		assert.Equal(t, raws[i+1], h.RawID)
		assert.Equal(t, "Sam", h.Form)
		assert.Equal(t, "exact", h.Reason)
		require.Len(t, h.Candidates, 2)
		got := []string{h.Candidates[0].PersonKey, h.Candidates[1].PersonKey}
		assert.Equal(t, pair, got)
	}
	assert.NotContains(t, out, `"dormant`, "no dormant key while no one is dormant")
	text := runOK(t, "person", "reconcile")
	assert.Contains(t, text, "\nHeld mentions — counted for no one until you settle each:\n- "+raws[1]+`: "Sam" could mean `)

	// Making Sam Rivera dormant settles both held mentions on Sam and leaves a
	// dormant look-alike: the two share the written form "sam".
	runOK(t, "person", "dormant", river)
	view, keys, _ = personReconcileJSON(t)
	assert.Equal(t, []string{"candidates", "dormant_shares"}, keys)
	assert.Empty(t, view.Candidates, "the dormant person leaves the merge suggestions")
	assert.Equal(t, []reconcileDormantShareView{{
		Form: "sam", ActiveKey: sam, ActiveName: "Sam", DormantKey: river, DormantName: "Sam Rivera",
		SuggestedRestore: "lucid person dormant " + river + " --restore",
		SuggestedMerge:   "lucid person merge " + sam + " " + river,
	}}, view.DormantShares)

	view, keys, out = personReconcileJSON(t, "--all")
	assert.Equal(t, []string{"candidates"}, keys)
	require.Len(t, view.Candidates, 1)
	c := view.Candidates[0]
	assert.NotEqual(t, c.SourceDormant, c.TargetDormant, "exactly one of the pair is dormant")
	require.NotNil(t, c.DormantShare)
	assert.Equal(t, river, c.DormantShare.DormantKey)
	assert.Nil(t, c.Overlap)
	assert.NotContains(t, out, `"overlap"`)
	text = runOK(t, "person", "reconcile", "--all")
	assert.Contains(t, text, `"Sam Rivera" (`+river+`, dormant)`)
	assert.Contains(t, text, `  "Sam Rivera" (`+river+`) is dormant — restore them before merging:`)

	// A first-name overlap with no merge pair projects under overlaps.
	runOK(t, "person", "create", "Jo")
	runOK(t, "person", "create", "Jo Lark")
	jo := identityCLIKey(t, "Jo")
	lark := identityCLIKey(t, "Jo Lark")
	view, keys, _ = personReconcileJSON(t)
	assert.Equal(t, []string{"candidates", "dormant_shares", "overlaps"}, keys)
	require.Len(t, view.Overlaps, 1)
	o := view.Overlaps[0]
	assert.Equal(t, "Jo", o.Form)
	assert.Equal(t, jo, o.HolderKey)
	assert.Equal(t, "Jo", o.HolderName)
	assert.Equal(t, "2026-11-17", o.HolderFirstSeenAt)
	assert.Equal(t, lark, o.SharerKey)
	assert.Equal(t, "Jo Lark", o.SharerName)
	assert.Equal(t, `lucid person claim `+lark+` "Jo"`, o.SuggestedClaim)
	assert.Equal(t, "lucid person dormant "+jo, o.SuggestedDormant)
	text = runOK(t, "person", "reconcile")
	assert.Contains(t, text, "\nFirst-name overlaps — a bare name goes to one person while another shares that first name:\n")
}
