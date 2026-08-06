package workout

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidExtraction_SorenessBlankPartRejected covers the body-state rule that
// a soreness reading with no named part is malformed — the agent retries or
// degrades rather than writing a partless reading.
func TestValidExtraction_SorenessBlankPartRejected(t *testing.T) {
	ext := extraction{
		Type:     "run",
		Soreness: []bodyStatePayload{{Part: "   "}},
	}
	assert.False(t, validExtraction(ext), "a soreness reading with a blank part is invalid")
}

// TestToAnchorCounts_AllBlankNamesYieldNil proves that anchor items that are all
// blank-named drop to a nil slice — a stray empty entry is noise, not a count.
func TestToAnchorCounts_AllBlankNamesYieldNil(t *testing.T) {
	assert.Nil(t, toAnchorCounts(nil), "no items maps to nil")
	assert.Nil(t, toAnchorCounts([]anchorPayload{{Name: "  "}}), "all-blank items map to nil, never an empty-named count")

	// A real item still maps through, with an unstated count carried as such.
	five := 5
	got := toAnchorCounts([]anchorPayload{{Name: " squats ", Count: &five}, {Name: ""}})
	assert.Equal(t, []AnchorCount{{Name: "squats", Count: 5, HasCount: true}}, got)
}

// TestToBodyStates_BlankPartDropped covers the defensive skip: a body-state
// payload with a blank part is dropped rather than emitted as a partless reading
// (validExtraction already rejects such a reply, so this is the mapper's own
// guard).
func TestToBodyStates_BlankPartDropped(t *testing.T) {
	assert.Nil(t, toBodyStates(nil), "no payloads maps to nil")

	sore := 4
	got := toBodyStates([]bodyStatePayload{{Part: "  "}, {Part: " calves ", Soreness: &sore}})
	assert.Equal(t, []BodyState{{Part: "calves", Soreness: &sore}}, got, "the blank-part reading is dropped, the named one is kept and trimmed")
}
