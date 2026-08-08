package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// seedLegacyAnchors appends pre-id anchors directly through storage so the
// backfill has something to adopt. Every label is synthetic.
func seedLegacyAnchors(t *testing.T, a *storage.Adapter, records ...engine.Anchor) {
	t.Helper()
	require.NoError(t, a.ScaffoldEngine())
	for _, rec := range records {
		require.NoError(t, a.AppendAnchor(rec))
	}
}

// anchorsFileBytes reads the raw anchors.json bytes so a test can assert the
// file is byte-identical across a run.
func anchorsFileBytes(t *testing.T, home string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "engine", "anchors.json"))
	require.NoError(t, err)
	return b
}

// TestBackfillAnchorIDs_IsIdempotent: the first run adopts every pre-id anchor,
// and a second run appends nothing — anchors.json is byte-identical across it.
func TestBackfillAnchorIDs_IsIdempotent(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedLegacyAnchors(t, a,
		engine.Anchor{Label: "gate-a", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		engine.Anchor{Label: "gate-b", Date: "2026-02-01", RecordedAt: "2026-02-01T10:00:00Z"},
	)

	first, err := r.BackfillAnchorIDs(BackfillAnchorIDsRequest{Now: fixedNow()})
	require.NoError(t, err)
	assert.True(t, first.Written, "the first run appends adoptions")
	require.Len(t, first.Planned, 2, "both pre-id anchors are adopted")
	for _, rec := range first.Planned {
		require.NotEmpty(t, rec.ID, "every adoption carries a minted id")
		assert.NotEmpty(t, rec.Adopts, "every adoption names the legacy identity it takes over")
	}
	assert.Len(t, engine.LatestAnchors(readAnchorLog(t, a)), 2, "no anchor forks")

	beforeSecond := anchorsFileBytes(t, home)

	second, err := r.BackfillAnchorIDs(BackfillAnchorIDsRequest{Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, second.Written, "a re-run writes nothing")
	assert.Empty(t, second.Planned, "every anchor already carries an id")
	assert.Equal(t, "Every anchor already carries an id; nothing was appended.", second.Ack)

	assert.Equal(t, beforeSecond, anchorsFileBytes(t, home), "a re-run must not touch anchors.json")
}

// TestBackfillAnchorIDs_DryRunWritesNothing: a dry run plans the adoptions but
// leaves anchors.json byte-identical, and the plan is non-empty so the byte
// assertion is meaningful.
func TestBackfillAnchorIDs_DryRunWritesNothing(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedLegacyAnchors(t, a,
		engine.Anchor{Label: "gate-a", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
	)

	before := anchorsFileBytes(t, home)

	res, err := r.BackfillAnchorIDs(BackfillAnchorIDsRequest{Now: fixedNow(), DryRun: true})
	require.NoError(t, err)
	require.NotEmpty(t, res.Planned, "the dry run must plan something for the no-write assertion to mean anything")
	assert.False(t, res.Written, "a dry run never writes")
	assert.Contains(t, res.Ack, "nothing was written")

	assert.Equal(t, before, anchorsFileBytes(t, home), "a dry run must not touch anchors.json")

	// And the store still sees the anchor as pre-id, so a later real run can
	// still adopt it.
	require.Len(t, engine.PlanAnchorAdoptions(readAnchorLog(t, a), fixedNow()), 1)
}

// TestBackfillAnchorIDs_EmptyLedger: a store with no anchors plans nothing and
// returns no error — the no-op ack, not a failure.
func TestBackfillAnchorIDs_EmptyLedger(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())

	res, err := r.BackfillAnchorIDs(BackfillAnchorIDsRequest{Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, res.Written)
	assert.Empty(t, res.Planned)
	assert.Equal(t, "Every anchor already carries an id; nothing was appended.", res.Ack)
}

// TestBackfillAnchorIDs_LeavesIDdAnchorsAlone: an anchor that already carries an
// id is never re-adopted, so a store that mixes pre-id and id'd records adopts
// only the pre-id ones.
func TestBackfillAnchorIDs_LeavesIDdAnchorsAlone(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedLegacyAnchors(t, a,
		engine.Anchor{Label: "gate-a", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		engine.Anchor{ID: "anchor_2026_07_01_a", Label: "gate-b", Date: "2026-07-01", RecordedAt: "2026-07-01T10:00:00Z"},
	)

	res, err := r.BackfillAnchorIDs(BackfillAnchorIDsRequest{Now: fixedNow()})
	require.NoError(t, err)
	require.Len(t, res.Planned, 1, "only the pre-id anchor is adopted")
	assert.Equal(t, "gate-a", res.Planned[0].Label)
	assert.Contains(t, res.Ack, "1 anchor")
	assert.Contains(t, res.Ack, "gate-a")
}

// readAnchorLog reads the append-only anchors store through the adapter.
func readAnchorLog(t *testing.T, a *storage.Adapter) engine.AnchorLog {
	t.Helper()
	log, err := a.ReadAnchors()
	require.NoError(t, err)
	return log
}
