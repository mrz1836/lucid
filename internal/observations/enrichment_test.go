package observations

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuantizeCoord(t *testing.T) {
	assert.Equal(t, "38.72", QuantizeCoord(38.7223))
	assert.Equal(t, "-9.14", QuantizeCoord(-9.139))
	assert.Equal(t, "0.00", QuantizeCoord(0))
	// A value already within quantization is unchanged.
	assert.Equal(t, "12.30", QuantizeCoord(12.3))
}

func TestBuildOpenMeteoURL_IsPinnedAndQuantized(t *testing.T) {
	url := BuildOpenMeteoURL(38.7223, -9.1393, "2026-07-02")
	assert.True(t, strings.HasPrefix(url, "https://"+OpenMeteoHost+OpenMeteoPath+"?"), url)
	assert.Contains(t, url, "latitude=38.72")
	assert.Contains(t, url, "longitude=-9.14")
	assert.Contains(t, url, "start_date=2026-07-02")
	assert.Contains(t, url, "end_date=2026-07-02")
	// Full-precision coordinates never appear.
	assert.NotContains(t, url, "38.7223")
	assert.NotContains(t, url, "9.1393")
	// The URL it builds always passes its own validator.
	require.NoError(t, ValidateEnricherURL(EnricherWeather, url))
}

func TestValidateEnricherURL_Accepts(t *testing.T) {
	require.NoError(t, ValidateEnricherURL(EnricherWeather, BuildOpenMeteoURL(1.5, 2.5, "2026-07-02")))
}

func TestValidateEnricherURL_Rejects(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"non-https", "http://" + OpenMeteoHost + OpenMeteoPath + "?latitude=1.00&longitude=2.00&start_date=2026-07-02&end_date=2026-07-02&daily=temperature_2m_mean"},
		{"wrong host", "https://evil.example.com/v1/forecast?latitude=1.00&longitude=2.00&start_date=2026-07-02&end_date=2026-07-02&daily=temperature_2m_mean"},
		{"wrong path", "https://" + OpenMeteoHost + "/v2/other?latitude=1.00&longitude=2.00&start_date=2026-07-02&end_date=2026-07-02&daily=temperature_2m_mean"},
		{"no query", "https://" + OpenMeteoHost + OpenMeteoPath},
		{"disallowed param", "https://" + OpenMeteoHost + OpenMeteoPath + "?latitude=1.00&longitude=2.00&start_date=2026-07-02&end_date=2026-07-02&apikey=secret"},
		{"full-precision coord", "https://" + OpenMeteoHost + OpenMeteoPath + "?latitude=38.7223&longitude=2.00&start_date=2026-07-02&end_date=2026-07-02&daily=temperature_2m_mean"},
		{"non-numeric coord", "https://" + OpenMeteoHost + OpenMeteoPath + "?latitude=north&longitude=2.00&start_date=2026-07-02&end_date=2026-07-02&daily=temperature_2m_mean"},
		{"bad date", "https://" + OpenMeteoHost + OpenMeteoPath + "?latitude=1.00&longitude=2.00&start_date=nope&end_date=2026-07-02&daily=temperature_2m_mean"},
		{"unknown daily field", "https://" + OpenMeteoHost + OpenMeteoPath + "?latitude=1.00&longitude=2.00&start_date=2026-07-02&end_date=2026-07-02&daily=secret_field"},
		{"malformed param", "https://" + OpenMeteoHost + OpenMeteoPath + "?latitude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Error(t, ValidateEnricherURL(EnricherWeather, tc.url))
		})
	}
}

func TestValidateEnricherURL_NonNetworkEnricher(t *testing.T) {
	assert.Error(t, ValidateEnricherURL(EnricherCalendarFrame, "https://"+OpenMeteoHost+OpenMeteoPath+"?latitude=1.00"))
}

func TestParseOpenMeteoDaily(t *testing.T) {
	body := []byte(`{"daily":{"time":["2026-07-02"],"temperature_2m_mean":[21.3],"precipitation_sum":[0.0],"pressure_msl_mean":[1015.2],"relative_humidity_2m_mean":[64]}}`)
	payload, err := ParseOpenMeteoDaily(body, "place_a-river")
	require.NoError(t, err)
	assert.Equal(t, "place_a-river", payload["place_ref"])
	assert.InDelta(t, 21.3, payload["temp_mean_c"], 0.001)
	assert.InDelta(t, 0.0, payload["precipitation_mm"], 0.001)
	assert.InDelta(t, 1015.2, payload["pressure_msl_hpa"], 0.001)
	assert.InDelta(t, 64.0, payload["humidity_pct"], 0.001)
}

func TestParseOpenMeteoDaily_PartialAndBad(t *testing.T) {
	// A partial response still yields a source-attributed payload.
	partial, err := ParseOpenMeteoDaily([]byte(`{"daily":{"time":["2026-07-02"],"temperature_2m_mean":[18.0]}}`), "place_a-river")
	require.NoError(t, err)
	assert.Contains(t, partial, "temp_mean_c")
	assert.NotContains(t, partial, "pressure_msl_hpa")

	_, err = ParseOpenMeteoDaily([]byte(`{not json`), "place_a-river")
	assert.Error(t, err)
}

func TestCalendarFramePayload(t *testing.T) {
	payload, err := CalendarFramePayload("2026-07-02", time.UTC) // a Thursday
	require.NoError(t, err)
	assert.Equal(t, "Thursday", payload["weekday"])
	assert.Equal(t, 27, payload["iso_week"])
	assert.Equal(t, 2026, payload["iso_year"])
	assert.Equal(t, false, payload["holiday"])

	_, err = CalendarFramePayload("not-a-date", time.UTC)
	assert.Error(t, err)
}

func TestBuildContextDayEvent(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	ev, err := BuildContextDayEvent(EnricherWeather, "2026-07-02", map[string]any{"place_ref": "place_a-river"}, now, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, KindContextDay, ev.Kind)
	assert.Equal(t, EnricherSource(EnricherWeather), ev.Source)
	assert.Equal(t, "2026-07-02", ev.LogicalDate)
	assert.Equal(t, PrecisionApproximate, ev.OccurredAtPrecision)
	assert.Equal(t, "2026-07-02T12:00:00Z", ev.OccurredAt) // local noon of the logical day
	require.NoError(t, ev.Validate())

	_, err = BuildContextDayEvent(EnricherWeather, "bad", nil, now, time.UTC)
	assert.Error(t, err)
}

func locationEvent(id, date, placeRef string) Event {
	return Event{
		ID: id, Schema: Schema, Kind: KindLocation, LogicalDate: date,
		Source: SourceMicrolog, Payload: map[string]any{"place_ref": placeRef},
	}
}

func TestAsOfPlaceRef(t *testing.T) {
	events := []Event{
		locationEvent("obs_2026_06_28_001", "2026-06-28", "place_old"),
		locationEvent("obs_2026_07_02_001", "2026-07-02", "place_new"),
	}
	// A backfill for 07-01 uses the location on file on or before it (the old
	// place) — never the later 07-02 move.
	ref, ok := AsOfPlaceRef(events, "2026-07-01")
	require.True(t, ok)
	assert.Equal(t, "place_old", ref)

	// On 07-02 the new location applies.
	ref, ok = AsOfPlaceRef(events, "2026-07-03")
	require.True(t, ok)
	assert.Equal(t, "place_new", ref)

	// No location on or before the target.
	_, ok = AsOfPlaceRef(events, "2026-06-01")
	assert.False(t, ok)
}

func TestAsOfPlaceRef_SameDayTieBreaksOnID(t *testing.T) {
	events := []Event{
		locationEvent("obs_2026_07_02_001", "2026-07-02", "place_first"),
		locationEvent("obs_2026_07_02_002", "2026-07-02", "place_second"),
	}
	ref, ok := AsOfPlaceRef(events, "2026-07-02")
	require.True(t, ok)
	assert.Equal(t, "place_second", ref, "the later same-day event wins")
}

func TestAlreadyEnriched(t *testing.T) {
	day := []Event{
		{ID: "obs_2026_07_02_001", Kind: KindPain, Source: SourceMicrolog},
		{ID: "obs_2026_07_02_002", Kind: KindContextDay, Source: EnricherSource(EnricherWeather)},
	}
	assert.True(t, AlreadyEnriched(day, EnricherWeather))
	assert.False(t, AlreadyEnriched(day, EnricherCalendarFrame))
}
