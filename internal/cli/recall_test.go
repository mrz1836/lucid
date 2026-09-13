package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// seedRegistry runs a registry-write verb with --json and returns the resolved
// (salted) key — the browse tests need the real key the store minted. Only
// `era create` mints a chapter now (the bare `era <name>` form is an amend-only
// alias), so an era seed is routed through the create subcommand
// (life-archive.md §4); every other kind keeps its create-or-amend bare form.
func seedRegistry(t *testing.T, args ...string) string {
	t.Helper()
	if len(args) > 0 && args[0] == "era" {
		args = append([]string{"era", "create"}, args[1:]...)
	}
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
	assert.Contains(t, out, "--pet")
	assert.Contains(t, out, "name or key", "the flag help states each reference flag takes a name or key")
}

// TestRecall_EmptyIndex prints the calm fallback when nothing is archived yet.
func TestRecall_EmptyIndex(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall")
	require.NoError(t, err)
	assert.Contains(t, out, "Nothing archived")
}

// TestRecall_IndexListsReferents seeds an era, an injury, and a pet and proves
// the bare index lists all three, Discord-friendly (no markdown table).
func TestRecall_IndexListsReferents(t *testing.T) {
	isolatedHome(t)

	seedRegistry(t, "era", "wild summer", "--start", "2010-06-01")
	seedRegistry(t, "injury", "left knee")
	seedRegistry(t, "pet", "Fixture Pet", "--species", "dog")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall")
	require.NoError(t, err)
	assert.Contains(t, out, "Archive index")
	assert.Contains(t, out, "wild summer")
	assert.Contains(t, out, "left knee")
	assert.Contains(t, out, "Fixture Pet")
	assert.Contains(t, out, "Cites:", "every index entry is cited")
	assert.NotContains(t, out, "|", "no markdown table in Discord output")
}

// TestRecall_ByPetJSON seeds a pet and proves --pet resolves its first-class
// fields through the stable read-only JSON projection.
func TestRecall_ByPetJSON(t *testing.T) {
	isolatedHome(t)

	petKey := seedRegistry(t, "pet", "Fixture Pet", "--species", "dog", "--note", "synthetic companion")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--pet", petKey, "--json")
	require.NoError(t, err)

	var view recallView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.True(t, view.Found)
	assert.Equal(t, router.RecallPet, view.Dimension)
	require.NotNil(t, view.Referent)
	assert.Equal(t, "Fixture Pet", view.Referent.DisplayName)
	assert.Equal(t, "active", view.Referent.Status)
	assert.Equal(t, []recallFieldView{
		{Label: "Species", Value: "dog"},
		{Label: "Note", Value: "synthetic companion"},
	}, view.Referent.Fields)
	assert.Empty(t, view.Items)
}

// TestRecall_ByPetWithLifeSpan proves a browsed pet surfaces its backdate-aware
// life-span in the documented field order (species → start → end → note), so a
// past companion reads back with the dates it was recorded under.
func TestRecall_ByPetWithLifeSpan(t *testing.T) {
	isolatedHome(t)

	petKey := seedRegistry(t, "pet", "Old Sable",
		"--species", "dog", "--start", "2005", "--end", "2012-06", "--status", "passed")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--pet", petKey, "--json")
	require.NoError(t, err)

	var view recallView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.NotNil(t, view.Referent)
	assert.Equal(t, "passed", view.Referent.Status)
	assert.Equal(t, []recallFieldView{
		{Label: "Species", Value: "dog"},
		{Label: "Start", Value: "2005"},
		{Label: "End", Value: "2012-06"},
	}, view.Referent.Fields, "life-span renders in the documented order, precision fields stay hidden")
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

// TestRecall_MissingReferent prints the guided not-found line for a key that does
// not resolve — no longer the bare dead-end, but a message that names the accepted
// input forms and points at the discovery command.
func TestRecall_MissingReferent(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", "era_nope")
	require.NoError(t, err)
	assert.NotContains(t, out, "No era found", "the bare not-found copy is replaced")
	assert.Contains(t, out, "by name or key", "the guided not-found names the accepted input forms")
	assert.Contains(t, out, "lucid era list", "the guided not-found points at the discovery command")
}

// TestRecall_ByEraName proves a browse by the human name resolves the record and
// renders byte-identically to browsing it by its opaque key — the reported bug
// (a name dead-ending) is fixed, and name-input agrees with key-input (AC-1).
func TestRecall_ByEraName(t *testing.T) {
	isolatedHome(t)

	eraKey := seedRegistry(t, "era", "wild summer", "--start", "2010-06-01")

	byName, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", "wild summer", "--json")
	require.NoError(t, err)
	byKey, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", eraKey, "--json")
	require.NoError(t, err)

	assert.Equal(t, byKey, byName, "browsing by name renders identically to browsing by key")

	var view recallView
	require.NoError(t, json.Unmarshal([]byte(byName), &view))
	assert.True(t, view.Found)
	require.NotNil(t, view.Referent)
	assert.Equal(t, eraKey, view.Referent.Key, "the name resolves to the record's opaque key")
	assert.Equal(t, "wild summer", view.Referent.DisplayName)
}

// TestRecall_AmbiguousNamePrompt proves a name matching more than one referent
// lists the candidates with their keys and browses none, mirroring the
// `lucid person <name>` read path — a read outcome (exit 0), not an error (AC-5).
// The only way two eras can collide is an overlapping rename-history aka[], so the
// fixture routes each stable key through a shared historic name via the storage
// UpdateRegistry seam (the public write verbs derive the key from the name and so
// cannot rename to a new display name at a stable key).
func TestRecall_AmbiguousNamePrompt(t *testing.T) {
	home := isolatedHome(t)

	keyA := seedRegistry(t, "era", "summer of 2009")
	keyB := seedRegistry(t, "era", "summer of 2011")

	a := storage.New(home)
	for _, step := range []struct{ key, name string }{
		{keyA, "wild summer"},
		{keyA, "summer of 2009"},
		{keyB, "wild summer"},
		{keyB, "summer of 2011"},
	} {
		_, err := a.UpdateRegistry(observations.RegistryEra, step.key,
			observations.RegistryPatch{DisplayName: step.name, At: "2026-01-02T15:04:05Z"})
		require.NoError(t, err)
	}

	human, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", "wild summer")
	require.NoError(t, err, "an ambiguous name is a read outcome, not an error")
	assert.Contains(t, human, "matches more than one")
	assert.Contains(t, human, keyA, "the prompt lists the first candidate's key")
	assert.Contains(t, human, keyB, "the prompt lists the second candidate's key")

	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", "wild summer", "--json")
	require.NoError(t, err)
	var view recallView
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &view))
	assert.True(t, view.Ambiguous, "an ambiguous browse flags ambiguous")
	assert.False(t, view.Found, "an ambiguous browse resolves nothing")
	require.Len(t, view.Candidates, 2)
	gotKeys := map[string]bool{view.Candidates[0].Key: true, view.Candidates[1].Key: true}
	assert.True(t, gotKeys[keyA] && gotKeys[keyB], "both matches are listed as candidates")
}

// TestRecall_KeyJSONShapeStable proves the additive ambiguity projection does not
// alter an ordinary key browse: the raw --json object omits `ambiguous` and
// `candidates`, so existing key-based callers see a byte-stable shape (AC-3).
func TestRecall_KeyJSONShapeStable(t *testing.T) {
	isolatedHome(t)

	eraKey := seedRegistry(t, "era", "wild summer", "--start", "2010-06-01")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", eraKey, "--json")
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	_, hasAmbiguous := raw["ambiguous"]
	_, hasCandidates := raw["candidates"]
	assert.False(t, hasAmbiguous, "a key browse omits the ambiguity flag")
	assert.False(t, hasCandidates, "a key browse omits the candidates list")
}

// TestRecall_NotFoundGuides proves a name (or key) that matches nothing fails with
// a guided message — naming the accepted input forms and pointing at the discovery
// command — never the bare dead-end, and stays a read outcome (exit 0) (AC-6).
func TestRecall_NotFoundGuides(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall", "--era", "college years")
	require.NoError(t, err, "a not-found browse is a read outcome, not an error")
	assert.NotContains(t, out, "No era found", "the bare dead-end copy is gone")
	assert.Contains(t, out, "name or key", "the guidance names the accepted input forms")
	assert.Contains(t, out, "lucid era list", "the guidance points at the era discovery command")
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

// TestRecallDimension covers the flag→(dimension,key) mapping directly: the set
// flag wins and its value is trimmed, and no flag is the bare index.
func TestRecallDimension(t *testing.T) {
	cases := []struct {
		name                  string
		era, thread, inj, pet string
		wantDim, wantKey      string
	}{
		{"era", " sobriety ", "", "", "", router.RecallEra, "sobriety"},
		{"thread", "", " writing ", "", "", router.RecallThread, "writing"},
		{"injury", "", "", " knee ", "", router.RecallInjury, "knee"},
		{"pet", "", "", "", " fixture-pet ", router.RecallPet, "fixture-pet"},
		{"none", "", "", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dim, key := recallDimension(tc.era, tc.thread, tc.inj, tc.pet)
			assert.Equal(t, tc.wantDim, dim)
			assert.Equal(t, tc.wantKey, key)
		})
	}
}

// TestRecall_BootError proves the read surface surfaces a boot failure rather
// than answering over a half-open Ledger.
func TestRecall_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "recall")
	require.Error(t, err)
}
