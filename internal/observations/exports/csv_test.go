package exports

import (
	"strings"
	"testing"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func painEvent(id, date string, intensity int) observations.Event {
	return observations.Event{ID: id, Kind: observations.KindPain, LogicalDate: date, Payload: map[string]any{"intensity": intensity}}
}

func moodEvent(id, date string, level int) observations.Event {
	return observations.Event{ID: id, Kind: observations.KindMood, LogicalDate: date, Payload: map[string]any{"level": level}}
}

func TestBuildSeriesRows_JoinsAndAggregates(t *testing.T) {
	pain := []observations.Event{
		painEvent("obs_2026_07_01_001", "2026-07-01", 3),
		painEvent("obs_2026_07_01_002", "2026-07-01", 6), // same day → max wins
		painEvent("obs_2026_07_02_001", "2026-07-02", 4),
	}
	mood := []observations.Event{
		moodEvent("obs_2026_07_02_001", "2026-07-02", 2),
	}
	capByDate := map[string]int{"2026-07-01": 4, "2026-07-03": 5}

	rows := BuildSeriesRows(pain, mood, capByDate)
	require.Len(t, rows, 3)
	// Sorted by date.
	assert.Equal(t, "2026-07-01", rows[0].Date)
	assert.Equal(t, "2026-07-02", rows[1].Date)
	assert.Equal(t, "2026-07-03", rows[2].Date)

	require.NotNil(t, rows[0].Pain)
	assert.Equal(t, 6, *rows[0].Pain, "max pain that day")
	require.NotNil(t, rows[0].Capacity)
	assert.Equal(t, 4, *rows[0].Capacity)
	assert.Nil(t, rows[0].Mood, "no mood that day → empty")

	require.NotNil(t, rows[1].Mood)
	assert.Equal(t, 2, *rows[1].Mood)

	// A day with only capacity still appears.
	assert.Nil(t, rows[2].Pain)
	require.NotNil(t, rows[2].Capacity)
	assert.Equal(t, 5, *rows[2].Capacity)
}

func TestSeriesCSV_ValidWithEmptyCells(t *testing.T) {
	rows := BuildSeriesRows(
		[]observations.Event{painEvent("obs_2026_07_01_001", "2026-07-01", 6)},
		nil,
		map[string]int{"2026-07-01": 4},
	)
	csv := SeriesCSV(rows)
	lines := strings.Split(strings.TrimRight(csv, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "logical_date,pain,mood,capacity", lines[0])
	assert.Equal(t, "2026-07-01,6,,4", lines[1], "mood is an empty cell")
	assert.True(t, strings.HasSuffix(csv, "\n"))
}

func TestBuildSeriesRows_Deterministic(t *testing.T) {
	pain := []observations.Event{painEvent("obs_2026_07_02_001", "2026-07-02", 5), painEvent("obs_2026_07_01_001", "2026-07-01", 3)}
	a := SeriesCSV(BuildSeriesRows(pain, nil, nil))
	b := SeriesCSV(BuildSeriesRows(pain, nil, nil))
	assert.Equal(t, a, b, "the CSV is byte-stable across reruns")
}
