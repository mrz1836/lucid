package router

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestDayLines_EngineStates covers the engine-day summary for missed and
// partial records (the day view is inventory, never a score).
func TestDayLines_EngineStates(t *testing.T) {
	r := bootedObs(t)
	require.NoError(t, r.Store().ScaffoldEngine())

	// A missed day.
	require.NoError(t, r.Store().WriteEngineDay(engine.DayRecord{
		DayID: "day_2026_07_02", LogicalDate: "2026-07-02", Missed: true,
	}))
	res, err := r.DayView("2026-07-02", nowEDT())
	require.NoError(t, err)
	assert.Contains(t, strings.Join(res.Lines, "\n"), "Engine: missed")
}

func TestDayLines_PartialRecord(t *testing.T) {
	r := bootedObs(t)
	require.NoError(t, r.Store().ScaffoldEngine())
	require.NoError(t, r.Store().WriteEngineDay(engine.DayRecord{
		DayID: "day_2026_07_02", LogicalDate: "2026-07-02", Partial: true, Mode: engine.ModeYellow,
	}))
	res, err := r.DayView("2026-07-02", nowEDT())
	require.NoError(t, err)
	joined := strings.Join(res.Lines, "\n")
	assert.Contains(t, joined, "Engine: partial")
	assert.Contains(t, joined, "mode yellow")
}

// TestDayView_RecordedNeitherState covers the fallback "recorded" summary for
// a day record that is neither completed, missed, nor partial.
func TestDayView_RecordedNeitherState(t *testing.T) {
	r := bootedObs(t)
	require.NoError(t, r.Store().ScaffoldEngine())
	require.NoError(t, r.Store().WriteEngineDay(engine.DayRecord{
		DayID: "day_2026_07_02", LogicalDate: "2026-07-02",
	}))
	res, err := r.DayView("2026-07-02", nowEDT())
	require.NoError(t, err)
	assert.Contains(t, strings.Join(res.Lines, "\n"), "Engine: recorded")
}
