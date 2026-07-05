package engine

import "time"

// ResolveBackfillTarget picks the default `/closeout backfill` target: the
// most recent logical day without a completed record, scanning back from
// yesterday up to window days (engine-module.md §"/closeout backfill
// sequence": "the most recent logical day without a completed record").
// When every day in the window is already completed there is nothing to
// backfill, so it returns yesterday — the router then reports the
// idempotent no-op. records are expected folded.
func ResolveBackfillTarget(records []DayRecord, today time.Time, window int) time.Time {
	completed := completedDateSet(records)
	yesterday := AddDays(DateOf(today), -1)
	for i := 0; i < window; i++ {
		d := AddDays(yesterday, -i)
		if !completed[DateString(d)] {
			return d
		}
	}
	return yesterday
}

// BackfillInWindow reports whether target is within window days of today —
// the guard for engine-module.md §Error states ("/closeout backfill beyond
// backfill_window_days" is rejected). A future target (span < 1) is out of
// window: backfill only records past days.
func BackfillInWindow(target, today time.Time, window int) bool {
	span := DaysBetween(target, today)
	return span >= 1 && span <= window
}

// completedDateSet is the set of YYYY-MM-DD strings with a completed
// (folded) record.
func completedDateSet(records []DayRecord) map[string]bool {
	out := map[string]bool{}
	for _, r := range records {
		if r.Completed {
			out[r.LogicalDate] = true
		}
	}
	return out
}
