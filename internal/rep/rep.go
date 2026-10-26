// Package rep is the practice-rep record family (reps.md): the small concrete
// steps a person keeps and surfaces one-per-day, each with an optional solo
// variant and an optional level, stored as append-only, schema'd JSONL entries
// under ~/.lucid/reps/. Like the focus and reframe cores it is entirely
// agent-free — deterministic value functions with no LLM in any path
// (architecture P9) and no filesystem access (architecture P3: the storage
// adapter is the only code that touches ~/.lucid/). This package owns the entry
// envelope, its id grammar, byte-stable (un)marshaling, and the pure active/
// retired state fold; the storage adapter owns the append/read discipline and the
// separate, rebuildable surface-state projection.
package rep

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// Schema is the entry schema version (reps.md §2). It is versioned per record
// family exactly as the observation, reframe, and focus envelopes are: new needs
// go in tags, refs, or a new optional field under a bumped schema — readers
// tolerate an unknown field and a higher version.
const Schema = 1

// Rep sources (reps.md §2 "source"): provenance for every entry, so a hand-typed
// rep is always distinguishable from a one-time bulk import.
const (
	// SourceRep is a normal `lucid rep add` (and the retirement events
	// `lucid rep retire` appends).
	SourceRep = "rep"
	// SourceMigration is a one-time bulk import of pre-existing reps.
	SourceMigration = "migration"
)

// Rep states (reps.md §2 "state"). A rep is active until a retirement event
// references it; retirement is append-only, so history is preserved — a retired
// rep stays on the record, it is only excluded from the surface and the default
// list.
const (
	// StateActive is a live rep the surface rotates through.
	StateActive = "active"
	// StateRetired is a rep retired by a later retirement event — kept for the
	// audit view (`list --all`), never surfaced.
	StateRetired = "retired"
)

// refsRetires is the reserved refs key naming the rep a retirement event retires
// (reps.md §2 "Append-only, retired by reference"). Kept private; the
// [NewRetirement] constructor is the only supported way to mint the marker.
const refsRetires = "retires"

// dateLayout is the logical_date string form (YYYY-MM-DD, local civil date),
// matching the observation, reframe, and focus layers' join-key form.
const dateLayout = "2006-01-02"

// Rep is one practice-rep entry, appended verbatim as one JSONL line to its
// per-logical-day file (reps.md §2). Field order matches the documented schema
// so a marshaled entry reads like the spec; encoding/json sorts map keys and
// preserves struct field order, so the same entry always marshals to the same
// bytes — the property the single-writer append discipline relies on.
//
// Solo and Level carry no omitempty: both are always emitted (Solo as "" when
// there is no solo variant, Level as 0 when the rep is unlevelled) so an
// optional field never changes an entry's shape. A rep carries exactly one
// primary text and at most one solo variant — there is no third variant field.
// Level is the user's own label for how big a step the rep is; it has no
// maximum and no meaning inside this package beyond being non-negative.
type Rep struct {
	ID          string         `json:"id"`
	Schema      int            `json:"schema"`
	Text        string         `json:"text"`
	Solo        string         `json:"solo"`
	Level       int            `json:"level"`
	State       string         `json:"state"`
	RecordedAt  string         `json:"recorded_at"`
	LogicalDate string         `json:"logical_date"`
	Source      string         `json:"source"`
	Tags        []string       `json:"tags"`
	Refs        map[string]any `json:"refs"`
}

// NewRetirement builds an append-only retirement event that retires the rep
// named by targetID (reps.md §2, §5). The storage adapter assigns its id and
// appends it; [FoldState] reads its refs.retires to mark the target retired. It
// carries no text — it is a state-change marker, not a rep — and so it is
// omitted from the folded view while still being byte-preserved on disk.
func NewRetirement(targetID, logicalDate, recordedAt string) Rep {
	return Rep{
		Schema:      Schema,
		State:       StateRetired,
		RecordedAt:  recordedAt,
		LogicalDate: logicalDate,
		Source:      SourceRep,
		Refs:        map[string]any{refsRetires: targetID},
	}
}

// MarshalLine renders the rep as exactly one compact JSON line (no trailing
// newline). The caller (the storage adapter) adds the line terminator and
// fsyncs. Collection fields are normalized so an entry with no tags or refs
// marshals as [] / {} rather than null — a stable on-disk shape.
func (r Rep) MarshalLine() ([]byte, error) {
	r = r.normalized()
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("rep: marshal entry: %w", err)
	}
	return b, nil
}

// UnmarshalRepLine parses one JSONL line into a Rep. A malformed line is a
// decode error the reader turns into a skip-and-count, never a crash (reps.md
// §2; error-states "JSONL corruption").
func UnmarshalRepLine(line []byte) (Rep, error) {
	var r Rep
	if err := json.Unmarshal(line, &r); err != nil {
		return Rep{}, fmt.Errorf("rep: parse entry line: %w", err)
	}
	return r, nil
}

// Validate reports the first structural problem with an entry before it is
// appended (reps.md §2). It guards the required fields the writer cannot invent;
// the text itself is stored verbatim, so any non-empty text is valid. A
// retirement event carries no text — it is a state-change marker keyed by
// refs.retires — so the text requirement is waived for it. A level of 0 is the
// unlevelled value and is valid; only a negative level is rejected here (the CLI
// additionally refuses an explicitly given level below 1).
func (r Rep) Validate() error {
	if r.Schema != Schema {
		return fmt.Errorf("rep: unsupported schema %d (want %d)", r.Schema, Schema)
	}
	if !r.IsRetirement() && strings.TrimSpace(r.Text) == "" {
		return fmt.Errorf("rep: text is required")
	}
	if r.Level < 0 {
		return fmt.Errorf("rep: level must be a positive integer (or 0 for unlevelled), got %d", r.Level)
	}
	if r.State != StateActive && r.State != StateRetired {
		return fmt.Errorf("rep: state must be %q or %q", StateActive, StateRetired)
	}
	if r.RecordedAt == "" {
		return fmt.Errorf("rep: recorded_at is required")
	}
	if r.LogicalDate == "" {
		return fmt.Errorf("rep: logical_date is required")
	}
	if _, err := time.Parse(dateLayout, r.LogicalDate); err != nil {
		return fmt.Errorf("rep: logical_date must be YYYY-MM-DD, got %q", r.LogicalDate)
	}
	if r.Source == "" {
		return fmt.Errorf("rep: source is required")
	}
	return nil
}

// Retires returns the id of the rep this entry retires and whether one is set
// (reps.md §2). A retirement event is a new entry whose refs.retires names its
// target; readers mark the target retired and drop the marker itself from the
// rep view (see [FoldState]).
func (r Rep) Retires() (string, bool) {
	if r.Refs == nil {
		return "", false
	}
	v, ok := r.Refs[refsRetires]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// IsRetirement reports whether this entry is a retirement event rather than a
// rep — true exactly when refs.retires names a target.
func (r Rep) IsRetirement() bool {
	_, ok := r.Retires()
	return ok
}

// normalized returns a copy with the collection fields never nil, so an entry
// with no tags or refs marshals as [] / {} rather than null.
func (r Rep) normalized() Rep {
	if r.Tags == nil {
		r.Tags = []string{}
	}
	if r.Refs == nil {
		r.Refs = map[string]any{}
	}
	return r
}

// FoldState resolves each rep's current state and returns the rep view (reps.md
// §2, §5). A rep whose id is named by a later retirement event's refs.retires is
// returned with State=retired; every other rep is returned with State=active.
// Nothing is dropped from history — a retired rep stays in the result (the audit
// view shows it); only the retirement markers themselves are omitted, since they
// are state changes, not reps. Output preserves input order, so a by-id-sorted
// input yields a by-id-sorted result.
func FoldState(in []Rep) []Rep {
	retired := make(map[string]bool, len(in))
	for _, r := range in {
		if target, ok := r.Retires(); ok {
			retired[target] = true
		}
	}
	out := make([]Rep, 0, len(in))
	for _, r := range in {
		if r.IsRetirement() {
			continue // a state-change marker, not a rep
		}
		if retired[r.ID] {
			r.State = StateRetired
		} else {
			r.State = StateActive
		}
		out = append(out, r)
	}
	return out
}

// ID renders the entry id for a logical date and sequence (reps.md §2:
// rep_<logical_date>_<seq>, the date in underscores, seq zero-padded to three
// digits, wider values legal). A backdated entry's id therefore encodes the
// logical date, not the recording time.
func ID(logicalDate string, seq int) string {
	return fmt.Sprintf("rep_%s_%03d", strings.ReplaceAll(logicalDate, "-", "_"), seq)
}

// ParseSeq extracts the numeric sequence from an entry id, parsed numerically so
// a wider value (rep_..._1000) is legal (reps.md §2). It returns ok=false for any
// id that is not a well-formed rep id — the single-writer seq derivation ignores
// such lines rather than counting them.
func ParseSeq(id string) (seq int, ok bool) {
	if !strings.HasPrefix(id, "rep_") {
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

// Date extracts the YYYY-MM-DD logical date a rep id encodes — the read-side
// inverse of [ID] (rep_<logical_date>_<seq>, the date in underscores). It
// reports ok=false for anything that is not a well-formed rep id: a missing rep_
// prefix, a date component that is not three underscore-joined fields forming a
// real civil date, or no sequence field after them. The date is returned in
// dashed form so it feeds the day-file readers directly.
func Date(id string) (date string, ok bool) {
	rest, cut := strings.CutPrefix(id, "rep_")
	if !cut {
		return "", false
	}
	// rep_YYYY_MM_DD_seq → the first three fields are the date, and a fourth
	// field must exist so a bare rep_YYYY_MM_DD (no seq) is not a valid id.
	parts := strings.SplitN(rest, "_", 4)
	if len(parts) < 4 {
		return "", false
	}
	d := parts[0] + "-" + parts[1] + "-" + parts[2]
	if _, err := time.Parse(dateLayout, d); err != nil {
		return "", false
	}
	return d, true
}

// LogicalDay returns the logical-day key for an instant — the 04:00-rollover
// civil day the observation, reframe, focus, and Engine surfaces already use
// (reps.md §4), so "today" means the same boundary everywhere in the Ledger. It
// reuses the observation layer's rollover primitives, so the rep rotation and the
// day-view join stay aligned. The CLI passes now; storage takes the resolved key
// so its rotation logic is deterministic and injectable in tests.
func LogicalDay(now time.Time) string {
	return observations.DateString(observations.LogicalBaseDate(now, observations.DefaultRolloverMin))
}
