package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveBackfillTarget_DefaultMostRecentGap(t *testing.T) {
	today := at(2026, 7, 10, 0, 0)
	// 07-09 and 07-08 completed; 07-07 is the most recent gap.
	recs := []DayRecord{completed("2026-07-09"), completed("2026-07-08")}
	got := ResolveBackfillTarget(recs, today, 7)
	assert.Equal(t, "2026-07-07", DateString(got))
}

func TestResolveBackfillTarget_YesterdayWhenAllComplete(t *testing.T) {
	today := at(2026, 7, 10, 0, 0)
	recs := make([]DayRecord, 0, 7)
	for _, d := range []string{"2026-07-09", "2026-07-08", "2026-07-07", "2026-07-06", "2026-07-05", "2026-07-04", "2026-07-03"} {
		recs = append(recs, completed(d))
	}
	got := ResolveBackfillTarget(recs, today, 7)
	assert.Equal(t, "2026-07-09", DateString(got)) // nothing to backfill → yesterday
}

func TestResolveBackfillTarget_YesterdayGap(t *testing.T) {
	today := at(2026, 7, 10, 0, 0)
	got := ResolveBackfillTarget(nil, today, 7)
	assert.Equal(t, "2026-07-09", DateString(got))
}

func TestBackfillInWindow(t *testing.T) {
	today := at(2026, 7, 10, 0, 0)
	assert.True(t, BackfillInWindow(at(2026, 7, 9, 0, 0), today, 7))   // 1 day back
	assert.True(t, BackfillInWindow(at(2026, 7, 3, 0, 0), today, 7))   // exactly 7 days back
	assert.False(t, BackfillInWindow(at(2026, 7, 2, 0, 0), today, 7))  // 8 days back — out
	assert.False(t, BackfillInWindow(today, today, 7))                 // today — not a past day
	assert.False(t, BackfillInWindow(at(2026, 7, 11, 0, 0), today, 7)) // future
}
