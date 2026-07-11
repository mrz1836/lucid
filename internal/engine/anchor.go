package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// AnchorVersion is the only anchors.json schema version the MVP understands.
const AnchorVersion = 1

// Anchor is one recorded "days since" milestone (engine-module.md
// §anchors.json): a labeled civil date the days-since metric counts from —
// a cessation or a gate the practice measures elapsed days against. The
// store is append-only, so a correction (a mistyped date) and a reset (the
// count restarts) are the same operation: a new dated append. RecordedAt is
// the append time and is what resolves latest-wins per label.
type Anchor struct {
	Label      string `json:"label"`
	Date       string `json:"date"`
	Note       string `json:"note,omitempty"`
	RecordedAt string `json:"recorded_at"`
}

// AnchorLog is anchors.json: the schema version plus the append-only history
// of every recorded anchor. The full history is kept (audit-faithful);
// LatestAnchors folds it to the effective set the metric reads.
type AnchorLog struct {
	Version int      `json:"version"`
	History []Anchor `json:"history"`
}

// ValidateAnchor reports whether label/date are a legal anchor to record: a
// non-empty label and a date that parses as a civil YYYY-MM-DD. Any past or
// future civil date is accepted — anchors are backdatable — so only the
// shape is checked, never the value's relation to today. It is pure (no
// clock, no model), so the write path can reject before touching disk.
func ValidateAnchor(label, date string) error {
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("engine: anchor label must not be empty")
	}
	if _, err := time.Parse(dateLayout, date); err != nil {
		return fmt.Errorf("engine: anchor date %q is not a valid YYYY-MM-DD date", date)
	}
	return nil
}

// LatestAnchors folds the append-only history to the effective anchor per
// label: the most-recently-appended record wins (append order, i.e.
// RecordedAt), so a correction whose date is *earlier* than the original
// still supersedes it, and a reset to a later date supersedes too. The
// result is returned sorted by label for a stable projection. An empty log
// yields an empty slice.
func LatestAnchors(log AnchorLog) []Anchor {
	latest := make(map[string]Anchor, len(log.History))
	for _, a := range log.History {
		latest[a.Label] = a // last write per label wins (append order)
	}
	out := make([]Anchor, 0, len(latest))
	for _, a := range latest {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
