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
// its receipt id; `show` folds the latest snapshot per month into the
// month-over-month trend, and `list` names the recorded months. Every step is deterministic and agent-free (architecture P9):
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

// wheelTrendMonths is how many stored months the sparkline spans (wheel.md
// §7.2): the shown month and up to five before it.
const wheelTrendMonths = 6

// wheelNoHistoryLine is what `show` and `list` print when no wheel is recorded
// at all (wheel.md §7.2, §7.3) — never an invented row, rating, or delta.
const wheelNoHistoryLine = "no prior month — no wheel recorded yet"

// wheelNoPrior is the header and `biggest drop:` wording for a month with no
// earlier stored month to compare against (wheel.md §7.2).
const wheelNoPrior = "no prior month"

// wheelSparkBlocks is the fixed eight-block sparkline scale (wheel.md §7.2).
// It is never auto-scaled to a pillar's own range, so a block means the same
// rating band on every pillar and every month.
const wheelSparkBlocks = "▁▂▃▄▅▆▇█"

// ShowWheelRequest is one `lucid wheel show` turn (wheel.md §7.2). Month picks
// the month to show (YYYY-MM, a leading `@` tolerated); empty means the most
// recent stored month.
type ShowWheelRequest struct {
	Month string
}

// WheelShowResult is a rendered `wheel show`: View is the --json payload and
// Lines the human output (bullets and `key: value` lines, never a table).
type WheelShowResult struct {
	View  WheelShowView
	Lines []string
}

// WheelShowView is the `wheel show --json` payload (wheel.md §7.2). Every array
// is present and never null. Month is "" when no wheel is recorded; PriorMonth
// is "" when the shown month is the first one recorded. Calibration is the only
// place a stored `suggested` value appears — nothing else here is derived from
// one (wheel.md §5).
type WheelShowView struct {
	Month            string             `json:"month"`
	PriorMonth       string             `json:"prior_month"`
	ReceiptID        string             `json:"receipt_id"`
	Months           []string           `json:"months"`
	Pillars          []WheelPillarTrend `json:"pillars"`
	Lowest           []WheelPillarScore `json:"lowest"`
	BiggestDrop      []WheelPillarDelta `json:"biggest_drop"`
	VisionReviewed   bool               `json:"vision_reviewed"`
	VisionReflection string             `json:"vision_reflection"`
	Calibration      WheelCalibration   `json:"calibration"`
}

// WheelPillarTrend is one pillar in `wheel show` (wheel.md §7.2): your stored
// rating verbatim, its signed delta against the prior stored month (nil when
// there is none — never a fabricated 0), and its ratings over the trend window,
// where Trend[i] is the rating in the view's Months[i].
type WheelPillarTrend struct {
	Pillar    string `json:"pillar"`
	Label     string `json:"label"`
	Score     int    `json:"score"`
	Delta     *int   `json:"delta"`
	Trend     []int  `json:"trend"`
	Sparkline string `json:"sparkline"`
	Note      string `json:"note"`
}

// WheelPillarScore names a pillar and its rating — one `lowest` callout.
type WheelPillarScore struct {
	Pillar string `json:"pillar"`
	Score  int    `json:"score"`
}

// WheelPillarDelta names a pillar and its delta — one `biggest_drop` callout.
type WheelPillarDelta struct {
	Pillar string `json:"pillar"`
	Delta  int    `json:"delta"`
}

// WheelCalibration carries the shown month's stored `suggested` values, keyed
// by pillar (wheel.md §5) — {} when none. It feeds no rating, delta,
// sparkline, or callout.
type WheelCalibration struct {
	Suggested map[string]int `json:"suggested"`
}

// WheelListResult is a rendered `wheel list`: View is the --json payload and
// Lines the human output.
type WheelListResult struct {
	View  WheelListView
	Lines []string
}

// WheelListView is the `wheel list --json` payload (wheel.md §7.3): every
// recorded month, most recent first; Months is [] (never null) when empty.
type WheelListView struct {
	Months []WheelListMonth `json:"months"`
}

// WheelListMonth is one recorded month in `wheel list` (wheel.md §7.3): the
// month's stable id, its latest snapshot's receipt and write time, how many
// snapshots it keeps, and whether that snapshot records a vision re-read.
type WheelListMonth struct {
	Month          string `json:"month"`
	EntryID        string `json:"entry_id"`
	ReceiptID      string `json:"receipt_id"`
	Snapshots      int    `json:"snapshots"`
	RecordedAt     string `json:"recorded_at"`
	VisionReviewed bool   `json:"vision_reviewed"`
}

// wheelMonth is one recorded month folded to its current wheel: the entry and
// its latest snapshot (amend by append, latest wins).
type wheelMonth struct {
	entry observations.WheelEntry
	snap  observations.WheelSnapshot
}

// ShowWheel renders one month's wheel against the most recent prior stored
// month (wheel.md §7.2). It is a pure read — it writes nothing and reaches no
// model. Every number it shows is a stored self-rating printed as stored: the
// delta, sparkline, and callouts are computed from `score` alone, and a stored
// `suggested` value appears only in the view's calibration block, never in the
// human lines. With no wheel recorded it returns the empty view and the
// no-history line; a malformed month, or one with no wheel, is a clean error.
func (r *Router) ShowWheel(req ShowWheelRequest) (WheelShowResult, error) {
	months, err := r.readWheelMonths()
	if err != nil {
		return WheelShowResult{}, err
	}
	idx, err := pickWheelMonth(months, req.Month)
	if err != nil {
		return WheelShowResult{}, err
	}
	if idx < 0 {
		return WheelShowResult{View: emptyWheelShowView(), Lines: []string{wheelNoHistoryLine}}, nil
	}
	window := months[max(0, idx-(wheelTrendMonths-1)) : idx+1]
	for _, m := range window {
		if err = checkWheelRatings(m); err != nil {
			return WheelShowResult{}, err
		}
	}
	view := wheelShowViewOf(window)
	return WheelShowResult{View: view, Lines: wheelShowLines(view)}, nil
}

// ListWheel lists every recorded month, most recent first (wheel.md §7.3). It
// is a pure read — it writes nothing and reaches no model. With no wheel
// recorded, Months is empty and the only line is the no-history line.
func (r *Router) ListWheel() (WheelListResult, error) {
	months, err := r.readWheelMonths()
	if err != nil {
		return WheelListResult{}, err
	}
	rows := make([]WheelListMonth, 0, len(months))
	for i := len(months) - 1; i >= 0; i-- {
		m := months[i]
		rows = append(rows, WheelListMonth{
			Month:          m.entry.Month,
			EntryID:        m.entry.Key,
			ReceiptID:      m.snap.ID,
			Snapshots:      m.entry.SnapshotCount(),
			RecordedAt:     m.snap.At,
			VisionReviewed: m.snap.VisionReviewed,
		})
	}
	return WheelListResult{View: WheelListView{Months: rows}, Lines: wheelListLines(rows)}, nil
}

// readWheelMonths reads every stored month, oldest first, folded to its latest
// snapshot. A month file with no snapshot holds no wheel and is skipped.
func (r *Router) readWheelMonths() ([]wheelMonth, error) {
	entries, err := r.store.ReadWheelAll()
	if err != nil {
		return nil, fmt.Errorf("could not read the wheel: %w", err)
	}
	months := make([]wheelMonth, 0, len(entries))
	for _, e := range entries {
		if snap, ok := e.Fold(); ok {
			months = append(months, wheelMonth{entry: e, snap: snap})
		}
	}
	return months, nil
}

// pickWheelMonth returns the index of the month to show: the most recent when
// arg is empty (-1 when nothing is recorded), else the named month. A malformed
// month, or a month with no wheel, is a clean error.
func pickWheelMonth(months []wheelMonth, arg string) (int, error) {
	month := strings.TrimPrefix(strings.TrimSpace(arg), "@")
	if month == "" {
		return len(months) - 1, nil
	}
	if !observations.ValidWheelMonth(month) {
		return 0, fmt.Errorf("could not read the month %q (want YYYY-MM)", arg)
	}
	for i, m := range months {
		if m.entry.Month == month {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no wheel is recorded for %s", month)
}

// checkWheelRatings refuses to show a month whose latest snapshot lacks a valid
// 1–10 rating for any pillar — a hand-edited file, say — rather than print a
// number that was never recorded.
func checkWheelRatings(m wheelMonth) error {
	for _, p := range observations.WheelPillars() {
		ps, ok := m.snap.Pillars[p.Key]
		if !ok || !observations.ValidWheelScore(ps.Score) {
			return fmt.Errorf(
				"could not show the %s wheel: %s has no valid %d–%d rating in %s; nothing is shown rather than a made-up number",
				m.entry.Month, p.Label, observations.WheelScoreMin, observations.WheelScoreMax, m.snap.ID,
			)
		}
	}
	return nil
}

// emptyWheelShowView is the --json view with no wheel recorded: month "", every
// array [] and the calibration map {} — never null.
func emptyWheelShowView() WheelShowView {
	return WheelShowView{
		Months:      []string{},
		Pillars:     []WheelPillarTrend{},
		Lowest:      []WheelPillarScore{},
		BiggestDrop: []WheelPillarDelta{},
		Calibration: WheelCalibration{Suggested: map[string]int{}},
	}
}

// wheelShowViewOf builds the view for the last month in window, which holds up
// to six stored months oldest first; the month before the shown one, when
// present, is the prior month the deltas are against. Ratings, deltas,
// sparklines, and callouts read `score` only; `suggested` is copied into the
// calibration block and nowhere else.
func wheelShowViewOf(window []wheelMonth) WheelShowView {
	cur := window[len(window)-1]
	view := emptyWheelShowView()
	view.Month = cur.entry.Month
	view.ReceiptID = cur.snap.ID
	view.VisionReviewed = cur.snap.VisionReviewed
	view.VisionReflection = cur.snap.VisionReflection
	for _, m := range window {
		view.Months = append(view.Months, m.entry.Month)
	}
	var prior *wheelMonth
	if len(window) > 1 {
		prior = &window[len(window)-2]
		view.PriorMonth = prior.entry.Month
	}

	for _, p := range observations.WheelPillars() {
		ps := cur.snap.Pillars[p.Key]
		trend := make([]int, len(window))
		for i, m := range window {
			trend[i] = m.snap.Pillars[p.Key].Score
		}
		pt := WheelPillarTrend{
			Pillar:    p.Key,
			Label:     p.Label,
			Score:     ps.Score,
			Trend:     trend,
			Sparkline: wheelSparkline(trend),
			Note:      ps.Note,
		}
		if prior != nil {
			d := ps.Score - prior.snap.Pillars[p.Key].Score
			pt.Delta = &d
		}
		view.Pillars = append(view.Pillars, pt)
		if ps.Suggested != nil {
			view.Calibration.Suggested[p.Key] = *ps.Suggested
		}
	}
	view.Lowest = wheelLowest(view.Pillars)
	view.BiggestDrop = wheelBiggestDrop(view.Pillars)
	return view
}

// wheelLowest names every pillar tied at the lowest rating, in canonical order.
func wheelLowest(pillars []WheelPillarTrend) []WheelPillarScore {
	out := []WheelPillarScore{}
	if len(pillars) == 0 {
		return out
	}
	low := pillars[0].Score
	for _, p := range pillars[1:] {
		low = min(low, p.Score)
	}
	for _, p := range pillars {
		if p.Score == low {
			out = append(out, WheelPillarScore{Pillar: p.Pillar, Score: p.Score})
		}
	}
	return out
}

// wheelBiggestDrop names every pillar tied at the most negative delta, in
// canonical order — empty when there is no prior month or nothing dropped.
func wheelBiggestDrop(pillars []WheelPillarTrend) []WheelPillarDelta {
	out := []WheelPillarDelta{}
	worst := 0
	for _, p := range pillars {
		if p.Delta != nil && *p.Delta < worst {
			worst = *p.Delta
		}
	}
	if worst == 0 {
		return out
	}
	for _, p := range pillars {
		if p.Delta != nil && *p.Delta == worst {
			out = append(out, WheelPillarDelta{Pillar: p.Pillar, Delta: worst})
		}
	}
	return out
}

// wheelSparkline renders ratings onto the fixed block scale (wheel.md §7.2):
// block index = ((rating − 1) × 7 + 4) ÷ 9, integer arithmetic. It is shape
// only; the rating and delta carry the exact numbers.
func wheelSparkline(trend []int) string {
	blocks := []rune(wheelSparkBlocks)
	var b strings.Builder
	for _, n := range trend {
		idx := ((n-1)*7 + 4) / 9
		idx = min(max(idx, 0), len(blocks)-1)
		b.WriteRune(blocks[idx])
	}
	return b.String()
}

// wheelShowLines renders the human `wheel show` (wheel.md §7.2): a header
// naming the comparison month, one `label: N (±d) <sparkline>` line per pillar
// (no delta at all when there is no prior month), the lowest and biggest-drop
// callouts, any notes as `- label: note` bullets, and the vision fields. Only
// `key: value` lines and `- ` bullets — never a markdown table, never an
// average or total, never a suggestion.
func wheelShowLines(view WheelShowView) []string {
	hasPrior := view.PriorMonth != ""
	lines := make([]string, 0, len(view.Pillars)+8)
	if hasPrior {
		lines = append(lines, fmt.Sprintf("wheel: %s (vs %s)", view.Month, view.PriorMonth))
	} else {
		lines = append(lines, fmt.Sprintf("wheel: %s (%s)", view.Month, wheelNoPrior))
	}
	for _, p := range view.Pillars {
		if p.Delta != nil {
			lines = append(lines, fmt.Sprintf("%s: %d (%s) %s", p.Label, p.Score, signedWheelDelta(*p.Delta), p.Sparkline))
		} else {
			lines = append(lines, fmt.Sprintf("%s: %d %s", p.Label, p.Score, p.Sparkline))
		}
	}

	lowest := make([]string, len(view.Lowest))
	for i, l := range view.Lowest {
		label, _ := observations.WheelPillarLabel(l.Pillar)
		lowest[i] = fmt.Sprintf("%s (%d)", label, l.Score)
	}
	lines = append(lines, "lowest: "+strings.Join(lowest, ", "))

	drop := "none"
	switch {
	case !hasPrior:
		drop = wheelNoPrior
	case len(view.BiggestDrop) > 0:
		parts := make([]string, len(view.BiggestDrop))
		for i, d := range view.BiggestDrop {
			label, _ := observations.WheelPillarLabel(d.Pillar)
			parts[i] = fmt.Sprintf("%s (%s)", label, signedWheelDelta(d.Delta))
		}
		drop = strings.Join(parts, ", ")
	}
	lines = append(lines, "biggest drop: "+drop)

	var notes []string
	for _, p := range view.Pillars {
		if strings.TrimSpace(p.Note) != "" {
			notes = append(notes, fmt.Sprintf("- %s: %s", p.Label, p.Note))
		}
	}
	if len(notes) > 0 {
		lines = append(append(lines, "notes:"), notes...)
	}

	reviewed := "no"
	if view.VisionReviewed {
		reviewed = "yes"
	}
	lines = append(lines, "vision reviewed: "+reviewed)
	if strings.TrimSpace(view.VisionReflection) != "" {
		lines = append(lines, "vision reflection: "+view.VisionReflection)
	}
	return lines
}

// signedWheelDelta renders a delta with an ASCII sign: +1, -2, and a bare 0.
func signedWheelDelta(d int) string {
	if d == 0 {
		return "0"
	}
	return fmt.Sprintf("%+d", d)
}

// wheelListLines renders the human `wheel list` (wheel.md §7.3): one line per
// month, most recent first — the month, its latest receipt, the snapshot count
// when the month was amended, and whether the vision was reviewed — or the
// no-history line when nothing is recorded.
func wheelListLines(rows []WheelListMonth) []string {
	if len(rows) == 0 {
		return []string{wheelNoHistoryLine}
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		line := fmt.Sprintf("%s: %s", row.Month, row.ReceiptID)
		if row.Snapshots > 1 {
			line += fmt.Sprintf(" (%d snapshots)", row.Snapshots)
		}
		if row.VisionReviewed {
			line += " · vision reviewed"
		}
		lines = append(lines, line)
	}
	return lines
}
