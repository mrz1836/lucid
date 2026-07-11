// Package observations is the MVP slice of the observation & enrichment
// layer (observations.md, mvp/observations-module.md): micro-log capture,
// the salted registries, and the day-view join. Like the Engine module it
// is entirely agent-free — deterministic parsers, arithmetic, and template
// output, with no LLM in any path (architecture P9). The storage adapter is
// the only code that touches ~/.lucid/; this package owns the frozen event
// envelope, the capture grammar, registry key derivation, and the read-only
// day-view assembly, all as pure functions over values.
package observations

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schema is the frozen envelope's schema version. New needs go in payload
// (versioned per kind), tags, or refs — never a new top-level field
// (observations.md §2 "The envelope never changes").
const Schema = 1

// occurred_at precision values (observations.md §2, mirroring raw entries):
// the moment is known exactly, only approximately (a placeholder date), or
// spans a range with an end.
const (
	PrecisionExact       = "exact"
	PrecisionApproximate = "approximate"
	PrecisionRange       = "range"
)

// Event sources (observations.md §2 "source"): provenance for every event,
// so machine-added context is always distinguishable from human testimony.
// The MVP capture surface writes SourceMicrolog; enrichers (Phase 12) write
// enricher:<name>.
const (
	SourceMicrolog   = "microlog"
	SourceCheckin    = "checkin"
	SourceCloseout   = "closeout"
	SourceExcavation = "excavation"
)

// DefaultRolloverMin is the civil-day rollover boundary applied to an
// exact-precision occurred_at (04:00), matching the Engine default profile
// (engine-module.md §1). observations.md §2 binds logical_date derivation to
// "the rollover boundary (engine §1)"; the MVP default profile uses 04:00,
// so the two trees resolve the same logical day — the join key stays aligned.
// Phase 11 is independent of the Engine phases, so the value is a constant
// here rather than a read of chain.json.
const DefaultRolloverMin = 4 * 60

// dateLayout is the logical_date string form (YYYY-MM-DD, local civil date).
const dateLayout = "2006-01-02"

// Event is one observation, the frozen envelope (observations.md §2). Field
// order matches the documented schema so a marshaled event reads like the
// spec. It is appended verbatim as one JSONL line to the per-logical-day
// file; the envelope never grows a top-level field.
type Event struct {
	ID                  string         `json:"id"`
	Schema              int            `json:"schema"`
	Kind                string         `json:"kind"`
	RecordedAt          string         `json:"recorded_at"`
	OccurredAt          string         `json:"occurred_at"`
	OccurredAtPrecision string         `json:"occurred_at_precision"`
	OccurredAtEnd       *string        `json:"occurred_at_end"`
	LogicalDate         string         `json:"logical_date"`
	Source              string         `json:"source"`
	Payload             map[string]any `json:"payload"`
	Tags                []string       `json:"tags"`
	Refs                map[string]any `json:"refs"`
}

// MarshalLine renders the event as exactly one compact JSON line (no
// trailing newline). encoding/json sorts map keys and preserves struct
// field order, so the same event always marshals to the same bytes — the
// property the /day byte-stability and single-writer append discipline rely
// on. The caller (the storage adapter) adds the line terminator and fsyncs.
func (e Event) MarshalLine() ([]byte, error) {
	e = e.normalized()
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("observations: marshal event: %w", err)
	}
	return b, nil
}

// UnmarshalEventLine parses one JSONL line into an Event. A malformed line
// is a decode error the reader turns into a skip-and-count, never a crash
// (observations.md §2 ids; error-states "JSONL corruption").
func UnmarshalEventLine(line []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return Event{}, fmt.Errorf("observations: parse event line: %w", err)
	}
	return e, nil
}

// Validate reports the first structural problem with an event before it is
// appended. It guards the frozen fields the writer cannot invent; payload
// contents are the parser's concern (capture never blocks, so a partial
// payload is valid — validation here is about the envelope, not the value).
func (e Event) Validate() error {
	if e.Schema != Schema {
		return fmt.Errorf("observations: unsupported schema %d (want %d)", e.Schema, Schema)
	}
	if e.Kind == "" {
		return fmt.Errorf("observations: event kind is required")
	}
	if e.RecordedAt == "" {
		return fmt.Errorf("observations: recorded_at is required")
	}
	if e.OccurredAt == "" {
		return fmt.Errorf("observations: occurred_at is required")
	}
	switch e.OccurredAtPrecision {
	case PrecisionExact, PrecisionApproximate, PrecisionRange:
	default:
		return fmt.Errorf("observations: invalid occurred_at_precision %q", e.OccurredAtPrecision)
	}
	if e.OccurredAtPrecision == PrecisionRange && e.OccurredAtEnd == nil {
		return fmt.Errorf("observations: range precision requires occurred_at_end")
	}
	if e.LogicalDate == "" {
		return fmt.Errorf("observations: logical_date is required")
	}
	if e.Source == "" {
		return fmt.Errorf("observations: source is required")
	}
	return nil
}

// normalized returns a copy with the collection fields never nil, so an
// event with no tags or refs marshals as [] / {} rather than null — a stable
// on-disk shape across events.
func (e Event) normalized() Event {
	if e.Payload == nil {
		e.Payload = map[string]any{}
	}
	if e.Tags == nil {
		e.Tags = []string{}
	}
	if e.Refs == nil {
		e.Refs = map[string]any{}
	}
	return e
}

// DeriveLogicalDate computes the universal join key for an event
// (observations.md §2, binding). It is the file-placement rule, so it must
// match the Engine's civil-day boundary for the day view to join:
//
//   - exact: apply the rollover boundary to occurred_at's own wall clock in
//     its own recorded offset — before the mark the moment belongs to
//     yesterday, at or after it to today.
//   - approximate: the plain calendar date of occurred_at, no rollover — an
//     approximate midnight ("around September 2014") must not slip to the
//     day before.
//   - range: the calendar date of the range start.
//
// rolloverMin is the boundary in minutes since local midnight; pass
// [DefaultRolloverMin] for the MVP default profile.
func DeriveLogicalDate(occurredAt time.Time, precision string, rolloverMin int) string {
	switch precision {
	case PrecisionExact:
		return DateString(LogicalBaseDate(occurredAt, rolloverMin))
	default: // approximate and range both key on the plain calendar date
		return DateString(DateOf(occurredAt))
	}
}

// LogicalBaseDate truncates t to the civil midnight of its logical day under
// the exact-precision rollover rule (observations.md §2): before rolloverMin
// the moment belongs to the previous day, at or after it to t's own date. It
// is the time.Time companion to DeriveLogicalDate's exact branch — the /day
// view and /closeout backfill resolve "today"/"yesterday" through it so they
// land on the same civil-day boundary events are filed under, never a naive
// calendar date (which, before the rollover, names the day still in progress).
func LogicalBaseDate(t time.Time, rolloverMin int) time.Time {
	base := DateOf(t)
	if minutesOfDay(t) < rolloverMin {
		return base.AddDate(0, 0, -1)
	}
	return base
}

// EventID renders the event id for a logical date and sequence
// (observations.md §2: obs_<logical_date>_<seq>, the date in underscores,
// seq zero-padded to three digits, wider values legal). An excavated
// memory's id therefore encodes the logical date, not the recording time.
func EventID(logicalDate string, seq int) string {
	return fmt.Sprintf("obs_%s_%03d", strings.ReplaceAll(logicalDate, "-", "_"), seq)
}

// ParseSeq extracts the numeric sequence from an event id, parsed
// numerically so a wider value (obs_..._1000) is legal (observations.md §2).
// It returns ok=false for any id that is not a well-formed obs id — the
// single-writer seq derivation ignores such lines rather than counting them.
func ParseSeq(id string) (seq int, ok bool) {
	if !strings.HasPrefix(id, "obs_") {
		return 0, false
	}
	i := strings.LastIndex(id, "_")
	if i < 0 || i+1 >= len(id) {
		return 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// DateOf truncates t to local civil midnight — the anchor for a logical day.
func DateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// DateString renders a civil date as YYYY-MM-DD.
func DateString(d time.Time) string { return d.Format(dateLayout) }

// ParseDate parses a YYYY-MM-DD civil date in loc.
func ParseDate(s string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	return time.ParseInLocation(dateLayout, s, loc)
}

// minutesOfDay returns t's time-of-day as minutes since local midnight.
func minutesOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }
