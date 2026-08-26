// Package reframes is the reframe record family (reframes.md): the self-talk
// "catch → flip" pairs a person keeps, stored as append-only, schema'd JSONL
// entries under ~/.lucid/reframes/. Like the observation core it is entirely
// agent-free — deterministic value functions with no LLM in any path
// (architecture P9) and no filesystem access (architecture P3: the storage
// adapter is the only code that touches ~/.lucid/). This package owns the
// entry envelope, its id grammar, byte-stable (un)marshaling, and the pure
// correction fold; the storage adapter owns the append/read discipline and the
// separate, rebuildable surface-state projection.
package reframes

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// Schema is the entry schema version (reframes.md §2). It is versioned per
// record family exactly as the observation envelope is: new needs go in tags,
// refs, or a new optional field under a bumped schema — readers tolerate an
// unknown field and a higher version.
const Schema = 1

// Reframe sources (reframes.md §2 "source"): provenance for every entry, so a
// hand-typed reframe is always distinguishable from a one-time bulk import.
const (
	// SourceReframe is a normal `lucid reframe add`.
	SourceReframe = "reframe"
	// SourceMigration is a one-time bulk import of pre-existing reframes.
	SourceMigration = "migration"
)

// refsCorrects is the reserved refs key naming an entry this one supersedes
// (reframes.md §2 "Append-only, corrected by reference").
const refsCorrects = "corrects"

// dateLayout is the logical_date string form (YYYY-MM-DD, local civil date),
// matching the observation layer's join-key form.
const dateLayout = "2006-01-02"

// Reframe is one "catch → flip" entry, appended verbatim as one JSONL line to
// its per-logical-day file (reframes.md §2). Field order matches the documented
// schema so a marshaled entry reads like the spec; encoding/json sorts map keys
// and preserves struct field order, so the same entry always marshals to the
// same bytes — the property the single-writer append discipline relies on.
type Reframe struct {
	ID          string         `json:"id"`
	Schema      int            `json:"schema"`
	Catch       string         `json:"catch"`
	Flip        string         `json:"flip"`
	RecordedAt  string         `json:"recorded_at"`
	LogicalDate string         `json:"logical_date"`
	Source      string         `json:"source"`
	Tags        []string       `json:"tags"`
	Refs        map[string]any `json:"refs"`
}

// MarshalLine renders the reframe as exactly one compact JSON line (no trailing
// newline). The caller (the storage adapter) adds the line terminator and
// fsyncs. Collection fields are normalized so an entry with no tags or refs
// marshals as [] / {} rather than null — a stable on-disk shape.
func (r Reframe) MarshalLine() ([]byte, error) {
	r = r.normalized()
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("reframes: marshal entry: %w", err)
	}
	return b, nil
}

// UnmarshalReframeLine parses one JSONL line into a Reframe. A malformed line is
// a decode error the reader turns into a skip-and-count, never a crash
// (reframes.md §2; error-states "JSONL corruption").
func UnmarshalReframeLine(line []byte) (Reframe, error) {
	var r Reframe
	if err := json.Unmarshal(line, &r); err != nil {
		return Reframe{}, fmt.Errorf("reframes: parse entry line: %w", err)
	}
	return r, nil
}

// Validate reports the first structural problem with an entry before it is
// appended (reframes.md §2). It guards the required fields the writer cannot
// invent; the catch/flip text itself is stored verbatim, so any non-empty pair
// is valid.
func (r Reframe) Validate() error {
	if r.Schema != Schema {
		return fmt.Errorf("reframes: unsupported schema %d (want %d)", r.Schema, Schema)
	}
	if strings.TrimSpace(r.Catch) == "" {
		return fmt.Errorf("reframes: catch is required")
	}
	if strings.TrimSpace(r.Flip) == "" {
		return fmt.Errorf("reframes: flip is required")
	}
	if r.RecordedAt == "" {
		return fmt.Errorf("reframes: recorded_at is required")
	}
	if r.LogicalDate == "" {
		return fmt.Errorf("reframes: logical_date is required")
	}
	if _, err := time.Parse(dateLayout, r.LogicalDate); err != nil {
		return fmt.Errorf("reframes: logical_date must be YYYY-MM-DD, got %q", r.LogicalDate)
	}
	if r.Source == "" {
		return fmt.Errorf("reframes: source is required")
	}
	return nil
}

// Corrects returns the id of the entry this one supersedes and whether one is
// set (reframes.md §2). A correction is a new entry whose refs.corrects names
// its target; readers fold corrections onto their targets and drop the
// superseded entry (see [FoldCorrections]).
func (r Reframe) Corrects() (string, bool) {
	if r.Refs == nil {
		return "", false
	}
	v, ok := r.Refs[refsCorrects]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// normalized returns a copy with the collection fields never nil, so an entry
// with no tags or refs marshals as [] / {} rather than null.
func (r Reframe) normalized() Reframe {
	if r.Tags == nil {
		r.Tags = []string{}
	}
	if r.Refs == nil {
		r.Refs = map[string]any{}
	}
	return r
}

// FoldCorrections returns the entries with superseded ones removed: any entry
// whose id is named by another entry's refs.corrects drops out, so `list` and
// `surface` selection see only live reframes (reframes.md §2). Output preserves
// input order, so a by-id-sorted input yields a by-id-sorted result. A chain of
// corrections folds transitively — each link names the id it supersedes, and
// every named target is dropped.
func FoldCorrections(in []Reframe) []Reframe {
	superseded := make(map[string]bool, len(in))
	for _, r := range in {
		if target, ok := r.Corrects(); ok {
			superseded[target] = true
		}
	}
	out := make([]Reframe, 0, len(in))
	for _, r := range in {
		if !superseded[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

// ReframeID renders the entry id for a logical date and sequence (reframes.md
// §2: reframe_<logical_date>_<seq>, the date in underscores, seq zero-padded to
// three digits, wider values legal). A backdated entry's id therefore encodes
// the logical date, not the recording time.
func ReframeID(logicalDate string, seq int) string {
	return fmt.Sprintf("reframe_%s_%03d", strings.ReplaceAll(logicalDate, "-", "_"), seq)
}

// ParseSeq extracts the numeric sequence from an entry id, parsed numerically so
// a wider value (reframe_..._1000) is legal (reframes.md §2). It returns
// ok=false for any id that is not a well-formed reframe id — the single-writer
// seq derivation ignores such lines rather than counting them.
func ParseSeq(id string) (seq int, ok bool) {
	if !strings.HasPrefix(id, "reframe_") {
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

// ReframeDate extracts the YYYY-MM-DD logical date a reframe id encodes — the
// read-side inverse of [ReframeID] (reframe_<logical_date>_<seq>, the date in
// underscores). It reports ok=false for anything that is not a well-formed
// reframe id: a missing reframe_ prefix, a date component that is not three
// underscore-joined fields forming a real civil date, or no sequence field
// after them. The date is returned in dashed form so it feeds the day-file
// readers directly.
func ReframeDate(id string) (date string, ok bool) {
	rest, cut := strings.CutPrefix(id, "reframe_")
	if !cut {
		return "", false
	}
	// reframe_YYYY_MM_DD_seq → the first three fields are the date, and a fourth
	// field must exist so a bare reframe_YYYY_MM_DD (no seq) is not a valid id.
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
// civil day the observation and Engine surfaces already use (reframes.md §4),
// so "today" means the same boundary everywhere in the Ledger. It reuses the
// observation layer's rollover primitives, so the reframe rotation and the
// day-view join stay aligned. The CLI passes now; storage takes the resolved
// key so its rotation logic is deterministic and injectable in tests.
func LogicalDay(now time.Time) string {
	return observations.DateString(observations.LogicalBaseDate(now, observations.DefaultRolloverMin))
}
