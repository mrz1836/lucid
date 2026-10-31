package exports

import (
	"cmp"
	"slices"
	"strings"

	"github.com/mrz1836/lucid/internal/observations"
)

// measurementsHeader is the fixed header of the long-format measurement CSV
// (usage/commands.md §export, "Measurement export rules").
const measurementsHeader = "logical_date,metric,value,unit,id\n"

// MeasurementRow is one kept measurement reading in long format
// (observations.md §7): metric names are user-chosen and unbounded, so each
// reading is its own row rather than a column. ID is the reading's own obs_…
// event id, so every row traces back to the Ledger event it came from. The
// reading's note and tags are never carried.
type MeasurementRow struct {
	Date   string
	Metric string
	Value  string
	Unit   string
	ID     string
}

// MeasurementFilter narrows [BuildMeasurementRows]. Start and End are
// inclusive YYYY-MM-DD logical dates; an empty bound is unbounded. Metric is
// one exact, case-sensitive slug; empty keeps every metric.
type MeasurementFilter struct {
	Start  string
	End    string
	Metric string
}

// BuildMeasurementRows is the single row builder both measurement readers
// share — the measurement export and the clinician packet's "Measurements in
// range" section. It runs in a fixed order:
//
//  1. Fold voids over the whole slice ([observations.FoldMeasurementVoids]),
//     so a void filed outside the window still drops its in-window target.
//  2. Keep KindMeasurement readings with a non-empty string metric and a
//     string value that is a plain decimal (`^-?[0-9]+(\.[0-9]+)?$`), kept
//     byte-verbatim. Partial captures, voids, non-numeric tokens, and
//     non-string values are skipped — never coerced. A unit is carried
//     byte-exact, or empty when absent or not a string; it is never converted.
//  3. Apply the inclusive window, then the exact metric match.
//  4. Sort by logical_date, then metric, then id (byte order), so same-day
//     readings are all kept in a deterministic order.
//
// It reads only metric, value, unit, and parse from a payload — never a note,
// never tags.
func BuildMeasurementRows(events []observations.Event, f MeasurementFilter) []MeasurementRow {
	var rows []MeasurementRow
	for _, e := range observations.FoldMeasurementVoids(events) {
		row, ok := measurementRow(e)
		if !ok || !f.keeps(row) {
			continue
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b MeasurementRow) int {
		return cmp.Or(
			cmp.Compare(a.Date, b.Date),
			cmp.Compare(a.Metric, b.Metric),
			cmp.Compare(a.ID, b.ID),
		)
	})
	return rows
}

// measurementRow reads one event as a measurement row, reporting false for
// anything that is not a complete numeric measurement reading.
func measurementRow(e observations.Event) (MeasurementRow, bool) {
	if e.Kind != observations.KindMeasurement {
		return MeasurementRow{}, false
	}
	if payloadStr(e.Payload, "parse") == observations.ParseMarkerPartial {
		return MeasurementRow{}, false
	}
	metric := payloadStr(e.Payload, "metric")
	value, isString := e.Payload["value"].(string)
	if metric == "" || !isString || !isPlainDecimal(value) {
		return MeasurementRow{}, false
	}
	return MeasurementRow{
		Date:   e.LogicalDate,
		Metric: metric,
		Value:  value,
		Unit:   payloadStr(e.Payload, "unit"),
		ID:     e.ID,
	}, true
}

// keeps reports whether a row falls inside the filter's inclusive window and
// matches its metric.
func (f MeasurementFilter) keeps(r MeasurementRow) bool {
	if f.Start != "" && r.Date < f.Start {
		return false
	}
	if f.End != "" && r.Date > f.End {
		return false
	}
	return f.Metric == "" || r.Metric == f.Metric
}

// isPlainDecimal reports whether s is a plain decimal token — the language of
// `^-?[0-9]+(\.[0-9]+)?$`: an optional leading minus, one or more ASCII
// digits, and optionally a dot followed by one or more ASCII digits. A
// hand-written scanner keeps regexp out of the pure render path.
func isPlainDecimal(s string) bool {
	s = strings.TrimPrefix(s, "-")
	whole, frac, hasDot := strings.Cut(s, ".")
	if !allDigits(whole) {
		return false
	}
	return !hasDot || allDigits(frac)
}

// allDigits reports whether s is one or more ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// MeasurementsCSV renders measurement rows as the long-format CSV with a fixed
// header (logical_date,metric,value,unit,id). Every line is LF-terminated, so
// no rows renders a header-only file that still ends with a newline.
func MeasurementsCSV(rows []MeasurementRow) string {
	var b strings.Builder
	b.WriteString(measurementsHeader)
	for _, r := range rows {
		b.WriteString(csvCell(r.Date))
		b.WriteByte(',')
		b.WriteString(csvCell(r.Metric))
		b.WriteByte(',')
		b.WriteString(csvCell(r.Value))
		b.WriteByte(',')
		b.WriteString(csvCell(r.Unit))
		b.WriteByte(',')
		b.WriteString(csvCell(r.ID))
		b.WriteByte('\n')
	}
	return b.String()
}

// csvCell quotes a cell per RFC 4180 only when it holds a comma, a double
// quote, a CR, or an LF: the cell is wrapped in double quotes and each inner
// quote doubled. Every other cell is emitted bare, byte-for-byte.
func csvCell(s string) string {
	if !strings.ContainsAny(s, ",\"\r\n") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
