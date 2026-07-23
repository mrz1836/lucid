package engine

import "time"

// WeekWindow is the 7-day adherence rollup ending at ref — "this week" for the
// weekly witness report. It is a thin, exported wrapper over the same
// windowStats fold the 30-day adherence and the 30/60/90 gate rollups already
// use, so a witness-facing weekly number is produced by the identical
// arithmetic as the metrics projection and can never diverge from it. Pure: no
// model, no IO. A day is "decided" once completed or missed; weekly misses are
// Decided - Completed on the returned Window (floors count as completions —
// teeth math has no shame score, so a Yellow floor day scores 1.0). A nil loc
// falls back to UTC, matching BuildMetrics/BuildStatus, so a caller that never
// resolves a location cannot panic the fold.
func WeekWindow(records []DayRecord, ref time.Time, loc *time.Location) Window {
	if loc == nil {
		loc = time.UTC
	}
	return windowStats(records, ref, 7, loc)
}
