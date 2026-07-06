package router

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

func TestParsePacketArg(t *testing.T) {
	cases := []struct {
		arg     string
		start   string
		all     bool
		wantErr bool
	}{
		{"", "", false, false},
		{"all", "", true, false},
		{"@2026-06-01", "2026-06-01", false, false},
		{"@bad", "", false, true},
		{"nonsense", "", false, true},
	}
	for _, tc := range cases {
		start, all, err := parsePacketArg(tc.arg)
		if tc.wantErr {
			require.Errorf(t, err, "arg %q", tc.arg)
			continue
		}
		require.NoErrorf(t, err, "arg %q", tc.arg)
		assert.Equal(t, tc.start, start)
		assert.Equal(t, tc.all, all)
	}
}

func TestClinicianPacket_PostsOnlyPath(t *testing.T) {
	r := bootedObs(t)
	require.NoError(t, r.Store().ScaffoldEngine())

	out, err := r.ClinicianPacket("", nowEDT())
	require.NoError(t, err)
	assert.Equal(t, out.Path, out.Message, "only the path is posted")
	assert.Contains(t, out.Path, "packet_clinician_")

	// The message carries no packet body content.
	body, err := os.ReadFile(out.Path)
	require.NoError(t, err)
	assert.NotEqual(t, string(body), out.Message)
}

func TestSeriesExport(t *testing.T) {
	r := bootedObs(t)
	require.NoError(t, r.Store().ScaffoldEngine())
	capture(t, r, "pain", "6", "knee")

	out, err := r.SeriesExport(nowEDT())
	require.NoError(t, err)
	assert.Contains(t, out.Path, "series.csv")
	body, err := os.ReadFile(out.Path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "logical_date,pain,mood,capacity")
}

func TestCuriosity_MissingLocationThenSpent(t *testing.T) {
	r := bootedObs(t)

	// No location on file → the missing-location micro-question fires once.
	ask, err := r.Curiosity(nowEDT(), false)
	require.NoError(t, err)
	assert.Contains(t, ask, "/obs where")

	// Budget spent for the day → silence on the next call.
	ask2, err := r.Curiosity(nowEDT(), false)
	require.NoError(t, err)
	assert.Empty(t, ask2)
}

func TestCuriosity_NoneWhenLocationKnown(t *testing.T) {
	r := bootedObs(t)
	capture(t, r, "obs", "where", "Lisbon") // location now on file

	ask, err := r.Curiosity(nowEDT(), false)
	require.NoError(t, err)
	assert.Empty(t, ask, "with a location on file and no pain-no-site, nothing fires")
}

func TestCapture_InterventionShowsPacketPointerOnce(t *testing.T) {
	r := bootedObs(t)

	first := capture(t, r, "obs", "intervention", "physio", "session")
	assert.Contains(t, first.Ack, packetPointerLine, "the first intervention ack points to the packet")

	// A week later, still inside 30 days → the pointer does not repeat.
	res, err := r.Capture(CaptureRequest{
		Tokens: []string{"obs", "intervention", "injection"},
		Now:    nowEDT().AddDate(0, 0, 7),
	})
	require.NoError(t, err)
	assert.NotContains(t, res.Ack, packetPointerLine, "the pointer appears at most once per 30 days")
	assert.Equal(t, 1, strings.Count(first.Ack+res.Ack, packetPointerLine))
}

func TestDayView_NoLocationNoteWhenWeatherEnabled(t *testing.T) {
	r := bootedObs(t)
	// Enable the weather enricher.
	cfg, err := r.Store().ReadObservationsConfig()
	require.NoError(t, err)
	for i := range cfg.Enrichers {
		if cfg.Enrichers[i].Name == observations.EnricherWeather {
			cfg.Enrichers[i].Enabled = true
		}
	}
	require.NoError(t, r.Store().SaveObservationsConfig(cfg))

	// A day with a capture but no location on file → the note appears.
	capture(t, r, "pain", "6", "knee")
	res, err := r.DayView("", nowEDT())
	require.NoError(t, err)
	assert.Contains(t, strings.Join(res.Lines, "\n"), "weather: no location on file")
}
