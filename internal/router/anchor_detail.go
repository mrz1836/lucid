package router

import (
	"cmp"
	"slices"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// anchorDetailWeekDays is the witness report's week for the anchor detail: the
// last seven logical days, today included — the same week the projection's
// counts without notes (slips_7d, miss_run_7d) read.
const anchorDetailWeekDays = 7

// AnchorDetailEvent is one anchor event in the witness report's week, joined
// with its anchor's label and current kind: one line of the opt-in Anchor
// detail section (usage/witness-report.md §"Anchors in the report"). It carries
// the note verbatim, which is why it is read only when the operator opted in to
// full detail; summary mode never builds one. Day is the event's civil day (a
// confirm's through-day), From a confirm's first day, Intent a use's planned or
// slip, and Kind/Allowance/WindowDays a kind event's parameters.
type AnchorDetailEvent struct {
	ID         string `json:"id"`
	AnchorID   string `json:"anchor_id"`
	Label      string `json:"label"`
	AnchorKind string `json:"anchor_kind"`
	Type       string `json:"type"`
	Day        string `json:"day"`
	From       string `json:"from,omitempty"`
	Intent     string `json:"intent,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Allowance  int    `json:"allowance,omitempty"`
	WindowDays int    `json:"window_days,omitempty"`
	Note       string `json:"note,omitempty"`
}

// AnchorDetail reads the anchor events dated in the witness report's week —
// the seven logical days ending at the current one — for the opt-in Anchor
// detail section. It returns every anchor's applied slips, uses, misses,
// confirms and kind changes, plus a graduation once its sunset half exists,
// ordered by label, then day, then receipt. A voided event and its void are
// left out, as is anything the fold does not apply (a dangling add --kind
// event). Every anchor with an event that week appears, a retired one
// included. It is a read: it writes nothing beyond the idempotent engine-tree
// scaffold, and no model is reachable from this path.
func (r *Router) AnchorDetail(now time.Time) ([]AnchorDetailEvent, error) {
	now = whenOr(now)
	if err := r.prepareEngine(); err != nil {
		return nil, err
	}
	view, err := r.readAnchorView()
	if err != nil {
		return nil, err
	}
	today, err := r.anchorToday(now)
	if err != nil {
		return nil, err
	}
	return anchorDetailEvents(view.facts, today), nil
}

// anchorDetailEvents selects the week's applied events from the folded facts
// and joins each with its anchor's label and kind. The week is compared as
// YYYY-MM-DD strings, which order as their dates do.
func anchorDetailEvents(facts map[string]engine.AnchorFacts, today time.Time) []AnchorDetailEvent {
	first := engine.DateString(today.AddDate(0, 0, -(anchorDetailWeekDays - 1)))
	last := engine.DateString(today)

	var out []AnchorDetailEvent
	for _, f := range facts {
		lists := [][]engine.AnchorEvent{f.KindHistory, f.Slips, f.Uses, f.Misses, f.Confirms}
		if f.Graduated {
			// A graduate event counts only once its plain sunset landed; an
			// unfinished graduation is not one yet.
			lists = append(lists, f.Graduates)
		}
		for _, list := range lists {
			for _, e := range list {
				if e.Day < first || e.Day > last {
					continue
				}
				out = append(out, AnchorDetailEvent{
					ID:         e.ID,
					AnchorID:   f.AnchorID,
					Label:      f.Anchor.Label,
					AnchorKind: f.Kind,
					Type:       e.Type,
					Day:        e.Day,
					From:       e.From,
					Intent:     e.Intent,
					Kind:       e.Kind,
					Allowance:  e.Allowance,
					WindowDays: e.WindowDays,
					Note:       e.Note,
				})
			}
		}
	}
	slices.SortFunc(out, func(a, b AnchorDetailEvent) int {
		return cmp.Or(cmp.Compare(a.Label, b.Label), cmp.Compare(a.Day, b.Day), cmp.Compare(a.ID, b.ID))
	})
	return out
}
