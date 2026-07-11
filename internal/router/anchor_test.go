package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestAnchorAdd_RecordsAndAcks records a backdated milestone and asserts the
// inventory ack plus a byte-stable RecordedAt from the pinned clock.
func TestAnchorAdd_RecordsAndAcks(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	res, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "Anchor recorded: sobriety — 2026-01-01.", res.Ack)
	assert.Equal(t, "sobriety", res.Anchor.Label)
	assert.Equal(t, "2026-01-01", res.Anchor.Date)
	assert.Equal(t, fixedNow().Format("2006-01-02T15:04:05Z07:00"), res.Anchor.RecordedAt)

	// The record persisted to the append-only store.
	log, err := a.ReadAnchors()
	require.NoError(t, err)
	require.Len(t, log.History, 1)
	assert.Equal(t, "sobriety", log.History[0].Label)
}

// TestAnchorAdd_KeepsNote records the optional note verbatim.
func TestAnchorAdd_KeepsNote(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	res, err := r.AnchorAdd(AnchorAddRequest{Label: "gate", Date: "2026-03-15", Note: "first ninety-day gate", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "first ninety-day gate", res.Anchor.Note)
}

// TestAnchorAdd_RejectsBadDate rejects an unparseable date without writing.
func TestAnchorAdd_RejectsBadDate(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine()) // an empty store exists so "no write" is meaningful

	_, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-13-40", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorRejected)
	assert.Contains(t, err.Error(), "YYYY-MM-DD")

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Empty(t, log.History, "a rejected input must not write")
}

// TestAnchorAdd_RejectsEmptyLabel rejects a blank label without writing.
func TestAnchorAdd_RejectsEmptyLabel(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())

	_, err := r.AnchorAdd(AnchorAddRequest{Label: "   ", Date: "2026-01-01", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorRejected)
	assert.Contains(t, err.Error(), "label")

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Empty(t, log.History)
}

// TestAnchorAdd_SecondAppendSupersedes records the same label twice: both
// persist in history and latest-wins folds to the newer record.
func TestAnchorAdd_SecondAppendSupersedes(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	// A correction with an *earlier* date, appended later, still supersedes.
	_, err = r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2025-12-15", Now: fixedNow()})
	require.NoError(t, err)

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	require.Len(t, log.History, 2, "both records persist in the append-only history")

	latest := engine.LatestAnchors(log)
	require.Len(t, latest, 1)
	assert.Equal(t, "2025-12-15", latest[0].Date, "the most-recently-appended record wins")
}
