package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseInput() CloseoutInput {
	return CloseoutInput{
		LogicalDay: at(2026, 7, 5, 0, 0),
		RecordedAt: at(2026, 7, 5, 22, 41),
		Links:      map[string]string{"journal": StatusDone, "dock": StatusDone, "read": StatusFloor},
		Capacity:   3,
		LimiterTag: "wrist",
		RawEntryID: "raw_2026_07_05_22_41",
	}
}

func TestBuildDayRecord_Completed(t *testing.T) {
	chain := DefaultChain()
	rec := BuildDayRecord(chain, baseInput())
	assert.Equal(t, "day_2026_07_05", rec.DayID)
	assert.Equal(t, "2026-07-05", rec.LogicalDate)
	assert.Equal(t, ModeGreen, rec.Mode) // undeclared defaults to green
	assert.Empty(t, rec.ModeDeclaredAt)
	assert.True(t, rec.Completed)
	assert.False(t, rec.Missed)
	assert.False(t, rec.Partial)
	assert.True(t, rec.FloorDay) // read ran at floor
	assert.Equal(t, 3, rec.Capacity)
	assert.Equal(t, "wrist", rec.LimiterTag)
	assert.Equal(t, DefaultProfile, rec.Profile)
	assert.Equal(t, "raw_2026_07_05_22_41", rec.RawEntryID)
	assert.NotNil(t, rec.Corrections)
	assert.Empty(t, rec.Corrections)
}

func TestBuildDayRecord_Skip(t *testing.T) {
	rec := BuildDayRecord(DefaultChain(), CloseoutInput{
		LogicalDay: at(2026, 7, 5, 0, 0), RecordedAt: at(2026, 7, 5, 22, 0), Skip: true,
	})
	assert.True(t, rec.Missed)
	assert.False(t, rec.Completed)
	assert.Empty(t, rec.Links)
}

func TestBuildDayRecord_PartialSurvivalRan(t *testing.T) {
	in := baseInput()
	in.Partial = true
	in.Links = map[string]string{"journal": StatusFloor} // survival ran at floor
	rec := BuildDayRecord(DefaultChain(), in)
	assert.True(t, rec.Partial)
	assert.True(t, rec.Completed) // survival floor still counts
	assert.False(t, rec.Missed)
}

func TestBuildDayRecord_PartialSurvivalMissed(t *testing.T) {
	in := baseInput()
	in.Partial = true
	in.Links = map[string]string{"dock": StatusDone} // survival (journal) did not run
	rec := BuildDayRecord(DefaultChain(), in)
	assert.True(t, rec.Partial)
	assert.False(t, rec.Completed)
	assert.True(t, rec.Missed)
}

func TestBuildDayRecord_NoFloorAllDone(t *testing.T) {
	in := baseInput()
	in.Links = map[string]string{"journal": StatusDone, "dock": StatusDone, "read": StatusDone}
	rec := BuildDayRecord(DefaultChain(), in)
	assert.False(t, rec.FloorDay)
	assert.True(t, rec.Completed)
}

func TestBuildDayRecord_ModeOverride(t *testing.T) {
	in := baseInput()
	in.Mode = ModeRed
	in.ModeDeclaredAt = "2026-07-05T14:00:00Z"
	in.Storm = true
	rec := BuildDayRecord(DefaultChain(), in)
	assert.Equal(t, ModeRed, rec.Mode)
	assert.Equal(t, "2026-07-05T14:00:00Z", rec.ModeDeclaredAt)
	assert.True(t, rec.Storm)
}

func TestSurvivalRan(t *testing.T) {
	in := CloseoutInput{Links: map[string]string{"journal": StatusFloor}}
	assert.True(t, in.SurvivalRan("journal"))
	in.Links["journal"] = StatusSkipped
	assert.False(t, in.SurvivalRan("journal"))
	assert.False(t, in.SurvivalRan("absent"))
}

// TestCompactEqualsGuided is the "compact form and guided form write
// identical records" criterion.
func TestCompactEqualsGuided(t *testing.T) {
	chain := DefaultChain()
	links, capacity, tag, journal, err := ParseCompact(chain, "dfx 3/wrist Long day but the chain ran.")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"journal": StatusDone, "dock": StatusFloor, "read": StatusSkipped}, links)
	assert.Equal(t, 3, capacity)
	assert.Equal(t, "wrist", tag)
	assert.Equal(t, "Long day but the chain ran.", journal)

	now := at(2026, 7, 5, 22, 41)
	day := at(2026, 7, 5, 0, 0)
	compact := BuildDayRecord(chain, CloseoutInput{
		LogicalDay: day, RecordedAt: now, Links: links, Capacity: capacity, LimiterTag: tag, RawEntryID: "raw_x",
	})
	guided := BuildDayRecord(chain, CloseoutInput{
		LogicalDay: day, RecordedAt: now, RawEntryID: "raw_x", Capacity: 3, LimiterTag: "wrist",
		Links: map[string]string{"journal": StatusDone, "dock": StatusFloor, "read": StatusSkipped},
	})
	assert.Equal(t, guided, compact)
}

func TestParseCompact_NoTag(t *testing.T) {
	links, capacity, tag, journal, err := ParseCompact(DefaultChain(), "ddd 5 all done")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"journal": StatusDone, "dock": StatusDone, "read": StatusDone}, links)
	assert.Equal(t, 5, capacity)
	assert.Empty(t, tag)
	assert.Equal(t, "all done", journal)
}

func TestParseCompact_Errors(t *testing.T) {
	chain := DefaultChain()
	cases := []string{
		"dfx",           // missing capacity
		"df 3 short",    // wrong link-char count
		"dfz 3 journal", // unknown link char
		"dfx 6 journal", // capacity out of range
		"dfx 0 journal", // capacity out of range
		"dfx x journal", // capacity not a digit
	}
	for _, in := range cases {
		_, _, _, _, err := ParseCompact(chain, in)
		assert.Errorf(t, err, "expected %q to be rejected", in)
	}
}

// TestGuidedPromptBudget is the "≤ (links + 3) prompts" two-minute-budget
// criterion.
func TestGuidedPromptBudget(t *testing.T) {
	chain := DefaultChain()
	prompts := GuidedPrompts(chain)
	assert.LessOrEqual(t, len(prompts), PromptBudget(chain))
	assert.Len(t, prompts, len(chain.Links)+2) // one per link + capacity + journal
	assert.Equal(t, len(chain.Links)+3, PromptBudget(chain))
}

func TestBuildDayRecord_RecordedAtFormat(t *testing.T) {
	rec := BuildDayRecord(DefaultChain(), baseInput())
	_, err := time.Parse(time.RFC3339, rec.RecordedAt)
	assert.NoError(t, err)
}
