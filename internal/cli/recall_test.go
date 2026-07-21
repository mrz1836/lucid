package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedRegistry runs a registry-write verb with --json and returns the resolved
// (salted) key — the browse tests need the real key the store minted.
func seedRegistry(t *testing.T, args ...string) string {
	t.Helper()
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, append(args, "--json")...)
	require.NoError(t, err)
	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.NotEmpty(t, view.Key)
	return view.Key
}

// TestRecall_Registered confirms the read-only browse verb is on the spine and
// self-documents its dimension flags in --help.
func TestRecall_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["recall"], "recall verb not registered")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "recall")
	assert.Contains(t, out, "Read-only", "the help names it read-only")
	assert.Contains(t, out, "--era")
	assert.Contains(t, out, "--thread")
	assert.Contains(t, out, "--injury")
}

// TestRecall_EmptyIndex prints the calm fallback when nothing is archived yet.
func TestRecall_EmptyIndex(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall")
	require.NoError(t, err)
	assert.Contains(t, out, "Nothing archived")
}

// TestRecall_IndexListsReferents seeds an era and an injury and proves the bare
// index lists both, Discord-friendly (no markdown table).
func TestRecall_IndexListsReferents(t *testing.T) {
	isolatedHome(t)

	seedRegistry(t, "era", "wild summer", "--start", "2010-06-01")
	seedRegistry(t, "injury", "left knee")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall")
	require.NoError(t, err)
	assert.Contains(t, out, "Archive index")
	assert.Contains(t, out, "wild summer")
	assert.Contains(t, out, "left knee")
	assert.Contains(t, out, "Cites:", "every index entry is cited")
	assert.NotContains(t, out, "|", "no markdown table in Discord output")
}

// TestRecall_ByEraJSON seeds an era + a linked story and proves the --era browse
// returns the referent and the cited story in the stable --json shape (AC-10,
// AC-11).
func TestRecall_ByEraJSON(t *testing.T) {
	enableMemoryHome(t)

	eraKey := seedRegistry(t, "era", "wild summer", "--start", "2010-06-01")

	memOut, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"memory", "a night we drove to the coast", "--era", eraKey, "--certainty", "vivid", "--json")
	require.NoError(t, err)
	var mem memoryWriteView
	require.NoError(t, json.Unmarshal([]byte(memOut), &mem))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", eraKey, "--json")
	require.NoError(t, err)

	var view recallView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.True(t, view.Found)
	assert.Equal(t, "era", view.Dimension)
	require.NotNil(t, view.Referent)
	assert.Equal(t, "wild summer", view.Referent.DisplayName)
	assert.Equal(t, "registry", view.Referent.Source)

	require.Len(t, view.Items, 1)
	story := view.Items[0]
	assert.Equal(t, "story", story.Kind)
	assert.Equal(t, "a night we drove to the coast", story.Title)
	assert.Equal(t, "excavation", story.Source)
	assert.Contains(t, story.SupportingEntryIDs, mem.EventID, "the story cites its own observation id")
}

// TestRecall_ByInjuryText seeds an injury with convention fields and proves the
// --injury browse renders them as bullets with a citation, Discord-friendly.
func TestRecall_ByInjuryText(t *testing.T) {
	isolatedHome(t)

	injKey := seedRegistry(t, "injury", "left knee",
		"--body-area", "left knee, medial", "--severity", "moderate", "--current-limitations", "no deep squats")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--injury", injKey)
	require.NoError(t, err)
	assert.Contains(t, out, "left knee")
	assert.Contains(t, out, "Body area: left knee, medial")
	assert.Contains(t, out, "Severity: moderate")
	assert.Contains(t, out, "Cites:")
	assert.Contains(t, out, "(registry)", "an injury referent is cited by its registry provenance")
	assert.NotContains(t, out, "|", "no markdown table in Discord output")
}

// TestRecall_MissingReferent prints an honest not-found line for a key that does
// not resolve.
func TestRecall_MissingReferent(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", "era_nope")
	require.NoError(t, err)
	assert.Contains(t, out, "No era found")
}

// TestRecall_DimensionsMutuallyExclusive proves two dimension flags at once is a
// usage error, not a silent pick.
func TestRecall_DimensionsMutuallyExclusive(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", "e", "--injury", "i")
	require.Error(t, err, "two dimension flags at once is rejected, not silently picked")
	assert.Contains(t, err.Error(), "none of the others can be")
}

// TestRecall_ReadOnly proves the surface writes nothing: the Ledger file count
// is identical before and after a keyed browse and an index over a seeded store.
func TestRecall_ReadOnly(t *testing.T) {
	home := enableMemoryHome(t)

	eraKey := seedRegistry(t, "era", "wild summer")
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "memory", "a story", "--era", eraKey)
	require.NoError(t, err)

	before := countHomeFiles(t, home, "")
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", eraKey)
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "recall")
	require.NoError(t, err)
	assert.Equal(t, before, countHomeFiles(t, home, ""), "recall writes nothing")
}
