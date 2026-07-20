package observations

import (
	"fmt"
	"testing"
)

// benchDayViewFixture builds nDay point events (in reverse id order so the sort
// has real work) plus nRange spanning range candidates, the shape the day-view
// join sees on the daily surface.
func benchDayViewFixture(nDay, nRange int) (day, candidates []Event) {
	day = make([]Event, 0, nDay)
	for i := range nDay {
		day = append(day, Event{
			ID:          fmt.Sprintf("obs_2026_07_02_%04d", nDay-i),
			Schema:      Schema,
			Kind:        KindPain,
			LogicalDate: "2026-07-02",
			Source:      SourceMicrolog,
			Payload:     map[string]any{"intensity": i % 10},
		})
	}
	candidates = make([]Event, 0, nRange)
	for i := range nRange {
		candidates = append(candidates, rangeEvent(
			fmt.Sprintf("obs_2026_07_01_%04d", i), "2026-07-01", "2026-07-02T07:10:00-04:00"))
	}
	return day, candidates
}

// BenchmarkAssembleDayView covers the daily-surface join hot path: sort the
// day's events and filter the range candidates that span into the day.
func BenchmarkAssembleDayView(b *testing.B) {
	day, candidates := benchDayViewFixture(64, 8)
	b.ReportAllocs()
	for b.Loop() {
		_ = AssembleDayView("2026-07-02", day, candidates, loc)
	}
}

// BenchmarkDayViewLines covers rendering the assembled view to its byte-stable
// lines (payload keys emitted in sorted order).
func BenchmarkDayViewLines(b *testing.B) {
	day, candidates := benchDayViewFixture(64, 8)
	dv := AssembleDayView("2026-07-02", day, candidates, loc)
	b.ReportAllocs()
	for b.Loop() {
		_ = dv.Lines()
	}
}

// BenchmarkSortEventsByID isolates the clone-and-stable-sort every projection
// runs to surface events in id (time) order.
func BenchmarkSortEventsByID(b *testing.B) {
	day, _ := benchDayViewFixture(64, 0)
	b.ReportAllocs()
	for b.Loop() {
		_ = SortEventsByID(day)
	}
}
