package observations

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Enricher names in the MVP reference set (observations.md §5). weather is a
// keyless network source (Open-Meteo); calendar-frame is pure local compute
// and never opens a socket.
const (
	EnricherWeather       = "weather"
	EnricherCalendarFrame = "calendar-frame"
)

// Open-Meteo endpoint pinning (observations.md §5: "pinned host, parameter
// names restricted to coordinate/date variants"). The URL builder and the
// allowlist validator both key on these constants so the transmitted URL can
// never drift off the pinned host or carry a free-text parameter.
const (
	OpenMeteoHost = "api.open-meteo.com"
	OpenMeteoPath = "/v1/forecast"

	// CoordDecimals is the coordinate quantization ceiling (observations.md
	// §10: "coordinate quantization 2 decimals", ~1 km) — enforced before any
	// send so a full-precision location never leaves the instance.
	CoordDecimals = 2
)

// openMeteoDailyFields is the fixed field vocabulary the weather enricher
// requests. It is a closed set so the `daily` parameter can never smuggle
// free text past the allowlist — the validator checks membership.
var openMeteoDailyFields = []string{ //nolint:gochecknoglobals // a fixed, read-only vocabulary
	"temperature_2m_mean",
	"precipitation_sum",
	"pressure_msl_mean",
	"relative_humidity_2m_mean",
}

// allowedEnricherParam reports whether a query parameter name is on the
// coordinate/date allowlist (observations.md §5). Anything else — a free-text
// key, an api key — is rejected by [ValidateEnricherURL].
func allowedEnricherParam(name string) bool {
	switch name {
	case "latitude", "longitude", "start_date", "end_date", "daily":
		return true
	default:
		return false
	}
}

// EnricherSource returns the provenance string an enricher stamps on its
// events (observations.md §2: source is enricher:<name>), so machine-added
// context is always distinguishable from human testimony.
func EnricherSource(name string) string { return "enricher:" + name }

// QuantizeCoord renders a coordinate quantized to [CoordDecimals] places — the
// only form allowed to leave the instance (observations.md §5). The value is
// rounded, not truncated, so 38.7223 → "38.72" and -9.139 → "-9.14".
func QuantizeCoord(v float64) string {
	return strconv.FormatFloat(v, 'f', CoordDecimals, 64)
}

// BuildOpenMeteoURL builds the pinned, quantized weather query for one day.
// Coordinates are quantized here (never full precision) and only
// coordinate/date parameters plus the closed `daily` vocabulary appear, so the
// result always passes [ValidateEnricherURL]. The date is the logical day
// (YYYY-MM-DD); start and end are the same single day.
func BuildOpenMeteoURL(lat, lon float64, date string) string {
	return fmt.Sprintf(
		"https://%s%s?latitude=%s&longitude=%s&start_date=%s&end_date=%s&daily=%s",
		OpenMeteoHost, OpenMeteoPath,
		QuantizeCoord(lat), QuantizeCoord(lon), date, date,
		strings.Join(openMeteoDailyFields, ","),
	)
}

// ValidateEnricherURL is the outbound-minimalism gate (observations.md §5):
// the storage adapter calls it on the exact URL it is about to transmit,
// before opening any socket, and rejects anything off the pinned host or
// outside the coordinate/date allowlist. It parses with plain string ops (the
// module stays pure — no net import) and enforces, for the weather enricher:
// https scheme, the pinned host and path, only allowlisted parameter names,
// coordinates quantized to ≤ [CoordDecimals] decimals, valid dates, and a
// `daily` value drawn only from the closed field vocabulary.
func ValidateEnricherURL(name, rawURL string) error {
	if name != EnricherWeather {
		return fmt.Errorf("observations: enricher %q has no network endpoint", name)
	}
	rest, ok := strings.CutPrefix(rawURL, "https://")
	if !ok {
		return fmt.Errorf("observations: enricher url must be https: %q", rawURL)
	}
	hostPath, query, _ := strings.Cut(rest, "?")
	host, path, _ := strings.Cut(hostPath, "/")
	if host != OpenMeteoHost {
		return fmt.Errorf("observations: enricher url host %q is not the pinned %q", host, OpenMeteoHost)
	}
	if "/"+path != OpenMeteoPath {
		return fmt.Errorf("observations: enricher url path /%s is not the pinned %q", path, OpenMeteoPath)
	}
	if query == "" {
		return fmt.Errorf("observations: enricher url carries no query")
	}
	for kv := range strings.SplitSeq(query, "&") {
		key, val, found := strings.Cut(kv, "=")
		if !found {
			return fmt.Errorf("observations: malformed enricher url parameter %q", kv)
		}
		if !allowedEnricherParam(key) {
			return fmt.Errorf("observations: enricher url carries disallowed parameter %q", key)
		}
		if err := validateEnricherParam(key, val); err != nil {
			return err
		}
	}
	return nil
}

// validateEnricherParam checks one allowlisted parameter's value: coordinates
// must be quantized (no more than CoordDecimals decimals), dates must be
// YYYY-MM-DD, and every requested daily field must be in the closed vocabulary.
func validateEnricherParam(key, val string) error {
	switch key {
	case "latitude", "longitude":
		return validateQuantizedCoord(key, val)
	case "start_date", "end_date":
		if _, err := time.Parse(dateLayout, val); err != nil {
			return fmt.Errorf("observations: enricher url %s %q is not a date", key, val)
		}
	case "daily":
		for f := range strings.SplitSeq(val, ",") {
			if !slices.Contains(openMeteoDailyFields, f) {
				return fmt.Errorf("observations: enricher url requests unknown daily field %q", f)
			}
		}
	}
	return nil
}

// validateQuantizedCoord rejects a coordinate carrying more than CoordDecimals
// decimal places — the on-the-wire enforcement of the quantization rule.
func validateQuantizedCoord(key, val string) error {
	if _, err := strconv.ParseFloat(val, 64); err != nil {
		return fmt.Errorf("observations: enricher url %s %q is not a number", key, val)
	}
	if dot := strings.IndexByte(val, '.'); dot >= 0 && len(val)-dot-1 > CoordDecimals {
		return fmt.Errorf("observations: enricher url %s %q exceeds %d-decimal quantization", key, val, CoordDecimals)
	}
	return nil
}

// openMeteoDaily is the slice of the Open-Meteo daily response the weather
// enricher reads. Arrays carry one element per requested day; a single-day
// query yields length-one arrays.
type openMeteoDaily struct {
	Daily struct {
		Time          []string  `json:"time"`
		Temperature   []float64 `json:"temperature_2m_mean"`
		Precipitation []float64 `json:"precipitation_sum"`
		Pressure      []float64 `json:"pressure_msl_mean"`
		Humidity      []float64 `json:"relative_humidity_2m_mean"`
	} `json:"daily"`
}

// ParseOpenMeteoDaily turns a single-day Open-Meteo response into the
// context.day payload for the weather enricher, attributing it to placeRef.
// Missing series are simply omitted — a partial response still writes an
// honest, source-attributed event rather than nothing.
func ParseOpenMeteoDaily(body []byte, placeRef string) (map[string]any, error) {
	var resp openMeteoDaily
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("observations: parse open-meteo response: %w", err)
	}
	payload := map[string]any{"place_ref": placeRef}
	if len(resp.Daily.Temperature) > 0 {
		payload["temp_mean_c"] = resp.Daily.Temperature[0]
	}
	if len(resp.Daily.Precipitation) > 0 {
		payload["precipitation_mm"] = resp.Daily.Precipitation[0]
	}
	if len(resp.Daily.Pressure) > 0 {
		payload["pressure_msl_hpa"] = resp.Daily.Pressure[0]
	}
	if len(resp.Daily.Humidity) > 0 {
		payload["humidity_pct"] = resp.Daily.Humidity[0]
	}
	return payload, nil
}

// CalendarFramePayload is the calendar-frame enricher's pure local computation
// (observations.md §5: "sends nothing (local)"): the weekday, ISO week, and a
// holiday flag for the logical day. The MVP ships no holiday calendar, so the
// flag is always false — an honest placeholder the frozen envelope already
// carries, upgraded later without a schema change.
func CalendarFramePayload(date string, loc *time.Location) (map[string]any, error) {
	d, err := ParseDate(date, loc)
	if err != nil {
		return nil, fmt.Errorf("observations: calendar-frame date %q: %w", date, err)
	}
	isoYear, isoWeek := d.ISOWeek()
	return map[string]any{
		"weekday":  d.Weekday().String(),
		"iso_year": isoYear,
		"iso_week": isoWeek,
		"holiday":  false,
	}, nil
}

// BuildContextDayEvent assembles the frozen envelope for one enricher event
// (observations.md §3 context.day: "occurred_at = the logical date at local
// noon, precision approximate; written by enrichers only"). recordedAt is the
// wall-clock now; the id is assigned by the storage adapter on append.
func BuildContextDayEvent(name, date string, payload map[string]any, recordedAt time.Time, loc *time.Location) (Event, error) {
	d, err := ParseDate(date, loc)
	if err != nil {
		return Event{}, fmt.Errorf("observations: context.day date %q: %w", date, err)
	}
	noon := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, d.Location())
	return Event{
		Schema:              Schema,
		Kind:                KindContextDay,
		RecordedAt:          recordedAt.Format(time.RFC3339),
		OccurredAt:          noon.Format(time.RFC3339),
		OccurredAtPrecision: PrecisionApproximate,
		LogicalDate:         date,
		Source:              EnricherSource(name),
		Payload:             payload,
	}, nil
}

// AsOfPlaceRef resolves the location for a target day (observations.md §5
// "As-of location"): the place_ref of the most recent context.location event
// whose logical_date ≤ the target — never a later location. locationEvents may
// be the whole tree's context.location events, in any order. ok is false when
// no location was on file on or before the target day.
func AsOfPlaceRef(locationEvents []Event, target string) (placeRef string, ok bool) {
	bestDate, bestID := "", ""
	found := false
	for _, e := range locationEvents {
		if e.Kind != KindLocation || e.LogicalDate > target {
			continue
		}
		ref, isStr := e.Payload["place_ref"].(string)
		if !isStr || ref == "" {
			continue
		}
		// Later logical_date wins; the id (seq) breaks a same-day tie, since
		// ids sort in time order within a day.
		if !found || e.LogicalDate > bestDate || (e.LogicalDate == bestDate && e.ID > bestID) {
			bestDate, bestID, placeRef, found = e.LogicalDate, e.ID, ref, true
		}
	}
	return placeRef, found
}

// AlreadyEnriched reports whether an enricher has already written its one event
// for a day (observations.md §5: "exactly one context.day event per enricher
// per logical day" — the idempotency check the job runs before fetching).
func AlreadyEnriched(dayEvents []Event, name string) bool {
	src := EnricherSource(name)
	return slices.ContainsFunc(dayEvents, func(e Event) bool {
		return e.Kind == KindContextDay && e.Source == src
	})
}
