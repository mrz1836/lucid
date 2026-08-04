package router

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestAnchorAdd_CorrectionReusesID records the same active label twice: the
// second append targets the first anchor rather than minting a second one, so
// the two records share one identity and fold to a single entry.
func TestAnchorAdd_CorrectionReusesID(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	first, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	require.NotEmpty(t, first.Anchor.ID, "a first use mints an id")

	second, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-02-01", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, first.Anchor.ID, second.Anchor.ID, "a correction targets the existing anchor")
	assert.Equal(t, "Anchor recorded: gate-30 — 2026-02-01.", second.Ack, "a correction is not a new milestone")

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	require.Len(t, log.History, 2, "both records persist")
	assert.Len(t, engine.LatestAnchors(log), 1, "one identity, one folded entry")
}

// TestAnchorAdd_LegacyCorrectionStaysIDLess corrects an anchor written before
// ids existed. The append must stay id-less so it folds under the same
// synthetic identity — an id here would fork the label into two anchors.
func TestAnchorAdd_LegacyCorrectionStaysIDLess(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, a.AppendAnchor(engine.Anchor{
		Label: "gate-30", Date: "2026-01-01", RecordedAt: "2026-06-01T09:00:00Z",
	}))

	res, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-02-01", Now: fixedNow()})
	require.NoError(t, err)
	assert.Empty(t, res.Anchor.ID, "a correction to a pre-id record stays id-less")

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	require.Len(t, log.History, 2)
	latest := engine.LatestAnchors(log)
	require.Len(t, latest, 1, "the label stays one anchor")
	assert.Equal(t, "2026-02-01", latest[0].Date)
	assert.Equal(t, "legacy:gate-30", engine.AnchorIdentity(latest[0]))
}

// TestAnchorAdd_AfterSunsetMintsNewID reuses a retired name. Because no active
// anchor holds it, the add mints a *new* identity — the two runs stay distinct
// — and the ack names the earlier retirement rather than implying continuity.
func TestAnchorAdd_AfterSunsetMintsNewID(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	first, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Now: fixedNow()})
	require.NoError(t, err)

	again, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-02-01", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t,
		"Anchor added: gate-30 — 2026-02-01 (a previous gate-30 was sunset 2026-07-05).", again.Ack,
	)
	assert.NotEqual(t, first.Anchor.ID, again.Anchor.ID, "a reused name is a new milestone")
	assert.NotEmpty(t, again.Anchor.ID)

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	require.Len(t, log.History, 3, "add, sunset, add — nothing rewritten")
	assert.Len(t, engine.LatestAnchors(log), 2, "the retired run and the new one are distinct")
}

// TestAnchorSunset_AppendsStateRecord retires an anchor and asserts the whole
// appended record, the ack, and that the record it supersedes is untouched.
func TestAnchorSunset_AppendsStateRecord(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	added, err := r.AnchorAdd(AnchorAddRequest{
		Label: "gate-30", Date: "2026-01-01", Note: "first gate", Now: fixedNow(),
	})
	require.NoError(t, err)

	res, err := r.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Reason: "mistyped label", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, engine.Anchor{
		ID:         added.Anchor.ID,
		Label:      "gate-30",
		Date:       "2026-01-01", // the milestone's date, never the retirement day
		Note:       "mistyped label",
		RecordedAt: fixedNow().Format(time.RFC3339),
		State:      engine.AnchorStateSunset,
	}, res.Anchor)
	assert.Equal(t,
		"Anchor sunset: gate-30 — 2026-01-01. It no longer counts on the metrics surface; the record is kept.",
		res.Ack,
	)

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	require.Len(t, log.History, 2, "a sunset appends; it never deletes")
	assert.Equal(t, added.Anchor, log.History[0], "the superseded record is unchanged")
	assert.True(t, log.History[1].IsSunset())
}

// TestAnchorSunset_LegacyAnchorStaysIDLess retires an anchor written before ids
// existed. The sunset record must stay id-less so it folds under the same
// synthetic identity and actually retires it.
func TestAnchorSunset_LegacyAnchorStaysIDLess(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, a.AppendAnchor(engine.Anchor{
		Label: "gate-30", Date: "2026-01-01", RecordedAt: "2026-06-01T09:00:00Z",
	}))

	res, err := r.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Now: fixedNow()})
	require.NoError(t, err)
	assert.Empty(t, res.Anchor.ID, "retiring a pre-id anchor stays id-less")

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	latest := engine.LatestAnchors(log)
	require.Len(t, latest, 1, "the sunset record supersedes rather than forking")
	assert.True(t, latest[0].IsSunset(), "the legacy identity is genuinely retired")
	_, active := engine.FindActiveAnchor(latest, "gate-30")
	assert.False(t, active, "the label leaves the active surface")
}

// TestAnchorSunset_UnknownLabelListsLabels rejects a label no anchor holds and
// names the labels that do exist, since there is no `anchor list` verb.
func TestAnchorSunset_UnknownLabelListsLabels(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-02", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "gate-3", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorUnknownLabel)
	assert.Equal(t, `no anchor named "gate-3" — recorded anchors: gate-30, sobriety`, err.Error())

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Len(t, log.History, 2, "a rejection appends nothing")
}

// TestAnchorSunset_AlreadySunset refuses to retire a retired anchor and points
// at the supported path — adding the label again.
func TestAnchorSunset_AlreadySunset(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorAlreadySunset)
	assert.Contains(t, err.Error(), `"gate-30" was already sunset on 2026-07-05`)
	assert.Contains(t, err.Error(), "nothing was saved")

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Len(t, log.History, 2, "a rejection appends nothing")
}

// TestAnchorSunset_AppendFailureSurfaced: a retirement whose append cannot be
// written is surfaced, not swallowed — the anchor stays active.
func TestAnchorSunset_AppendFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)

	anchorsPath := filepath.Join(home, "engine", "anchors.json")
	require.NoError(t, os.Chmod(anchorsPath, 0o400)) // still readable, so the failure is the write
	t.Cleanup(func() { _ = os.Chmod(anchorsPath, 0o600) })

	_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestAnchorSunset_EmptyStoreMessage says the store is empty rather than
// trailing off after a list with nothing in it.
func TestAnchorSunset_EmptyStoreMessage(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())

	_, err := r.AnchorSunset(AnchorSunsetRequest{Label: "gate-3", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorUnknownLabel)
	assert.Equal(t, `no anchor named "gate-3" — no anchors are recorded yet`, err.Error())

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Empty(t, log.History)
}
