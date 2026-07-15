package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestMode_AcceptedBeforeBell declares a mode in the afternoon: it writes the
// mode-only base record for today and acks it.
func TestMode_AcceptedBeforeBell(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.Mode(engine.ModeYellow, atUTC(2026, 7, 5, 14, 2))
	require.NoError(t, err)
	assert.False(t, res.Rejected)
	assert.False(t, res.Invalid)
	assert.Equal(t, engine.ModeYellow, res.Mode)
	assert.Equal(t, "2026-07-05", res.LogicalDate)
	assert.Equal(t, "Mode set to yellow for 2026-07-05.", res.Ack)

	rec := readDay(t, a, "day_2026_07_05")
	assert.Equal(t, engine.ModeYellow, rec.Mode)
	assert.NotEmpty(t, rec.ModeDeclaredAt)
	assert.False(t, rec.Completed)
	assert.False(t, rec.Missed)
}

// TestMode_RejectedAfterBell: a declaration after the bell is rejected with
// the fixed copy and writes nothing.
func TestMode_RejectedAfterBell(t *testing.T) {
	r, a, home := newBootedRouter(t)
	res, err := r.Mode(engine.ModeRed, atUTC(2026, 7, 5, 22, 0))
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Equal(t, "Mode is fixed at the bell (19:00). Tonight runs as declared; the budget absorbs hard days.", res.Ack)
	assert.Equal(t, 0, countFiles(t, home, "engine/days"), "a rejected mode writes no record")

	_, found, err := a.ReadEngineDay("day_2026_07_05")
	require.NoError(t, err)
	assert.False(t, found)
}

// TestMode_InvalidName rejects an unknown mode with no disk effect — it
// returns before even scaffolding the engine tree.
func TestMode_InvalidName(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	res, err := r.Mode("purple", atUTC(2026, 7, 5, 14, 0))
	require.NoError(t, err)
	assert.True(t, res.Invalid)
	assert.Equal(t, modeInvalidMsg, res.Ack)

	_, found, err := a.ReadEngineDay("day_2026_07_05")
	require.NoError(t, err)
	assert.False(t, found)
}

// TestMode_FirstDeclarationWins: a second `/mode` before the bell is an
// idempotent no-op reporting the mode that already stands.
func TestMode_FirstDeclarationWins(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.Mode(engine.ModeYellow, atUTC(2026, 7, 5, 14, 0))
	require.NoError(t, err)

	res, err := r.Mode(engine.ModeRed, atUTC(2026, 7, 5, 15, 0))
	require.NoError(t, err)
	assert.True(t, res.Idempotent)
	assert.Equal(t, engine.ModeYellow, res.Mode)
	assert.Equal(t, "Mode already set to yellow for 2026-07-05.", res.Ack)

	rec := readDay(t, a, "day_2026_07_05")
	assert.Equal(t, engine.ModeYellow, rec.Mode, "first declaration wins")
}

// TestMode_DeclaredModeSurvivesCloseout: the evening close-out folds the
// practice result in via corrections without touching the declared mode
// (mode is immutable — engine §2).
func TestMode_DeclaredModeSurvivesCloseout(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.Mode(engine.ModeYellow, atUTC(2026, 7, 5, 14, 0))
	require.NoError(t, err)

	res, err := r.Closeout(CloseoutRequest{
		Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Capacity: 3, Journal: "the chain ran",
	})
	require.NoError(t, err)
	assert.True(t, res.Completed)

	folded := readDay(t, a, "day_2026_07_05")
	assert.Equal(t, engine.ModeYellow, folded.Mode, "declared mode preserved through close-out")
	assert.True(t, folded.Completed)

	// The base body kept the mode; the completion came in as a correction.
	raw, _, err := a.ReadEngineDay("day_2026_07_05")
	require.NoError(t, err)
	assert.Equal(t, engine.ModeYellow, raw.Mode)
	assert.Len(t, raw.Corrections, 1)
}

// TestMode_ProfileBellInRejectCopy: under a named profile the reject copy
// names that profile's bell time, not the default.
func TestMode_ProfileBellInRejectCopy(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	// Switch to nights (bell 08:30) effective the next day, then declare on
	// that day after its 08:30 bell.
	_, err := r.Profile("nights", atUTC(2026, 7, 5, 22, 0))
	require.NoError(t, err)
	res, err := r.Mode(engine.ModeGreen, atUTC(2026, 7, 6, 9, 0))
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Contains(t, res.Ack, "(08:30)")
}

// TestMode_CorruptChainErrors surfaces a malformed chain.json rather than
// silently defaulting.
func TestMode_CorruptChainErrors(t *testing.T) {
	r, _, home := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	writeCorruptChain(t, home)
	_, err := r.Mode(engine.ModeGreen, atUTC(2026, 7, 5, 14, 0))
	assert.Error(t, err)
}

// TestMode_CorruptProfileErrors surfaces a malformed profile.json (the
// governing-clocks read) rather than defaulting silently.
func TestMode_CorruptProfileErrors(t *testing.T) {
	r, _, home := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	require.NoError(t, os.WriteFile(filepath.Join(home, "engine", "profile.json"), []byte("{bad"), 0o600))
	_, err := r.Mode(engine.ModeGreen, atUTC(2026, 7, 5, 14, 0))
	assert.Error(t, err)
}

// TestMode_BadClockErrors: a chain with a malformed clock string fails the
// clock resolution rather than mis-scheduling the bell.
func TestMode_BadClockErrors(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	chain, err := r.store.ReadChainConfig()
	require.NoError(t, err)
	chain.BellTime = "99:99"
	require.NoError(t, r.store.WriteChainConfig(chain))
	_, err = r.Mode(engine.ModeGreen, atUTC(2026, 7, 5, 14, 0))
	assert.Error(t, err)
}

// TestMode_ScaffoldFails surfaces an engine-tree scaffold failure.
func TestMode_ScaffoldFails(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	require.NoError(t, os.Chmod(home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	_, err := r.Mode(engine.ModeGreen, atUTC(2026, 7, 5, 14, 0))
	assert.Error(t, err)
}

// TestMode_WriteDayFails: an accepted declaration whose record write fails is
// surfaced, not swallowed.
func TestMode_WriteDayFails(t *testing.T) {
	skipIfRoot(t)
	r, _, _ := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	daysDir := filepath.Join(r.store.Home(), "engine", "days")
	require.NoError(t, os.Chmod(daysDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(daysDir, 0o700) })
	_, err := r.Mode(engine.ModeGreen, atUTC(2026, 7, 5, 14, 0))
	assert.Error(t, err)
}

// TestMode_RebuildFails: the record writes but the derived status rebuild
// fails (engine dir read-only, days writable) — the command surfaces it.
func TestMode_RebuildFails(t *testing.T) {
	skipIfRoot(t)
	r, _, _ := newBootedRouter(t)
	require.NoError(t, r.store.ScaffoldEngine())
	engineDir := filepath.Join(r.store.Home(), "engine")
	require.NoError(t, os.Chmod(engineDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(engineDir, 0o700) })
	_, err := r.Mode(engine.ModeGreen, atUTC(2026, 7, 5, 14, 0))
	assert.Error(t, err)
}
