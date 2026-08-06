package workout

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestBuildTrendBodyResponseKeepsNewerWhenOlderComesLater proves the recency
// fold is order-independent: when a newer reading for a part is processed before
// an older one, the later-but-older event is dropped and the newer reading still
// wins. It complements the existing older-first case, locking the fold's
// deterministic result regardless of slice order.
func TestBuildTrendBodyResponseKeepsNewerWhenOlderComesLater(t *testing.T) {
	t.Parallel()

	tr := BuildTrend(TrendInput{
		BodyState: []observations.Event{
			bodyStateOn("2026-07-19", "legs", 6, -1), // newer — processed first
			bodyStateOn("2026-07-17", "legs", 4, -1), // older — arrives later, must be skipped
		},
		Now: mustTime(t, trendNow),
		Loc: time.UTC,
	})

	require.Len(t, tr.BodyResponse, 1)
	require.NotNil(t, tr.BodyResponse[0].Soreness)
	assert.Equal(t, 6, *tr.BodyResponse[0].Soreness, "the newer reading wins even when the older one comes later")
	assert.Equal(t, "2026-07-19", tr.BodyResponse[0].AsOf)
}
