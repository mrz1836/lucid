package router

import (
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
