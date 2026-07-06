package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ts is a UTC instant builder for the storm-timing fixtures.
//
//nolint:unparam // y is kept explicit for fixture readability even though the cases share 2026
func ts(y int, mo time.Month, d, hh, mm int) time.Time {
	return time.Date(y, mo, d, hh, mm, 0, 0, time.UTC)
}

func TestValidStormLabel(t *testing.T) {
	h := StormHistory{Clauses: []string{"clause-1", "clause-2"}}
	assert.True(t, ValidStormLabel(h, "clause-1"))
	assert.True(t, ValidStormLabel(h, StormUnwritten))
	assert.False(t, ValidStormLabel(h, "clause-9"))
	assert.False(t, ValidStormLabel(StormHistory{}, "anything"))
}

func TestDeclareStorm(t *testing.T) {
	h := StormHistory{Clauses: []string{"clause-1"}}

	ev, err := DeclareStorm(h, "clause-1", ts(2026, 7, 14, 7, 5))
	require.NoError(t, err)
	assert.Equal(t, StormDeclared, ev.Event)
	assert.Equal(t, "clause-1", ev.Label)
	assert.Equal(t, "2026-07-14T07:05:00Z", ev.At)

	_, err = DeclareStorm(h, "no-such-clause", ts(2026, 7, 14, 7, 5))
	require.Error(t, err, "an unknown label is rejected")

	// `/storm unwritten` is always declarable.
	ev, err = DeclareStorm(StormHistory{}, StormUnwritten, ts(2026, 7, 14, 7, 5))
	require.NoError(t, err)
	assert.Equal(t, StormUnwritten, ev.Label)
}

func TestConfirmStorm(t *testing.T) {
	declared := StormHistory{History: []StormEvent{{At: "2026-07-14T07:05:00Z", Event: StormDeclared, Label: "clause-1"}}}

	ev, err := ConfirmStorm(declared, "J.", "confirmed — that's real.", ts(2026, 7, 14, 9, 40), 14)
	require.NoError(t, err)
	assert.Equal(t, StormConfirmed, ev.Event)
	assert.Equal(t, "J.", ev.By)
	assert.Equal(t, "2026-07-28", ev.Through, "through = confirm date + 14 days")

	// Zero duration falls back to the default.
	ev, err = ConfirmStorm(declared, "J.", "ok", ts(2026, 7, 14, 9, 40), 0)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-28", ev.Through)

	_, err = ConfirmStorm(StormHistory{}, "J.", "ok", ts(2026, 7, 14, 9, 40), 14)
	assert.Error(t, err, "nothing to confirm without a pending declaration")
}

func TestRenewStorm(t *testing.T) {
	standing := StormHistory{DurationDays: 14, History: []StormEvent{
		{At: "2026-07-14T09:40:00Z", Event: StormConfirmed, Through: "2026-07-28"},
	}}

	ev, err := RenewStorm(standing, ts(2026, 7, 27, 8, 0), 14)
	require.NoError(t, err)
	assert.Equal(t, StormRenewed, ev.Event)
	assert.Equal(t, "2026-08-10", ev.Through)

	// A second renewal is rejected — a storm renews once.
	renewedOnce := standing.WithEvents(ev)
	_, err = RenewStorm(renewedOnce, ts(2026, 8, 9, 8, 0), 14)
	require.Error(t, err)

	// No standing storm ⇒ nothing to renew.
	_, err = RenewStorm(StormHistory{}, ts(2026, 7, 27, 8, 0), 14)
	assert.Error(t, err)
}

func TestEndStorm(t *testing.T) {
	standing := StormHistory{History: []StormEvent{{Event: StormConfirmed, Through: "2026-07-28"}}}
	ev, err := EndStorm(standing, ts(2026, 7, 20, 8, 0))
	require.NoError(t, err)
	assert.Equal(t, StormEnded, ev.Event)

	_, err = EndStorm(StormHistory{}, ts(2026, 7, 20, 8, 0))
	assert.Error(t, err, "nothing to end when no storm stands")
}

func TestRenewalCount(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{Event: StormConfirmed}, {Event: StormRenewed}, {Event: StormRenewed},
	}}
	assert.Equal(t, 2, RenewalCount(h))
	assert.Equal(t, 0, RenewalCount(StormHistory{}))
}

func TestStandingConfirmedDate(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-07-14T07:05:00Z", Event: StormDeclared},
		{At: "2026-07-14T09:40:00Z", Event: StormConfirmed, Through: "2026-07-28"},
	}}
	assert.Equal(t, "2026-07-14", StandingConfirmedDate(h, time.UTC))
	assert.Empty(t, StandingConfirmedDate(StormHistory{}, time.UTC))
}

// TestStormInForce_RespectsLowerBound: a confirmed storm applies from its
// declaration forward, never to a day before it ("entry never annotates a day
// before its declaration").
func TestStormInForce_RespectsLowerBound(t *testing.T) {
	h := StormHistory{History: []StormEvent{
		{At: "2026-07-14T07:05:00Z", Event: StormDeclared},
		{At: "2026-07-14T09:40:00Z", Event: StormConfirmed, Through: "2026-07-28"},
	}}
	assert.False(t, StormInForce(h, ts(2026, 7, 10, 0, 0), time.UTC), "a day before the declaration is not a storm day")
	assert.True(t, StormInForce(h, ts(2026, 7, 20, 0, 0), time.UTC))
	assert.False(t, StormInForce(h, ts(2026, 7, 29, 0, 0), time.UTC), "past through is not in force")

	// A bare ambush window lower-bounds itself.
	win := StormHistory{Windows: []StormWindow{{Label: "w1", Start: "2026-11-02", End: "2026-11-09"}}}
	assert.False(t, StormInForce(win, ts(2026, 11, 1, 0, 0), time.UTC))
	assert.True(t, StormInForce(win, ts(2026, 11, 5, 0, 0), time.UTC))
}

// TestStormBookkeeping_Lapse: a pending declaration past the 72h window lapses.
func TestStormBookkeeping_Lapse(t *testing.T) {
	h := StormHistory{History: []StormEvent{{At: "2026-07-14T07:05:00Z", Event: StormDeclared, Label: "clause-1"}}}

	// Within the window: no lapse.
	events, lapsed := StormBookkeeping(h, ts(2026, 7, 16, 7, 0), time.UTC)
	assert.Empty(t, events)
	assert.False(t, lapsed)

	// Past 72h: exactly one lapse event.
	events, lapsed = StormBookkeeping(h, ts(2026, 7, 17, 8, 0), time.UTC)
	require.Len(t, events, 1)
	assert.Equal(t, StormLapsed, events[0].Event)
	assert.True(t, lapsed)
}

// TestStormBookkeeping_Expire: a standing storm past its through date expires.
func TestStormBookkeeping_Expire(t *testing.T) {
	h := StormHistory{History: []StormEvent{{At: "2026-07-14T09:40:00Z", Event: StormConfirmed, Through: "2026-07-28"}}}

	events, lapsed := StormBookkeeping(h, ts(2026, 7, 28, 9, 0), time.UTC)
	assert.Empty(t, events, "on the through date the storm still stands")
	assert.False(t, lapsed)

	events, _ = StormBookkeeping(h, ts(2026, 7, 29, 9, 0), time.UTC)
	require.Len(t, events, 1)
	assert.Equal(t, StormExpired, events[0].Event)
}

// TestStormBookkeeping_EnterWindow: an ambush window whose start has arrived
// is entered — appended with no send, forward from the window start.
func TestStormBookkeeping_EnterWindow(t *testing.T) {
	h := StormHistory{Windows: []StormWindow{{Label: "w1", Start: "2026-11-02", End: "2026-11-09"}}}

	// Before the window: nothing.
	events, _ := StormBookkeeping(h, ts(2026, 11, 1, 9, 0), time.UTC)
	assert.Empty(t, events)

	// On the start date: exactly one entered event with the window's end.
	events, _ = StormBookkeeping(h, ts(2026, 11, 2, 9, 0), time.UTC)
	require.Len(t, events, 1)
	assert.Equal(t, StormEntered, events[0].Event)
	assert.Equal(t, "w1", events[0].Label)
	assert.Equal(t, "2026-11-09", events[0].Through)

	// Already entered (history carries it): no duplicate.
	entered := h.WithEvents(events[0])
	events, _ = StormBookkeeping(entered, ts(2026, 11, 3, 9, 0), time.UTC)
	assert.Empty(t, events, "a window already entered is not re-entered")
}

func TestStormHistory_WithEvents(t *testing.T) {
	h := StormHistory{History: []StormEvent{{Event: StormDeclared}}}
	out := h.WithEvents(StormEvent{Event: StormConfirmed})
	assert.Len(t, out.History, 2)
	assert.Len(t, h.History, 1, "the receiver is not mutated")
}
