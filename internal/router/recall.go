package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// recall.go is the read-only recall/browse seam for the life-archive module
// (mvp/life-archive.md §7). It browses the archive by era / thread / injury,
// each surfaced item carrying its source context — the raw/observation ids
// behind it and its provenance — so no surfaced item is uncited. A bare request
// (no dimension) returns an index of every era, thread, and injury. Like the
// excavation-selection seam it reads the registries and the memory events
// **only** through the storage adapter's projection seams (ReadRegistry /
// ReadRegistryKind / ReadObservationsKind) — never the raw
// engine/observations/registries trees — and it writes nothing beyond the
// idempotent tree scaffold the read already performs (agent-contracts.md, the
// sanctuary boundary). No model runs in any path (architecture P9); a thin or
// missing store degrades to an honest empty result. The same projection-only
// reads are what the weekly reflection surface consumes, so there is no second
// data path onto the archive.

// Recall dimensions (mvp/life-archive.md §7). The empty dimension is the bare
// index over all three.
const (
	RecallEra    = "era"
	RecallThread = "thread"
	RecallInjury = "injury"
)

// Source provenance for a surfaced item: a registry-sourced referent (an era /
// thread / injury record — primary, owner-held data) vs. an excavated story (a
// memory event, source: excavation). Both are legible in the citation so a
// consumer can tell testimony from a chapter heading.
const (
	recallSourceRegistry = "registry"
)

// recallFieldOrder fixes the canonical, byte-stable render order of each
// referent's convention Fields (mvp/life-archive.md §2/§4). Precision sidecar
// keys (onset_precision/start_precision/end_precision) are deliberately omitted
// — they are derived metadata, not testimony. Thread status is the record's
// top-level status, not a Fields key, so it is not listed here.
var recallFieldOrder = map[string][]string{ //nolint:gochecknoglobals // the fixed convention vocabulary render order (life-archive.md §2/§4)
	RecallInjury: {"onset", "timeline", "body_area", "cause", "severity", "lasting_effects", "current_limitations", "treatments", "uncertainty", "note"},
	RecallEra:    {"start", "end", "note"},
	RecallThread: {"intent", "domains", "note"},
}

// RecallRequest is the read-only browse selector (mvp/life-archive.md §7):
// Dimension is "era" | "thread" | "injury" | "" (the bare index), Key names the
// referent to browse within a dimension. Now is carried for signature stability
// and a possible future as-of filter; browsing the archive is time-independent
// today (like the injury-context projection), so it is currently unread.
type RecallRequest struct {
	Dimension string
	Key       string
	Now       time.Time
}

// RecallField is one convention Field of a browsed referent, rendered with a
// human label and its verbatim value (a list value joined). The order is fixed
// by recallFieldOrder so the render is byte-stable.
type RecallField struct {
	Label string
	Value string
}

// RecallReferent is the era / thread / injury being browsed (mvp/life-archive.md
// §7): its identity, status, and the convention Fields it carries. It is
// primary, owner-held registry data, so its Source is "registry" and its
// SupportingEntryIDs are the story ids filed under it (empty when none) — the
// referent is cited by those stories or, failing that, by its own key.
type RecallReferent struct {
	Kind               string
	Key                string
	DisplayName        string
	Status             string
	Fields             []RecallField
	Source             string
	SupportingEntryIDs []string
}

// RecallItem is one surfaced archive item: a story filed under the referent, or
// an entry in the bare index. It always carries its source context (AC-11) — a
// story cites its own observation id plus any attached-photo raw id and is
// sourced "excavation"; an index entry is a registry referent cited by its key
// and sourced "registry". No item is ever uncited.
type RecallItem struct {
	Kind               string
	Key                string
	Title              string
	Detail             string
	Source             string
	SupportingEntryIDs []string
}

// RecallResult is the read-only browse result (mvp/life-archive.md §7). For a
// keyed dimension it carries the Referent and the Items (stories) filed under
// it; for the bare index it carries only the Items (one per era/thread/injury,
// Referent nil). Found is false for a keyed browse whose referent does not
// resolve, or an empty index — an honest empty result, no model spent over it.
type RecallResult struct {
	Dimension string
	Key       string
	Found     bool
	Referent  *RecallReferent
	Items     []RecallItem
}

// Recall browses the archive by dimension (mvp/life-archive.md §7). With no
// dimension it returns the index of every era, thread, and injury; with a
// dimension + key it resolves that referent and the memory stories filed under
// it (refs.<dimension> == key), each item carrying its source-context ids and
// provenance. It reads only through the storage projection seams and writes
// nothing. An unknown dimension is a usage error; a missing referent is an
// honest empty result (Found: false), never a fabricated one.
func (r *Router) Recall(req RecallRequest) (RecallResult, error) {
	if err := r.prepareObservations(); err != nil {
		return RecallResult{}, err
	}
	dim := strings.TrimSpace(req.Dimension)
	if dim == "" {
		return r.recallIndex()
	}
	kind, ok := recallKind(dim)
	if !ok {
		return RecallResult{}, fmt.Errorf("recall: unknown dimension %q (want era, thread, or injury)", dim)
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return RecallResult{}, fmt.Errorf("recall: %s browse needs a key; nothing to browse", dim)
	}

	rec, found, err := r.store.ReadRegistry(kind, key)
	if err != nil {
		return RecallResult{}, fmt.Errorf("recall: read %s %q: %w", dim, key, err)
	}
	if !found {
		return RecallResult{Dimension: dim, Key: key, Found: false}, nil
	}

	stories, err := r.recallStories(dim, key)
	if err != nil {
		return RecallResult{}, err
	}
	ref := buildReferent(dim, rec, storyIDs(stories))
	return RecallResult{Dimension: dim, Key: key, Found: true, Referent: &ref, Items: stories}, nil
}

// recallIndex reads every era, thread, and injury registry through the
// projection seam and returns one index item per record, grouped by dimension
// in era→thread→injury order (each kind already key-sorted by
// ReadRegistryKind), so the render is deterministic. An empty store yields an
// empty, honest index.
func (r *Router) recallIndex() (RecallResult, error) {
	var items []RecallItem
	for _, dim := range []string{RecallEra, RecallThread, RecallInjury} {
		kind, _ := recallKind(dim)
		recs, err := r.store.ReadRegistryKind(kind)
		if err != nil {
			return RecallResult{}, fmt.Errorf("recall: read %s index: %w", dim, err)
		}
		for _, rec := range recs {
			items = append(items, indexItem(dim, rec))
		}
	}
	return RecallResult{Found: len(items) > 0, Items: items}, nil
}

// recallStories returns the memory stories filed under a referent — the memory
// events whose refs.<dimension> resolves to the key. Only an era link is
// written by v1's story-capture path (resolveMemoryRefs); thread and injury
// links are read the same way so the browse is forward-compatible with a future
// body-state linker, degrading to an honest empty list until then. Order
// follows ReadObservationsKind's id sort, so it is byte-stable.
func (r *Router) recallStories(dim, key string) ([]RecallItem, error) {
	memories, err := r.store.ReadObservationsKind(observations.KindMemory)
	if err != nil {
		return nil, fmt.Errorf("recall: read memories: %w", err)
	}
	var out []RecallItem
	for _, ev := range memories {
		if refString(ev.Refs, dim) == key {
			out = append(out, storyItem(ev))
		}
	}
	return out, nil
}

// storyItem projects one memory event into a cited archive item: its title is
// the story's own words, its detail the certainty/tone, and its source context
// is its observation id plus any attached-photo raw id (refs.entry), sourced by
// the event's provenance (excavation). A partial (text-less) memory still
// surfaces, titled honestly.
func storyItem(ev observations.Event) RecallItem {
	ids := []string{ev.ID}
	if entry := refString(ev.Refs, "entry"); entry != "" {
		ids = append(ids, entry)
	}
	return RecallItem{
		Kind:               "story",
		Key:                ev.ID,
		Title:              storyTitle(ev.Payload),
		Detail:             storyDetail(ev.Payload),
		Source:             ev.Source,
		SupportingEntryIDs: ids,
	}
}

// indexItem projects one registry record into a bare-index entry: its display
// name and a short dimension-appropriate detail (an era's date range, a
// thread's or injury's status), sourced "registry". It carries no supporting
// ids — an index entry is cited by its own key + source in the render.
func indexItem(dim string, rec observations.Registry) RecallItem {
	return RecallItem{
		Kind:   dim,
		Key:    rec.Key,
		Title:  rec.DisplayName,
		Detail: indexDetail(dim, rec),
		Source: recallSourceRegistry,
	}
}

// buildReferent assembles the browsed referent: its identity, status, the
// convention Fields in canonical order, and the story ids filed under it as its
// supporting citation (empty for an injury, which is itself primary source).
func buildReferent(dim string, rec observations.Registry, supporting []string) RecallReferent {
	return RecallReferent{
		Kind:               dim,
		Key:                rec.Key,
		DisplayName:        rec.DisplayName,
		Status:             rec.Status,
		Fields:             referentFields(dim, rec.Fields),
		Source:             recallSourceRegistry,
		SupportingEntryIDs: supporting,
	}
}

// referentFields renders a referent's present convention Fields in canonical
// order (recallFieldOrder), skipping any that are absent or empty — so a thin
// record surfaces honestly with only what it holds.
func referentFields(dim string, fields map[string]any) []RecallField {
	var out []RecallField
	for _, key := range recallFieldOrder[dim] {
		if v := fieldValue(fields[key]); v != "" {
			out = append(out, RecallField{Label: humanizeLabel(key), Value: v})
		}
	}
	return out
}

// indexDetail renders the short detail line for a bare-index entry: an era's
// date range, or the status for a thread/injury.
func indexDetail(dim string, rec observations.Registry) string {
	if dim == RecallEra {
		return eraRange(rec.Fields)
	}
	return rec.Status
}

// eraRange renders an era's start/end as "start – end", either side omitted
// when unset (an open end is a still-running chapter). An era with no dates
// yields "".
func eraRange(fields map[string]any) string {
	start := fieldValue(fields["start"])
	end := fieldValue(fields["end"])
	switch {
	case start != "" && end != "":
		return start + " – " + end
	case start != "":
		return start + " –"
	case end != "":
		return "– " + end
	default:
		return ""
	}
}

// storyTitle returns a memory's own words as its title, or an honest fallback
// for a partial (text-less) memory.
func storyTitle(payload map[string]any) string {
	if s := payloadStr(payload, observations.MemoryFieldText); s != "" {
		return s
	}
	return "(untitled memory)"
}

// storyDetail renders a memory's certainty and tone as its secondary line,
// omitting either when unset.
func storyDetail(payload map[string]any) string {
	var parts []string
	if c := payloadStr(payload, observations.MemoryFieldCertainty); c != "" {
		parts = append(parts, "certainty: "+c)
	}
	if tone := payloadStr(payload, observations.MemoryFieldTone); tone != "" {
		parts = append(parts, "tone: "+tone)
	}
	return strings.Join(parts, "; ")
}

// storyIDs collects the surfaced stories' observation ids as the referent's
// supporting citation. It returns nil for an empty set so the referent falls
// back to citing its own key.
func storyIDs(items []RecallItem) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Key)
	}
	return out
}

// recallKind maps a browse dimension to its registry kind.
func recallKind(dim string) (string, bool) {
	switch dim {
	case RecallEra:
		return observations.RegistryEra, true
	case RecallThread:
		return observations.RegistryThread, true
	case RecallInjury:
		return observations.RegistryInjury, true
	default:
		return "", false
	}
}

// refString reads a string-valued ref (refs.era / refs.entry / …), trimmed, or
// "" when the key is absent or (after a hand-edit) not a string.
func refString(refs map[string]any, key string) string {
	if refs == nil {
		return ""
	}
	if s, ok := refs[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// payloadStr reads a string-valued payload key, trimmed, or "" when absent or
// not a string.
func payloadStr(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if s, ok := payload[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// fieldValue renders a registry Fields value as a display string: a string
// verbatim, a list (as read back from JSON it is []any, or []string in-memory)
// joined with ", ", anything else "". Blank list entries are dropped.
func fieldValue(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		var parts []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, ", ")
	case []string:
		var parts []string
		for _, s := range t {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		return ""
	}
}

// humanizeLabel turns a convention key into a display label: underscores become
// spaces and the first letter is capitalized ("body_area" → "Body area",
// "why_it_matters" → "Why it matters").
func humanizeLabel(key string) string {
	s := strings.ReplaceAll(key, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
