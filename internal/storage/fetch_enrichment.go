package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// Enrichment file names and bounds. The outbound audit log lives under
// observations/ (a backup-critical tree — what left the instance is not
// recomputable, ADR-0002); the ephemeral ask-state files live under
// projections/ (rebuildable — losing them only resets backoff, never the
// Ledger, observations.md §6).
const (
	enrichmentAuditFile    = "enrichment_audit.log"
	discoveryStateFile     = ".discovery.json"
	curiosityStateFile     = ".curiosity.json"
	enrichmentBackfillDays = 7
	enrichmentHTTPTimeout  = 10 * time.Second
	enrichmentBodyCap      = 1 << 20 // 1 MiB ceiling on an enricher response
)

// EnrichmentFetcher performs the one outbound HTTP GET the enrichment job
// makes. Tests inject a fake so no real socket opens (plan.md A4: provider and
// network access is faked through Phase 15); production uses [defaultHTTPGet].
type EnrichmentFetcher func(url string) (status int, body []byte, err error)

// SetEnrichmentFetcher overrides the network transport used by
// [Adapter.FetchEnrichment]. It exists for tests and for a future host layer
// that wants a custom client; the default is a plain https getter.
func (a *Adapter) SetEnrichmentFetcher(fn EnrichmentFetcher) { a.enrichFetch = fn }

// fetcher returns the injected transport or the default https getter.
func (a *Adapter) fetcher() EnrichmentFetcher {
	if a.enrichFetch != nil {
		return a.enrichFetch
	}
	return defaultHTTPGet
}

// defaultHTTPGet is the real, read-only outbound query: a bounded-timeout GET
// that reads at most [enrichmentBodyCap] bytes. It performs no send and posts
// no message — a fetch is not a send (observations.md §5).
func defaultHTTPGet(url string) (int, []byte, error) {
	client := &http.Client{Timeout: enrichmentHTTPTimeout}
	resp, err := client.Get(url) //nolint:noctx // url is validated against the enricher allowlist before any call
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, enrichmentBodyCap))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("storage: read enricher response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// FetchEnrichment is the single audited network op for the module
// (observations.md §5, observations-module.md §"Storage additions"). It
// validates the exact URL against the per-enricher allowlist before opening a
// socket, and **the adapter — not the enricher — writes the outbound audit log
// from the exact URL it transmits**, so "coordinates and dates only, quantized"
// is enforced and logged by code the enricher does not control. A failed fetch
// still writes its audit line (no silent state) and returns an error with no
// body, so the caller writes no event and retries next run.
func (a *Adapter) FetchEnrichment(name, url string, now time.Time) ([]byte, error) {
	if err := observations.ValidateEnricherURL(name, url); err != nil {
		return nil, err
	}
	status, body, err := a.fetcher()(url)

	outcome := "ok"
	switch {
	case err != nil:
		outcome = "fail(transport)"
	case status != http.StatusOK:
		outcome = fmt.Sprintf("fail(status=%d)", status)
	}
	if auditErr := a.appendAuditLine(now, name, outcome, url); auditErr != nil {
		return nil, auditErr
	}

	if err != nil {
		return nil, fmt.Errorf("storage: fetch %s: %w", name, err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("storage: fetch %s returned status %d", name, status)
	}
	return body, nil
}

// appendAuditLine records one outbound attempt from the exact transmitted URL.
// The line is tab-delimited (timestamp, enricher, outcome, url) and holds no
// user content — only the pinned-host, quantized URL and a fixed outcome word.
func (a *Adapter) appendAuditLine(now time.Time, name, outcome, url string) error {
	line := fmt.Sprintf("%s\t%s\t%s\t%s", now.Format(time.RFC3339), name, outcome, url)
	if err := appendLineFsync(a.enrichmentAuditPath(), []byte(line)); err != nil {
		return fmt.Errorf("storage: append enrichment audit: %w", err)
	}
	return nil
}

// enrichmentAuditPath returns observations/enrichment_audit.log.
func (a *Adapter) enrichmentAuditPath() string {
	return filepath.Join(a.observationsDir(), enrichmentAuditFile)
}

// ReadEnrichmentAudit returns the outbound audit log's lines (empty when the
// job has never run). It is the surface tests grep to prove only pinned-host,
// quantized URLs ever left the instance.
func (a *Adapter) ReadEnrichmentAudit() ([]string, error) {
	lines, err := readJSONLLines(a.enrichmentAuditPath())
	if err != nil {
		return nil, err
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = string(ln)
	}
	return out, nil
}

// EnrichmentReport is the outcome of one enrichment run: how many events were
// written and why days were skipped — the surface the host layer and tests
// read to confirm idempotency and the as-of/no-coordinates paths.
type EnrichmentReport struct {
	Written           int
	Skipped           int // already enriched (idempotent)
	SkippedNoLocation int // no context.location on file on or before the day
	SkippedNoCoords   int // location on file but the place has no lat/lon
	Failures          int // fetch failed; audit line written, no event
}

// RunEnrichment runs the enrichment job for yesterday's logical day and the
// prior [enrichmentBackfillDays] days (observations-module.md §"The enrichment
// job"). It is agent-free and Engine-independent: for each enabled enricher on
// each still-missing day it checks idempotency first, resolves the as-of
// location, fetches through the audited adapter op, and writes exactly one
// atomic context.day event — or logs a failure and moves on. It opens no send
// path and touches no Engine tree.
func (a *Adapter) RunEnrichment(now time.Time, loc *time.Location) (EnrichmentReport, error) {
	if loc == nil {
		loc = time.UTC
	}
	if err := a.ScaffoldObservations(); err != nil {
		return EnrichmentReport{}, err
	}
	cfg, err := a.ReadObservationsConfig()
	if err != nil {
		return EnrichmentReport{}, err
	}
	locationEvents, err := a.ReadObservationsKind(observations.KindLocation)
	if err != nil {
		return EnrichmentReport{}, err
	}

	var rep EnrichmentReport
	today := observations.DateOf(now.In(loc))
	for offset := 1; offset <= enrichmentBackfillDays; offset++ {
		date := observations.DateString(today.AddDate(0, 0, -offset))
		dayEvents, _, derr := a.ReadObservationsDay(date)
		if derr != nil {
			return EnrichmentReport{}, derr
		}
		for _, enr := range cfg.Enrichers {
			if !enr.Enabled {
				continue
			}
			if observations.AlreadyEnriched(dayEvents, enr.Name) {
				rep.Skipped++
				continue
			}
			if werr := a.runEnricher(enr.Name, date, locationEvents, now, loc, &rep); werr != nil {
				return EnrichmentReport{}, werr
			}
		}
	}
	return rep, nil
}

// runEnricher runs one enricher for one day, appending a context.day event on
// success and tallying the outcome. calendar-frame is pure local compute (no
// fetch); weather resolves the as-of location and coordinates before the single
// audited fetch. An unknown enricher name is skipped (only the reference set is
// supported in the MVP).
func (a *Adapter) runEnricher(name, date string, locationEvents []observations.Event, now time.Time, loc *time.Location, rep *EnrichmentReport) error {
	switch name {
	case observations.EnricherCalendarFrame:
		payload, err := observations.CalendarFramePayload(date, loc)
		if err != nil {
			return err
		}
		return a.appendContextDay(name, date, payload, now, loc, rep)

	case observations.EnricherWeather:
		placeRef, ok := observations.AsOfPlaceRef(locationEvents, date)
		if !ok {
			rep.SkippedNoLocation++
			return nil
		}
		lat, lon, ok, err := a.placeCoords(placeRef)
		if err != nil {
			return err
		}
		if !ok {
			rep.SkippedNoCoords++
			return nil
		}
		url := observations.BuildOpenMeteoURL(lat, lon, date)
		body, ferr := a.FetchEnrichment(observations.EnricherWeather, url, now)
		if ferr != nil {
			rep.Failures++
			return nil //nolint:nilerr // the fetch failure is audited; the day is retried next run, not aborted
		}
		payload, perr := observations.ParseOpenMeteoDaily(body, placeRef)
		if perr != nil {
			rep.Failures++
			return nil //nolint:nilerr // a malformed body is a counted failure; the run continues
		}
		return a.appendContextDay(name, date, payload, now, loc, rep)

	default:
		return nil
	}
}

// appendContextDay builds and appends one enricher event, counting it.
func (a *Adapter) appendContextDay(name, date string, payload map[string]any, now time.Time, loc *time.Location, rep *EnrichmentReport) error {
	ev, err := observations.BuildContextDayEvent(name, date, payload, now, loc)
	if err != nil {
		return err
	}
	if _, err := a.AppendObservation(ev); err != nil {
		return err
	}
	rep.Written++
	return nil
}

// placeCoords reads a place registry record's lat/lon (added once, by the user,
// observations.md §8). ok is false when the place has no coordinates on file —
// the "config lacks coordinates" skip path.
func (a *Adapter) placeCoords(placeRef string) (lat, lon float64, ok bool, err error) {
	rec, found, err := a.ReadRegistry(observations.RegistryPlace, placeRef)
	if err != nil || !found {
		return 0, 0, false, err
	}
	lat, latOK := coordField(rec.Fields, "lat")
	lon, lonOK := coordField(rec.Fields, "lon")
	if !latOK || !lonOK {
		return 0, 0, false, nil
	}
	return lat, lon, true, nil
}

// coordField coerces a registry coordinate field (a JSON number, or a string
// a hand-edit may have used) to a float64.
func coordField(fields map[string]any, key string) (float64, bool) {
	switch v := fields[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// LocationOnFile reports whether any context.location event is on file on or
// before date — the read behind `/day`'s one-time "no location on file" note
// when a weather enricher is enabled but has nothing to work with.
func (a *Adapter) LocationOnFile(date string) (bool, error) {
	events, err := a.ReadObservationsKind(observations.KindLocation)
	if err != nil {
		return false, err
	}
	_, ok := observations.AsOfPlaceRef(events, date)
	return ok, nil
}

// discoveryState is the ephemeral packet-pointer ask-state (observations.md §7
// discovery): the last date the /obs intervention ack mentioned the clinician
// packet. It lives under projections/ (rebuildable), never the Ledger.
type discoveryState struct {
	ClinicianPointerLast string `json:"clinician_pointer_last"`
}

// discoveryStatePath returns projections/.discovery.json.
func (a *Adapter) discoveryStatePath() string {
	return filepath.Join(a.projectionsDir(), discoveryStateFile)
}

// ShouldShowPacketPointer reports whether the /obs intervention ack may append
// the clinician-packet pointer today (at most once per 30 days), and records
// that it was shown when it returns true (observations.md §7). now is the
// capture time; the 30-day window is measured in logical days.
func (a *Adapter) ShouldShowPacketPointer(now time.Time) (bool, error) {
	if err := a.ScaffoldObservations(); err != nil {
		return false, err
	}
	st, err := a.readDiscoveryState()
	if err != nil {
		return false, err
	}
	today := observations.DateOf(now)
	if st.ClinicianPointerLast != "" {
		last, perr := observations.ParseDate(st.ClinicianPointerLast, now.Location())
		if perr == nil && today.Sub(last).Hours() < 30*24 {
			return false, nil
		}
	}
	st.ClinicianPointerLast = observations.DateString(today)
	if err := a.writeDiscoveryState(st); err != nil {
		return false, err
	}
	return true, nil
}

// readDiscoveryState loads the ephemeral discovery ask-state (a missing file is
// the fresh zero value, not an error).
func (a *Adapter) readDiscoveryState() (discoveryState, error) {
	b, err := os.ReadFile(a.discoveryStatePath())
	if errors.Is(err, fs.ErrNotExist) {
		return discoveryState{}, nil
	}
	if err != nil {
		return discoveryState{}, fmt.Errorf("storage: read discovery state: %w", err)
	}
	var st discoveryState
	if err := json.Unmarshal(b, &st); err != nil {
		return discoveryState{}, nil //nolint:nilerr // a corrupt ephemeral file resets, never blocks capture
	}
	return st, nil
}

// writeDiscoveryState persists the ephemeral discovery ask-state.
func (a *Adapter) writeDiscoveryState(st discoveryState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("storage: marshal discovery state: %w", err)
	}
	if err := os.WriteFile(a.discoveryStatePath(), b, filePerm); err != nil {
		return fmt.Errorf("storage: write discovery state: %w", err)
	}
	return nil
}

// curiosityStatePath returns projections/.curiosity.json.
func (a *Adapter) curiosityStatePath() string {
	return filepath.Join(a.projectionsDir(), curiosityStateFile)
}

// ReadCuriosityState loads the ephemeral curiosity ask-state (a missing or
// corrupt file is the fresh zero value, never a capture-blocking error —
// curiosity is skippable by design, observations.md §6).
func (a *Adapter) ReadCuriosityState() (observations.CuriosityState, error) {
	b, err := os.ReadFile(a.curiosityStatePath())
	if errors.Is(err, fs.ErrNotExist) {
		return observations.CuriosityState{}, nil
	}
	if err != nil {
		return observations.CuriosityState{}, fmt.Errorf("storage: read curiosity state: %w", err)
	}
	var st observations.CuriosityState
	if err := json.Unmarshal(b, &st); err != nil {
		return observations.CuriosityState{}, nil //nolint:nilerr // a corrupt ephemeral file resets, never blocks
	}
	return st, nil
}

// WriteCuriosityState persists the ephemeral curiosity ask-state under
// projections/ (rebuildable — never the Ledger).
func (a *Adapter) WriteCuriosityState(st observations.CuriosityState) error {
	if err := a.ScaffoldObservations(); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("storage: marshal curiosity state: %w", err)
	}
	if err := os.WriteFile(a.curiosityStatePath(), b, filePerm); err != nil {
		return fmt.Errorf("storage: write curiosity state: %w", err)
	}
	return nil
}
