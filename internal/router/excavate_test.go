package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestExcavate_SelectsThinInjuryReadOnly proves the router seam builds the
// projection-only bundle, selects the thin injury on the injury track, emits its
// prompts, and writes nothing (AC-8, AC-9). The file-count probe isolates that
// Excavate is a pure read: the tree is identical before and after.
func TestExcavate_SelectsThinInjuryReadOnly(t *testing.T) {
	r, _, home := newBootedRouter(t)

	_, err := r.WriteInjury(InjuryWriteRequest{Name: "left knee", Now: fixedNow()})
	require.NoError(t, err)

	// The injury write already scaffolded + wrote; snapshot the warm tree so the
	// probe measures exactly what Excavate writes (nothing).
	before := countTreeFiles(t, home)

	res, err := r.Excavate(fixedNow())
	require.NoError(t, err)
	assert.True(t, res.Found)
	assert.Equal(t, "injury", res.Track)
	assert.Equal(t, "left knee", res.DisplayName)
	assert.Len(t, res.Gaps, 9, "a bare injury is missing every convention field")
	assert.Len(t, res.Prompts, 10, "a lead-in plus one prompt per gap")
	assert.NotEmpty(t, res.Reason)

	assert.Equal(t, before, countTreeFiles(t, home), "excavate writes nothing")
}

// TestExcavate_StoryTrackFromEra proves the story track is selected when no
// injury has gaps, naming the era and offering the full story-dimension set.
func TestExcavate_StoryTrackFromEra(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Start: "2010-06-01", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.Excavate(fixedNow())
	require.NoError(t, err)
	assert.True(t, res.Found)
	assert.Equal(t, "story", res.Track)
	assert.Equal(t, era.Key, res.Key)
	assert.Equal(t, "wild summer", res.DisplayName)
	assert.Len(t, res.Gaps, 6, "an era cluster offers the full story-dimension set")
	assert.Len(t, res.Prompts, 7, "a lead-in plus one prompt per dimension")
}

// TestExcavate_EmptyStoreHonestEmpty proves an empty store returns Found: false
// with no cluster and no model call.
func TestExcavate_EmptyStoreHonestEmpty(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	res, err := r.Excavate(fixedNow())
	require.NoError(t, err)
	assert.False(t, res.Found)
	assert.Empty(t, res.Track)
	assert.Empty(t, res.Prompts)
}

// TestBuildExcavationBundle_ReadsProjectionSeams proves the bundle gathers the
// injury/era registries and the memory events through the storage projection
// seams — the sanctuary-safe read the selection engine consumes.
func TestBuildExcavationBundle_ReadsProjectionSeams(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	_, err := r.WriteInjury(InjuryWriteRequest{Name: "left knee", Now: fixedNow()})
	require.NoError(t, err)
	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.WriteMemory(MemoryWriteRequest{Text: "a night we drove to the coast", Era: era.Key, Now: fixedNow()})
	require.NoError(t, err)

	bundle, err := r.BuildExcavationBundle()
	require.NoError(t, err)
	assert.Len(t, bundle.Injuries, 1)
	assert.Len(t, bundle.Eras, 1)
	require.Len(t, bundle.Memories, 1)
	assert.Equal(t, observations.KindMemory, bundle.Memories[0].Kind)
	assert.Equal(t, era.Key, bundle.Memories[0].Refs["era"])
}
