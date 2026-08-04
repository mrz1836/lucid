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

// TestAnchorRename_SameIDNewLabel renames an anchor that carries an id: one
// append under that same id, with the date and note carried forward, and the
// record it supersedes left untouched.
func TestAnchorRename_SameIDNewLabel(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	added, err := r.AnchorAdd(AnchorAddRequest{
		Label: "gate-30", Date: "2026-01-01", Note: "first gate", Now: fixedNow(),
	})
	require.NoError(t, err)

	res, err := r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: "gate-thirty", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "Anchor renamed: gate-30 → gate-thirty — 2026-01-01.", res.Ack)
	assert.Nil(t, res.Retired, "an id-bearing rename writes one record")
	assert.Equal(t, engine.Anchor{
		ID:         added.Anchor.ID, // the identity is what a rename must not move
		Label:      "gate-thirty",
		Date:       "2026-01-01",
		Note:       "first gate",
		RecordedAt: fixedNow().Format(time.RFC3339),
	}, res.Anchor)

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	require.Len(t, log.History, 2, "a rename appends; it never edits")
	assert.Equal(t, added.Anchor, log.History[0], "the superseded record is unchanged")

	latest := engine.LatestAnchors(log)
	require.Len(t, latest, 1, "one identity, one folded entry")
	assert.Equal(t, "gate-thirty", latest[0].Label)
	_, old := engine.FindActiveAnchor(latest, "gate-30")
	assert.False(t, old, "the old name leaves the active surface")
}

// TestAnchorRename_DaysSinceUnchanged proves a rename costs the user nothing:
// the same milestone counts the same number of days before and after, because
// the date rides along with the identity.
func TestAnchorRename_DaysSinceUnchanged(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)

	before, err := r.Metrics(fixedNow())
	require.NoError(t, err)
	require.Len(t, before.Metrics.Anchors, 1)

	_, err = r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: "gate-thirty", Now: fixedNow()})
	require.NoError(t, err)

	after, err := r.Metrics(fixedNow())
	require.NoError(t, err)
	require.Len(t, after.Metrics.Anchors, 1)
	assert.Equal(t, before.Metrics.Anchors[0].DaysSince, after.Metrics.Anchors[0].DaysSince)
	assert.Equal(t, before.Metrics.Anchors[0].ID, after.Metrics.Anchors[0].ID, "the identity is stable")
	assert.Equal(t, "gate-thirty", after.Metrics.Anchors[0].Label, "only the display name moved")
	assert.Empty(t, after.Metrics.AnchorsSunset, "an id-bearing rename retires nothing")
}

// TestAnchorRename_LegacyAdoptionPair renames an anchor recorded before ids
// existed. It is adopted into the identity model by a single write of two
// records: an id-less stub retiring the synthetic identity, and a minted record
// carrying the milestone forward.
func TestAnchorRename_LegacyAdoptionPair(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	seeded := engine.Anchor{
		Label: "gate-30", Date: "2026-01-01", Note: "first gate", RecordedAt: "2026-06-01T09:00:00Z",
	}
	require.NoError(t, a.AppendAnchor(seeded))

	res, err := r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: "gate-thirty", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t,
		"Anchor renamed: gate-30 → gate-thirty — 2026-01-01. The old name is retired and appears in anchors_sunset.",
		res.Ack, "the retired stub is announced, not discovered",
	)

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	require.Len(t, log.History, 3, "exactly two records were appended")
	assert.Equal(t, seeded, log.History[0], "history is never rewritten")

	stub := log.History[1]
	assert.Empty(t, stub.ID, "the stub stays id-less so it retires the synthetic identity")
	assert.Equal(t, "gate-30", stub.Label)
	assert.Equal(t, "2026-01-01", stub.Date)
	assert.Empty(t, stub.Note, "the note travels forward, it does not stay on the stub")
	assert.Equal(t, engine.AnchorStateSunset, stub.State)
	require.NotNil(t, res.Retired)
	assert.Equal(t, stub, *res.Retired)

	adopted := log.History[2]
	assert.Equal(t, res.Anchor, adopted)
	assert.Contains(t, adopted.ID, engine.AnchorIDPrefix, "the milestone is adopted into the id model")
	assert.Equal(t, "gate-thirty", adopted.Label)
	assert.Equal(t, "2026-01-01", adopted.Date, "the original date carries forward")
	assert.Equal(t, "first gate", adopted.Note, "the original note carries forward")

	// The projected surfaces: the old name is gone from the counting one and
	// appears exactly once, addressably, on the retired one.
	m, err := r.Metrics(fixedNow())
	require.NoError(t, err)
	require.Len(t, m.Metrics.Anchors, 1)
	assert.Equal(t, "gate-thirty", m.Metrics.Anchors[0].Label)
	require.Len(t, m.Metrics.AnchorsSunset, 1, "exactly one retired stub")
	assert.Equal(t, "gate-30", m.Metrics.AnchorsSunset[0].Label)
	assert.Equal(t, "legacy:gate-30", m.Metrics.AnchorsSunset[0].ID)
}

// TestAnchorRename_LegacyPairIsAtomic: the two-record write either lands whole
// or not at all. A failed write leaves the history byte-unchanged — never one
// half of a rename, which would put both names on the active surface at once.
func TestAnchorRename_LegacyPairIsAtomic(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, a.AppendAnchor(engine.Anchor{
		Label: "gate-30", Date: "2026-01-01", Note: "first gate", RecordedAt: "2026-06-01T09:00:00Z",
	}))

	anchorsPath := filepath.Join(home, "engine", "anchors.json")
	before, err := os.ReadFile(anchorsPath)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(anchorsPath, 0o400)) // still readable, so the failure is the write
	t.Cleanup(func() { _ = os.Chmod(anchorsPath, 0o600) })

	_, err = r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: "gate-thirty", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")

	after, err := os.ReadFile(anchorsPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "no half-applied rename")
}

// TestAnchorRename_RawStoreHasNoLegacyID: the synthetic identity is produced at
// the projection boundary and never written. The adoption path is the one that
// could leak it, so it is the one that proves it does not.
func TestAnchorRename_RawStoreHasNoLegacyID(t *testing.T) {
	r, a, home := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, a.AppendAnchor(engine.Anchor{
		Label: "gate-30", Date: "2026-01-01", RecordedAt: "2026-06-01T09:00:00Z",
	}))

	_, err := r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: "gate-thirty", Now: fixedNow()})
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(home, "engine", "anchors.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), engine.LegacyAnchorIDPrefix, "a synthesized id is never persisted")
}

// TestAnchorRename_OntoSunsetLabelSucceeds: a name held only by a retired
// anchor is free, exactly as it is for `anchor add`. This is the one rejection
// that must not fire.
func TestAnchorRename_OntoSunsetLabelSucceeds(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-02-01", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.AnchorRename(AnchorRenameRequest{Label: "sobriety", NewLabel: "gate-30", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "gate-30", res.Anchor.Label)

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	latest := engine.LatestAnchors(log)
	active, ok := engine.FindActiveAnchor(latest, "gate-30")
	require.True(t, ok, "the freed name is live again, under the renamed milestone")
	assert.Equal(t, "2026-02-01", active.Date, "it is the renamed anchor, not the retired one")
	assert.Len(t, latest, 2, "the retired run and the renamed one stay distinct")
}

// TestAnchorRename_RejectsTakenActiveLabel refuses to move a name an active
// anchor already holds: at most one active anchor per label is what lets every
// verb take a bare label.
func TestAnchorRename_RejectsTakenActiveLabel(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-02", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: "sobriety", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorLabelTaken)
	assert.Equal(t,
		`an active anchor already uses "sobriety" — sunset or rename it first; nothing was saved.`, err.Error(),
	)

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Len(t, log.History, 2, "a rejection appends nothing")
}

// TestAnchorRename_RejectsUnknownLabel names the labels that do exist, the same
// remedy the sunset verb offers.
func TestAnchorRename_RejectsUnknownLabel(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-02", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.AnchorRename(AnchorRenameRequest{Label: "gate-3", NewLabel: "gate-thirty", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorUnknownLabel)
	assert.Equal(t, `no anchor named "gate-3" — recorded anchors: gate-30, sobriety`, err.Error())

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Len(t, log.History, 2, "a rejection appends nothing")
}

// TestAnchorRename_RejectsAlreadySunset: a retired anchor is history, not an
// editable record.
func TestAnchorRename_RejectsAlreadySunset(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.AnchorSunset(AnchorSunsetRequest{Label: "gate-30", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: "gate-thirty", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorAlreadySunset)
	assert.Contains(t, err.Error(), `"gate-30" was already sunset on 2026-07-05`)
	assert.Contains(t, err.Error(), "nothing was saved")

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Len(t, log.History, 2, "a rejection appends nothing")
}

// TestAnchorRename_RejectsSameLabel refuses a rename to the name the anchor
// already has, before the store is even read.
func TestAnchorRename_RejectsSameLabel(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: " gate-30 ", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorLabelTaken)
	assert.Equal(t, `"gate-30" is already its name; nothing was saved.`, err.Error())

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Len(t, log.History, 1, "a rejection appends nothing")
}

// TestAnchorRename_RejectsEmptyNewLabel refuses a blank new name with the same
// copy `anchor add` raises for a blank label.
func TestAnchorRename_RejectsEmptyNewLabel(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: "   ", Now: fixedNow()})
	require.ErrorIs(t, err, ErrAnchorRejected)
	assert.Contains(t, err.Error(), "non-empty label")

	log, err := a.ReadAnchors()
	require.NoError(t, err)
	assert.Len(t, log.History, 1, "a rejection appends nothing")
}

// TestAnchorRename_AppendFailureSurfaced: a rename whose append cannot be
// written is surfaced, not swallowed — the anchor keeps its old name.
func TestAnchorRename_AppendFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)

	anchorsPath := filepath.Join(home, "engine", "anchors.json")
	require.NoError(t, os.Chmod(anchorsPath, 0o400)) // still readable, so the failure is the write
	t.Cleanup(func() { _ = os.Chmod(anchorsPath, 0o600) })

	_, err = r.AnchorRename(AnchorRenameRequest{Label: "gate-30", NewLabel: "gate-thirty", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}
