package engine

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// PlanAnchorAdoptions returns the adoption records the log needs so every
// anchor carries a written id, in append order. It is pure and idempotent: a
// log whose legacy identities have all been adopted yields nothing.
//
// An adoption record is a new append, never a rewrite. It carries a freshly
// minted anchor_YYYY_MM_DD_<slot> id, the legacy:<label> identity it takes
// over (Adopts), the real backfill time (AdoptedAt), and the adopted record's
// Label/Date/Note/State/RecordedAt copied verbatim. RecordedAt especially is
// copied rather than restamped, so LatestSunsetFor and SunsetDate keep
// reporting the true retirement date rather than the day the backfill ran.
//
// Once these records are appended, LatestAnchors' remap pre-pass folds each
// pre-id record onto its minted id, so the folded set is identical in every
// projected field (labels, dates, notes, states, recorded_at) — only the
// published id changes, from legacy:<label> to the minted id.
func PlanAnchorAdoptions(log AnchorLog, now time.Time) []Anchor {
	// The folded set already applies any prior adoptions' remap, so a winner
	// whose identity still carries the legacy prefix is exactly a pre-id anchor
	// that has not been adopted yet. An already-adopted identity folds under
	// its minted id and is skipped — which is what makes this idempotent.
	var pending []Anchor
	for _, w := range LatestAnchors(log) {
		if strings.HasPrefix(AnchorIdentity(w), LegacyAnchorIDPrefix) {
			pending = append(pending, w)
		}
	}

	// Sort deterministically by the legacy identity so two runs plan the same
	// records in the same order regardless of map iteration.
	slices.SortFunc(pending, func(a, b Anchor) int {
		return cmp.Compare(AnchorIdentity(a), AnchorIdentity(b))
	})

	// Mint against a working log that grows as records are planned: appending
	// each planned record before minting the next keeps NextAnchorID from
	// reissuing a slot the backfill has already consumed on this same day.
	working := AnchorLog{Version: log.Version, History: slices.Clone(log.History)}
	adoptedAt := now.Format(time.RFC3339)

	var planned []Anchor
	for _, w := range pending {
		rec := Anchor{
			ID:         NextAnchorID(working, now),
			Label:      w.Label,
			Date:       w.Date,
			Note:       w.Note,
			RecordedAt: w.RecordedAt, // verbatim: SunsetDate/LatestSunsetFor read this
			State:      w.State,
			Adopts:     AnchorIdentity(w), // the legacy:<label> this record takes over
			AdoptedAt:  adoptedAt,
		}
		planned = append(planned, rec)
		working.History = append(working.History, rec)
	}
	return planned
}
