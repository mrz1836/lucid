package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildMetrics_NilLocDefaultsUTC: BuildMetrics tolerates a nil location,
// resolving the anchoring math in UTC rather than panicking.
func TestBuildMetrics_NilLocDefaultsUTC(t *testing.T) {
	m := BuildMetrics(MetricsInput{
		Records: []DayRecord{completedDay("2026-04-10", ModeGreen)},
		Chain:   defChain(),
		Loc:     nil,
	})
	require.NotNil(t, m.Ref)
	assert.Equal(t, "2026-04-10", *m.Ref)
}

// TestPartitionAnchors_SortsBothSurfacesByLabelThenID exercises the total-order
// comparators on both projected surfaces: records sharing a display name are
// broken by identity, and differing labels order lexically. Two active and two
// retired anchors share a label so the identity tiebreak fires, and a third of
// each carries a different label so the label comparison fires.
func TestPartitionAnchors_SortsBothSurfacesByLabelThenID(t *testing.T) {
	logicalDay := DateOf(time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC))
	anchors := []Anchor{
		// Deliberately unsorted input so the sort has real work to do.
		{ID: "anchor_b", Label: "beta", Date: "2026-04-05", RecordedAt: "2026-04-05T09:00:00Z"},
		{ID: "anchor_a2", Label: "alpha", Date: "2026-04-05", RecordedAt: "2026-04-05T09:00:00Z"},
		{ID: "anchor_a1", Label: "alpha", Date: "2026-04-05", RecordedAt: "2026-04-05T09:00:00Z"},
		{ID: "sunset_y", Label: "yankee", Date: "2026-02-01", RecordedAt: "2026-04-08T09:00:00Z", State: AnchorStateSunset},
		{ID: "sunset_x2", Label: "xray", Date: "2026-02-01", RecordedAt: "2026-04-08T09:00:00Z", State: AnchorStateSunset},
		{ID: "sunset_x1", Label: "xray", Date: "2026-02-01", RecordedAt: "2026-04-08T09:00:00Z", State: AnchorStateSunset},
	}

	active, sunset := partitionAnchors(anchors, logicalDay)

	require.Len(t, active, 3)
	assert.Equal(t, []string{"anchor_a1", "anchor_a2", "anchor_b"},
		[]string{active[0].ID, active[1].ID, active[2].ID},
		"active anchors sort by label then identity")

	require.Len(t, sunset, 3)
	assert.Equal(t, []string{"sunset_x1", "sunset_x2", "sunset_y"},
		[]string{sunset[0].ID, sunset[1].ID, sunset[2].ID},
		"retired anchors sort by label then identity")
}
