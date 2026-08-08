package engine

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backfillFixture is the one synthetic fixture the adoption matrix folds
// against: an active pre-id anchor, a sunset pre-id anchor, two pre-id records
// sharing a label across time (a correction), and one record that already
// carries an id. Every label is synthetic.
func backfillFixture() AnchorLog {
	return AnchorLog{Version: AnchorVersion, History: []Anchor{
		// active pre-id anchor
		{Label: "gate-a", Date: "2026-01-01", Note: "the first gate", RecordedAt: "2026-01-01T10:00:00Z"},
		// two pre-id records sharing a label across time (a correction of gate-b)
		{Label: "gate-b", Date: "2026-02-01", RecordedAt: "2026-02-01T10:00:00Z"},
		{Label: "gate-b", Date: "2026-02-15", RecordedAt: "2026-02-20T10:00:00Z"},
		// a sunset pre-id anchor: its retirement date must be preserved verbatim
		{
			Label: "cessation-x", Date: "2025-06-01",
			RecordedAt: "2026-03-01T10:00:00Z", State: AnchorStateSunset,
		},
		// one record that already carries an id: never adopted, id unchanged
		{ID: "anchor_2026_07_01_a", Label: "milestone-c", Date: "2026-07-01", RecordedAt: "2026-07-01T10:00:00Z"},
	}}
}

// withAdoptions returns log with the planned adoption records appended, without
// mutating log's backing array.
func withAdoptions(log AnchorLog, plans []Anchor) AnchorLog {
	return AnchorLog{Version: log.Version, History: append(slices.Clone(log.History), plans...)}
}

// TestLatestAnchors_FoldIdenticalAfterAdoption is the central proof that
// appending the planned adoption records changes nothing the fold projects
// except the published id. Labels, dates, notes, states, and recorded_at are
// identical before and after, the count does not change (no fork), the sunset
// retirement date SunsetDate renders is preserved, and the active label set is
// unchanged.
func TestLatestAnchors_FoldIdenticalAfterAdoption(t *testing.T) {
	log := backfillFixture()
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)

	before := LatestAnchors(log)
	after := LatestAnchors(withAdoptions(log, PlanAnchorAdoptions(log, now)))

	require.Len(t, after, len(before), "adoption must not fork or drop an anchor")

	// The folded set is sorted by label, and every label here is unique, so
	// index-by-index comparison is a stable, total match.
	for i := range before {
		b, a := before[i], after[i]
		require.Equal(t, b.Label, a.Label, "label and order unchanged at index %d", i)
		assert.Equal(t, b.Date, a.Date, "date unchanged for %s", b.Label)
		assert.Equal(t, b.Note, a.Note, "note unchanged for %s", b.Label)
		assert.Equal(t, b.State, a.State, "state unchanged for %s", b.Label)
		assert.Equal(t, b.RecordedAt, a.RecordedAt, "recorded_at unchanged for %s", b.Label)
	}

	// SunsetDate and LatestSunsetFor read RecordedAt, so a restamped adoption
	// record would silently move the retirement date. It must not.
	sBefore, okBefore := LatestSunsetFor(before, "cessation-x")
	sAfter, okAfter := LatestSunsetFor(after, "cessation-x")
	require.True(t, okBefore)
	require.True(t, okAfter)
	assert.Equal(t, sBefore.RecordedAt, sAfter.RecordedAt, "the retirement RecordedAt is copied verbatim")
	assert.Equal(t, SunsetDate(sBefore), SunsetDate(sAfter), "the rendered retirement date is unchanged")
	assert.Equal(t, "2026-03-01", SunsetDate(sAfter))

	assert.Equal(t, AnchorLabels(before), AnchorLabels(after), "the active label set is unchanged")

	// The one intended, documented change: a backfilled anchor now publishes
	// its minted id instead of the read-time-only legacy:<label>. A record that
	// already carried an id keeps it.
	for i := range before {
		bID := ProjectAnchor(before[i]).ID
		aID := ProjectAnchor(after[i]).ID
		if strings.HasPrefix(bID, LegacyAnchorIDPrefix) {
			assert.True(t, strings.HasPrefix(aID, AnchorIDPrefix),
				"%s now publishes a minted id, was %s now %s", before[i].Label, bID, aID)
		} else {
			assert.Equal(t, bID, aID, "%s already had an id and keeps it", before[i].Label)
		}
	}
}

// TestPlanAnchorAdoptions_IsIdempotent proves a second run against an
// already-adopted log plans nothing: once every legacy identity has been
// adopted it folds under a minted id and is no longer a candidate.
func TestPlanAnchorAdoptions_IsIdempotent(t *testing.T) {
	log := backfillFixture()
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)

	plans := PlanAnchorAdoptions(log, now)
	require.Len(t, plans, 3, "gate-a, gate-b, and cessation-x are the three pre-id anchors")

	again := PlanAnchorAdoptions(withAdoptions(log, plans), now.Add(24*time.Hour))
	assert.Empty(t, again, "a fully adopted log plans nothing further")
}

// TestPlanAnchorAdoptions_DoesNotReissueSlots proves two anchors adopted on the
// same day take distinct slots, and a subsequent mint for that day advances
// past both — the backfill threads its growing log through NextAnchorID so it
// never reissues a slot it has already consumed.
func TestPlanAnchorAdoptions_DoesNotReissueSlots(t *testing.T) {
	log := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{Label: "gate-a", Date: "2026-01-01", RecordedAt: "2026-01-01T10:00:00Z"},
		{Label: "gate-b", Date: "2026-02-01", RecordedAt: "2026-02-01T10:00:00Z"},
	}}
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)

	plans := PlanAnchorAdoptions(log, now)
	require.Len(t, plans, 2)
	assert.Equal(t, "anchor_2026_08_08_a", plans[0].ID, "sorted by identity: legacy:gate-a first")
	assert.Equal(t, "anchor_2026_08_08_b", plans[1].ID)
	assert.NotEqual(t, plans[0].ID, plans[1].ID, "two same-day adoptions get distinct slots")

	assert.Equal(t, "anchor_2026_08_08_c", NextAnchorID(withAdoptions(log, plans), now),
		"a subsequent mint for that day advances past both adoptions")
}

// TestPlanAnchorAdoptions_CopiesFieldsVerbatim pins the record shape: the
// minted id and the two new fields are set, while label/date/note/state and —
// critically — recorded_at are copied from the folded winner unchanged.
func TestPlanAnchorAdoptions_CopiesFieldsVerbatim(t *testing.T) {
	log := backfillFixture()
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)

	plans := PlanAnchorAdoptions(log, now)
	byLabel := map[string]Anchor{}
	for _, p := range plans {
		byLabel[p.Label] = p
	}

	cessation, ok := byLabel["cessation-x"]
	require.True(t, ok)
	assert.Equal(t, "legacy:cessation-x", cessation.Adopts, "adopts records the identity taken over")
	assert.Equal(t, now.Format(time.RFC3339), cessation.AdoptedAt, "adopted_at is the real backfill time")
	assert.Equal(t, "2026-03-01T10:00:00Z", cessation.RecordedAt, "recorded_at is copied verbatim, not restamped")
	assert.Equal(t, AnchorStateSunset, cessation.State, "the sunset state is carried")
	assert.True(t, strings.HasPrefix(cessation.ID, AnchorIDPrefix), "the id is freshly minted")

	// gate-b's winner is the later correction, so the adoption copies that
	// record's date and recorded_at, not the superseded original's.
	gateB, ok := byLabel["gate-b"]
	require.True(t, ok)
	assert.Equal(t, "2026-02-15", gateB.Date, "the correction's date is adopted, not the original's")
	assert.Equal(t, "2026-02-20T10:00:00Z", gateB.RecordedAt)
}

// TestPlanAnchorAdoptions_EmptyLog plans nothing for an empty ledger and never
// errors — a fresh install has no anchors to adopt.
func TestPlanAnchorAdoptions_EmptyLog(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	assert.Empty(t, PlanAnchorAdoptions(AnchorLog{Version: AnchorVersion, History: []Anchor{}}, now))
}

// TestPlanAnchorAdoptions_SkipsAlreadyIDdAnchors proves a log whose anchors all
// carry ids plans nothing: only pre-id records are adopted.
func TestPlanAnchorAdoptions_SkipsAlreadyIDdAnchors(t *testing.T) {
	log := AnchorLog{Version: AnchorVersion, History: []Anchor{
		{ID: "anchor_2026_07_01_a", Label: "milestone-c", Date: "2026-07-01", RecordedAt: "2026-07-01T10:00:00Z"},
	}}
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	assert.Empty(t, PlanAnchorAdoptions(log, now), "a record that already carries an id is never adopted")
}
