package storage

import (
	"path/filepath"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/observations/exports"
)

// measurementsFileName is the fixed-name long-format measurement projection
// under projections/, overwritten on each run like series.csv.
const measurementsFileName = "measurements.csv"

// ExportMeasurementsCSV writes the long-format measurement CSV
// (usage/commands.md §export, "Measurement export rules") under projections/
// and appends a disclosure-log line. The window runs from startOverride
// (inclusive; "" is all history) to today; metric, when non-empty, keeps only
// that exact slug. It reads every measurement event regardless of whether the
// kind is still enabled — enabling gates capture, not projection — and the
// shared row builder folds voids over the whole history before windowing, so a
// void filed outside the window still drops its target. Notes and tags are
// never read. The file is byte-stable across reruns for the same Ledger.
func (a *Adapter) ExportMeasurementsCSV(now time.Time, loc *time.Location, startOverride, metric string) (ExportResult, error) {
	if loc == nil {
		loc = time.UTC
	}
	if err := a.ScaffoldObservations(); err != nil {
		return ExportResult{}, err
	}
	events, err := a.ReadObservationsKind(observations.KindMeasurement)
	if err != nil {
		return ExportResult{}, err
	}

	windowEnd := observations.DateString(observations.DateOf(now.In(loc)))
	rows := exports.BuildMeasurementRows(events, exports.MeasurementFilter{
		Start: startOverride, End: windowEnd, Metric: metric,
	})
	path := filepath.Join(a.projectionsDir(), measurementsFileName)
	if err := a.writeProjection(path, []byte(exports.MeasurementsCSV(rows))); err != nil {
		return ExportResult{}, err
	}

	what := "measurements"
	if metric != "" {
		what += ":" + metric
	}
	windowStart := startOverride
	if windowStart == "" {
		windowStart = "all"
	}
	res := ExportResult{What: what, Path: path, WindowStart: windowStart, WindowEnd: windowEnd}
	if err := a.appendExportsLog(now, res); err != nil {
		return ExportResult{}, err
	}
	return res, nil
}
