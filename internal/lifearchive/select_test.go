package lifearchive

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// fixedNow is a deterministic instant; selection is time-independent today, but
// the input carries it as part of the contract (life-archive.md §6).
func fixedNow() time.Time { return time.Date(2026, time.July, 5, 12, 0, 0, 0, time.UTC) }

// injuryRec builds an injury registry record with the given convention Fields.
func injuryRec(key, name string, fields map[string]any) observations.Registry {
	if fields == nil {
		fields = map[string]any{}
	}
	return observations.Registry{
		Kind:        observations.RegistryInjury,
		Key:         key,
		DisplayName: name,
		Status:      observations.StatusActive,
		Fields:      fields,
	}
}

// fullInjuryFields returns every convention key filled, so the record has no
// gaps and drops out of the injury track.
func fullInjuryFields() map[string]any {
	f := map[string]any{}
	for _, k := range injuryGapKeys {
		f[k] = "filled"
	}
	return f
}

// eraRec builds an era registry record with an optional start date.
func eraRec(key, name, start string) observations.Registry {
	f := map[string]any{}
	if start != "" {
		f["start"] = start
	}
	return observations.Registry{
		Kind:        observations.RegistryEra,
		Key:         key,
		DisplayName: name,
		Status:      observations.StatusActive,
		Fields:      f,
	}
}

// memoryIn builds a memory event filed under an era key (refs.era). An empty key
// leaves the story unfiled.
func memoryIn(eraKey string) observations.Event {
	refs := map[string]any{}
	if eraKey != "" {
		refs["era"] = eraKey
	}
	return observations.Event{Kind: observations.KindMemory, Refs: refs}
}

// TestSelectCluster_ThinInjuryLeadsInjuryTrack proves the auto rule picks the
// injury track while any injury has gaps, choosing the thinnest record and
// naming exactly the missing convention fields.
func TestSelectCluster_ThinInjuryLeadsInjuryTrack(t *testing.T) {
	bare := injuryRec("injury_bare", "left knee", nil)                                                         // 9 gaps
	partial := injuryRec("injury_partial", "old ankle", map[string]any{"body_area": "ankle", "cause": "roll"}) // 7 gaps
	full := injuryRec("injury_full", "wrist", fullInjuryFields())                                              // 0 gaps

	c, ok := SelectCluster(SelectInput{
		Injuries: []observations.Registry{full, partial, bare},
		Eras:     []observations.Registry{eraRec("era_x", "some era", "")},
		Now:      fixedNow(),
	})
	require.True(t, ok)
	assert.Equal(t, TrackInjury, c.Track, "an injury with gaps outranks the story track")
	assert.Equal(t, "injury_bare", c.Key, "the thinnest injury (most gaps) is chosen")
	assert.Equal(t, "left knee", c.DisplayName)
	assert.Equal(t, injuryGapKeys, c.Gaps, "a bare injury is missing every convention field, in canonical order")
	assert.NotEmpty(t, c.Reason)
}

// TestSelectCluster_InjuryTiebreakByKey proves equal-gap injuries tiebreak by
// key, deterministically, regardless of input order.
func TestSelectCluster_InjuryTiebreakByKey(t *testing.T) {
	// Both are missing the same eight keys (only onset filled) → 8 gaps each.
	a := injuryRec("injury_a", "knee", map[string]any{"onset": "2014"})
	b := injuryRec("injury_b", "back", map[string]any{"onset": "2015"})

	// Pass unsorted; the engine sorts by key before scanning.
	c, ok := SelectCluster(SelectInput{Injuries: []observations.Registry{b, a}, Now: fixedNow()})
	require.True(t, ok)
	assert.Equal(t, "injury_a", c.Key, "the lower key wins an equal-gap tie")
	assert.NotContains(t, c.Gaps, "onset", "the filled field is not a gap")
	assert.Len(t, c.Gaps, 8)
}

// TestSelectCluster_WhitespaceFieldIsAGap proves a present-but-blank field still
// reads as missing, so an empty testimony value does not falsely complete a
// record.
func TestSelectCluster_WhitespaceFieldIsAGap(t *testing.T) {
	inj := injuryRec("injury_ws", "shoulder", map[string]any{"body_area": "   "})
	c, ok := SelectCluster(SelectInput{Injuries: []observations.Registry{inj}, Now: fixedNow()})
	require.True(t, ok)
	assert.Contains(t, c.Gaps, "body_area", "a whitespace-only field counts as a gap")
}

// TestSelectCluster_StoryTrackWhenInjuriesComplete proves the auto rule falls to
// the story track once every injury is filled, choosing the least-excavated era.
func TestSelectCluster_StoryTrackWhenInjuriesComplete(t *testing.T) {
	full := injuryRec("injury_full", "wrist", fullInjuryFields())
	linked := eraRec("era_wild", "wild summer", "2010")
	empty := eraRec("era_quiet", "quiet year", "2012")

	c, ok := SelectCluster(SelectInput{
		Injuries: []observations.Registry{full},
		Eras:     []observations.Registry{linked, empty},
		Memories: []observations.Event{memoryIn("era_wild"), memoryIn("era_wild")},
		Now:      fixedNow(),
	})
	require.True(t, ok)
	assert.Equal(t, TrackStory, c.Track, "with injuries complete, stories lead")
	assert.Equal(t, "era_quiet", c.Key, "the era with the fewest linked memories is chosen")
	assert.Equal(t, storyGapKeys, c.Gaps, "an era cluster offers the full story-dimension set")
	assert.Contains(t, c.Reason, "no stories")
}

// TestSelectCluster_StoryTiebreakOldestStart proves equal-memory eras tiebreak by
// oldest start, with an undated era sorting last.
func TestSelectCluster_StoryTiebreakOldestStart(t *testing.T) {
	recent := eraRec("era_recent", "recent", "2014")
	oldest := eraRec("era_oldest", "oldest", "1999")
	undated := eraRec("era_undated", "undated", "")

	c, ok := SelectCluster(SelectInput{
		Eras: []observations.Registry{recent, undated, oldest},
		Now:  fixedNow(),
	})
	require.True(t, ok)
	assert.Equal(t, "era_oldest", c.Key, "the oldest-started, least-excavated era wins")
}

// TestSelectCluster_ForcedTrack proves an explicit Track selects within that
// track only: story even when injuries have gaps, and injury returns nothing
// when no injury has gaps.
func TestSelectCluster_ForcedTrack(t *testing.T) {
	thin := injuryRec("injury_thin", "knee", nil)
	era := eraRec("era_x", "an era", "2000")

	story, ok := SelectCluster(SelectInput{
		Injuries: []observations.Registry{thin},
		Eras:     []observations.Registry{era},
		Track:    TrackStory,
		Now:      fixedNow(),
	})
	require.True(t, ok)
	assert.Equal(t, TrackStory, story.Track, "a forced story track ignores an open injury")

	full := injuryRec("injury_full", "wrist", fullInjuryFields())
	_, ok = SelectCluster(SelectInput{Injuries: []observations.Registry{full}, Track: TrackInjury, Now: fixedNow()})
	assert.False(t, ok, "a forced injury track with no gaps has no candidate")
}

// TestSelectCluster_EmptyAndMissing proves an empty store, and a store with
// memories but no era, both degrade to an honest empty result.
func TestSelectCluster_EmptyAndMissing(t *testing.T) {
	_, ok := SelectCluster(SelectInput{Now: fixedNow()})
	assert.False(t, ok, "an empty store has nothing to excavate")

	full := injuryRec("injury_full", "wrist", fullInjuryFields())
	_, ok = SelectCluster(SelectInput{
		Injuries: []observations.Registry{full},
		Memories: []observations.Event{memoryIn("")}, // unfiled memory, no era
		Now:      fixedNow(),
	})
	assert.False(t, ok, "memories with no era give the story track nothing to browse by")
}

// TestPrompts_InjuryGenericAndDataDriven proves the injury prompts are the fixed
// generic templates plus a data-driven lead-in — the personal specific (the
// display name) appears only in the lead-in, never baked into the questions.
func TestPrompts_InjuryGenericAndDataDriven(t *testing.T) {
	c := Cluster{Track: TrackInjury, Key: "injury_k", DisplayName: "left knee", Gaps: []string{"onset", "cause"}}
	got := Prompts(c)

	require.Len(t, got, 3, "a lead-in plus one prompt per gap")
	assert.Contains(t, got[0], "left knee", "the lead-in names the cluster from data")
	assert.Equal(t, injuryPrompts["onset"], got[1], "gap prompts render in gap order")
	assert.Equal(t, injuryPrompts["cause"], got[2])
	for _, q := range got[1:] {
		assert.NotContains(t, q, "left knee", "no personal specific is baked into a generic template")
	}
}

// TestPrompts_StoryGeneric proves the story prompts are the full generic
// dimension set behind a data-driven lead-in.
func TestPrompts_StoryGeneric(t *testing.T) {
	c := Cluster{Track: TrackStory, Key: "era_w", DisplayName: "wild summer", Gaps: storyGapKeys}
	got := Prompts(c)

	require.Len(t, got, 1+len(storyGapKeys))
	assert.Contains(t, got[0], "wild summer")
	assert.Equal(t, storyPrompts["date"], got[1])
	assert.Equal(t, storyPrompts["follow_up"], got[len(got)-1])
}

// TestPrompts_UnknownTrackAndFallbacks proves an unknown track yields no prompts
// and a blank display name falls back to generic copy.
func TestPrompts_UnknownTrackAndFallbacks(t *testing.T) {
	assert.Nil(t, Prompts(Cluster{Track: ""}))

	got := Prompts(Cluster{Track: TrackInjury, Gaps: []string{"onset"}})
	require.Len(t, got, 2)
	assert.Contains(t, got[0], "this injury", "a blank name falls back to generic copy")
}

// TestPrompts_UnknownGapDropped proves a gap with no template is skipped rather
// than rendered blank (forward-compatibility for a future gap key).
func TestPrompts_UnknownGapDropped(t *testing.T) {
	got := Prompts(Cluster{Track: TrackInjury, DisplayName: "knee", Gaps: []string{"onset", "made_up_key"}})
	require.Len(t, got, 2, "the unknown gap is dropped, leaving the lead-in + the known prompt")
	assert.Equal(t, injuryPrompts["onset"], got[1])
}
