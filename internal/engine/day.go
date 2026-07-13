package engine

import (
	"slices"
	"time"
)

// Link statuses recorded per link in a day record (engine-module.md
// §"/closeout sequence" step 3).
const (
	StatusDone    = "done"
	StatusFloor   = "floor"
	StatusSkipped = "skipped"
)

// Engine modes (engine-module.md §2). Green is the default when the day
// was never explicitly declared; /mode (Phase 9) sets it at the bell.
const (
	ModeGreen  = "green"
	ModeYellow = "yellow"
	ModeRed    = "red"
)

// Correction is one appended amendment to a day record (engine-module.md
// §corrections[]). The record body is never rewritten; corrections are
// folded at read time, last write per field winning.
type Correction struct {
	At     string         `json:"at"`
	Fields map[string]any `json:"fields"`
	Reason string         `json:"reason"`
	Source string         `json:"source"`
}

// DayRecord is one logical day's Engine record (engine-module.md §"Day
// record"). Field order matches the documented schema. capacity and
// limiter_tag live only here — Engine telemetry, never surfaced to the
// witness view.
type DayRecord struct {
	DayID          string            `json:"day_id"`
	LogicalDate    string            `json:"logical_date"`
	RecordedAt     string            `json:"recorded_at"`
	Mode           string            `json:"mode"`
	ModeDeclaredAt string            `json:"mode_declared_at"`
	Links          map[string]string `json:"links"`
	FloorDay       bool              `json:"floor_day"`
	Completed      bool              `json:"completed"`
	Missed         bool              `json:"missed"`
	Partial        bool              `json:"partial"`
	Capacity       int               `json:"capacity"`
	LimiterTag     string            `json:"limiter_tag"`
	RawEntryID     string            `json:"raw_entry_id"`
	Backfilled     bool              `json:"backfilled"`
	Storm          bool              `json:"storm"`
	Profile        string            `json:"profile"`
	Corrections    []Correction      `json:"corrections"`
}

// FoldableField reports whether a correction may amend the day-record
// field name (engine-module.md §corrections[]). Any other field is
// immutable — a correction naming it is rejected before it reaches disk.
func FoldableField(name string) bool {
	switch name {
	case "links", "floor_day", "completed", "missed", "partial", "capacity", "limiter_tag", "raw_entry_id":
		return true
	default:
		return false
	}
}

// ImmutableCorrectionFields returns the never-foldable keys present in a
// correction's Fields, in sorted order — empty when the correction is
// legal. The AppendCorrection storage op rejects a correction that names
// any of them (engine-module.md §Error states: "Correction attempts an
// immutable field").
func ImmutableCorrectionFields(c Correction) []string {
	var bad []string
	for k := range c.Fields {
		if !FoldableField(k) {
			bad = append(bad, k)
		}
	}
	slices.Sort(bad)
	return bad
}

// Folded returns the effective day record with every correction applied
// in array order (engine-module.md §corrections[] fold rule: last write
// per field wins; the original record body stays untouched). The receiver
// is not mutated — its Links map is deep-copied first.
func (r DayRecord) Folded() DayRecord {
	out := r
	out.Links = cloneLinks(r.Links)
	for _, c := range r.Corrections {
		for k, v := range c.Fields {
			applyFold(&out, k, v)
		}
	}
	return out
}

// applyFold applies one foldable field from a correction. Values arrive as
// either Go-native types (corrections built in-process) or JSON-decoded
// types (corrections read from disk), so each case tolerates both. An
// unfoldable or ill-typed field is ignored here — the append path already
// rejected immutable fields, so this only guards against a hand-edited
// file.
func applyFold(r *DayRecord, key string, v any) {
	switch key {
	case "links":
		if m, ok := toStringMap(v); ok {
			r.Links = m
		}
	case "floor_day":
		if b, ok := toBool(v); ok {
			r.FloorDay = b
		}
	case "completed":
		if b, ok := toBool(v); ok {
			r.Completed = b
		}
	case "missed":
		if b, ok := toBool(v); ok {
			r.Missed = b
		}
	case "partial":
		if b, ok := toBool(v); ok {
			r.Partial = b
		}
	case "capacity":
		if n, ok := toInt(v); ok {
			r.Capacity = n
		}
	case "limiter_tag":
		if s, ok := v.(string); ok {
			r.LimiterTag = s
		}
	case "raw_entry_id":
		if s, ok := v.(string); ok {
			r.RawEntryID = s
		}
	}
}

// cloneLinks returns a deep copy of a links map (nil stays nil).
func cloneLinks(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// toBool coerces a Go bool or JSON bool.
func toBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

// toInt coerces a Go int or a JSON number (float64) to int.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
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

// toStringMap coerces a map[string]string or a JSON-decoded
// map[string]any of strings.
func toStringMap(v any) (map[string]string, bool) {
	switch m := v.(type) {
	case map[string]string:
		return cloneLinks(m), true
	case map[string]any:
		out := make(map[string]string, len(m))
		for k, val := range m {
			s, ok := val.(string)
			if !ok {
				return nil, false
			}
			out[k] = s
		}
		return out, true
	default:
		return nil, false
	}
}

// Streaks is the streak arithmetic derived from folded day records: the
// current run of consecutive completed logical days ending at the most
// recent completed day, and the longest such run ever.
type Streaks struct {
	Current int
	Longest int
}

// ComputeStreaks folds the completed logical dates of records into the
// current and longest consecutive-day runs. "Consecutive" means
// calendar-adjacent civil dates: a gap (a missing or missed day) breaks
// the run. Records are expected already folded; only Completed and
// LogicalDate are read. loc is the location the dates are interpreted in.
func ComputeStreaks(records []DayRecord, loc *time.Location) Streaks {
	dates := completedDates(records, loc)
	if len(dates) == 0 {
		return Streaks{}
	}
	slices.SortFunc(dates, func(a, b time.Time) int { return a.Compare(b) })

	longest, run := 1, 1
	for i := 1; i < len(dates); i++ {
		if DaysBetween(dates[i-1], dates[i]) == 1 {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
	}

	// Current streak: walk back from the most recent completed day.
	current := 1
	for i := len(dates) - 1; i > 0; i-- {
		if DaysBetween(dates[i-1], dates[i]) == 1 {
			current++
		} else {
			break
		}
	}
	return Streaks{Current: current, Longest: longest}
}

// completedDates returns the deduplicated civil dates of completed
// records, parsed in loc.
func completedDates(records []DayRecord, loc *time.Location) []time.Time {
	if loc == nil {
		loc = time.UTC
	}
	seen := map[string]bool{}
	var out []time.Time
	for _, r := range records {
		if !r.Completed || seen[r.LogicalDate] {
			continue
		}
		d, err := time.ParseInLocation(dateLayout, r.LogicalDate, loc)
		if err != nil {
			continue
		}
		seen[r.LogicalDate] = true
		out = append(out, d)
	}
	return out
}

// EarliestCompletedDate returns the YYYY-MM-DD of the earliest completed
// record, or "" when none is completed. It stamps chain_start on the first
// completed close-out (engine-module.md §chain.json: "set automatically on
// the first completed close-out and never changed thereafter").
func EarliestCompletedDate(records []DayRecord, loc *time.Location) string {
	dates := completedDates(records, loc)
	if len(dates) == 0 {
		return ""
	}
	slices.SortFunc(dates, func(a, b time.Time) int { return a.Compare(b) })
	return DateString(dates[0])
}
