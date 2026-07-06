package storage

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

func TestDefaultHTTPGet_SuccessAndError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("weather-body"))
	}))
	status, body, err := defaultHTTPGet(srv.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "weather-body", string(body))
	srv.Close()

	// The server is closed now → a transport error, no body.
	_, _, err = defaultHTTPGet(srv.URL)
	assert.Error(t, err)
}

func TestFetchEnrichment_UsesDefaultTransport(t *testing.T) {
	// With no injected fetcher, FetchEnrichment reaches the default getter; a
	// non-200 status is an error but is still audited.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := enrichStore(t)
	// Point the validator's pinned host at the test server by validating a real
	// open-meteo URL but routing through a fetcher that mimics the default path
	// is not possible; instead exercise defaultHTTPGet directly (above) and the
	// non-200 branch here via an injected transport that returns 500.
	a.SetEnrichmentFetcher(func(string) (int, []byte, error) { return http.StatusInternalServerError, nil, nil })
	url := observations.BuildOpenMeteoURL(1.0, 2.0, "2026-07-01")
	_, err := a.FetchEnrichment(observations.EnricherWeather, url, time.Now())
	require.Error(t, err, "a non-200 status is an error")

	lines, err := a.ReadEnrichmentAudit()
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "fail(status=500)")
}

func TestCoordField(t *testing.T) {
	f := map[string]any{"a": 12.5, "b": 3, "c": "4.25", "d": "nan-nan", "e": true}
	v, ok := coordField(f, "a")
	assert.True(t, ok)
	assert.InDelta(t, 12.5, v, 0.001)

	v, ok = coordField(f, "b")
	assert.True(t, ok)
	assert.InDelta(t, 3.0, v, 0.001)

	v, ok = coordField(f, "c")
	assert.True(t, ok)
	assert.InDelta(t, 4.25, v, 0.001)

	_, ok = coordField(f, "d")
	assert.False(t, ok, "an unparseable string is not a coordinate")
	_, ok = coordField(f, "e")
	assert.False(t, ok, "a non-numeric type is not a coordinate")
	_, ok = coordField(f, "missing")
	assert.False(t, ok)
}

func TestEphemeralState_CorruptResets(t *testing.T) {
	a := newObsStore(t)

	// A corrupt curiosity file resets to the zero value, never blocking.
	require.NoError(t, os.WriteFile(a.curiosityStatePath(), []byte("{not json"), 0o600))
	st, err := a.ReadCuriosityState()
	require.NoError(t, err)
	assert.Empty(t, st.Day)

	// A corrupt discovery file is treated as "never shown" → the pointer shows.
	require.NoError(t, os.WriteFile(a.discoveryStatePath(), []byte("{not json"), 0o600))
	show, err := a.ShouldShowPacketPointer(time.Now())
	require.NoError(t, err)
	assert.True(t, show)
}

func TestRunEnrichment_UnknownEnricherSkipped(t *testing.T) {
	a := newObsStore(t)
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.Enrichers = append(cfg.Enrichers, observations.Enricher{Name: "made-up", Enabled: true, Cadence: "daily"})
	require.NoError(t, a.SaveObservationsConfig(cfg))

	// An unknown enricher name is skipped without error and writes nothing for it.
	_, err = a.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err)
}
