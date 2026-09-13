package router

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// labelIndex returns the position of a field label in a referent's ordered
// Fields, or -1 when absent — a small helper for the canonical-order assertions.
func labelIndex(fields []RecallField, label string) int {
	return slices.IndexFunc(fields, func(f RecallField) bool { return f.Label == label })
}

// TestRecall_ByEraCitesStory proves a browse by era resolves the era referent
// and surfaces the memory story filed under it, each cited by its observation id
// and sourced as excavation (AC-10, AC-11).
func TestRecall_ByEraCitesStory(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Start: "2010-06-01", Now: fixedNow()})
	require.NoError(t, err)
	mem, err := r.WriteMemory(MemoryWriteRequest{
		Text: "a night we drove to the coast", Era: era.Key,
		Certainty: "vivid", Tone: "electric", Now: fixedNow(),
	})
	require.NoError(t, err)

	res, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: era.Key, Now: fixedNow()})
	require.NoError(t, err)
	assert.True(t, res.Found)
	require.NotNil(t, res.Referent)
	assert.Equal(t, "wild summer", res.Referent.DisplayName)
	assert.Equal(t, recallSourceRegistry, res.Referent.Source)

	require.Len(t, res.Items, 1)
	story := res.Items[0]
	assert.Equal(t, "story", story.Kind)
	assert.Equal(t, mem.EventID, story.Key)
	assert.Equal(t, "a night we drove to the coast", story.Title)
	assert.Equal(t, observations.SourceExcavation, story.Source)
	assert.Contains(t, story.SupportingEntryIDs, mem.EventID, "a story cites its own observation id")
	assert.Contains(t, story.Detail, "vivid")

	// The referent is cited by the story filed under it.
	assert.Contains(t, res.Referent.SupportingEntryIDs, mem.EventID)
}

// TestRecall_ByEraCitesAttachedPhoto proves a story with an attached photo cites
// the photo's raw id alongside its own observation id (AC-11).
func TestRecall_ByEraCitesAttachedPhoto(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Now: fixedNow()})
	require.NoError(t, err)
	photo := writeTempFile(t, "coast.jpg", []byte("not-a-real-image"))
	att, err := r.Attach(AttachRequest{Path: photo, Now: fixedNow()})
	require.NoError(t, err)
	mem, err := r.WriteMemory(MemoryWriteRequest{
		Text: "the coast photo", Era: era.Key, EntryRef: att.RawID, Now: fixedNow(),
	})
	require.NoError(t, err)

	res, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: era.Key, Now: fixedNow()})
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	assert.Contains(t, res.Items[0].SupportingEntryIDs, mem.EventID)
	assert.Contains(t, res.Items[0].SupportingEntryIDs, att.RawID, "an attached photo is cited by its raw id")
}

// TestRecall_ByInjuryRendersConventionFields proves a browse by injury resolves
// the injury referent and renders its convention Fields in canonical order,
// diagnosis-free registry data (AC-10). An injury carries no linked stories in
// v1, so Items is empty but the referent is fully surfaced.
func TestRecall_ByInjuryRendersConventionFields(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	inj, err := r.WriteInjury(InjuryWriteRequest{
		Name: "left knee", Onset: "2014-09", BodyArea: "left knee, medial",
		Severity: "moderate", CurrentLimitations: "no deep squats", Now: fixedNow(),
	})
	require.NoError(t, err)

	res, err := r.Recall(RecallRequest{Dimension: RecallInjury, Key: inj.Key})
	require.NoError(t, err)
	assert.True(t, res.Found)
	require.NotNil(t, res.Referent)
	assert.Equal(t, "left knee", res.Referent.DisplayName)
	assert.Equal(t, observations.StatusActive, res.Referent.Status)
	assert.Equal(t, recallSourceRegistry, res.Referent.Source)
	assert.Empty(t, res.Items, "an injury has no linked stories in v1")

	onset := labelIndex(res.Referent.Fields, "Onset")
	body := labelIndex(res.Referent.Fields, "Body area")
	severity := labelIndex(res.Referent.Fields, "Severity")
	require.NotEqual(t, -1, onset)
	require.NotEqual(t, -1, body)
	require.NotEqual(t, -1, severity)
	assert.Less(t, onset, body, "onset renders before body_area (canonical order)")
	assert.Less(t, body, severity, "body_area renders before severity (canonical order)")
	assert.Equal(t, "left knee, medial", res.Referent.Fields[body].Value)
}

// TestRecall_ByThreadHonestEmpty proves a thread with no linked stories still
// resolves its referent and surfaces an honest empty item list — the browse
// degrades cleanly on a thin dimension.
func TestRecall_ByThreadHonestEmpty(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	th, err := r.WriteThread(ThreadWriteRequest{Name: "learning piano", Intent: "play for fun", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.Recall(RecallRequest{Dimension: RecallThread, Key: th.Key})
	require.NoError(t, err)
	assert.True(t, res.Found)
	require.NotNil(t, res.Referent)
	assert.Empty(t, res.Items)
	assert.Empty(t, res.Referent.SupportingEntryIDs, "no stories ⇒ the referent is cited by its own key")
	assert.Equal(t, "play for fun", res.Referent.Fields[labelIndex(res.Referent.Fields, "Intent")].Value)
}

// TestRecall_IndexListsEveryReferent proves the bare index returns one entry per
// era, thread, injury, and pet, each sourced "registry" (AC-10).
func TestRecall_IndexListsEveryReferent(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	_, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Start: "2010-06-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.WriteThread(ThreadWriteRequest{Name: "learning piano", Intent: "play", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.WriteInjury(InjuryWriteRequest{Name: "left knee", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.WritePet(PetWriteRequest{Name: "Fixture Pet", Species: "dog", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.Recall(RecallRequest{})
	require.NoError(t, err)
	assert.True(t, res.Found)
	assert.Nil(t, res.Referent, "the bare index has no single referent")

	byKind := map[string]RecallItem{}
	for _, it := range res.Items {
		byKind[it.Kind] = it
		assert.Equal(t, recallSourceRegistry, it.Source, "every index entry is sourced registry")
	}
	require.Contains(t, byKind, RecallEra)
	require.Contains(t, byKind, RecallThread)
	require.Contains(t, byKind, RecallInjury)
	require.Contains(t, byKind, RecallPet)
	assert.Equal(t, "wild summer", byKind[RecallEra].Title)
	assert.Contains(t, byKind[RecallEra].Detail, "2010-06-01", "an era index entry shows its range")
	assert.Equal(t, "Fixture Pet", byKind[RecallPet].Title)
	assert.Equal(t, observations.StatusActive, byKind[RecallPet].Detail)
}

// TestRecall_EmptyStoreHonestIndex proves an empty store returns an empty,
// honest index — Found false, no items.
func TestRecall_EmptyStoreHonestIndex(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	res, err := r.Recall(RecallRequest{})
	require.NoError(t, err)
	assert.False(t, res.Found)
	assert.Empty(t, res.Items)
}

// TestRecall_MissingReferentHonestEmpty proves a browse of a key that does not
// resolve is an honest empty result, never a fabricated one.
func TestRecall_MissingReferentHonestEmpty(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	res, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: "era_does-not-exist"})
	require.NoError(t, err)
	assert.False(t, res.Found)
	assert.Nil(t, res.Referent)
	assert.Empty(t, res.Items)
}

// TestRecall_BadRequestErrors proves an unknown dimension and a keyless
// dimension are usage errors, not silent empties.
func TestRecall_BadRequestErrors(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	_, err := r.Recall(RecallRequest{Dimension: "person", Key: "x"})
	require.Error(t, err)

	_, err = r.Recall(RecallRequest{Dimension: RecallEra})
	require.Error(t, err, "a dimension browse needs a key")
}

// TestRecall_ReflectsAmendedValues proves a browse folds amendments — recall
// shows the corrected story text and the amended certainty, not the pre-amend
// values, and surfaces one story rather than the base plus its amendment
// (AC-11, fold-on-read everywhere).
func TestRecall_ReflectsAmendedValues(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Start: "2010-06-01", Now: fixedNow()})
	require.NoError(t, err)
	mem, err := r.WriteMemory(MemoryWriteRequest{
		Text: "old text", Era: era.Key, Certainty: "hazy", Now: fixedNow(),
	})
	require.NoError(t, err)

	_, err = r.AmendMemory(AmendMemoryRequest{
		ObsID: mem.EventID, Body: "corrected text", BodyChanged: true,
		Certainty: "vivid", CertaintyChanged: true, Now: fixedNow(),
	})
	require.NoError(t, err)

	res, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: era.Key, Now: fixedNow()})
	require.NoError(t, err)
	require.Len(t, res.Items, 1, "the amendment folds onto the base — one story, not two")
	assert.Equal(t, mem.EventID, res.Items[0].Key, "the folded story keeps the stable base id")
	assert.Equal(t, "corrected text", res.Items[0].Title, "recall shows the amended text")
	assert.Contains(t, res.Items[0].Detail, "vivid", "recall shows the amended certainty")
	assert.NotContains(t, res.Items[0].Detail, "hazy", "the pre-amend certainty does not surface")
}

// TestRecall_EraRefileSurfacesUnderLaterMintedEra proves the headline use case
// end-to-end through recall: a story re-filed under a chapter minted *after* it
// was created surfaces only once the amend files it there (AC-11, AC-12).
func TestRecall_EraRefileSurfacesUnderLaterMintedEra(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	mem, err := r.WriteMemory(MemoryWriteRequest{Text: "sometime that autumn", Day: "2014-09-01", Now: fixedNow()})
	require.NoError(t, err)

	// The chapter is minted after the memory already exists.
	era, err := r.WriteEra(EraWriteRequest{Name: "the Lisbon years", Start: "2014", Now: fixedNow()})
	require.NoError(t, err)

	before, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: era.Key, Now: fixedNow()})
	require.NoError(t, err)
	assert.Empty(t, before.Items, "the story is not yet filed under the later-minted era")

	_, err = r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Era: era.Key, EraChanged: true, Now: fixedNow()})
	require.NoError(t, err)

	after, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: era.Key, Now: fixedNow()})
	require.NoError(t, err)
	require.Len(t, after.Items, 1, "the re-filed story now surfaces under the later-minted era")
	assert.Equal(t, mem.EventID, after.Items[0].Key)
}

// TestEraSpan covers the shared chapter-span helper directly (life-archive.md
// §4): both bounds render with the arrow separator, an open era reads "ongoing
// since", an end-only era reads "until", and a dateless era yields "".
func TestEraSpan(t *testing.T) {
	for _, tc := range []struct {
		name, start, end, want string
	}{
		{"both bounds", "2008-09", "2010-01", "2008-09 → 2010-01"},
		{"start only is ongoing", "2008-09", "", "ongoing since 2008-09"},
		{"end only is until", "", "2010-01", "until 2010-01"},
		{"no dates yields empty", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, eraSpan(tc.start, tc.end))
		})
	}
}

// TestEraRange_DelegatesToEraSpan proves the bare-index range now renders the
// arrow span through the shared helper (was a spaced en-dash), so the index and
// the write ack cannot drift.
func TestEraRange_DelegatesToEraSpan(t *testing.T) {
	assert.Equal(t, "2008-09 → 2010-01", eraRange(map[string]any{"start": "2008-09", "end": "2010-01"}))
	assert.Equal(t, "ongoing since 2004", eraRange(map[string]any{"start": "2004"}))
	assert.Equal(t, "until 2010-01", eraRange(map[string]any{"end": "2010-01"}))
	assert.Empty(t, eraRange(map[string]any{}))
	assert.NotContains(t, eraRange(map[string]any{"start": "2008-09", "end": "2010-01"}), " – ",
		"the spaced en-dash form is gone")
}

// renameRegistry patches a registry record's display name in place through the
// UpdateRegistry seam, folding the prior name into aka[]. The public write verbs
// derive the key from the name, so they cannot rename a record to a new display
// name at a stable key — this seam is the only way to construct the
// rename-history aka[] fixtures the resolver's aka/ambiguity paths need.
func renameRegistry(t *testing.T, a *storage.Adapter, kind, key, newName string) {
	t.Helper()
	_, err := a.UpdateRegistry(kind, key, observations.RegistryPatch{
		DisplayName: newName,
		At:          fixedNow().Format(time.RFC3339),
	})
	require.NoError(t, err)
}

// TestRecall_ResolvesByName proves a browse by a human name resolves the record
// and renders identically to browsing it by its opaque key — the reported bug
// (name input dead-ending) is fixed, and name-input and key-input agree
// byte-for-byte (AC-1).
func TestRecall_ResolvesByName(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	era, err := r.WriteEra(EraWriteRequest{Name: "Wild Summer", Start: "2010-06-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.WriteMemory(MemoryWriteRequest{Text: "a night we drove to the coast", Era: era.Key, Now: fixedNow()})
	require.NoError(t, err)

	byName, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: "Wild Summer", Now: fixedNow()})
	require.NoError(t, err)
	byKey, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: era.Key, Now: fixedNow()})
	require.NoError(t, err)

	assert.True(t, byName.Found)
	require.NotNil(t, byName.Referent)
	assert.Equal(t, era.Key, byName.Referent.Key, "name-input resolves to the record's opaque key")
	assert.Equal(t, byKey.Key, byName.Key, "the resolved Key matches the key-based browse")
	assert.Equal(t, byKey.Referent, byName.Referent, "same referent as the key browse")
	assert.Equal(t, byKey.Items, byName.Items, "same stories as the key browse")
	assert.False(t, byName.Ambiguous)
}

// TestRecall_ResolvesCaseInsensitive proves name matching folds case: every
// spelling of the same name resolves to the same record (AC-2).
func TestRecall_ResolvesCaseInsensitive(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	era, err := r.WriteEra(EraWriteRequest{Name: "Wild Summer", Now: fixedNow()})
	require.NoError(t, err)

	for _, q := range []string{"wild summer", "WILD SUMMER", "Wild Summer"} {
		res, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: q, Now: fixedNow()})
		require.NoError(t, err, "query %q", q)
		require.True(t, res.Found, "query %q resolves", q)
		require.NotNil(t, res.Referent)
		assert.Equal(t, era.Key, res.Referent.Key, "query %q resolves to the same record", q)
	}
}

// TestRecall_ResolvesByAka proves a renamed record still resolves by an old
// spelling kept in aka[] — mirroring person alias lookup — while the current
// display name is what surfaces (AC-2).
func TestRecall_ResolvesByAka(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)
	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Now: fixedNow()})
	require.NoError(t, err)
	renameRegistry(t, a, observations.RegistryEra, era.Key, "high summer")

	res, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: "wild summer", Now: fixedNow()})
	require.NoError(t, err)
	require.True(t, res.Found, "an old spelling resolves via aka[]")
	require.NotNil(t, res.Referent)
	assert.Equal(t, era.Key, res.Referent.Key)
	assert.Equal(t, "high summer", res.Referent.DisplayName, "the current display name is surfaced")
	assert.False(t, res.Ambiguous)
}

// TestRecall_ResolvesByKeyStillWorks proves the opaque-key form resolves exactly
// as before — no regression for key-based callers (AC-3).
func TestRecall_ResolvesByKeyStillWorks(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Start: "2010-06-01", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: era.Key, Now: fixedNow()})
	require.NoError(t, err)
	require.True(t, res.Found)
	require.NotNil(t, res.Referent)
	assert.Equal(t, era.Key, res.Key, "the opaque key resolves to itself")
	assert.Equal(t, era.Key, res.Referent.Key)
	assert.False(t, res.Ambiguous)
}

// TestRecall_UnknownNameHonestEmpty proves an unknown name is an honest empty
// result — not found, no referent, no items, and not flagged ambiguous (AC-1,
// AC-7).
func TestRecall_UnknownNameHonestEmpty(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	_, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: "college years", Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, res.Found)
	assert.Nil(t, res.Referent)
	assert.Empty(t, res.Items)
	assert.False(t, res.Ambiguous)
	assert.Empty(t, res.Candidates)
}

// TestRecall_AmbiguousName proves a name matching more than one record (via an
// overlapping rename-history aka[]) browses nothing and lists the matches,
// key-sorted, mirroring the person read path (AC-5).
func TestRecall_AmbiguousName(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)
	eraA, err := r.WriteEra(EraWriteRequest{Name: "summer of 2009", Now: fixedNow()})
	require.NoError(t, err)
	eraB, err := r.WriteEra(EraWriteRequest{Name: "summer of 2011", Now: fixedNow()})
	require.NoError(t, err)

	// Route each stable key through a shared historic name and back to its
	// distinct current name, so both records retain "wild summer" in aka[] — the
	// only way two eras can collide (a rename-history overlap; same-name eras
	// share a derived key and so cannot coexist).
	renameRegistry(t, a, observations.RegistryEra, eraA.Key, "wild summer")
	renameRegistry(t, a, observations.RegistryEra, eraA.Key, "summer of 2009")
	renameRegistry(t, a, observations.RegistryEra, eraB.Key, "wild summer")
	renameRegistry(t, a, observations.RegistryEra, eraB.Key, "summer of 2011")

	res, err := r.Recall(RecallRequest{Dimension: RecallEra, Key: "wild summer", Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, res.Found, "an ambiguous name browses nothing")
	assert.Nil(t, res.Referent)
	assert.True(t, res.Ambiguous)
	require.Len(t, res.Candidates, 2)

	keys := []string{res.Candidates[0].Key, res.Candidates[1].Key}
	assert.True(t, slices.IsSorted(keys), "candidates are key-sorted for determinism")
	names := map[string]string{}
	for _, c := range res.Candidates {
		names[c.Key] = c.DisplayName
	}
	assert.Equal(t, "summer of 2009", names[eraA.Key], "each candidate carries its current display name")
	assert.Equal(t, "summer of 2011", names[eraB.Key])
}

// TestRecall_ResolvesNameAcrossKinds proves the resolver is generic across every
// registry-backed dimension: a name resolves for injury, pet, and thread the same
// way it does for era — one resolver, not a per-flag mechanism (AC-4, AC-7).
func TestRecall_ResolvesNameAcrossKinds(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	inj, err := r.WriteInjury(InjuryWriteRequest{Name: "left knee", Now: fixedNow()})
	require.NoError(t, err)
	pet, err := r.WritePet(PetWriteRequest{Name: "Rex", Species: "dog", Now: fixedNow()})
	require.NoError(t, err)
	th, err := r.WriteThread(ThreadWriteRequest{Name: "learning piano", Intent: "play for fun", Now: fixedNow()})
	require.NoError(t, err)

	for _, tc := range []struct {
		name, dim, query, wantKey string
	}{
		{"injury", RecallInjury, "LEFT KNEE", inj.Key},
		{"pet", RecallPet, "rex", pet.Key},
		{"thread", RecallThread, "Learning Piano", th.Key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := r.Recall(RecallRequest{Dimension: tc.dim, Key: tc.query, Now: fixedNow()})
			require.NoError(t, err)
			require.True(t, res.Found, "a name resolves for %s", tc.dim)
			require.NotNil(t, res.Referent)
			assert.Equal(t, tc.wantKey, res.Referent.Key)
			assert.False(t, res.Ambiguous)
		})
	}
}

// TestRecall_ReadOnly proves the surface writes nothing: the Ledger file count
// is identical before and after a keyed browse and an index over a warm store.
func TestRecall_ReadOnly(t *testing.T) {
	r, _, home := bootedMemoryRouter(t)

	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.WriteMemory(MemoryWriteRequest{Text: "a story", Era: era.Key, Now: fixedNow()})
	require.NoError(t, err)

	before := countTreeFiles(t, home)
	_, err = r.Recall(RecallRequest{Dimension: RecallEra, Key: era.Key, Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.Recall(RecallRequest{Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, before, countTreeFiles(t, home), "recall writes nothing")
}
