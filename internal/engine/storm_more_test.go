package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStandingConfirmedDate_NilLocDefaultsUTC: a nil location falls back to UTC
// rather than panicking, so a caller that has not resolved a profile location
// still gets an honest civil date.
func TestStandingConfirmedDate_NilLocDefaultsUTC(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-07-14T09:40:00Z", Event: StormConfirmed, Through: "2026-07-28"},
	}}
	assert.Equal(t, "2026-07-14", StandingConfirmedDate(h, nil))
}

// TestStandingConfirmedDate_MalformedTimestampIsEmpty: a standing event whose
// timestamp is hand-corrupted yields "" rather than a garbage date or a panic.
func TestStandingConfirmedDate_MalformedTimestampIsEmpty(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "not-a-timestamp", Event: StormConfirmed, Through: "2026-07-28"},
	}}
	assert.Empty(t, StandingConfirmedDate(h, time.UTC))
}

// TestStormInForce_NilLocDefaultsUTC: StormInForce tolerates a nil location the
// same way the rest of the storm math does.
func TestStormInForce_NilLocDefaultsUTC(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-07-14T07:05:00Z", Event: StormDeclared},
		{At: "2026-07-14T09:40:00Z", Event: StormConfirmed, Through: "2026-07-28"},
	}}
	assert.True(t, StormInForce(h, ts(2026, 7, 20, 0, 0), nil))
}

// TestStormBookkeeping_NilLocDefaultsUTC: the bookkeeping pass defaults a nil
// location to UTC, so a lapse is still detected without a resolved profile.
func TestStormBookkeeping_NilLocDefaultsUTC(t *testing.T) {
	h := StormHistory{History: []StormEvent{{At: "2026-07-14T07:05:00Z", Event: StormDeclared, Label: "clause-1"}}}
	events, lapsed := StormBookkeeping(h, ts(2026, 7, 17, 8, 0), nil)
	require.Len(t, events, 1)
	assert.Equal(t, StormLapsed, events[0].Event)
	assert.True(t, lapsed)
}

// TestPendingDeclaredAt_MalformedTimestampIsNotPending: a trailing `declared`
// event with a corrupt timestamp reads as "no pending declaration" rather than
// panicking — so bookkeeping over a hand-edited file never lapses a garbage
// declaration.
func TestPendingDeclaredAt_MalformedTimestampIsNotPending(t *testing.T) {
	h := StormHistory{History: []StormEvent{{At: "garbage", Event: StormDeclared, Label: "clause-1"}}}
	_, pending := pendingDeclaredAt(h)
	assert.False(t, pending)

	// And the same corrupt declaration never lapses through the public pass.
	events, lapsed := StormBookkeeping(h, ts(2026, 7, 20, 8, 0), time.UTC)
	assert.Empty(t, events)
	assert.False(t, lapsed)
}

// TestWindowEntered_MatchesOnEndAndLabel: the entered-window probe matches on
// both the window end and the label, so two windows sharing an end date are not
// confused, and an entry is recognized only for its own window.
func TestWindowEntered_MatchesOnEndAndLabel(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{Event: StormEntered, Label: "w1", Through: "2026-11-09"},
	}}
	assert.True(t, windowEntered(h, StormWindow{Label: "w1", Start: "2026-11-02", End: "2026-11-09"}))
	assert.False(t, windowEntered(h, StormWindow{Label: "w2", Start: "2026-11-02", End: "2026-11-09"}),
		"a different label is not the same entry")
	assert.False(t, windowEntered(StormHistory{}, StormWindow{Label: "w1", End: "2026-11-09"}))
}

// TestStormStart_EnteredResolvesWindowStart: for an entered ambush the storm's
// lower bound is the window's own start date, resolved from the matching window
// definition rather than the entry timestamp.
func TestStormStart_EnteredResolvesWindowStart(t *testing.T) {
	h := StormHistory{
		Windows: []StormWindow{{Label: "w1", Start: "2026-11-02", End: "2026-11-09"}},
		History: []StormEvent{{At: "2026-11-02T17:00:00Z", Event: StormEntered, Label: "w1", Through: "2026-11-09"}},
	}
	start, ok := stormStart(h, time.UTC)
	require.True(t, ok)
	assert.Equal(t, DateOf(ts(2026, 11, 2, 0, 0)), start)
}

// TestStormStart_EnteredWithNoMatchingWindowFallsBackToTimestamp: an entered
// event whose window definition is gone falls back to the entry timestamp's
// civil date rather than losing the lower bound entirely.
func TestStormStart_EnteredWithNoMatchingWindowFallsBackToTimestamp(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-11-02T17:00:00Z", Event: StormEntered, Label: "orphan", Through: "2026-11-09"},
	}}
	start, ok := stormStart(h, time.UTC)
	require.True(t, ok)
	assert.Equal(t, DateOf(ts(2026, 11, 2, 0, 0)), start)
}

// TestStormStart_ConfirmedWithoutDeclaredFallsBackToTimestamp: a confirmed
// storm with no `declared` event in history (a hand-edited file) resolves its
// lower bound from the confirmation timestamp.
func TestStormStart_ConfirmedWithoutDeclaredFallsBackToTimestamp(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-07-14T09:40:00Z", Event: StormConfirmed, Through: "2026-07-28"},
	}}
	start, ok := stormStart(h, time.UTC)
	require.True(t, ok)
	assert.Equal(t, DateOf(ts(2026, 7, 14, 0, 0)), start)
}

// TestStormStart_UnparseableTimestampHasNoStart: a standing event whose only
// timestamp is corrupt yields no lower bound (ok=false), so StormInForce trusts
// the bare-standing answer instead of a garbage date.
func TestStormStart_UnparseableTimestampHasNoStart(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "not-a-time", Event: StormConfirmed, Through: "2026-07-28"},
	}}
	_, ok := stormStart(h, time.UTC)
	assert.False(t, ok)
}

// TestEarliestDeclaredDate_NoneDeclared: a history with no `declared` event has
// no earliest declaration date.
func TestEarliestDeclaredDate_NoneDeclared(t *testing.T) {
	h := StormHistory{History: []StormEvent{{At: "2026-07-14T09:40:00Z", Event: StormConfirmed}}}
	_, ok := earliestDeclaredDate(h, time.UTC)
	assert.False(t, ok)
}
