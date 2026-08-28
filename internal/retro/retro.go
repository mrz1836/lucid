// Package retro is the retro parking-lot record family (retro.md): the R-NNN
// queue — the single auditable home for anything parked to revisit at the weekly
// Retro or a Gate, stored as append-only, schema'd JSONL events under
// ~/.lucid/retro/. Like the focus and reframe cores it is entirely agent-free —
// deterministic value functions with no LLM in any path (architecture P9) and no
// filesystem access (architecture P3: the storage adapter is the only code that
// touches ~/.lucid/). This package owns the event envelope, the two id grammars
// (the parked-item R-NNN and the per-write receipt), byte-stable
// (un)marshaling, and the pure open/resolved/deferred state fold; the storage
// adapter owns the append/read discipline and the two seq derivations.
package retro

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// Schema is the event schema version (retro.md §2). It is versioned per record
// family exactly as the observation, reframe, and focus envelopes are: new needs
// go in tags, refs, a new optional field, or a new event_type under a bumped
// schema — readers tolerate an unknown field and a higher version.
const Schema = 1

// Event types (retro.md §2 "event_type"): which lifecycle event a line is. A
// park creates a parked item; a resolve or defer transitions an existing one
// (§5). The fold (§4) resolves the current status from the stream of these.
const (
	// EventPark creates a parked item (status open).
	EventPark = "park"
	// EventResolve moves an item to resolved, recording what we did.
	EventResolve = "resolve"
	// EventDefer moves an item to deferred, recording a one-line reason.
	EventDefer = "defer"
)

// Statuses (retro.md §2 "status"). An item is open until a transition references
// it; transitions are append-only, so history is preserved — a resolved item
// stays in the Ledger (the audit trail), it is only excluded from the default
// list. A deferred item is a first-class not-now that stays in the default view.
const (
	// StatusOpen is a live parked item awaiting the Sunday walk.
	StatusOpen = "open"
	// StatusResolved is an item a later resolve event moved to the audit trail.
	StatusResolved = "resolved"
	// StatusDeferred is an item a later defer event consciously set aside, kept
	// visible in the default list with its reason.
	StatusDeferred = "deferred"
)

// Sources (retro.md §2 "source"): provenance for every park, so a hand-typed
// park is always distinguishable from a one-time bulk import.
const (
	// SourceRetro is a normal `lucid retro park` (and the actor a transition
	// records) when --source names nothing else.
	SourceRetro = "retro"
	// SourceMigration is the one-time bulk import of a pre-existing queue.
	SourceMigration = "migration"
)

// dateLayout is the logical_date / parked-date string form (YYYY-MM-DD, local
// civil date), matching the observation, reframe, and focus layers' join-key
// form.
const dateLayout = "2006-01-02"

// Retro is one lifecycle event, appended verbatim as one JSONL line to its
// per-logical-day file (retro.md §2). Field order matches the documented schema
// so a marshaled event reads like the spec; encoding/json sorts map keys and
// preserves struct field order, so the same event always marshals to the same
// bytes — the property the single-writer append discipline relies on.
//
// Resolution, ResolvedDate, and DeferReason carry no omitempty: they are always
// emitted (as "" when absent) so the on-disk shape stays stable across every
// event and a transition never changes an entry's shape.
type Retro struct {
	EventID      string         `json:"event_id"`
	Schema       int            `json:"schema"`
	EventType    string         `json:"event_type"`
	ID           string         `json:"id"`
	Item         string         `json:"item"`
	Status       string         `json:"status"`
	Source       string         `json:"source"`
	Resolution   string         `json:"resolution"`
	ResolvedDate string         `json:"resolved_date"`
	DeferReason  string         `json:"defer_reason"`
	RecordedAt   string         `json:"recorded_at"`
	LogicalDate  string         `json:"logical_date"`
	Tags         []string       `json:"tags"`
	Refs         map[string]any `json:"refs"`
}

// Item is the folded parked-item view (retro.md §2 "The folded item") — what
// `list` and `show` return, grouped by id: the park supplies id, parked-date,
// source, and item text; a later resolve folds in status + resolution +
// resolved-date; a later defer folds in status + defer-reason. Every field is
// always present (empty when unset) so the machine shape stays stable. It is not
// a raw event — it never carries an event_id or event_type.
type Item struct {
	ID           string `json:"id"`
	ParkedDate   string `json:"parked_date"`
	Source       string `json:"source"`
	Item         string `json:"item"`
	Status       string `json:"status"`
	Resolution   string `json:"resolution"`
	ResolvedDate string `json:"resolved_date"`
	DeferReason  string `json:"defer_reason"`
}

// MarshalLine renders the event as exactly one compact JSON line (no trailing
// newline). The caller (the storage adapter) adds the line terminator and
// fsyncs. Collection fields are normalized so an event with no tags or refs
// marshals as [] / {} rather than null — a stable on-disk shape.
func (r Retro) MarshalLine() ([]byte, error) {
	r = r.normalized()
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("retro: marshal event: %w", err)
	}
	return b, nil
}

// UnmarshalLine parses one JSONL line into a Retro. A malformed line is a decode
// error the reader turns into a skip-and-count, never a crash (retro.md §2;
// append-only "JSONL corruption").
func UnmarshalLine(line []byte) (Retro, error) {
	var r Retro
	if err := json.Unmarshal(line, &r); err != nil {
		return Retro{}, fmt.Errorf("retro: parse event line: %w", err)
	}
	return r, nil
}

// Validate reports the first structural problem with an event before it is
// appended (retro.md §2). It guards the fields the writer cannot invent; the
// item text and every transition field are stored verbatim, so any non-empty
// item on a park is valid. A resolve/defer carries no item — it is a
// transition keyed by id — so the item requirement is waived for them.
func (r Retro) Validate() error {
	if r.Schema != Schema {
		return fmt.Errorf("retro: unsupported schema %d (want %d)", r.Schema, Schema)
	}
	switch r.EventType {
	case EventPark, EventResolve, EventDefer:
	default:
		return fmt.Errorf("retro: event_type must be %q, %q, or %q", EventPark, EventResolve, EventDefer)
	}
	if _, ok := ParseSeq(r.ID); !ok {
		return fmt.Errorf("retro: id must be a well-formed R-NNN, got %q", r.ID)
	}
	if r.EventType == EventPark && strings.TrimSpace(r.Item) == "" {
		return fmt.Errorf("retro: item is required on a park")
	}
	switch r.Status {
	case StatusOpen, StatusResolved, StatusDeferred:
	default:
		return fmt.Errorf("retro: status must be %q, %q, or %q", StatusOpen, StatusResolved, StatusDeferred)
	}
	if r.RecordedAt == "" {
		return fmt.Errorf("retro: recorded_at is required")
	}
	if r.LogicalDate == "" {
		return fmt.Errorf("retro: logical_date is required")
	}
	if _, err := time.Parse(dateLayout, r.LogicalDate); err != nil {
		return fmt.Errorf("retro: logical_date must be YYYY-MM-DD, got %q", r.LogicalDate)
	}
	if r.Source == "" {
		return fmt.Errorf("retro: source is required")
	}
	return nil
}

// normalized returns a copy with the collection fields never nil, so an event
// with no tags or refs marshals as [] / {} rather than null.
func (r Retro) normalized() Retro {
	if r.Tags == nil {
		r.Tags = []string{}
	}
	if r.Refs == nil {
		r.Refs = map[string]any{}
	}
	return r
}

// FoldState resolves each parked item's current status from the event stream and
// returns the folded item view (retro.md §4), grouped by id in ascending R-NNN
// order. A park creates the item (status open, carrying its parked-date, source,
// and item text); a later resolve folds in status resolved + resolution +
// resolved-date; a later defer folds in status deferred + defer-reason. The most
// recent transition wins, so a deferred item can later be resolved. Nothing is
// dropped — every parked item is returned with its status resolved, and the
// transition events are bookkeeping, not separate rows. A transition that names
// no parked item is ignored (it cannot conjure one).
func FoldState(in []Retro) []Item {
	items := make(map[string]*Item, len(in))
	var order []string
	for _, ev := range in {
		if ev.EventType != EventPark {
			continue
		}
		if _, exists := items[ev.ID]; !exists {
			order = append(order, ev.ID)
		}
		items[ev.ID] = &Item{
			ID:         ev.ID,
			ParkedDate: ev.LogicalDate,
			Source:     ev.Source,
			Item:       ev.Item,
			Status:     StatusOpen,
		}
	}
	// Second pass in stream (time) order: the last transition applied to an item
	// wins, so status reflects the most recent resolve/defer.
	for _, ev := range in {
		it, ok := items[ev.ID]
		if !ok {
			continue // a transition referencing an unknown park: not an item
		}
		switch ev.EventType {
		case EventResolve:
			it.Status = StatusResolved
			it.Resolution = ev.Resolution
			it.ResolvedDate = ev.LogicalDate
		case EventDefer:
			it.Status = StatusDeferred
			it.DeferReason = ev.DeferReason
		}
	}
	out := make([]Item, 0, len(order))
	for _, id := range order {
		out = append(out, *items[id])
	}
	return out
}

// ID renders the parked-item id for a sequence (retro.md §2: R-NNN, zero-padded
// to three digits, parsed numerically so wider values — R-1000 — are legal). It
// is minted global-monotonic across the whole store; only park events feed the
// max the storage adapter derives (§ nextRetroSeq).
func ID(seq int) string {
	return fmt.Sprintf("R-%03d", seq)
}

// ParseSeq extracts the numeric sequence from a parked-item id, parsed
// numerically so a wider value (R-1000) is legal (retro.md §2). It returns
// ok=false for anything that is not a well-formed R-NNN — the id minter and the
// event validator ignore/reject such values rather than counting them.
func ParseSeq(id string) (seq int, ok bool) {
	rest, cut := strings.CutPrefix(id, "R-")
	if !cut || rest == "" {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// EventID renders the per-write receipt for a logical date and sequence
// (retro.md §2: retro_event_<logical_date>_<seq>, the date in underscores, seq
// zero-padded to three digits, wider values legal). Every appended event — park,
// resolve, and defer alike — carries one, minted per logical-day, so two writes
// touching the same R-NNN return two different receipts. A backdated park's
// receipt encodes the logical date, not the recording time.
func EventID(logicalDate string, seq int) string {
	return fmt.Sprintf("retro_event_%s_%03d", strings.ReplaceAll(logicalDate, "-", "_"), seq)
}

// ParseEventSeq extracts the numeric sequence from a receipt id, parsed
// numerically so a wider value (retro_event_..._1000) is legal (retro.md §2). It
// returns ok=false for any id that is not a well-formed receipt — the
// single-writer seq derivation ignores such lines rather than counting them.
func ParseEventSeq(id string) (seq int, ok bool) {
	if !strings.HasPrefix(id, "retro_event_") {
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

// LogicalDay returns the logical-day key for an instant — the 04:00-rollover
// civil day the observation, reframe, focus, and Engine surfaces already use
// (retro.md §8), so "today" means the same boundary everywhere in the Ledger. It
// reuses the observation layer's rollover primitives, so a park's default day and
// a transition's day stay aligned with every other dated capture. The CLI passes
// now; storage/router take the resolved key so the logic is deterministic and
// injectable in tests.
func LogicalDay(now time.Time) string {
	return observations.DateString(observations.LogicalBaseDate(now, observations.DefaultRolloverMin))
}
