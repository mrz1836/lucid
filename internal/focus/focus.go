// Package focus is the focus record family (focus.md): the slow inner "work-ons"
// a person keeps and surfaces one-per-day, stored as append-only, schema'd JSONL
// entries under ~/.lucid/focus/. Like the reframe and observation cores it is
// entirely agent-free — deterministic value functions with no LLM in any path
// (architecture P9) and no filesystem access (architecture P3: the storage
// adapter is the only code that touches ~/.lucid/). This package owns the entry
// envelope, its id grammar, byte-stable (un)marshaling, and the pure active/
// retired state fold; the storage adapter owns the append/read discipline and the
// separate, rebuildable surface-state projection.
package focus

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// Schema is the entry schema version (focus.md §2). It is versioned per record
// family exactly as the observation and reframe envelopes are: new needs go in
// tags, refs, or a new optional field under a bumped schema — readers tolerate an
// unknown field and a higher version.
const Schema = 1

// Focus sources (focus.md §2 "source"): provenance for every entry, so a
// hand-typed focus is always distinguishable from a one-time bulk import.
const (
	// SourceFocus is a normal `lucid focus add` (and the retirement events
	// `lucid focus retire` appends).
	SourceFocus = "focus"
	// SourceMigration is a one-time bulk import of pre-existing focus items.
	SourceMigration = "migration"
)

// Focus states (focus.md §2 "state"). An item is active until a retirement event
// references it; retirement is append-only, so history is preserved — a retired
// item stays on the record, it is only excluded from the surface and the default
// list.
const (
	// StateActive is a live focus item the surface rotates through.
	StateActive = "active"
	// StateRetired is an item retired by a later retirement event — kept for the
	// audit view (`list --all`), never surfaced.
	StateRetired = "retired"
)

// refsRetires is the reserved refs key naming the focus item a retirement event
// retires (focus.md §2 "Append-only, retired by reference"). Kept private; the
// [NewRetirement] constructor is the only supported way to mint the marker.
const refsRetires = "retires"

// dateLayout is the logical_date string form (YYYY-MM-DD, local civil date),
// matching the observation and reframe layers' join-key form.
const dateLayout = "2006-01-02"

// Focus is one "work-on" entry, appended verbatim as one JSONL line to its
// per-logical-day file (focus.md §2). Field order matches the documented schema
// so a marshaled entry reads like the spec; encoding/json sorts map keys and
// preserves struct field order, so the same entry always marshals to the same
// bytes — the property the single-writer append discipline relies on.
//
// SuccessCriterion carries no omitempty: it is always emitted (as "" when
// absent) so the migration evidence and the audit view show the field
// unconditionally, and an optional criterion never changes an entry's shape.
type Focus struct {
	ID               string         `json:"id"`
	Schema           int            `json:"schema"`
	Text             string         `json:"text"`
	SuccessCriterion string         `json:"success_criterion"`
	State            string         `json:"state"`
	RecordedAt       string         `json:"recorded_at"`
	LogicalDate      string         `json:"logical_date"`
	Source           string         `json:"source"`
	Tags             []string       `json:"tags"`
	Refs             map[string]any `json:"refs"`
}

// NewRetirement builds an append-only retirement event that retires the focus
// item named by targetID (focus.md §2). The storage adapter assigns its id and
// appends it; [FoldState] reads its refs.retires to mark the target retired. It
// carries no text — it is a state-change marker, not a focus item, and so it is
// omitted from the folded item view while still being byte-preserved on disk.
func NewRetirement(targetID, logicalDate, recordedAt string) Focus {
	return Focus{
		Schema:      Schema,
		State:       StateRetired,
		RecordedAt:  recordedAt,
		LogicalDate: logicalDate,
		Source:      SourceFocus,
		Refs:        map[string]any{refsRetires: targetID},
	}
}

// MarshalLine renders the focus as exactly one compact JSON line (no trailing
// newline). The caller (the storage adapter) adds the line terminator and
// fsyncs. Collection fields are normalized so an entry with no tags or refs
// marshals as [] / {} rather than null — a stable on-disk shape.
func (f Focus) MarshalLine() ([]byte, error) {
	f = f.normalized()
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("focus: marshal entry: %w", err)
	}
	return b, nil
}

// UnmarshalFocusLine parses one JSONL line into a Focus. A malformed line is a
// decode error the reader turns into a skip-and-count, never a crash (focus.md
// §2; error-states "JSONL corruption").
func UnmarshalFocusLine(line []byte) (Focus, error) {
	var f Focus
	if err := json.Unmarshal(line, &f); err != nil {
		return Focus{}, fmt.Errorf("focus: parse entry line: %w", err)
	}
	return f, nil
}

// Validate reports the first structural problem with an entry before it is
// appended (focus.md §2). It guards the required fields the writer cannot invent;
// the text itself is stored verbatim, so any non-empty text is valid. A
// retirement event carries no text — it is a state-change marker keyed by
// refs.retires — so the text requirement is waived for it.
func (f Focus) Validate() error {
	if f.Schema != Schema {
		return fmt.Errorf("focus: unsupported schema %d (want %d)", f.Schema, Schema)
	}
	if !f.IsRetirement() && strings.TrimSpace(f.Text) == "" {
		return fmt.Errorf("focus: text is required")
	}
	if f.State != StateActive && f.State != StateRetired {
		return fmt.Errorf("focus: state must be %q or %q", StateActive, StateRetired)
	}
	if f.RecordedAt == "" {
		return fmt.Errorf("focus: recorded_at is required")
	}
	if f.LogicalDate == "" {
		return fmt.Errorf("focus: logical_date is required")
	}
	if f.Source == "" {
		return fmt.Errorf("focus: source is required")
	}
	return nil
}

// Retires returns the id of the focus item this entry retires and whether one is
// set (focus.md §2). A retirement event is a new entry whose refs.retires names
// its target; readers mark the target retired and drop the marker itself from the
// item view (see [FoldState]).
func (f Focus) Retires() (string, bool) {
	if f.Refs == nil {
		return "", false
	}
	v, ok := f.Refs[refsRetires]
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
// focus item — true exactly when refs.retires names a target.
func (f Focus) IsRetirement() bool {
	_, ok := f.Retires()
	return ok
}

// normalized returns a copy with the collection fields never nil, so an entry
// with no tags or refs marshals as [] / {} rather than null.
func (f Focus) normalized() Focus {
	if f.Tags == nil {
		f.Tags = []string{}
	}
	if f.Refs == nil {
		f.Refs = map[string]any{}
	}
	return f
}

// FoldState resolves each focus item's current state and returns the item view
// (focus.md §2). An item whose id is named by a later retirement event's
// refs.retires is returned with State=retired; every other item is returned with
// State=active. Nothing is dropped from history — a retired item stays in the
// result (the audit view shows it); only the retirement markers themselves are
// omitted, since they are state changes, not focus items. Output preserves input
// order, so a by-id-sorted input yields a by-id-sorted result.
func FoldState(in []Focus) []Focus {
	retired := make(map[string]bool, len(in))
	for _, f := range in {
		if target, ok := f.Retires(); ok {
			retired[target] = true
		}
	}
	out := make([]Focus, 0, len(in))
	for _, f := range in {
		if f.IsRetirement() {
			continue // a state-change marker, not a focus item
		}
		if retired[f.ID] {
			f.State = StateRetired
		} else {
			f.State = StateActive
		}
		out = append(out, f)
	}
	return out
}

// FocusID renders the entry id for a logical date and sequence (focus.md §2:
// focus_<logical_date>_<seq>, the date in underscores, seq zero-padded to three
// digits, wider values legal). A backdated entry's id therefore encodes the
// logical date, not the recording time.
func FocusID(logicalDate string, seq int) string {
	return fmt.Sprintf("focus_%s_%03d", strings.ReplaceAll(logicalDate, "-", "_"), seq)
}

// ParseSeq extracts the numeric sequence from an entry id, parsed numerically so
// a wider value (focus_..._1000) is legal (focus.md §2). It returns ok=false for
// any id that is not a well-formed focus id — the single-writer seq derivation
// ignores such lines rather than counting them.
func ParseSeq(id string) (seq int, ok bool) {
	if !strings.HasPrefix(id, "focus_") {
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

// FocusDate extracts the YYYY-MM-DD logical date a focus id encodes — the
// read-side inverse of [FocusID] (focus_<logical_date>_<seq>, the date in
// underscores). It reports ok=false for anything that is not a well-formed focus
// id: a missing focus_ prefix, a date component that is not three underscore-
// joined fields forming a real civil date, or no sequence field after them. The
// date is returned in dashed form so it feeds the day-file readers directly.
func FocusDate(id string) (date string, ok bool) {
	rest, cut := strings.CutPrefix(id, "focus_")
	if !cut {
		return "", false
	}
	// focus_YYYY_MM_DD_seq → the first three fields are the date, and a fourth
	// field must exist so a bare focus_YYYY_MM_DD (no seq) is not a valid id.
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
// civil day the observation, reframe, and Engine surfaces already use (focus.md
// §4), so "today" means the same boundary everywhere in the Ledger. It reuses the
// observation layer's rollover primitives, so the focus rotation and the day-view
// join stay aligned. The CLI passes now; storage takes the resolved key so its
// rotation logic is deterministic and injectable in tests.
func LogicalDay(now time.Time) string {
	return observations.DateString(observations.LogicalBaseDate(now, observations.DefaultRolloverMin))
}
