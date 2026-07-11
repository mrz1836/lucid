package router

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// ErrAnchorRejected marks a deterministic validation rejection of `anchor add`
// — an empty label or an unparseable YYYY-MM-DD date. It wraps the fixed,
// model-free reason so the CLI can print the copy on stderr and map it to a
// non-zero exit; a script never reads a rejected record as a success. The
// rejection happens before any disk write, so a bad input never mutates the
// Ledger.
var ErrAnchorRejected = errors.New("anchor rejected")

// AnchorAddRequest is one `lucid anchor add` intent: record a labeled civil-date
// milestone the days-since metric counts from (engine-module.md §anchors.json).
// Note is optional. Now is the record time — the wall clock when zero — and
// stamps RecordedAt, which resolves latest-wins per label at read.
type AnchorAddRequest struct {
	Label string
	Date  string
	Note  string
	Now   time.Time
}

// AnchorAddResult reports the recorded anchor and the inventory-only ack.
// Recording a milestone is never scored, so the ack carries no evaluative
// language (engine §0) — just the label and the date that were written.
type AnchorAddResult struct {
	Anchor engine.Anchor
	Ack    string
}

// AnchorAdd executes `lucid anchor add <label> <date> [note]` (engine-module.md
// §Commands): validate the label/date deterministically, then append one
// milestone to the append-only anchors store. The store is never rewritten in
// place, so a correction (a mistyped date) and a reset (the count restarts) are
// the same operation — a new dated append; [engine.LatestAnchors] folds the
// history to latest-per-label at read. No model is reachable from this write
// path (Sanctuary): validation is pure and the record is verbatim.
func (r *Router) AnchorAdd(req AnchorAddRequest) (AnchorAddResult, error) {
	now := whenOr(req.Now)

	// Validate before touching disk so a bad input is rejected without a
	// partial write. The reason is fixed copy (no clock, no model).
	if err := engine.ValidateAnchor(req.Label, req.Date); err != nil {
		return AnchorAddResult{}, anchorRejection(req.Label, req.Date)
	}

	if err := r.store.ScaffoldEngine(); err != nil {
		return AnchorAddResult{}, fmt.Errorf("could not prepare the engine tree: %w", err)
	}

	anchor := engine.Anchor{
		Label:      req.Label,
		Date:       req.Date,
		Note:       req.Note,
		RecordedAt: now.Format(time.RFC3339),
	}
	if err := r.store.AppendAnchor(anchor); err != nil {
		return AnchorAddResult{}, fmt.Errorf("could not record the anchor; nothing was saved: %w", err)
	}

	return AnchorAddResult{Anchor: anchor, Ack: anchorAck(anchor)}, nil
}

// anchorRejection composes the fixed rejection copy, distinguishing the two
// failure reasons [engine.ValidateAnchor] gates on so the message is actionable.
// It always wraps [ErrAnchorRejected] so the CLI can classify it.
func anchorRejection(label, date string) error {
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("%w: give a non-empty label, e.g. `anchor add sobriety 2026-01-01`", ErrAnchorRejected)
	}
	return fmt.Errorf("%w: date %q must be a YYYY-MM-DD date, e.g. 2026-01-01", ErrAnchorRejected, date)
}

// anchorAck builds the inventory ack for a recorded anchor: the label and the
// date that were written, nothing evaluative.
func anchorAck(a engine.Anchor) string {
	return fmt.Sprintf("Anchor recorded: %s — %s.", a.Label, a.Date)
}
