package engine

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// AnchorVersion is the anchors.json schema version this binary stamps on
// every append, so the number in a file is always true of that file. It is a
// signal, not a gate: reads are never version-checked, so a version 1 file
// (written before anchors carried an id or a state) still reads exactly as it
// always did. The honest consequence of that choice runs the other way — an
// older binary reading a version 2 file ignores the state field, and would
// keep counting an anchor this binary has sunset.
const AnchorVersion = 2

// AnchorStateSunset marks a record that retires its anchor: the milestone
// stops counting on the metrics and witness surfaces but stays in the
// append-only history. State absent means active, so every record written
// before this state existed remains valid untouched.
const AnchorStateSunset = "sunset"

// Anchor is one recorded "days since" milestone (engine-module.md
// §anchors.json): a labeled civil date the days-since metric counts from —
// a cessation or a gate the practice measures elapsed days against. The
// store is append-only, so a correction (a mistyped date) and a reset (the
// count restarts) are the same operation: a new dated append. RecordedAt is
// the append time and is what resolves latest-wins per identity.
//
// ID is the stable identity; Label is a mutable display name, so a label can
// be corrected or renamed without forking the milestone. ID is omitempty
// deliberately and permanently: a record written before ids existed carries
// none, and a write that targets such a record stays id-less on purpose so it
// folds under the same synthetic identity (see AnchorIdentity). Making ID
// required would fork every pre-id anchor on its next correction.
type Anchor struct {
	ID         string `json:"id,omitempty"`
	Label      string `json:"label"`
	Date       string `json:"date"`
	Note       string `json:"note,omitempty"`
	RecordedAt string `json:"recorded_at"`
	State      string `json:"state,omitempty"`
}

// IsSunset reports whether this record retires its anchor.
func (a Anchor) IsSunset() bool { return a.State == AnchorStateSunset }

// LegacyAnchorIDPrefix namespaces the synthetic identity of a record written
// before anchors carried an id. It is derived at read time and never written:
// the string never appears in anchors.json.
const LegacyAnchorIDPrefix = "legacy:"

// AnchorIdentity returns the identity a record folds under: its stable id
// when it has one, otherwise a synthetic identity derived from its label.
//
// The synthetic form is what lets a pre-id record keep behaving exactly as it
// always has — same label means same anchor — without rewriting a byte of
// history. It is derived here, at read time, and is never written back to the
// store.
func AnchorIdentity(a Anchor) string {
	if a.ID != "" {
		return a.ID
	}
	return LegacyAnchorIDPrefix + a.Label
}

// AnchorLog is anchors.json: the schema version plus the append-only history
// of every recorded anchor. The full history is kept (audit-faithful);
// LatestAnchors folds it to the effective set the metric reads.
type AnchorLog struct {
	Version int      `json:"version"`
	History []Anchor `json:"history"`
}

// AnchorIDPrefix is the minted-id namespace: anchor_YYYY_MM_DD_<slot>.
const AnchorIDPrefix = "anchor_"

// SlotLabel renders the nth per-day slot as a bijective base-26 lowercase
// label: 0→a, 25→z, 26→aa, 27→ab, ... It never returns the empty string, so
// every record minted in a day gets a unique, monotonic id. This is the
// shared per-day slot grammar: insight ids and anchor ids are the same form.
func SlotLabel(n int) string {
	var b []byte
	for {
		b = append([]byte{byte('a' + n%26)}, b...)
		n = n/26 - 1
		if n < 0 {
			break
		}
	}
	return string(b)
}

// NextAnchorID mints the next free anchor id for the given day:
// anchor_YYYY_MM_DD_<slot>, advancing the slot past every id already present
// for that date.
//
// It scans the whole history rather than the folded latest set on purpose: a
// slot belonging to an anchor that has since been sunset must never be
// reissued, or a retired milestone and a live one would share an identity and
// the two projected surfaces would collide.
//
// The id is never derived from the label. That is deliberate — the label is a
// mutable display name now, and seeding an identity from a mutable value is
// exactly the flaw this identity model exists to avoid. Do not "improve" this
// into a readable slug.
func NextAnchorID(log AnchorLog, day time.Time) string {
	prefix := fmt.Sprintf("%s%04d_%02d_%02d_", AnchorIDPrefix, day.Year(), int(day.Month()), day.Day())
	used := map[string]bool{}
	for _, a := range log.History {
		if slot, ok := strings.CutPrefix(a.ID, prefix); ok {
			used[slot] = true
		}
	}
	for n := 0; ; n++ {
		slot := SlotLabel(n)
		if !used[slot] {
			return prefix + slot
		}
	}
}

// AnchorRecord is the projected single-anchor JSON surface. It exists so a
// caller never marshals the stored record directly: an anchor written before
// ids existed carries none, and the surface must still publish a usable
// address — its synthetic legacy identity. Marshaling the stored record would
// silently emit an empty id for exactly the anchors every real ledger is full
// of, so this indirection is load-bearing, not ceremony.
//
// Field order mirrors Anchor so the stored and projected forms read the same.
// State is carried so a retirement is visible to a caller that asked for JSON;
// it is empty and omitted for an active record.
type AnchorRecord struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Date       string `json:"date"`
	Note       string `json:"note,omitempty"`
	RecordedAt string `json:"recorded_at"`
	State      string `json:"state,omitempty"`
}

// ProjectAnchor renders a stored anchor as its published JSON form, resolving
// the id a pre-id record does not carry (AnchorIdentity). The synthetic value
// is produced here, at the projection boundary, and is never written back.
func ProjectAnchor(a Anchor) AnchorRecord {
	return AnchorRecord{
		ID:         AnchorIdentity(a),
		Label:      a.Label,
		Date:       a.Date,
		Note:       a.Note,
		RecordedAt: a.RecordedAt,
		State:      a.State,
	}
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
// identity: the most-recently-appended record wins (append order, i.e.
// RecordedAt), so a correction whose date is *earlier* than the original
// still supersedes it, and a reset to a later date supersedes too. A record
// written before ids existed folds under an identity derived from its label
// (AnchorIdentity), so its behavior is unchanged — same label, same anchor.
//
// The result is sorted by label, then identity, so two records sharing a
// display name have a total, stable order rather than one that depends on map
// iteration. An empty log yields an empty slice.
func LatestAnchors(log AnchorLog) []Anchor {
	latest := make(map[string]Anchor, len(log.History))
	for _, a := range log.History {
		latest[AnchorIdentity(a)] = a // last write per identity wins (append order)
	}
	out := make([]Anchor, 0, len(latest))
	for _, a := range latest {
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b Anchor) int {
		if c := cmp.Compare(a.Label, b.Label); c != 0 {
			return c
		}
		return cmp.Compare(AnchorIdentity(a), AnchorIdentity(b))
	})
	return out
}

// FindActiveAnchor returns the single active anchor holding label, if any.
// latest is a folded set (LatestAnchors), and at most one active anchor can
// hold a label: recording an existing active label targets that anchor rather
// than minting a second one, so a label is unambiguous without an id.
func FindActiveAnchor(latest []Anchor, label string) (Anchor, bool) {
	for _, a := range latest {
		if !a.IsSunset() && a.Label == label {
			return a, true
		}
	}
	return Anchor{}, false
}

// LatestSunsetFor returns the most-recently-appended sunset record holding
// label, if any — the retired milestone a re-add reuses the name of, and the
// record whose retirement date an already-sunset rejection cites.
func LatestSunsetFor(latest []Anchor, label string) (Anchor, bool) {
	var (
		found Anchor
		ok    bool
	)
	for _, a := range latest {
		if a.IsSunset() && a.Label == label {
			if !ok || a.RecordedAt > found.RecordedAt {
				found, ok = a, true
			}
		}
	}
	return found, ok
}

// AnchorLabels lists the labels active anchors hold, sorted and deduped. It
// is what an unrecorded-label rejection names, so the remedy travels with the
// error; only active labels are listed, because those are the only ones a
// caller can actually sunset or rename. The result is empty (never nil) when
// nothing is recorded, so a caller can say so rather than printing a dangling
// list with nothing after it.
func AnchorLabels(latest []Anchor) []string {
	seen := make(map[string]bool, len(latest))
	out := make([]string, 0, len(latest))
	for _, a := range latest {
		if a.IsSunset() || seen[a.Label] {
			continue
		}
		seen[a.Label] = true
		out = append(out, a.Label)
	}
	slices.Sort(out)
	return out
}

// SunsetDate renders a sunset record's civil retirement date: the date half
// of its RFC3339 RecordedAt. A value too short to carry one is returned
// unchanged — a hand-corrupted timestamp degrades to printing what is there
// rather than failing a read path.
func SunsetDate(a Anchor) string {
	const civilDateLen = 10
	if len(a.RecordedAt) < civilDateLen {
		return a.RecordedAt
	}
	return a.RecordedAt[:civilDateLen]
}
