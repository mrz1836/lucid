package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// TestDayView_JoinsAndByteStable: /day joins engine + observations (incl. a
// spanning range event) + entries, and the render is byte-stable across
// reruns (AC-7).
func TestDayView_JoinsAndByteStable(t *testing.T) {
	r := bootedObs(t)
	require.NoError(t, r.Store().ScaffoldEngine())

	// Engine day record.
	require.NoError(t, r.Store().WriteEngineDay(engine.DayRecord{
		DayID: "day_2026_07_02", LogicalDate: "2026-07-02", Mode: engine.ModeGreen,
		Completed: true, Capacity: 3, Links: map[string]string{"floor": engine.StatusDone},
	}))

	// A raw entry recorded that day.
	_, err := r.Store().WriteRaw(storage.RawEntry{
		RecordedAt: time.Date(2026, 7, 2, 8, 15, 0, 0, edt), OccurredAt: time.Date(2026, 7, 2, 8, 15, 0, 0, edt),
		OccurredAtPrecision: storage.PrecisionExact, Source: "cli", Command: "/log", Body: "morning",
	})
	require.NoError(t, err)

	// A same-day observation and a prior-night sleep that spans into the day.
	capture(t, r, "pain", "6", "knee")
	end := "2026-07-02T07:10:00-04:00"
	night := observations.Event{
		Schema: observations.Schema, Kind: observations.KindSleep,
		RecordedAt: end, OccurredAt: "2026-07-01T23:00:00-04:00",
		OccurredAtPrecision: observations.PrecisionRange, OccurredAtEnd: &end,
		LogicalDate: "2026-07-01", Source: observations.SourceMicrolog,
		Payload: map[string]any{"quality": 3},
	}
	_, err = r.Store().AppendObservation(night)
	require.NoError(t, err)

	res, err := r.DayView("2026-07-02", nowEDT())
	require.NoError(t, err)
	assert.False(t, res.Empty)

	joined := strings.Join(res.Lines, "\n")
	assert.Contains(t, joined, "Day 2026-07-02")
	assert.Contains(t, joined, "Engine: closed out")
	assert.Contains(t, joined, "Observations:")
	assert.Contains(t, joined, "pain")
	assert.Contains(t, joined, "(spanning)", "the prior night's sleep surfaces on the day it spans")
	assert.Contains(t, joined, "Entries:")

	// Byte-stable across reruns.
	res2, err := r.DayView("2026-07-02", nowEDT())
	require.NoError(t, err)
	assert.Equal(t, res.Lines, res2.Lines)

	// No evaluative language leaks into the day surface.
	for _, banned := range []string{"streak", "keep it up", "score"} {
		assert.NotContains(t, strings.ToLower(joined), banned)
	}
}

// TestDayView_EmptyDayHonestMessage (AC-7 / error-states).
func TestDayView_EmptyDayHonestMessage(t *testing.T) {
	r := bootedObs(t)
	res, err := r.DayView("2026-07-09", nowEDT())
	require.NoError(t, err)
	assert.True(t, res.Empty)
	require.Len(t, res.Lines, 1)
	assert.Equal(t, "No record for 2026-07-09.", res.Lines[0])
}

// TestDayView_TodayBeforeRolloverJoinsLogicalDay is the regression for a
// pre-rollover `/day`: an observation logged at 00:52 files under the prior
// logical day, so `/day` with no argument must resolve "today" to that same
// logical day — the fresh calendar date would read as an empty new day.
func TestDayView_TodayBeforeRolloverJoinsLogicalDay(t *testing.T) {
	r := bootedObs(t)
	require.NoError(t, r.Store().ScaffoldEngine())
	now := time.Date(2026, 7, 11, 0, 52, 0, 0, edt) // before the 04:00 rollover

	logged, err := r.Capture(CaptureRequest{Tokens: []string{"pain", "6", "knee"}, Now: now})
	require.NoError(t, err)
	assert.Equal(t, "2026-07-10", logged.LogicalDate, "a pre-rollover capture files under the prior logical day")

	res, err := r.DayView("", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-10", res.Date)
	assert.False(t, res.Empty)
	assert.Contains(t, strings.Join(res.Lines, "\n"), "pain")
}

// TestDayView_ArgResolution: default is today, "yesterday" is the day before.
func TestDayView_ArgResolution(t *testing.T) {
	r := bootedObs(t)

	today, err := r.DayView("", nowEDT())
	require.NoError(t, err)
	assert.Equal(t, "2026-07-02", today.Date)

	yd, err := r.DayView("yesterday", nowEDT())
	require.NoError(t, err)
	assert.Equal(t, "2026-07-01", yd.Date)

	explicit, err := r.DayView("2026-06-15", nowEDT())
	require.NoError(t, err)
	assert.Equal(t, "2026-06-15", explicit.Date)
}

// TestDayView_SurfacesAttachedMediaRoundTrip is the Phase 5 round-trip (AC-8,
// AC-9): an image attached to today's logical day and a non-image (PDF)
// backdated with @yesterday both surface in `/day` for their own logical day,
// each stored file's sha256 recomputes-and-matches its sidecar, and each media
// carries a referencing raw entry the Retro can find. The two attaches use
// distinct minutes so each predicted raw id resolves to its own entry.
func TestDayView_SurfacesAttachedMediaRoundTrip(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	// An image attached to today's logical day (2026-07-05).
	img := []byte("\xff\xd8\xff synthetic jpeg bytes")
	imgRes, err := r.Attach(AttachRequest{
		Path:    writeTempFile(t, "before.jpg", img),
		Caption: "day 0 before photo",
		Now:     fixedNow(), // 2026-07-05 18:41
	})
	require.NoError(t, err)
	require.Equal(t, "2026-07-05", imgRes.Day)

	// A non-image (PDF) backdated to yesterday (2026-07-04), a distinct minute
	// so its predicted raw id does not collide with the image's.
	pdf := []byte("%PDF-1.7\n… scanned handwritten page …\n%%EOF")
	pdfNow := time.Date(2026, time.July, 5, 19, 15, 0, 0, time.UTC)
	pdfRes, err := r.Attach(AttachRequest{
		Path:    writeTempFile(t, "page.pdf", pdf),
		Caption: "handwritten page",
		DayArg:  "@yesterday",
		Now:     pdfNow,
	})
	require.NoError(t, err)
	require.Equal(t, "2026-07-04", pdfRes.Day)

	// Today's view surfaces the image on its Media line and its referencing raw
	// id in Entries (the raw entry is recorded on the same civil day).
	today, err := r.DayView("2026-07-05", fixedNow())
	require.NoError(t, err)
	assert.False(t, today.Empty)
	todayJoined := strings.Join(today.Lines, "\n")
	assert.Contains(t, todayJoined, "Media:")
	assert.Contains(t, todayJoined, filepath.Base(imgRes.StoredPath))
	assert.Contains(t, todayJoined, "day 0 before photo")
	assert.Contains(t, todayJoined, imgRes.RawID, "the referencing raw entry lists on the same day")
	assertMediaRoundTrip(t, a, today.View, filepath.Base(imgRes.StoredPath), img)

	// Yesterday's view is NOT empty even though its raw entry is filed under the
	// next civil day: the media attribution alone makes the day real, and the
	// PDF surfaces opaquely with its caption.
	yd, err := r.DayView("2026-07-04", fixedNow())
	require.NoError(t, err)
	assert.False(t, yd.Empty, "a media-only backdated day is a real day, not 'No record'")
	ydJoined := strings.Join(yd.Lines, "\n")
	assert.Contains(t, ydJoined, filepath.Base(pdfRes.StoredPath))
	assert.Contains(t, ydJoined, "handwritten page")
	assert.NotContains(t, ydJoined, filepath.Base(imgRes.StoredPath), "the image stays on its own day")
	assertMediaRoundTrip(t, a, yd.View, filepath.Base(pdfRes.StoredPath), pdf)

	// The day view never leaks the raw entry body or an evaluative frame.
	for _, banned := range []string{"score", "streak", "Media attachment media/"} {
		assert.NotContains(t, todayJoined, banned)
		assert.NotContains(t, ydJoined, banned)
	}
}

// assertMediaRoundTrip proves one media record in a day view round-trips: the
// view carries exactly one attachment named storedName, its sha256 recomputes
// from the stored bytes, and its linked raw entry exists and references the
// stored media (AC-9 "each has a referencing raw entry").
func assertMediaRoundTrip(t *testing.T, a *storage.Adapter, view storage.DayView, storedName string, content []byte) {
	t.Helper()
	require.Len(t, view.Media, 1, "the day carries its one attachment for --json consumers")
	rec := view.Media[0]
	assert.Equal(t, storedName, rec.ID)

	// sha256 recomputes from the stored bytes and matches the sidecar.
	assert.Equal(t, sha256Hex(content), rec.SHA256, "sidecar sha matches the input")
	stored, err := os.ReadFile(rec.StoredPath)
	require.NoError(t, err)
	assert.Equal(t, sha256Hex(stored), rec.SHA256, "recomputed sha of the stored file matches")

	// The referencing raw entry exists and points back at the stored media.
	require.NotEmpty(t, rec.RawEntryID, "media links to a raw entry")
	doc, err := a.ReadRaw(rec.RawEntryID)
	require.NoError(t, err)
	assert.Contains(t, doc.EntryText(), rec.ID, "the linked raw entry references the stored media")
}

// TestMediaLine covers the inventory-line render for a stored attachment: the
// bare filename when there is no caption, and `id — caption` when there is.
func TestMediaLine(t *testing.T) {
	assert.Equal(t, "2026-07-05-artifact.bin",
		mediaLine(storage.MediaRecord{ID: "2026-07-05-artifact.bin"}))
	assert.Equal(t, "2026-07-05-before.jpg — day 0 photo",
		mediaLine(storage.MediaRecord{ID: "2026-07-05-before.jpg", Caption: "day 0 photo"}))
	// Whitespace-only caption reads as absent (no dangling dash).
	assert.Equal(t, "2026-07-05-x.png",
		mediaLine(storage.MediaRecord{ID: "2026-07-05-x.png", Caption: "   "}))
}

// TestDayView_PostMidnightGroupsUnderLogicalDay: a bare `lucid log` at 00:33
// and a `--day @2026-09-27` log at 00:35 — both recorded on 2026-09-28, before
// the 04:00 rollover — list under 2026-09-27, the logical day of their
// occurred_at, and not under 2026-09-28, the date their ids carry. A bare
// `/day` at that moment opens on 2026-09-27 too. Synthetic text, injected
// clock and zone.
func TestDayView_PostMidnightGroupsUnderLogicalDay(t *testing.T) {
	r := bootedObs(t)
	require.NoError(t, r.Store().ScaffoldEngine())

	bareAt := time.Date(2026, 9, 28, 0, 33, 0, 0, edt)
	bare, err := r.Log(LogRequest{Text: "a late note about the day just lived", Now: bareAt})
	require.NoError(t, err)
	flagAt := time.Date(2026, 9, 28, 0, 35, 0, 0, edt)
	flagged, err := r.Log(LogRequest{Text: "attributed on purpose", Now: flagAt, DayArg: "@2026-09-27"})
	require.NoError(t, err)

	prior, err := r.DayView("2026-09-27", flagAt)
	require.NoError(t, err)
	assert.False(t, prior.Empty)
	assert.Contains(t, prior.Lines, "Entries: "+flagged.RawID+", "+bare.RawID,
		"both entries list under their logical day, ordered by occurred_at")

	today, err := r.DayView("", flagAt)
	require.NoError(t, err)
	assert.Equal(t, "2026-09-27", today.Date, "a pre-rollover bare /day opens on the day just lived")
	assert.Equal(t, prior.Lines, today.Lines)

	next, err := r.DayView("2026-09-28", flagAt)
	require.NoError(t, err)
	assert.NotContains(t, next.View.RawEntryIDs, bare.RawID, "never grouped by the id's recorded date")
	assert.NotContains(t, next.View.RawEntryIDs, flagged.RawID)
	assert.True(t, next.Empty, "nothing belongs to 2026-09-28 yet")
}

// writeChain rewrites the Ledger's chain.json with edit applied.
func writeChain(t *testing.T, r *Router, edit func(*engine.ChainConfig)) {
	t.Helper()
	chain, err := r.Chain()
	require.NoError(t, err)
	edit(&chain)
	require.NoError(t, r.Store().WriteChainConfig(chain))
}

// TestLogicalRolloverMin_FallbackToDefault: when chain.json is absent,
// unparseable, or its top-level rollover is missing or not a valid HH:MM, the
// resolver falls back to the documented 04:00 — and a bare `lucid log` and the
// day view both attribute on that same boundary (03:59:59 the day before,
// 04:00 its own day).
func TestLogicalRolloverMin_FallbackToDefault(t *testing.T) {
	chainPath := func(r *Router) string { return filepath.Join(r.Store().Home(), "engine", "chain.json") }
	cases := map[string]func(t *testing.T, r *Router){
		"absent": func(t *testing.T, r *Router) {
			require.NoError(t, os.Remove(chainPath(r)))
		},
		"unparseable": func(t *testing.T, r *Router) {
			require.NoError(t, os.WriteFile(chainPath(r), []byte("{not json"), 0o600))
		},
		"missing rollover": func(t *testing.T, r *Router) {
			writeChain(t, r, func(c *engine.ChainConfig) { c.Rollover = "" })
		},
		"invalid rollover": func(t *testing.T, r *Router) {
			writeChain(t, r, func(c *engine.ChainConfig) { c.Rollover = "25:99" })
		},
	}
	for name, breakChain := range cases {
		t.Run(name, func(t *testing.T) {
			r := bootedObs(t)
			require.NoError(t, r.Store().ScaffoldEngine())
			breakChain(t, r)
			assert.Equal(t, observations.DefaultRolloverMin, r.logicalRolloverMin())

			before, err := r.Log(LogRequest{Text: "one second before the rollover", Now: time.Date(2026, 9, 28, 3, 59, 59, 0, edt)})
			require.NoError(t, err)
			assert.Equal(t, "2026-09-27", before.Day)
			at, err := r.Log(LogRequest{Text: "exactly at the rollover", Now: time.Date(2026, 9, 28, 4, 0, 0, 0, edt)})
			require.NoError(t, err)
			assert.Equal(t, "2026-09-28", at.Day)
		})
	}

	t.Run("day view agrees on the fallback boundary", func(t *testing.T) {
		r := bootedObs(t)
		require.NoError(t, r.Store().ScaffoldEngine())
		writeChain(t, r, func(c *engine.ChainConfig) { c.Rollover = "25:99" })

		lastSecond := time.Date(2026, 9, 28, 3, 59, 59, 0, edt)
		before, err := r.Log(LogRequest{Text: "one second before the rollover", Now: lastSecond})
		require.NoError(t, err)
		atRollover := time.Date(2026, 9, 28, 4, 0, 0, 0, edt)
		at, err := r.Log(LogRequest{Text: "exactly at the rollover", Now: atRollover})
		require.NoError(t, err)

		prior, err := r.DayView("", lastSecond)
		require.NoError(t, err)
		assert.Equal(t, "2026-09-27", prior.Date)
		assert.Equal(t, before.Day, prior.Date, "the log and the day view name the same day")
		assert.Equal(t, []string{before.RawID}, prior.View.RawEntryIDs)

		own, err := r.DayView("", atRollover)
		require.NoError(t, err)
		assert.Equal(t, "2026-09-28", own.Date)
		assert.Equal(t, at.Day, own.Date, "the log and the day view name the same day")
		assert.Equal(t, []string{at.RawID}, own.View.RawEntryIDs)
	})
}

// TestLogicalRolloverMin_TopLevelIgnoresProfiles: the resolver reads only the
// top-level chain.json rollover — even with a profile that overrides it
// active — and the day view groups and resolves "today" on that value.
func TestLogicalRolloverMin_TopLevelIgnoresProfiles(t *testing.T) {
	r := bootedObs(t)
	require.NoError(t, r.Store().ScaffoldEngine())
	writeChain(t, r, func(c *engine.ChainConfig) {
		c.Rollover = "05:00"
		c.Profiles = map[string]engine.ProfileClocks{
			"nights": {BellTime: "08:30", TripwireTime: "17:00", Rollover: "12:00"},
		}
	})
	require.NoError(t, r.Store().AppendProfileEvent(engine.ProfileSwitch{
		At: "2026-09-01T09:00:00-04:00", From: engine.DefaultProfile, To: "nights", Effective: "2026-09-01",
	}))

	assert.Equal(t, 5*60, r.logicalRolloverMin(), "top-level rollover, not the active profile's 12:00")

	at := time.Date(2026, 9, 28, 4, 30, 0, 0, edt) // after 04:00, before the 05:00 top-level rollover
	logged, err := r.Log(LogRequest{Text: "between four and five", Now: at})
	require.NoError(t, err)
	assert.Equal(t, "2026-09-27", logged.Day, "the log attributes on the top-level rollover too")

	res, err := r.DayView("", at)
	require.NoError(t, err)
	assert.Equal(t, "2026-09-27", res.Date)
	assert.Equal(t, []string{logged.RawID}, res.View.RawEntryIDs)

	yd, err := r.DayView("yesterday", at)
	require.NoError(t, err)
	assert.Equal(t, "2026-09-26", yd.Date)
}
