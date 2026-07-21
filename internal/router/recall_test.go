package router

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
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
// era, thread, and injury, each sourced "registry" (AC-10).
func TestRecall_IndexListsEveryReferent(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	_, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Start: "2010-06-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.WriteThread(ThreadWriteRequest{Name: "learning piano", Intent: "play", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.WriteInjury(InjuryWriteRequest{Name: "left knee", Now: fixedNow()})
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
	assert.Equal(t, "wild summer", byKind[RecallEra].Title)
	assert.Contains(t, byKind[RecallEra].Detail, "2010-06-01", "an era index entry shows its range")
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
