// Package exports renders the MVP projection exports (observations.md §7):
// the pain/mood/capacity series as CSV and the clinician packet. Like the rest
// of the observation layer it is pure — every function is deterministic over
// values the storage adapter reads, so an export is byte-stable across reruns
// and no model or filesystem access sits in the render path. The adapter reads
// the trees, calls these builders, and writes the result under projections/;
// the excludes-by-default rules (note fields, location, weather) are enforced
// here by simply never reading those payloads.
package exports

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/mrz1836/lucid/internal/observations"
)

// SeriesRow is one logical day's joined series point (observations.md §7:
// "any observation field over time … pain, mood, capacity"). A field that had
// no reading that day renders as an empty cell rather than a fabricated zero.
type SeriesRow struct {
	Date     string
	Pain     *int // max pain intensity that day
	Mood     *int // max mood level that day
	Capacity *int // engine day-record capacity
}

// BuildSeriesRows joins pain, mood, and capacity on logical_date. Pain and
// mood aggregate to the day's maximum reading (a deterministic, defensible
// summary; multiple readings on one day collapse to one point); capacity is
// the Engine day record's own single value. Rows are sorted by date, so the
// CSV is byte-stable across reruns.
func BuildSeriesRows(painEvents, moodEvents []observations.Event, capacityByDate map[string]int) []SeriesRow {
	byDate := map[string]*SeriesRow{}
	get := func(date string) *SeriesRow {
		if r, ok := byDate[date]; ok {
			return r
		}
		r := &SeriesRow{Date: date}
		byDate[date] = r
		return r
	}
	for _, e := range painEvents {
		if v, ok := payloadInt(e.Payload, "intensity"); ok {
			r := get(e.LogicalDate)
			r.Pain = maxPtr(r.Pain, v)
		}
	}
	for _, e := range moodEvents {
		if v, ok := payloadInt(e.Payload, "level"); ok {
			r := get(e.LogicalDate)
			r.Mood = maxPtr(r.Mood, v)
		}
	}
	for date, cap := range capacityByDate {
		if cap > 0 {
			c := cap
			get(date).Capacity = &c
		}
	}
	rows := make([]SeriesRow, 0, len(byDate))
	for _, r := range byDate {
		rows = append(rows, *r)
	}
	slices.SortFunc(rows, func(a, b SeriesRow) int { return cmp.Compare(a.Date, b.Date) })
	return rows
}

// SeriesCSV renders the joined series as CSV with a fixed header. Absent
// values are empty cells. The output ends with a trailing newline so a file
// written from it is POSIX-clean.
func SeriesCSV(rows []SeriesRow) string {
	var b strings.Builder
	b.WriteString("logical_date,pain,mood,capacity\n")
	for _, r := range rows {
		b.WriteString(r.Date)
		b.WriteByte(',')
		b.WriteString(cell(r.Pain))
		b.WriteByte(',')
		b.WriteString(cell(r.Mood))
		b.WriteByte(',')
		b.WriteString(cell(r.Capacity))
		b.WriteByte('\n')
	}
	return b.String()
}

// cell renders an optional integer as a CSV cell — empty when nil.
func cell(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

// maxPtr returns a pointer to the larger of an existing optional value and a
// new reading.
func maxPtr(cur *int, v int) *int {
	if cur == nil || v > *cur {
		nv := v
		return &nv
	}
	return cur
}

// payloadInt coerces a payload numeric (a Go int built in-process or a JSON
// float64 read from disk) to an int, reporting whether the key held a number.
func payloadInt(payload map[string]any, key string) (int, bool) {
	switch n := payload[key].(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// payloadStr returns a payload string field, or "" when absent.
func payloadStr(payload map[string]any, key string) string {
	if s, ok := payload[key].(string); ok {
		return s
	}
	return ""
}

// payloadBool returns a payload boolean field with a default when absent.
func payloadBool(payload map[string]any, key string, def bool) bool {
	if b, ok := payload[key].(bool); ok {
		return b
	}
	return def
}
