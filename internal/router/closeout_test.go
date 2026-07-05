package router

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// atUTC returns a UTC instant on a date at a given time — the host clock
// is trusted, so a fixed zone keeps the logical-day math deterministic.
//
//nolint:unparam // y is kept explicit for fixture readability even though the cases share 2026
func atUTC(y int, mo time.Month, d, hh, mm int) time.Time {
	return time.Date(y, mo, d, hh, mm, 0, 0, time.UTC)
}

// compactLinks is the standard dfx link set for the default chain.
func compactLinks() map[string]string {
	return map[string]string{"journal": engine.StatusDone, "dock": engine.StatusFloor, "read": engine.StatusSkipped}
}

// readDay folds and returns a day record via the adapter.
func readDay(t *testing.T, a *storage.Adapter, dayID string) engine.DayRecord {
	t.Helper()
	rec, found, err := a.ReadEngineDayFolded(dayID)
	require.NoError(t, err)
	require.Truef(t, found, "day %s should exist", dayID)
	return rec
}

// TestCloseout_WritesRecordAndJournal is the AC-4 core: a close-out writes
// a day record and a journal line in raw/ with `command: /closeout` and
// valid frontmatter, and stamps chain_start.
func TestCloseout_WritesRecordAndJournal(t *testing.T) {
	r, a, home := newBootedRouter(t)
	now := atUTC(2026, 7, 5, 22, 41)

	res, err := r.Closeout(CloseoutRequest{
		Now: now, Links: compactLinks(), Capacity: 3, LimiterTag: "wrist", Journal: "Long day but the chain ran.",
	})
	require.NoError(t, err)
	assert.Equal(t, "day_2026_07_05", res.DayID)
	assert.True(t, res.Completed)
	assert.Equal(t, "Closed out 2026-07-05 — streak 1.", res.Ack)

	// Day record: completed, floor_day (dock at floor), profile stamped.
	rec := readDay(t, a, "day_2026_07_05")
	assert.True(t, rec.Completed)
	assert.True(t, rec.FloorDay)
	assert.Equal(t, 3, rec.Capacity)
	assert.Equal(t, "wrist", rec.LimiterTag)
	assert.Equal(t, engine.DefaultProfile, rec.Profile)
	assert.Equal(t, res.RawID, rec.RawEntryID)

	// Journal landed in raw/ with the right command + valid frontmatter.
	rawPath := filepath.Join(home, "raw", "2026", "07", res.RawID+".md")
	content, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	require.NoError(t, storage.ValidateRawFrontmatter(content))
	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "/closeout", doc.Fields["command"])
	assert.Contains(t, doc.Body, "Long day but the chain ran.")

	// chain_start was stamped exactly once, to the first completed day.
	chain, err := a.ReadChainConfig()
	require.NoError(t, err)
	require.NotNil(t, chain.ChainStart)
	assert.Equal(t, "2026-07-05", *chain.ChainStart)
}

// TestCloseout_RolloverAttribution drives the four binding rollover cases
// end-to-end through the written record's logical_date.
func TestCloseout_RolloverAttribution(t *testing.T) {
	t.Run("23:50 → today", func(t *testing.T) {
		r, _, _ := newBootedRouter(t)
		res, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 23, 50), Links: compactLinks(), Journal: "x"})
		require.NoError(t, err)
		assert.Equal(t, "2026-07-05", res.LogicalDate)
	})
	t.Run("03:50 → yesterday", func(t *testing.T) {
		r, _, _ := newBootedRouter(t)
		res, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 6, 3, 50), Links: compactLinks(), Journal: "x"})
		require.NoError(t, err)
		assert.Equal(t, "2026-07-05", res.LogicalDate)
	})
	t.Run("04:12 yesterday unrecorded → yesterday", func(t *testing.T) {
		r, _, _ := newBootedRouter(t)
		res, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 6, 4, 12), Links: compactLinks(), Journal: "x"})
		require.NoError(t, err)
		assert.Equal(t, "2026-07-05", res.LogicalDate)
	})
	t.Run("04:12 yesterday completed → today", func(t *testing.T) {
		r, _, _ := newBootedRouter(t)
		// Record yesterday first.
		_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "y"})
		require.NoError(t, err)
		res, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 6, 4, 12), Links: compactLinks(), Journal: "x"})
		require.NoError(t, err)
		assert.Equal(t, "2026-07-06", res.LogicalDate)
	})
}

// TestCloseout_Idempotent is the same-day repeat no-op (engine-module.md
// §Error states).
func TestCloseout_Idempotent(t *testing.T) {
	r, a, home := newBootedRouter(t)
	now := atUTC(2026, 7, 5, 22, 0)
	_, err := r.Closeout(CloseoutRequest{Now: now, Links: compactLinks(), Journal: "first"})
	require.NoError(t, err)
	before := countFiles(t, home, "raw")

	res, err := r.Closeout(CloseoutRequest{Now: now.Add(time.Minute), Links: compactLinks(), Journal: "second"})
	require.NoError(t, err)
	assert.True(t, res.Idempotent)
	assert.Equal(t, "Already closed out — streak 1.", res.Ack)
	assert.Equal(t, before, countFiles(t, home, "raw"), "idempotent close-out writes no new journal")

	// Only one correction-free record exists.
	rec := readDay(t, a, "day_2026_07_05")
	assert.Empty(t, rec.Corrections)
}

// TestCloseout_ChainStartStampedOnce confirms chain_start never moves after
// the first completed close-out.
func TestCloseout_ChainStartStampedOnce(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "d1"})
	require.NoError(t, err)
	_, err = r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 6, 22, 0), Links: compactLinks(), Journal: "d2"})
	require.NoError(t, err)

	chain, err := a.ReadChainConfig()
	require.NoError(t, err)
	require.NotNil(t, chain.ChainStart)
	assert.Equal(t, "2026-07-05", *chain.ChainStart)

	status, err := a.ReadEngineStatus()
	require.NoError(t, err)
	assert.Equal(t, 2, status.CurrentStreak)
}

// TestCloseout_Skip records an honest miss with no journal line.
func TestCloseout_Skip(t *testing.T) {
	r, a, home := newBootedRouter(t)
	res, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Skip: true})
	require.NoError(t, err)
	assert.True(t, res.Missed)
	assert.Equal(t, "Recorded a miss for 2026-07-05.", res.Ack)
	assert.Equal(t, 0, countFiles(t, home, "raw"), "a skip writes no journal line")

	rec := readDay(t, a, "day_2026_07_05")
	assert.True(t, rec.Missed)
	assert.False(t, rec.Completed)

	// A skip on an already-recorded day is an idempotent no-op.
	res, err = r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 30), Skip: true})
	require.NoError(t, err)
	assert.True(t, res.Idempotent)
}

// TestCloseout_PartialThenComplete covers the interrupted-flow path: a
// partial writes partial:true, a later close-out completes it via a folded
// correction (engine-module.md §Error states).
func TestCloseout_PartialThenComplete(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	now := atUTC(2026, 7, 5, 22, 0)

	// Partial where the survival link did not run → not completed.
	res, err := r.Closeout(CloseoutRequest{
		Now: now, Partial: true, Links: map[string]string{"dock": engine.StatusDone}, Journal: "partial",
	})
	require.NoError(t, err)
	assert.Equal(t, partialAckCopy, res.Ack)
	rec := readDay(t, a, "day_2026_07_05")
	assert.True(t, rec.Partial)
	assert.False(t, rec.Completed)

	// A full close-out completes the day via an appended correction.
	res, err = r.Closeout(CloseoutRequest{Now: now.Add(5 * time.Minute), Links: compactLinks(), Journal: "complete"})
	require.NoError(t, err)
	assert.True(t, res.Completed)
	folded := readDay(t, a, "day_2026_07_05")
	assert.True(t, folded.Completed)

	// The original body kept partial:true; a correction was appended.
	raw, _, err := a.ReadEngineDay("day_2026_07_05")
	require.NoError(t, err)
	assert.True(t, raw.Partial)
	assert.Len(t, raw.Corrections, 1)
}

func TestCloseout_ForceToday(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	// At 04:12 with yesterday unrecorded, the night-shift rule would
	// back-attribute to yesterday; `today` forces the current logical day.
	res, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 6, 4, 12), ForceToday: true, Links: compactLinks(), Journal: "x"})
	require.NoError(t, err)
	assert.Equal(t, "2026-07-06", res.LogicalDate)
}
