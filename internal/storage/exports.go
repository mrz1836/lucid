package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/observations/exports"
)

// Export file names under projections/ (observations-module.md §"Storage
// additions"). exports.log is the append-only disclosure record — the one
// non-rebuildable file under projections/ — recording every projection that
// left the instance (what, window, when, path).
const (
	exportsLogFile   = "exports.log"
	seriesFileName   = "series.csv"
	packetFilePrefix = "packet_clinician_"

	// packetFirstWindowDays is the trailing window the first-ever clinician
	// packet covers (observations.md §10: "first-ever export: trailing 90
	// days"). Inclusive, so the start is 89 days before the end.
	packetFirstWindowDays = 90
)

// ExportResult reports a written projection: its path (the only thing posted to
// a chat surface) plus the window it covered, for the caller's ack and the
// disclosure log.
type ExportResult struct {
	What        string
	Path        string
	WindowStart string
	WindowEnd   string
}

// ExportSeriesCSV writes the pain/mood/capacity series joined on logical_date
// (observations.md §7) under projections/, and appends a disclosure-log line.
// It reads only the three series (no notes, no location, no weather) and is
// byte-stable across reruns for the same Ledger.
func (a *Adapter) ExportSeriesCSV(now time.Time, loc *time.Location) (ExportResult, error) {
	if loc == nil {
		loc = time.UTC
	}
	if err := a.ScaffoldObservations(); err != nil {
		return ExportResult{}, err
	}
	painEvents, err := a.ReadObservationsKind(observations.KindPain)
	if err != nil {
		return ExportResult{}, err
	}
	moodEvents, err := a.ReadObservationsKind(observations.KindMood)
	if err != nil {
		return ExportResult{}, err
	}
	capByDate, err := a.capacityByDate()
	if err != nil {
		return ExportResult{}, err
	}

	rows := exports.BuildSeriesRows(painEvents, moodEvents, capByDate)
	csv := exports.SeriesCSV(rows)
	path := filepath.Join(a.projectionsDir(), seriesFileName)
	if err := a.writeProjection(path, []byte(csv)); err != nil {
		return ExportResult{}, err
	}

	windowEnd := observations.DateString(observations.DateOf(now.In(loc)))
	res := ExportResult{What: "series", Path: path, WindowStart: "all", WindowEnd: windowEnd}
	if err := a.appendExportsLog(now, res); err != nil {
		return ExportResult{}, err
	}
	return res, nil
}

// ExportClinicianPacket renders the clinician packet (observations.md §7) under
// projections/ and appends a disclosure-log line. The window runs to today;
// its start is the override (`@<date>`), "" for `all`, the previous packet's
// end (since-last-export), or — first ever — the trailing-90-day start. Note
// fields, location, and weather are excluded by construction. Only the path is
// meant for a chat surface; the render is byte-stable for a fixed Ledger + now.
func (a *Adapter) ExportClinicianPacket(now time.Time, loc *time.Location, startOverride string, all bool) (ExportResult, error) {
	if loc == nil {
		loc = time.UTC
	}
	if err := a.ScaffoldObservations(); err != nil {
		return ExportResult{}, err
	}
	cfg, err := a.ReadObservationsConfig()
	if err != nil {
		return ExportResult{}, err
	}

	windowEnd := observations.DateString(observations.DateOf(now.In(loc)))
	windowStart, err := a.resolvePacketWindowStart(windowEnd, startOverride, all)
	if err != nil {
		return ExportResult{}, err
	}

	input, err := a.buildClinicianInput(cfg, windowStart, windowEnd, all)
	if err != nil {
		return ExportResult{}, err
	}
	body := exports.RenderClinician(input)

	stamp := now.In(loc).Format("20060102T150405")
	path := filepath.Join(a.projectionsDir(), packetFilePrefix+stamp+".md")
	if err := a.writeProjection(path, []byte(body)); err != nil {
		return ExportResult{}, err
	}

	shownStart := windowStart
	if all {
		shownStart = "all"
	}
	res := ExportResult{What: "clinician", Path: path, WindowStart: shownStart, WindowEnd: windowEnd}
	if err := a.appendExportsLog(now, res); err != nil {
		return ExportResult{}, err
	}
	return res, nil
}

// resolvePacketWindowStart applies the window rule: an explicit `@<date>`
// override wins; `all` yields an empty (no lower bound) start; otherwise the
// previous clinician export's end is the new start (since last export), and the
// first-ever export falls back to the trailing-90-day start.
func (a *Adapter) resolvePacketWindowStart(windowEnd, startOverride string, all bool) (string, error) {
	if all {
		return "", nil
	}
	if startOverride != "" {
		return startOverride, nil
	}
	last, found, err := a.lastClinicianExport()
	if err != nil {
		return "", err
	}
	if found && last.WindowEnd != "" {
		return last.WindowEnd, nil
	}
	end, err := observations.ParseDate(windowEnd, time.UTC)
	if err != nil {
		return "", fmt.Errorf("storage: bad packet window end %q: %w", windowEnd, err)
	}
	return observations.DateString(end.AddDate(0, 0, -(packetFirstWindowDays - 1))), nil
}

// buildClinicianInput gathers the packet's data from the Ledger: clinical
// context (verbatim), active/managed injuries, the current regimen (latest
// event per med — never window-clipped, so "current" stays true), and the
// in-window episodes, capacity/mode, and pain/med/intervention timeline.
func (a *Adapter) buildClinicianInput(cfg observations.Config, windowStart, windowEnd string, all bool) (exports.ClinicianInput, error) {
	inWindow := func(date string) bool {
		if date > windowEnd {
			return false
		}
		if all || windowStart == "" {
			return true
		}
		return date >= windowStart
	}

	pain, err := a.readKindInWindow(observations.KindPain, inWindow)
	if err != nil {
		return exports.ClinicianInput{}, err
	}
	interventions, err := a.readKindInWindow(observations.KindIntervention, inWindow)
	if err != nil {
		return exports.ClinicianInput{}, err
	}
	medWindow, err := a.readKindInWindow(observations.KindMed, inWindow)
	if err != nil {
		return exports.ClinicianInput{}, err
	}
	medAll, err := a.ReadObservationsKind(observations.KindMed)
	if err != nil {
		return exports.ClinicianInput{}, err
	}
	injuries, err := a.activeInjuryLines()
	if err != nil {
		return exports.ClinicianInput{}, err
	}
	engineByDate, err := a.engineFactsInWindow(inWindow)
	if err != nil {
		return exports.ClinicianInput{}, err
	}

	epCount, episodes := exports.CountEpisodes(pain, exports.EpisodeThreshold, exports.EpisodeGapDays)
	shownStart := windowStart
	if all || windowStart == "" {
		shownStart = "all"
	}
	return exports.ClinicianInput{
		WindowStart:     shownStart,
		WindowEnd:       windowEnd,
		ClinicalContext: cfg.Packet.ClinicalContext,
		Injuries:        injuries,
		Regimen:         exports.DeriveRegimen(medAll),
		EpisodeCount:    epCount,
		Episodes:        episodes,
		Days:            exports.BuildPacketDayRows(pain, medWindow, interventions, engineByDate),
	}, nil
}

// readKindInWindow reads every event of a kind and keeps those the predicate
// admits, preserving id order.
func (a *Adapter) readKindInWindow(kind string, inWindow func(string) bool) ([]observations.Event, error) {
	all, err := a.ReadObservationsKind(kind)
	if err != nil {
		return nil, err
	}
	out := all[:0:0]
	for _, e := range all {
		if inWindow(e.LogicalDate) {
			out = append(out, e)
		}
	}
	return out, nil
}

// activeInjuryLines renders the active/managed injuries as "name (status)",
// sorted by key for byte-stability.
func (a *Adapter) activeInjuryLines() ([]string, error) {
	recs, err := a.ReadRegistryKind(observations.RegistryInjury)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range recs {
		if r.Status == observations.StatusActive || r.Status == observations.StatusManaged {
			out = append(out, fmt.Sprintf("%s (%s)", r.DisplayName, r.Status))
		}
	}
	return out, nil
}

// engineFactsInWindow maps in-window logical dates to their capacity/mode.
func (a *Adapter) engineFactsInWindow(inWindow func(string) bool) (map[string]exports.EngineDayFacts, error) {
	records, err := a.ReadEngineDays()
	if err != nil {
		return nil, err
	}
	out := map[string]exports.EngineDayFacts{}
	for _, r := range records {
		if inWindow(r.LogicalDate) {
			out[r.LogicalDate] = exports.EngineDayFacts{Capacity: r.Capacity, Mode: string(r.Mode)}
		}
	}
	return out, nil
}

// capacityByDate maps every engine day-record's logical date to its capacity —
// the capacity column of the series export.
func (a *Adapter) capacityByDate() (map[string]int, error) {
	records, err := a.ReadEngineDays()
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range records {
		out[r.LogicalDate] = r.Capacity
	}
	return out, nil
}

// writeProjection writes a rebuildable projection file, creating projections/
// if needed. Projections are overwritable — only exports.log is append-only.
func (a *Adapter) writeProjection(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("storage: prepare projections dir: %w", err)
	}
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return fmt.Errorf("storage: write projection %q: %w", path, err)
	}
	return nil
}

// exportsLogPath returns projections/exports.log.
func (a *Adapter) exportsLogPath() string {
	return filepath.Join(a.projectionsDir(), exportsLogFile)
}

// appendExportsLog appends one disclosure-log line (what, window, when, path)
// — the MVP seed of the disclosure log (observations.md §7). It is append-only
// with whole-line fsync, like every other Ledger append.
func (a *Adapter) appendExportsLog(now time.Time, res ExportResult) error {
	line := fmt.Sprintf("%s\t%s\t%s..%s\t%s",
		now.Format(time.RFC3339), res.What, res.WindowStart, res.WindowEnd, res.Path)
	if err := appendLineFsync(a.exportsLogPath(), []byte(line)); err != nil {
		return fmt.Errorf("storage: append exports.log: %w", err)
	}
	return nil
}

// ReadExportsLog returns the disclosure-log lines (empty when nothing has been
// exported).
func (a *Adapter) ReadExportsLog() ([]string, error) {
	lines, err := readJSONLLines(a.exportsLogPath())
	if err != nil {
		return nil, err
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = string(ln)
	}
	return out, nil
}

// lastClinicianExport returns the most recent clinician entry in the disclosure
// log, for the since-last-export window. A line is
// "<when>\t<what>\t<start>..<end>\t<path>".
func (a *Adapter) lastClinicianExport() (ExportResult, bool, error) {
	lines, err := a.ReadExportsLog()
	if err != nil {
		return ExportResult{}, false, err
	}
	var last ExportResult
	found := false
	for _, ln := range lines {
		parts := strings.Split(ln, "\t")
		if len(parts) < 4 || parts[1] != "clinician" {
			continue
		}
		window := strings.SplitN(parts[2], "..", 2)
		res := ExportResult{What: parts[1], Path: parts[3]}
		if len(window) == 2 {
			res.WindowStart, res.WindowEnd = window[0], window[1]
		}
		last, found = res, true
	}
	return last, found, nil
}
