package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// wheel.go is the user-facing Wheel of Life path (wheel.md §7): the monthly
// balance review. `add` records one month's whole wheel — your own 1–10 rating
// for all eight pillars, optional notes, optional `suggested` calibration
// values, and the vision-review fields — as one append-only snapshot and returns
// its receipt id. Every step is deterministic and agent-free (architecture P9):
// no model is reached from any wheel path, so the structured `add` completes
// with no provider and no companion. The self-rating is authoritative — a
// missing, blank, out-of-range, or non-integer rating is refused and nothing is
// written; no rating is ever defaulted, inferred, or filled in (wheel.md §3).

// AddWheelRequest is one `lucid wheel add` turn (wheel.md §7.1). Month is the
// calendar month the wheel covers (YYYY-MM, a leading `@` tolerated; empty means
// the month of the current logical day). Pillars maps each pillar key to its
// rating, optional note, and optional suggestion — a pillar absent from the map
// is a missing rating, refused rather than filled. VisionReviewed and
// VisionReflection are the vision-review fields (wheel.md §6); a non-blank
// reflection implies VisionReviewed. Now is injected so the month default, the
// future-month ceiling, and the snapshot's `at` are deterministic in tests.
type AddWheelRequest struct {
	Month            string
	Pillars          map[string]observations.PillarScore
	VisionReviewed   bool
	VisionReflection string
	Now              time.Time
}

// WheelWriteResult reports the appended snapshot. Receipt is the snapshot's own
// receipt id (wheel_YYYY_MM_<seq>) — distinct from EntryID, the month's stable
// id (wheel_YYYY-MM). Amended is true when the month already had a wheel before
// this write (the new snapshot wins on read; Replaced names the snapshot it
// supersedes, and Snapshots counts every snapshot the month now keeps). Scores is
// the eight stored self-ratings — never a suggestion (wheel.md §5).
type WheelWriteResult struct {
	Entry     observations.WheelEntry
	Snapshot  observations.WheelSnapshot
	Receipt   string
	EntryID   string
	Month     string
	Amended   bool
	Replaced  string
	Snapshots int
	Scores    map[string]int
	Ack       []string
}

// AddWheel records one month's wheel (wheel.md §7.1): it resolves the month,
// validates the whole wheel before anything is written, appends one snapshot
// through the storage adapter, and returns the snapshot's receipt id. The eight
// self-ratings are required and authoritative — an unknown pillar, a missing or
// off-scale rating, an off-scale suggestion, a malformed month, or a month after
// the current logical month is a clean error and nothing is saved. A month that
// already has a wheel is amended by append: the new snapshot wins the fold, and
// the earlier snapshot keeps its receipt on disk (wheel.md §4).
func (r *Router) AddWheel(req AddWheelRequest) (WheelWriteResult, error) {
	now := whenOr(req.Now)
	month, err := resolveWheelMonth(req.Month, now)
	if err != nil {
		return WheelWriteResult{}, err
	}
	pillars, err := wheelPillarsOf(req.Pillars)
	if err != nil {
		return WheelWriteResult{}, err
	}

	reflection := req.VisionReflection
	if strings.TrimSpace(reflection) == "" {
		reflection = ""
	}
	snap := observations.WheelSnapshot{
		Type:             observations.WheelEventSnapshot,
		Pillars:          pillars,
		VisionReviewed:   req.VisionReviewed || reflection != "",
		VisionReflection: reflection,
	}

	if err = r.store.ScaffoldWheel(); err != nil {
		return WheelWriteResult{}, fmt.Errorf("could not prepare the wheel tree: %w", err)
	}
	entry, appended, err := r.store.AppendWheelSnapshot(month, snap, now)
	if err != nil {
		return WheelWriteResult{}, fmt.Errorf("could not record the wheel; nothing was saved: %w", err)
	}

	res := WheelWriteResult{
		Entry:     entry,
		Snapshot:  appended,
		Receipt:   appended.ID,
		EntryID:   entry.Key,
		Month:     entry.Month,
		Snapshots: entry.SnapshotCount(),
		Scores:    appended.WheelScores(),
	}
	if prior, ok := priorWheelSnapshot(entry); ok {
		res.Amended = true
		res.Replaced = prior.ID
	}
	res.Ack = wheelAddAck(res)
	return res, nil
}

// resolveWheelMonth resolves a `--month` value (wheel.md §7.1): empty means the
// month of the current logical day (04:00 rollover); otherwise it must be a
// strict YYYY-MM, a leading `@` tolerated, and not after the current logical
// month — any earlier month may be recorded, so a missed month can be filled in
// late. A malformed or future month is a clean error that writes nothing.
func resolveWheelMonth(arg string, now time.Time) (string, error) {
	current := observations.WheelMonthOf(now)
	month := strings.TrimPrefix(strings.TrimSpace(arg), "@")
	if month == "" {
		return current, nil
	}
	if !observations.ValidWheelMonth(month) {
		return "", fmt.Errorf("could not read the month %q (want YYYY-MM); nothing was saved", arg)
	}
	if month > current {
		return "", fmt.Errorf(
			"cannot record the %s wheel — that month has not happened yet (current month %s); nothing was saved",
			month, current,
		)
	}
	return month, nil
}

// wheelPillarsOf validates a request's pillars and returns the snapshot's copy
// (wheel.md §3, §5). An unknown pillar key, any of the eight missing, a rating
// off the 1–10 scale, or a suggestion off it is a clean error naming the pillar
// and the range. A blank note is dropped rather than stored; a non-blank note is
// kept verbatim. The returned map is a fresh copy, so the caller's map is never
// shared with the Ledger write.
func wheelPillarsOf(in map[string]observations.PillarScore) (map[string]observations.PillarScore, error) {
	if unknown := observations.UnknownWheelPillars(in); len(unknown) > 0 {
		return nil, fmt.Errorf(
			"wheel: unknown pillar %s (want one of %s); nothing was saved",
			quoteJoin(unknown), strings.Join(observations.WheelPillarKeys(), ", "),
		)
	}
	if missing := observations.MissingWheelPillars(in); len(missing) > 0 {
		return nil, fmt.Errorf(
			"wheel: every pillar needs your own %d–%d rating — missing: %s; nothing was saved",
			observations.WheelScoreMin, observations.WheelScoreMax, wheelLabels(missing),
		)
	}
	out := make(map[string]observations.PillarScore, len(in))
	for _, key := range observations.WheelPillarKeys() {
		p := in[key]
		label, _ := observations.WheelPillarLabel(key)
		if !observations.ValidWheelScore(p.Score) {
			return nil, fmt.Errorf(
				"wheel: %s needs a whole number from %d to %d (got %d); nothing was saved",
				label, observations.WheelScoreMin, observations.WheelScoreMax, p.Score,
			)
		}
		if p.Suggested != nil && !observations.ValidWheelScore(*p.Suggested) {
			return nil, fmt.Errorf(
				"wheel: the suggestion for %s needs a whole number from %d to %d (got %d); nothing was saved",
				label, observations.WheelScoreMin, observations.WheelScoreMax, *p.Suggested,
			)
		}
		stored := observations.PillarScore{Score: p.Score}
		if strings.TrimSpace(p.Note) != "" {
			stored.Note = p.Note
		}
		if p.Suggested != nil {
			s := *p.Suggested
			stored.Suggested = &s
		}
		out[key] = stored
	}
	return out, nil
}

// priorWheelSnapshot returns the snapshot the newest one supersedes when read —
// the second-latest snapshot in the month's history — or ok=false when the month
// holds only the snapshot just written.
func priorWheelSnapshot(entry observations.WheelEntry) (observations.WheelSnapshot, bool) {
	seen := 0
	for i := len(entry.History) - 1; i >= 0; i-- {
		if entry.History[i].Type != observations.WheelEventSnapshot {
			continue
		}
		seen++
		if seen == 2 {
			return entry.History[i], true
		}
	}
	return observations.WheelSnapshot{}, false
}

// wheelAddAck builds the ack emitted only after the write lands (wheel.md
// §7.1): the month and the receipt — plus, on an amend, that the new snapshot
// replaces the earlier one when read and that every snapshot is kept — then the
// eight stored ratings verbatim, one `label: N` line each in canonical order.
// It never prints a suggestion, and computes no score, average, or total.
func wheelAddAck(res WheelWriteResult) []string {
	head := fmt.Sprintf("Recorded the %s wheel as %s.", res.Month, res.Receipt)
	if res.Amended {
		kept := "both kept"
		if res.Snapshots > 2 {
			kept = fmt.Sprintf("all %d snapshots kept", res.Snapshots)
		}
		head = fmt.Sprintf("Recorded the %s wheel as %s (replaces %s when read; %s).",
			res.Month, res.Receipt, res.Replaced, kept)
	}
	lines := make([]string, 0, len(observations.WheelPillars())+1)
	lines = append(lines, head)
	for _, p := range observations.WheelPillars() {
		lines = append(lines, fmt.Sprintf("%s: %d", p.Label, res.Scores[p.Key]))
	}
	return lines
}

// wheelLabels renders pillar keys as their human labels, comma-joined, in the
// order given. Every key passed is one of the eight.
func wheelLabels(keys []string) string {
	labels := make([]string, len(keys))
	for i, key := range keys {
		labels[i], _ = observations.WheelPillarLabel(key)
	}
	return strings.Join(labels, ", ")
}

// quoteJoin renders values as a comma-joined list of quoted strings.
func quoteJoin(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}
