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
