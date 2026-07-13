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
