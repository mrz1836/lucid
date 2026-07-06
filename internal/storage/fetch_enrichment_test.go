package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// enrichStore scaffolds an isolated Ledger with the weather enricher enabled
// (default config leaves it off) and returns the adapter.
func enrichStore(t *testing.T) *Adapter {
	t.Helper()
	a := newObsStore(t)
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	for i := range cfg.Enrichers {
		if cfg.Enrichers[i].Name == observations.EnricherWeather {
			cfg.Enrichers[i].Enabled = true
		}
	}
	require.NoError(t, a.SaveObservationsConfig(cfg))
	return a
}

// seedPlace writes a place registry record carrying full-precision coordinates
// (a hand-added lat/lon, observations.md §8).
func seedPlace(t *testing.T, a *Adapter, key, name string, lat, lon float64) {
	t.Helper()
	_, err := a.UpdateRegistry(observations.RegistryPlace, key, observations.RegistryPatch{
		DisplayName: name,
		At:          "2026-07-01T09:00:00Z",
		Fields:      map[string]any{"lat": lat, "lon": lon},
	})
	require.NoError(t, err)
}

// seedLocation appends a context.location event on a logical day.
func seedLocation(t *testing.T, a *Adapter, date, placeRef string) {
	t.Helper()
	_, err := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindLocation,
		RecordedAt: date + "T09:00:00Z", OccurredAt: date + "T09:00:00Z",
		OccurredAtPrecision: observations.PrecisionExact, LogicalDate: date,
		Source: observations.SourceMicrolog, Payload: map[string]any{"place_ref": placeRef},
	})
	require.NoError(t, err)
}

// okWeatherFetcher returns a well-formed single-day Open-Meteo response.
func okWeatherFetcher(url string) (int, []byte, error) {
	_ = url
	return 200, []byte(`{"daily":{"time":["2026-07-01"],"temperature_2m_mean":[21.3],"precipitation_sum":[0.0],"pressure_msl_mean":[1015.2],"relative_humidity_2m_mean":[64]}}`), nil
}

// weatherEventsFor counts a day file's weather enricher events.
func weatherEventsFor(t *testing.T, a *Adapter, date string) []observations.Event {
	t.Helper()
	events, _, err := a.ReadObservationsDay(date)
	require.NoError(t, err)
	src := observations.EnricherSource(observations.EnricherWeather)
	var out []observations.Event
	for _, e := range events {
		if e.Kind == observations.KindContextDay && e.Source == src {
			out = append(out, e)
		}
	}
	return out
}

func TestFetchEnrichment_RejectsBadURLBeforeSocket(t *testing.T) {
	a := enrichStore(t)
	called := false
	a.SetEnrichmentFetcher(func(string) (int, []byte, error) { called = true; return 200, nil, nil })

	_, err := a.FetchEnrichment(observations.EnricherWeather, "https://evil.example.com/v1/forecast?latitude=1.00", time.Now())
	require.Error(t, err)
	assert.False(t, called, "a disallowed URL never reaches the transport")
}

func TestFetchEnrichment_WritesAuditLineOnSuccess(t *testing.T) {
	a := enrichStore(t)
	a.SetEnrichmentFetcher(okWeatherFetcher)
	url := observations.BuildOpenMeteoURL(38.72, -9.14, "2026-07-01")

	body, err := a.FetchEnrichment(observations.EnricherWeather, url, time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.NotEmpty(t, body)

	lines, err := a.ReadEnrichmentAudit()
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "\tok\t")
	assert.Contains(t, lines[0], url)
}

func TestFetchEnrichment_FailureWritesAuditNoBody(t *testing.T) {
	a := enrichStore(t)
	a.SetEnrichmentFetcher(func(string) (int, []byte, error) { return 0, nil, fmt.Errorf("timeout") })
	url := observations.BuildOpenMeteoURL(38.72, -9.14, "2026-07-01")

	_, err := a.FetchEnrichment(observations.EnricherWeather, url, time.Now())
	require.Error(t, err)

	lines, err := a.ReadEnrichmentAudit()
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "fail(")
}

func TestRunEnrichment_OneEventPerEnricherPerDay_Idempotent(t *testing.T) {
	a := enrichStore(t)
	a.SetEnrichmentFetcher(okWeatherFetcher)
	seedPlace(t, a, "place_a-river", "Lisbon", 38.7223, -9.1393)
	seedLocation(t, a, "2026-06-01", "place_a-river") // old enough to cover the window

	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	rep, err := a.RunEnrichment(now, time.UTC)
	require.NoError(t, err)
	assert.Positive(t, rep.Written)

	// Each processed day carries exactly one weather event.
	assert.Len(t, weatherEventsFor(t, a, "2026-07-05"), 1)

	// A second run writes nothing new (idempotent) and every day still has one.
	rep2, err := a.RunEnrichment(now, time.UTC)
	require.NoError(t, err)
	assert.Zero(t, rep2.Written, "the rerun is idempotent")
	assert.Positive(t, rep2.Skipped)
	assert.Len(t, weatherEventsFor(t, a, "2026-07-05"), 1, "still exactly one after a rerun")
}

func TestRunEnrichment_AsOfLocation(t *testing.T) {
	a := enrichStore(t)
	a.SetEnrichmentFetcher(okWeatherFetcher)
	seedPlace(t, a, "place_old", "Old", 10.0, 20.0)
	seedPlace(t, a, "place_new", "New", 30.0, 40.0)
	// Move to the new place on D = 07-04; the old place applies before it.
	seedLocation(t, a, "2026-07-02", "place_old")
	seedLocation(t, a, "2026-07-04", "place_new")

	// Backfill for D-1 (07-03) runs on D+3 (07-07).
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	_, err := a.RunEnrichment(now, time.UTC)
	require.NoError(t, err)

	events := weatherEventsFor(t, a, "2026-07-03")
	require.Len(t, events, 1)
	assert.Equal(t, "place_old", events[0].Payload["place_ref"], "the backfill carries the as-of (old) place")
}

func TestRunEnrichment_AuditOnlyPinnedHostQuantized(t *testing.T) {
	a := enrichStore(t)
	a.SetEnrichmentFetcher(okWeatherFetcher)
	seedPlace(t, a, "place_a-river", "SecretStreetName", 38.7223, -9.1393)
	seedLocation(t, a, "2026-06-01", "place_a-river")

	_, err := a.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err)

	lines, err := a.ReadEnrichmentAudit()
	require.NoError(t, err)
	require.NotEmpty(t, lines)
	for _, ln := range lines {
		assert.Contains(t, ln, observations.OpenMeteoHost, "every audited URL is on the pinned host")
		assert.Contains(t, ln, "latitude=38.72")
		// No full-precision coordinates and no content words leak.
		assert.NotContains(t, ln, "38.7223")
		assert.NotContains(t, ln, "9.1393")
		assert.NotContains(t, ln, "SecretStreetName")
	}
}

func TestRunEnrichment_FetchFailureAuditNoEvent(t *testing.T) {
	a := enrichStore(t)
	a.SetEnrichmentFetcher(func(string) (int, []byte, error) { return 0, nil, fmt.Errorf("timeout") })
	seedPlace(t, a, "place_a-river", "Lisbon", 38.72, -9.14)
	seedLocation(t, a, "2026-06-01", "place_a-river")

	rep, err := a.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err)
	assert.Positive(t, rep.Failures)
	assert.Empty(t, weatherEventsFor(t, a, "2026-07-05"), "a failed fetch writes no event")

	lines, err := a.ReadEnrichmentAudit()
	require.NoError(t, err)
	assert.NotEmpty(t, lines, "the failure is audited, not silent")
}

func TestRunEnrichment_SkipsWhenNoLocationOrCoords(t *testing.T) {
	// No location on file at all → weather is skipped for every day.
	a := enrichStore(t)
	a.SetEnrichmentFetcher(okWeatherFetcher)
	rep, err := a.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err)
	assert.Positive(t, rep.SkippedNoLocation)
	assert.Empty(t, weatherEventsFor(t, a, "2026-07-05"))

	// A location whose place has no coordinates → skipped for coordinates.
	b := enrichStore(t)
	b.SetEnrichmentFetcher(okWeatherFetcher)
	_, err = b.UpdateRegistry(observations.RegistryPlace, "place_nocoord", observations.RegistryPatch{DisplayName: "Nowhere", At: "2026-06-01T09:00:00Z"})
	require.NoError(t, err)
	seedLocation(t, b, "2026-06-01", "place_nocoord")
	rep, err = b.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err)
	assert.Positive(t, rep.SkippedNoCoords)
}

func TestRunEnrichment_CalendarFrameIsLocal(t *testing.T) {
	a := enrichStore(t) // calendar-frame is enabled by default
	a.SetEnrichmentFetcher(func(string) (int, []byte, error) { return 0, nil, fmt.Errorf("no network expected") })

	_, err := a.RunEnrichment(time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err)

	events, _, err := a.ReadObservationsDay("2026-07-05")
	require.NoError(t, err)
	var cal *observations.Event
	for i := range events {
		if events[i].Source == observations.EnricherSource(observations.EnricherCalendarFrame) {
			cal = &events[i]
		}
	}
	require.NotNil(t, cal, "calendar-frame writes a local event with no fetch")
	assert.Contains(t, cal.Payload, "weekday")

	// calendar-frame never touched the network audit log.
	lines, err := a.ReadEnrichmentAudit()
	require.NoError(t, err)
	assert.Empty(t, lines)
}

func TestShouldShowPacketPointer_OncePerThirtyDays(t *testing.T) {
	a := newObsStore(t)
	day1 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	show, err := a.ShouldShowPacketPointer(day1)
	require.NoError(t, err)
	assert.True(t, show, "the first ask shows the pointer")

	// A week later — still inside 30 days → suppressed.
	show, err = a.ShouldShowPacketPointer(day1.AddDate(0, 0, 7))
	require.NoError(t, err)
	assert.False(t, show)

	// Past 30 days → shows again.
	show, err = a.ShouldShowPacketPointer(day1.AddDate(0, 0, 31))
	require.NoError(t, err)
	assert.True(t, show)
}

func TestLocationOnFile(t *testing.T) {
	a := newObsStore(t)
	seedLocation(t, a, "2026-07-01", "place_a-river")

	on, err := a.LocationOnFile("2026-07-02")
	require.NoError(t, err)
	assert.True(t, on)

	on, err = a.LocationOnFile("2026-06-01")
	require.NoError(t, err)
	assert.False(t, on)
}

func TestCuriosityState_RoundTrip(t *testing.T) {
	a := newObsStore(t)
	st, err := a.ReadCuriosityState()
	require.NoError(t, err)
	assert.Empty(t, st.Day, "a fresh instance has no ask-state")

	st.Day = "2026-07-02"
	st.AskedToday = 1
	require.NoError(t, a.WriteCuriosityState(st))

	got, err := a.ReadCuriosityState()
	require.NoError(t, err)
	assert.Equal(t, "2026-07-02", got.Day)
	assert.Equal(t, 1, got.AskedToday)
}
