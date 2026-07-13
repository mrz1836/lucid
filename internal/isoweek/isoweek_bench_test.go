package isoweek

import (
	"testing"
	"time"
)

// BenchmarkID tracks the cost of the ISO-week id, called throughout scheduling
// and reflection rollover. The instant is a mid-week, mid-year moment so no
// path short-circuits on a boundary.
func BenchmarkID(b *testing.B) {
	instant := time.Date(2026, 7, 5, 18, 41, 39, 0, time.UTC)
	b.ReportAllocs()
	for b.Loop() {
		_ = ID(instant)
	}
}

// BenchmarkBounds tracks the Monday–Sunday window computation used to frame
// reflection ranges.
func BenchmarkBounds(b *testing.B) {
	instant := time.Date(2026, 7, 5, 18, 41, 39, 0, time.UTC)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = Bounds(instant)
	}
}
