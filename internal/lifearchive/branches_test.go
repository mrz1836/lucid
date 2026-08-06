package lifearchive

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestHasField_NilAndNonString covers the two edge readings: a nil Fields map is
// never a match, and any present non-string value counts as filled (a
// hand-edited record degrades honestly).
func TestHasField_NilAndNonString(t *testing.T) {
	assert.False(t, hasField(nil, "onset"), "a nil Fields map holds nothing")
	assert.False(t, hasField(map[string]any{}, "onset"), "an absent key is not filled")
	assert.False(t, hasField(map[string]any{"onset": "  "}, "onset"), "a blank string is a gap")
	assert.True(t, hasField(map[string]any{"onset": "2014"}, "onset"))
	assert.True(t, hasField(map[string]any{"severity": 7}, "severity"), "a non-string present value counts as filled")
}

// TestMemoryEraKey_NilAndNonStringRefs covers the two "no era" readings: a memory
// with no refs, and a refs.era that is not a string, both file under no era.
func TestMemoryEraKey_NilAndNonStringRefs(t *testing.T) {
	assert.Empty(t, memoryEraKey(observations.Event{Refs: nil}), "no refs ⇒ no era")
	assert.Empty(t, memoryEraKey(observations.Event{Refs: map[string]any{"other": "x"}}), "no era ref ⇒ no era")
	assert.Empty(t, memoryEraKey(observations.Event{Refs: map[string]any{"era": 5}}), "a non-string era ref ⇒ no era")
	assert.Equal(t, "era_x", memoryEraKey(observations.Event{Refs: map[string]any{"era": " era_x "}}), "a string era ref is trimmed")
}

// TestStoryReasonAndCountable covers the linked>0 story reason and the singular
// arm of the count renderer — inventory language, never a quota.
func TestStoryReasonAndCountable(t *testing.T) {
	assert.Contains(t, storyReason(0), "no stories captured")
	assert.Equal(t, "1 story captured so far — room for more.", storyReason(1))
	assert.Equal(t, "3 stories captured so far — room for more.", storyReason(3))

	assert.Equal(t, "1 convention field", countable(1, "convention field", "convention fields"))
	assert.Equal(t, "2 convention fields", countable(2, "convention field", "convention fields"))
}

// TestSelectCluster_StoryTiebreakByKey proves that with equal linked counts and
// equal (undated) starts, the era key breaks the tie deterministically.
func TestSelectCluster_StoryTiebreakByKey(t *testing.T) {
	// Both eras are undated (start sorts to the same sentinel) and hold no
	// linked memories, so only the key can decide.
	b := eraRec("era_b", "second", "")
	a := eraRec("era_a", "first", "")

	c, ok := SelectCluster(SelectInput{Eras: []observations.Registry{b, a}, Track: TrackStory, Now: fixedNow()})
	assert.True(t, ok)
	assert.Equal(t, "era_a", c.Key, "the lower key wins an otherwise-equal era tie")
}
