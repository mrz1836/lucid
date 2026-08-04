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

// standingStorm seeds storm.json with a confirmed storm that stands through
// 2026-07-28.
func standingStorm(t *testing.T, a *storage.Adapter) {
	t.Helper()
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, a.AppendStormEvents(
		engine.StormEvent{At: "2026-07-14T07:05:00Z", Event: engine.StormDeclared, Label: "clause-1"},
		engine.StormEvent{At: "2026-07-14T09:40:00Z", Event: engine.StormConfirmed, Through: "2026-07-28"},
	))
}

// TestStorm_DeclareUnwritten: `/storm unwritten` is always declarable and acks
// pending witness confirmation.
func TestStorm_DeclareUnwritten(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.Storm("unwritten", atUTC(2026, 7, 14, 7, 5))
	require.NoError(t, err)
	assert.False(t, res.Rejected)
	assert.Equal(t, engine.StormDeclared, res.Event)
	assert.Equal(t, "storm declared (unwritten) — pending witness confirmation (72h).", res.Ack)

	h, err := a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, h.History, 1)
	assert.Equal(t, engine.StormDeclared, h.History[0].Event)
}

// TestStorm_UnknownLabelRejected: a label that is neither a registered clause
// nor `unwritten` is rejected with the fixed copy and writes nothing.
func TestStorm_UnknownLabelRejected(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.Storm("no-such-clause", atUTC(2026, 7, 14, 7, 5))
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Equal(t, stormUnknownLabelMsg, res.Ack)

	h, err := a.ReadStormState()
	require.NoError(t, err)
	assert.Empty(t, h.History, "a rejected declaration appends nothing")
}

// TestStorm_UsageWhenEmpty rejects an empty argument.
func TestStorm_UsageWhenEmpty(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	res, err := r.Storm("   ", atUTC(2026, 7, 14, 7, 5))
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Equal(t, stormUsageMsg, res.Ack)
}

// TestStorm_RenewOnceThenReject: a `/storm` re-issued while standing renews
// once; a second renewal is rejected as a season, not a storm.
func TestStorm_RenewOnceThenReject(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	standingStorm(t, a)

	first, err := r.Storm("clause-1", atUTC(2026, 7, 20, 8, 0))
	require.NoError(t, err)
	assert.False(t, first.Rejected)
	assert.Equal(t, engine.StormRenewed, first.Event)
	assert.Equal(t, "2026-08-03", first.Through)

	// Still standing (through 08-03), but the one renewal is spent.
	second, err := r.Storm("clause-1", atUTC(2026, 8, 1, 8, 0))
	require.NoError(t, err)
	assert.True(t, second.Rejected)
	assert.Equal(t, stormRenewTwiceMsg, second.Ack)
}

// TestStorm_End: `/storm end` ends a standing storm; with none standing it is
// rejected.
func TestStorm_End(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	none, err := r.Storm("end", atUTC(2026, 7, 20, 8, 0))
	require.NoError(t, err)
	assert.True(t, none.Rejected)
	assert.Equal(t, stormNoStandingMsg, none.Ack)

	standingStorm(t, a)
	ended, err := r.Storm("end", atUTC(2026, 7, 20, 8, 0))
	require.NoError(t, err)
	assert.False(t, ended.Rejected)
	assert.Equal(t, engine.StormEnded, ended.Event)

	h, err := a.ReadStormState()
	require.NoError(t, err)
	assert.Equal(t, engine.StormEnded, h.History[len(h.History)-1].Event)
	standing, _ := engine.StormStanding(h, atUTC(2026, 7, 21, 8, 0), nil)
	assert.False(t, standing, "an ended storm no longer stands")
}

// TestStorm_ScaffoldFails surfaces an engine-tree scaffold failure.
func TestStorm_ScaffoldFails(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	require.NoError(t, os.Chmod(home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	_, err := r.Storm("unwritten", atUTC(2026, 7, 14, 7, 5))
	assert.Error(t, err)
}

// TestStorm_CorruptStormErrors surfaces a malformed storm.json.
func TestStorm_CorruptStormErrors(t *testing.T) {
	r, a, home := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, os.WriteFile(filepath.Join(home, "engine", "storm.json"), []byte("{bad"), 0o600))
	_, err := r.Storm("unwritten", atUTC(2026, 7, 14, 7, 5))
	assert.Error(t, err)
}

// TestStorm_AppendFailureSurfaced: a declaration whose history append cannot be
// written (read-only storm.json) is surfaced, not swallowed.
func TestStorm_AppendFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	stormPath := filepath.Join(home, "engine", "storm.json")
	require.NoError(t, os.Chmod(stormPath, 0o400))
	t.Cleanup(func() { _ = os.Chmod(stormPath, 0o600) })
	_, err := r.Storm("unwritten", atUTC(2026, 7, 14, 7, 5))
	assert.Error(t, err)
}

// TestStorm_BackdatedDeclare: a storm too incapacitating to declare at the time
// can be declared after the fact, and the event carries the day it happened.
func TestStorm_BackdatedDeclare(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.StormCommand(StormRequest{
		Arg: "unwritten", DayArg: "@yesterday", Now: atUTC(2026, 7, 14, 9, 0),
	})
	require.NoError(t, err)
	assert.False(t, res.Rejected)
	assert.Equal(t, engine.StormDeclared, res.Event)

	h, err := a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, h.History, 1)
	assert.Equal(t, "2026-07-13T00:00:00Z", h.History[0].At)
}

// TestStorm_BackdatedDeclareKeepsTime: a `--day` carrying a time of day records
// that instant, not midnight.
func TestStorm_BackdatedDeclareKeepsTime(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.StormCommand(StormRequest{
		Arg: "unwritten", DayArg: "@yesterday 19:30", Now: atUTC(2026, 7, 14, 9, 0),
	})
	require.NoError(t, err)

	h, err := a.ReadStormState()
	require.NoError(t, err)
	require.Len(t, h.History, 1)
	assert.Equal(t, "2026-07-13T19:30:00Z", h.History[0].At)
}

// TestStorm_BackdatedRenewInsideWindow: a renewal dated inside the standing
// window succeeds, and `through` derives from the backdated instant rather
// than from now.
func TestStorm_BackdatedRenewInsideWindow(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	standingStorm(t, a)

	res, err := r.StormCommand(StormRequest{
		Arg: "clause-1", DayArg: "2026-07-20", Now: atUTC(2026, 7, 22, 8, 0),
	})
	require.NoError(t, err)
	assert.False(t, res.Rejected)
	assert.Equal(t, engine.StormRenewed, res.Event)
	assert.Equal(t, "2026-08-03", res.Through, "14 days out from the backdated instant, not from now")
}

// TestStorm_BackdatedEndWithNoStandingRejected: standing is evaluated at the
// backdated instant, so ending a storm at a moment before it stood is refused
// by the existing check — no new guard needed.
func TestStorm_BackdatedEndWithNoStandingRejected(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	standingStorm(t, a)
	before, err := a.ReadStormState()
	require.NoError(t, err)

	res, err := r.StormCommand(StormRequest{
		Arg: "end", DayArg: "2026-07-10", Now: atUTC(2026, 7, 20, 8, 0),
	})
	require.NoError(t, err)
	assert.True(t, res.Rejected)

	after, err := a.ReadStormState()
	require.NoError(t, err)
	assert.Len(t, after.History, len(before.History), "a rejected end appends nothing")
}

// TestStorm_OutOfOrderRejected: history folds in order and the standing
// computation reads the last matching event, so an event dated before the last
// recorded one is incoherent input rather than a silent corruption of the fold.
func TestStorm_OutOfOrderRejected(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	standingStorm(t, a)

	res, err := r.StormCommand(StormRequest{
		Arg: "clause-1", DayArg: "2026-07-13", Now: atUTC(2026, 7, 20, 8, 0),
	})
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Equal(t, stormOutOfOrderMsg, res.Ack)

	h, err := a.ReadStormState()
	require.NoError(t, err)
	assert.Len(t, h.History, 2, "nothing was appended")
}

// TestStorm_BackdatedToLastEventInstantAccepted: the guard rejects only what
// precedes the last event, so re-dating to that same instant still lands.
func TestStorm_BackdatedToLastEventInstantAccepted(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	standingStorm(t, a)

	res, err := r.StormCommand(StormRequest{
		Arg: "clause-1", DayArg: "2026-07-14 09:40", Now: atUTC(2026, 7, 20, 8, 0),
	})
	require.NoError(t, err)
	assert.False(t, res.Rejected)
	assert.Equal(t, engine.StormRenewed, res.Event)
}

// TestStorm_FutureDayRejected: `--day` is the strict tier, so a day that has
// not happened yet is a clean error with nothing appended.
func TestStorm_FutureDayRejected(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.StormCommand(StormRequest{
		Arg: "unwritten", DayArg: "2026-07-20", Now: atUTC(2026, 7, 14, 9, 0),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not happened yet")

	h, err := a.ReadStormState()
	require.NoError(t, err)
	assert.Empty(t, h.History)
}

// TestStorm_UnreadableDayRejected: a typo never becomes a storm dated today.
func TestStorm_UnreadableDayRejected(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.StormCommand(StormRequest{
		Arg: "unwritten", DayArg: "@yesterdya", Now: atUTC(2026, 7, 14, 9, 0),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")

	h, err := a.ReadStormState()
	require.NoError(t, err)
	assert.Empty(t, h.History)
}

// TestStorm_BackdatedRenewOnceStillHolds: backdating buys no extra renewals.
func TestStorm_BackdatedRenewOnceStillHolds(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	standingStorm(t, a)

	first, err := r.StormCommand(StormRequest{
		Arg: "clause-1", DayArg: "2026-07-20", Now: atUTC(2026, 7, 22, 8, 0),
	})
	require.NoError(t, err)
	require.False(t, first.Rejected)

	second, err := r.StormCommand(StormRequest{
		Arg: "clause-1", DayArg: "2026-07-21", Now: atUTC(2026, 7, 22, 8, 0),
	})
	require.NoError(t, err)
	assert.True(t, second.Rejected)
	assert.Equal(t, stormRenewTwiceMsg, second.Ack)
}

// TestStorm_UsageBeforeDayResolution: an empty argument is a usage rejection
// even when `--day` is unreadable — the command names no verb to date.
func TestStorm_UsageBeforeDayResolution(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	res, err := r.StormCommand(StormRequest{Arg: "  ", DayArg: "@yesterdya", Now: atUTC(2026, 7, 14, 9, 0)})
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Equal(t, stormUsageMsg, res.Ack)
}
