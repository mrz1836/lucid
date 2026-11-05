package router

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// wheelDay is a deterministic mid-month instant (EDT, shared `edt` from
// observation_test.go); the current logical month is 2026-09.
func wheelDay() time.Time { return time.Date(2026, 9, 6, 17, 12, 40, 0, edt) }

// bootedWheel returns a booted router over a fresh scaffolded Ledger.
func bootedWheel(t *testing.T) *Router {
	t.Helper()
	r := New(newScaffolded(t))
	_, err := r.Boot()
	require.NoError(t, err)
	return r
}

// syntheticWheelRatings returns all eight pillars rated — invented test data.
func syntheticWheelRatings() map[string]observations.PillarScore {
	return map[string]observations.PillarScore{
		observations.PillarHealth:        {Score: 6, Note: "synthetic walk note"},
		observations.PillarRelationships: {Score: 7},
		observations.PillarCareer:        {Score: 5},
		observations.PillarFinances:      {Score: 4},
		observations.PillarGrowth:        {Score: 7},
		observations.PillarFun:           {Score: 4},
		observations.PillarEnvironment:   {Score: 6},
		observations.PillarContribution:  {Score: 5},
	}
}

func wheelIntPtr(n int) *int { return &n }

// wheelTrendOf builds an expected null-aligned trend: one rating per month.
func wheelTrendOf(scores ...int) []*int {
	out := make([]*int, len(scores))
	for i, n := range scores {
		out[i] = wheelIntPtr(n)
	}
	return out
}

// readWheelFile returns the month's entry straight from the store.
func readWheelFile(t *testing.T, r *Router, month string) (observations.WheelEntry, bool) {
	t.Helper()
	e, found, err := r.store.ReadWheel(month)
	require.NoError(t, err)
	return e, found
}

// TestWheelAdd_RecordsReceipt: add writes one snapshot for the month with a
// receipt, the month's stable id, the stored self-ratings, and an ack that
// echoes the eight ratings in canonical order (wheel.md §7.1).
func TestWheelAdd_RecordsReceipt(t *testing.T) {
	r := bootedWheel(t)

	res, err := r.AddWheel(AddWheelRequest{Month: "2026-08", Pillars: syntheticWheelRatings(), Now: wheelDay()})
	require.NoError(t, err)
	assert.Equal(t, "wheel_2026_08_001", res.Receipt)
	assert.Equal(t, "wheel_2026-08", res.EntryID)
	assert.Equal(t, "2026-08", res.Month)
	assert.False(t, res.Amended)
	assert.Empty(t, res.Replaced)
	assert.Equal(t, 1, res.Snapshots)
	assert.Equal(t, 6, res.Scores[observations.PillarHealth])
	assert.Len(t, res.Scores, 8)

	assert.Equal(t, []string{
		"Recorded the 2026-08 wheel as wheel_2026_08_001.",
		"health: 6", "relationships: 7", "career/work: 5", "finances: 4",
		"personal growth: 7", "fun/recreation: 4", "environment: 6", "contribution: 5",
	}, res.Ack)

	entry, found := readWheelFile(t, r, "2026-08")
	require.True(t, found)
	require.Len(t, entry.History, 1)
	stored := entry.History[0]
	assert.Equal(t, res.Receipt, stored.ID)
	assert.Equal(t, "synthetic walk note", stored.Pillars[observations.PillarHealth].Note)
	assert.False(t, stored.VisionReviewed)
	assert.Empty(t, stored.VisionReflection)
}

// TestWheelSelfRatingRequired_MissingPillarRefused: the self-rating is required
// — a missing pillar is refused by name, never defaulted, inferred, or filled
// from a suggestion or a prior month, and nothing is written (wheel.md §3).
func TestWheelSelfRatingRequired_MissingPillarRefused(t *testing.T) {
	r := bootedWheel(t)
	_, err := r.AddWheel(AddWheelRequest{Month: "2026-08", Pillars: syntheticWheelRatings(), Now: wheelDay()})
	require.NoError(t, err)

	partial := syntheticWheelRatings()
	delete(partial, observations.PillarCareer)
	delete(partial, observations.PillarGrowth)
	_, err = r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: partial, Now: wheelDay()})
	require.Error(t, err)
	assert.Equal(t,
		"wheel: every pillar needs your own 1–10 rating — missing: career/work, personal growth; nothing was saved",
		err.Error())

	// A suggestion never stands in for the missing rating.
	suggestedOnly := syntheticWheelRatings()
	delete(suggestedOnly, observations.PillarCareer)
	h := suggestedOnly[observations.PillarHealth]
	h.Suggested = wheelIntPtr(5)
	suggestedOnly[observations.PillarHealth] = h
	_, err = r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: suggestedOnly, Now: wheelDay()})
	require.ErrorContains(t, err, "missing: career/work")

	_, err = r.AddWheel(AddWheelRequest{Month: "2026-09", Now: wheelDay()})
	require.ErrorContains(t, err, "missing: health, relationships, career/work, finances, personal growth")

	_, found := readWheelFile(t, r, "2026-09")
	assert.False(t, found, "a refused wheel writes nothing — not even an empty month")
}

// TestWheelScaleRejects_Router: a rating or a suggestion off the 1–10 scale, or
// an unknown pillar, is a clean error naming the pillar and the range, and
// nothing is written (wheel.md §3, §5).
func TestWheelScaleRejects_Router(t *testing.T) {
	r := bootedWheel(t)

	for _, bad := range []int{0, 11, -1} {
		p := syntheticWheelRatings()
		p[observations.PillarFun] = observations.PillarScore{Score: bad}
		_, err := r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: p, Now: wheelDay()})
		require.Errorf(t, err, "score %d", bad)
		assert.Contains(t, err.Error(), "fun/recreation needs a whole number from 1 to 10")
		assert.Contains(t, err.Error(), "nothing was saved")
	}

	p := syntheticWheelRatings()
	p[observations.PillarCareer] = observations.PillarScore{Score: 5, Suggested: wheelIntPtr(11)}
	_, err := r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: p, Now: wheelDay()})
	require.ErrorContains(t, err, "the suggestion for career/work needs a whole number from 1 to 10 (got 11)")

	p = syntheticWheelRatings()
	p["wealth"] = observations.PillarScore{Score: 5}
	_, err = r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: p, Now: wheelDay()})
	require.ErrorContains(t, err, `unknown pillar "wealth"`)

	_, found := readWheelFile(t, r, "2026-09")
	assert.False(t, found)
}

// TestWheelSameMonthLatestWins: a second (and third) add for a month appends a
// snapshot that wins on read; nothing is mutated, every attempt keeps its
// receipt, and the ack says the new one replaces the earlier one (wheel.md §4).
func TestWheelSameMonthLatestWins(t *testing.T) {
	r := bootedWheel(t)

	first, err := r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: syntheticWheelRatings(), Now: wheelDay()})
	require.NoError(t, err)

	amended := syntheticWheelRatings()
	amended[observations.PillarHealth] = observations.PillarScore{Score: 8}
	second, err := r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: amended, Now: wheelDay().Add(time.Minute)})
	require.NoError(t, err)
	assert.True(t, second.Amended)
	assert.Equal(t, first.Receipt, second.Replaced)
	assert.Equal(t, "wheel_2026_09_002", second.Receipt)
	assert.Equal(t, 2, second.Snapshots)
	assert.Equal(t,
		"Recorded the 2026-09 wheel as wheel_2026_09_002 (replaces wheel_2026_09_001 when read; both kept).",
		second.Ack[0])
	assert.Equal(t, "health: 8", second.Ack[1])

	third, err := r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: syntheticWheelRatings(), Now: wheelDay().Add(2 * time.Minute)})
	require.NoError(t, err)
	assert.Equal(t,
		"Recorded the 2026-09 wheel as wheel_2026_09_003 (replaces wheel_2026_09_002 when read; all 3 snapshots kept).",
		third.Ack[0])

	entry, found := readWheelFile(t, r, "2026-09")
	require.True(t, found)
	require.Len(t, entry.History, 3, "every attempt keeps its receipt")
	assert.Equal(t, 6, entry.History[0].Pillars[observations.PillarHealth].Score, "the first snapshot is untouched")
	assert.Equal(t, 8, entry.History[1].Pillars[observations.PillarHealth].Score)
	current, ok := entry.Fold()
	require.True(t, ok)
	assert.Equal(t, third.Receipt, current.ID, "latest wins")
}

// TestWheelSuggestedStoredSeparately: a suggestion is stored in its own field
// beside the self-rating and never becomes the score — not in the stored
// rating, the result's Scores, or the ack (wheel.md §5).
func TestWheelSuggestedStoredSeparately(t *testing.T) {
	r := bootedWheel(t)

	p := syntheticWheelRatings()
	p[observations.PillarHealth] = observations.PillarScore{Score: 6, Suggested: wheelIntPtr(2)}
	p[observations.PillarCareer] = observations.PillarScore{Score: 5, Suggested: wheelIntPtr(9)}
	res, err := r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: p, Now: wheelDay()})
	require.NoError(t, err)

	assert.Equal(t, 6, res.Scores[observations.PillarHealth], "the score is the self-rating")
	assert.Equal(t, 5, res.Scores[observations.PillarCareer])
	assert.Contains(t, res.Ack, "health: 6")
	assert.NotContains(t, res.Ack, "health: 2", "a suggestion is never echoed as a rating")

	entry, found := readWheelFile(t, r, "2026-09")
	require.True(t, found)
	stored := entry.History[0].Pillars[observations.PillarHealth]
	assert.Equal(t, 6, stored.Score)
	require.NotNil(t, stored.Suggested)
	assert.Equal(t, 2, *stored.Suggested)
	assert.Nil(t, entry.History[0].Pillars[observations.PillarFun].Suggested, "absent stays absent")

	// The caller's map is copied, never shared with the Ledger write.
	*p[observations.PillarHealth].Suggested = 7
	entry, _ = readWheelFile(t, r, "2026-09")
	assert.Equal(t, 2, *entry.History[0].Pillars[observations.PillarHealth].Suggested)
}

// TestWheelAdd_VisionFields: a non-blank reflection is stored verbatim and
// implies the re-read; a blank reflection or note is dropped, never stored as
// emptiness; the flag alone records the re-read with no reflection (wheel.md §6).
func TestWheelAdd_VisionFields(t *testing.T) {
	r := bootedWheel(t)

	_, err := r.AddWheel(AddWheelRequest{
		Month: "2026-07", Pillars: syntheticWheelRatings(), VisionReflection: "  Still true, synthetic.  ", Now: wheelDay(),
	})
	require.NoError(t, err)
	e, _ := readWheelFile(t, r, "2026-07")
	assert.True(t, e.History[0].VisionReviewed, "a reflection implies the re-read")
	assert.Equal(t, "  Still true, synthetic.  ", e.History[0].VisionReflection, "stored verbatim")

	blank := syntheticWheelRatings()
	blank[observations.PillarFun] = observations.PillarScore{Score: 4, Note: "   "}
	_, err = r.AddWheel(AddWheelRequest{Month: "2026-08", Pillars: blank, VisionReflection: " \n ", Now: wheelDay()})
	require.NoError(t, err)
	e, _ = readWheelFile(t, r, "2026-08")
	assert.False(t, e.History[0].VisionReviewed)
	assert.Empty(t, e.History[0].VisionReflection)
	assert.Empty(t, e.History[0].Pillars[observations.PillarFun].Note, "a blank note is dropped")

	_, err = r.AddWheel(AddWheelRequest{Month: "2026-09", Pillars: syntheticWheelRatings(), VisionReviewed: true, Now: wheelDay()})
	require.NoError(t, err)
	e, _ = readWheelFile(t, r, "2026-09")
	assert.True(t, e.History[0].VisionReviewed)
	assert.Empty(t, e.History[0].VisionReflection)
}

// TestWheelAdd_MonthResolution: the default month is the current logical
// month (04:00 rollover); a leading @ is tolerated; a malformed or future month
// is refused and nothing is written; an earlier month can be filled in late.
func TestWheelAdd_MonthResolution(t *testing.T) {
	r := bootedWheel(t)

	res, err := r.AddWheel(AddWheelRequest{Pillars: syntheticWheelRatings(), Now: wheelDay()})
	require.NoError(t, err)
	assert.Equal(t, "2026-09", res.Month)

	justAfterMidnight := time.Date(2026, 10, 1, 1, 30, 0, 0, edt)
	res, err = r.AddWheel(AddWheelRequest{Pillars: syntheticWheelRatings(), Now: justAfterMidnight})
	require.NoError(t, err)
	assert.Equal(t, "2026-09", res.Month, "a review finished after midnight on the 1st files under the month it reviewed")

	res, err = r.AddWheel(AddWheelRequest{Month: "@2025-12", Pillars: syntheticWheelRatings(), Now: wheelDay()})
	require.NoError(t, err)
	assert.Equal(t, "2025-12", res.Month, "a leading @ is tolerated and a past month can be filled in late")

	_, err = r.AddWheel(AddWheelRequest{Month: "2026-10", Pillars: syntheticWheelRatings(), Now: wheelDay()})
	require.ErrorContains(t, err, "cannot record the 2026-10 wheel — that month has not happened yet")

	for _, bad := range []string{"2026-9", "Sept", "2026-09-01", "2026-13"} {
		_, err = r.AddWheel(AddWheelRequest{Month: bad, Pillars: syntheticWheelRatings(), Now: wheelDay()})
		require.ErrorContainsf(t, err, "(want YYYY-MM); nothing was saved", "%q", bad)
	}

	all, err := r.store.ReadWheelAll()
	require.NoError(t, err)
	assert.Len(t, all, 2, "only 2025-12 and 2026-09 were written")
}

// TestWheelAddNoModel: add is deterministic and agent-free (architecture P9) —
// the wheel path imports no provider or agent package, so no model can be
// reached from it, and a booted router records a wheel with no provider wired
// into the request at all.
func TestWheelAddNoModel(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "wheel.go", nil, parser.ImportsOnly)
	require.NoError(t, err)
	for _, imp := range f.Imports {
		assert.NotContainsf(t, imp.Path.Value, "internal/provider", "wheel.go must not reach a model")
		assert.NotContainsf(t, imp.Path.Value, "internal/agents", "wheel.go must not reach an agent")
	}

	r := bootedWheel(t)
	res, err := r.AddWheel(AddWheelRequest{Pillars: syntheticWheelRatings(), Now: wheelDay()})
	require.NoError(t, err)
	assert.Equal(t, "wheel_2026_09_001", res.Receipt)
}

// TestWheelAdd_StorageFailures: a tree that cannot be scaffolded, or a corrupt
// month file, surfaces as an intent-named error and nothing is saved.
func TestWheelAdd_StorageFailures(t *testing.T) {
	r := bootedWheel(t)
	registries := filepath.Join(r.store.Home(), "registries")
	require.NoError(t, os.MkdirAll(registries, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(registries, "wheel"), []byte("x"), 0o600))
	_, err := r.AddWheel(AddWheelRequest{Pillars: syntheticWheelRatings(), Now: wheelDay()})
	require.ErrorContains(t, err, "could not prepare the wheel tree")

	r2 := bootedWheel(t)
	require.NoError(t, r2.store.ScaffoldWheel())
	path := filepath.Join(r2.store.Home(), "registries", "wheel", "wheel_2026-09.json")
	require.NoError(t, os.WriteFile(path, []byte("{bad"), 0o600))
	_, err = r2.AddWheel(AddWheelRequest{Pillars: syntheticWheelRatings(), Now: wheelDay()})
	require.ErrorContains(t, err, "could not record the wheel; nothing was saved")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{bad", string(b), "the corrupt month is left exactly as found")
}

// wheelRatings builds all eight pillars from ratings in canonical order —
// invented test data.
func wheelRatings(scores ...int) map[string]observations.PillarScore {
	keys := observations.WheelPillarKeys()
	out := make(map[string]observations.PillarScore, len(keys))
	for i, key := range keys {
		out[key] = observations.PillarScore{Score: scores[i]}
	}
	return out
}

// addWheelMonth records one month's wheel through the router.
func addWheelMonth(t *testing.T, r *Router, month string, pillars map[string]observations.PillarScore) {
	t.Helper()
	_, err := r.AddWheel(AddWheelRequest{Month: month, Pillars: pillars, Now: wheelDay()})
	require.NoError(t, err)
}

// showWheel runs ShowWheel and fails the test on error.
func showWheel(t *testing.T, r *Router, month string) WheelShowResult {
	t.Helper()
	res, err := r.ShowWheel(ShowWheelRequest{Month: month})
	require.NoError(t, err)
	return res
}

// pillarTrend returns the shown view's line for one pillar key.
func pillarTrend(t *testing.T, view WheelShowView, key string) WheelPillarTrend {
	t.Helper()
	for _, p := range view.Pillars {
		if p.Pillar == key {
			return p
		}
	}
	t.Fatalf("pillar %q not in view", key)
	return WheelPillarTrend{}
}

// assertDiscordSafe asserts human wheel output carries no markdown table or
// heading and no score/total/average (wheel.md §0, §7.2).
func assertDiscordSafe(t *testing.T, lines []string) {
	t.Helper()
	for _, line := range lines {
		assert.NotContains(t, line, "|", "no markdown table: %q", line)
		assert.False(t, strings.HasPrefix(line, "#"), "no heading: %q", line)
		lower := strings.ToLower(line)
		for _, banned := range []string{"average", "total", "balance score", "suggest", "calibration"} {
			assert.NotContains(t, lower, banned, "wheel output never carries %q: %q", banned, line)
		}
	}
}

// TestWheelShowDelta_VsPriorMonth: show renders the latest month against the
// prior stored month — one `label: N (±d) <sparkline>` line per pillar with an
// ASCII sign and a bare 0, the named comparison month, the lowest and
// biggest-drop callouts, notes as bullets, and the vision fields (wheel.md §7.2).
func TestWheelShowDelta_VsPriorMonth(t *testing.T) {
	r := bootedWheel(t)
	addWheelMonth(t, r, "2026-06", wheelRatings(4, 7, 6, 6, 5, 5, 6, 3))
	addWheelMonth(t, r, "2026-07", wheelRatings(5, 7, 6, 6, 6, 4, 6, 3))
	addWheelMonth(t, r, "2026-08", wheelRatings(5, 7, 6, 6, 7, 4, 6, 5))
	sep := wheelRatings(6, 7, 5, 4, 7, 4, 6, 5)
	sep[observations.PillarHealth] = observations.PillarScore{Score: 6, Note: "synthetic walk note"}
	sep[observations.PillarFinances] = observations.PillarScore{Score: 4, Note: "synthetic repair note"}
	_, err := r.AddWheel(AddWheelRequest{
		Month: "2026-09", Pillars: sep, VisionReflection: "Synthetic reflection.", Now: wheelDay(),
	})
	require.NoError(t, err)

	res := showWheel(t, r, "")
	assert.Equal(t, []string{
		"wheel: 2026-09 (vs 2026-08)",
		"health: 6 (+1) ▃▄▄▅",
		"relationships: 7 (0) ▆▆▆▆",
		"career/work: 5 (-1) ▅▅▅▄",
		"finances: 4 (-2) ▅▅▅▃",
		"personal growth: 7 (0) ▄▅▆▆",
		"fun/recreation: 4 (0) ▄▃▃▃",
		"environment: 6 (0) ▅▅▅▅",
		"contribution: 5 (0) ▃▃▄▄",
		"lowest: finances (4), fun/recreation (4)",
		"biggest drop: finances (-2)",
		"notes:",
		"- health: synthetic walk note",
		"- finances: synthetic repair note",
		"vision reviewed: yes",
		"vision reflection: Synthetic reflection.",
	}, res.Lines)
	assertDiscordSafe(t, res.Lines)

	v := res.View
	assert.Equal(t, "2026-09", v.Month)
	assert.Equal(t, "2026-08", v.PriorMonth)
	assert.Equal(t, "wheel_2026_09_001", v.ReceiptID)
	assert.Equal(t, []string{"2026-06", "2026-07", "2026-08", "2026-09"}, v.Months)
	require.Len(t, v.Pillars, 8)
	health := pillarTrend(t, v, observations.PillarHealth)
	require.NotNil(t, health.Delta)
	assert.Equal(t, 1, *health.Delta)
	assert.Equal(t, wheelTrendOf(4, 5, 5, 6), health.Trend)
	assert.Equal(t, "synthetic walk note", health.Note)
	assert.Equal(t, []WheelPillarScore{{Pillar: "finances", Score: 4}, {Pillar: "fun", Score: 4}}, v.Lowest)
	assert.Equal(t, []WheelPillarDelta{{Pillar: "finances", Delta: -2}}, v.BiggestDrop)
	assert.True(t, v.VisionReviewed, "a reflection implies the re-read happened")
}

// TestWheelShowDelta_SkipsGapMonths: a month with no wheel is skipped, never
// counted as zero — the delta is against the most recent prior STORED month,
// which the header names; --month shows an earlier month against its own prior
// month with the sparkline ending there; an amended month compares by its
// latest snapshot (wheel.md §7.2, §4).
func TestWheelShowDelta_SkipsGapMonths(t *testing.T) {
	r := bootedWheel(t)
	addWheelMonth(t, r, "2026-03", wheelRatings(2, 2, 2, 2, 2, 2, 2, 2))
	addWheelMonth(t, r, "2026-05", wheelRatings(5, 5, 5, 5, 5, 5, 5, 5))
	addWheelMonth(t, r, "2026-05", wheelRatings(3, 5, 5, 5, 5, 5, 5, 5)) // amend: latest wins
	addWheelMonth(t, r, "2026-08", wheelRatings(6, 5, 5, 5, 5, 5, 5, 8))

	res := showWheel(t, r, "")
	assert.Equal(t, "wheel: 2026-08 (vs 2026-05)", res.Lines[0])
	assert.Equal(t, "health: 6 (+3) ▂▃▅", res.Lines[1], "delta against the amended May snapshot, not the first")
	assert.Equal(t, "contribution: 8 (+3) ▂▄▆", res.Lines[8])
	assert.Equal(t, "biggest drop: none", res.Lines[10], "nothing dropped")
	assert.Equal(t, []string{"2026-03", "2026-05", "2026-08"}, res.View.Months, "the gap months are not rows")
	assert.Empty(t, res.View.BiggestDrop)
	assert.NotNil(t, res.View.BiggestDrop)

	mid := showWheel(t, r, "@2026-05")
	assert.Equal(t, "wheel: 2026-05 (vs 2026-03)", mid.Lines[0])
	assert.Equal(t, "health: 3 (+1) ▂▃", mid.Lines[1])
	assert.Equal(t, []string{"2026-03", "2026-05"}, mid.View.Months, "the sparkline ends at the shown month")
	assert.Equal(t, "wheel_2026_05_002", mid.View.ReceiptID)
}

// TestWheelShowNoPrior: the first month recorded is shown with no prior month —
// the header says so, the pillar lines carry no delta at all (never a
// fabricated (+0)), and biggest drop reads `no prior month` (wheel.md §7.2).
func TestWheelShowNoPrior(t *testing.T) {
	r := bootedWheel(t)
	addWheelMonth(t, r, "2026-09", wheelRatings(6, 7, 5, 4, 7, 4, 6, 5))

	res := showWheel(t, r, "")
	assert.Equal(t, []string{
		"wheel: 2026-09 (no prior month)",
		"health: 6 ▅",
		"relationships: 7 ▆",
		"career/work: 5 ▄",
		"finances: 4 ▃",
		"personal growth: 7 ▆",
		"fun/recreation: 4 ▃",
		"environment: 6 ▅",
		"contribution: 5 ▄",
		"lowest: finances (4), fun/recreation (4)",
		"biggest drop: no prior month",
		"vision reviewed: no",
	}, res.Lines)
	assertDiscordSafe(t, res.Lines)
	assert.Empty(t, res.View.PriorMonth)
	for _, p := range res.View.Pillars {
		assert.Nil(t, p.Delta, "%s has no delta without a prior month", p.Pillar)
	}

	b, err := json.Marshal(res.View)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"delta":null`)
	assert.Contains(t, string(b), `"biggest_drop":[]`)
	assert.Contains(t, string(b), `"prior_month":""`)
}

// TestWheelShowEmptyHistory: with no wheel recorded, show says so and returns
// the empty view — month "", every array [] and never null, no invented rating
// (wheel.md §7.2).
func TestWheelShowEmptyHistory(t *testing.T) {
	r := bootedWheel(t)
	res := showWheel(t, r, "")
	assert.Equal(t, []string{"no prior month — no wheel recorded yet"}, res.Lines)

	b, err := json.Marshal(res.View)
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`{
		"month": "", "prior_month": "", "receipt_id": "",
		"pillar_set": %d, "pillars_changed": null,
		"months": [], "pillars": [], "lowest": [], "biggest_drop": [],
		"vision_reviewed": false, "vision_reflection": "",
		"calibration": {"suggested": {}}
	}`, observations.CurrentWheelPillarSet), string(b))
}

// TestWheelShow_MonthSelection: --month tolerates a leading `@`; a malformed
// month, or a month with no wheel, is a clean error.
func TestWheelShow_MonthSelection(t *testing.T) {
	r := bootedWheel(t)
	_, err := r.ShowWheel(ShowWheelRequest{Month: "2026-08"})
	require.ErrorContains(t, err, "no wheel is recorded for 2026-08")

	addWheelMonth(t, r, "2026-08", wheelRatings(6, 7, 5, 4, 7, 4, 6, 5))
	assert.Equal(t, "2026-08", showWheel(t, r, " @2026-08 ").View.Month)

	for _, bad := range []string{"2026-8", "2026-13", "08-2026", "latest"} {
		_, err = r.ShowWheel(ShowWheelRequest{Month: bad})
		require.ErrorContains(t, err, "want YYYY-MM", bad)
	}
	_, err = r.ShowWheel(ShowWheelRequest{Month: "2026-07"})
	require.ErrorContains(t, err, "no wheel is recorded for 2026-07")
}

// TestWheelShowSparkline_FixedScaleSixMonths: the sparkline maps every rating
// onto the fixed eight-block scale — never auto-scaled — and spans at most the
// last six stored months, oldest first (wheel.md §7.2).
func TestWheelShowSparkline_FixedScaleSixMonths(t *testing.T) {
	want := map[int]string{1: "▁", 2: "▂", 3: "▃", 4: "▃", 5: "▄", 6: "▅", 7: "▆", 8: "▆", 9: "▇", 10: "█"}
	for n, block := range want {
		assert.Equal(t, block, wheelSparkline([]int{n}), "rating %d", n)
	}
	assert.Equal(t, "▁▂▃▃▄▅▆▆▇█", wheelSparkline([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}))

	r := bootedWheel(t)
	for i, month := range []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08"} {
		addWheelMonth(t, r, month, wheelRatings(i+1, 9, 9, 9, 9, 9, 9, 9))
	}
	v := showWheel(t, r, "").View
	assert.Equal(t, []string{"2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08"}, v.Months)
	health := pillarTrend(t, v, observations.PillarHealth)
	assert.Equal(t, wheelTrendOf(3, 4, 5, 6, 7, 8), health.Trend)
	assert.Equal(t, "▃▃▄▅▆▆", health.Sparkline)
	assert.Equal(t, "▇▇▇▇▇▇", pillarTrend(t, v, observations.PillarFun).Sparkline,
		"a flat pillar stays flat at its own band — no auto-scaling")
}

// TestWheelShowCallouts_Ties: lowest names every pillar tied at the lowest
// rating and biggest drop every pillar tied at the most negative delta, in
// canonical order.
func TestWheelShowCallouts_Ties(t *testing.T) {
	r := bootedWheel(t)
	addWheelMonth(t, r, "2026-08", wheelRatings(8, 8, 8, 8, 8, 8, 8, 8))
	addWheelMonth(t, r, "2026-09", wheelRatings(5, 8, 6, 8, 5, 3, 8, 3))

	res := showWheel(t, r, "")
	assert.Equal(t, "lowest: fun/recreation (3), contribution (3)", res.Lines[9])
	assert.Equal(t, "biggest drop: fun/recreation (-5), contribution (-5)", res.Lines[10])
	assert.Equal(t, []WheelPillarDelta{{Pillar: "fun", Delta: -5}, {Pillar: "contribution", Delta: -5}}, res.View.BiggestDrop)
}

// TestWheelSuggestedExcludedFromTrend: `suggested` is never the score and never
// in the trend (wheel.md §5) — two Ledgers with identical ratings but different
// (or no) suggestions render identical human output, ratings, deltas,
// sparklines, and callouts; the suggestions surface only in the view's
// calibration block.
func TestWheelSuggestedExcludedFromTrend(t *testing.T) {
	plain := bootedWheel(t)
	addWheelMonth(t, plain, "2026-08", wheelRatings(5, 7, 6, 6, 7, 4, 6, 5))
	addWheelMonth(t, plain, "2026-09", wheelRatings(6, 7, 5, 4, 7, 4, 6, 5))

	withSugg := bootedWheel(t)
	aug := wheelRatings(5, 7, 6, 6, 7, 4, 6, 5)
	sep := wheelRatings(6, 7, 5, 4, 7, 4, 6, 5)
	for _, key := range observations.WheelPillarKeys() {
		a, s := aug[key], sep[key]
		a.Suggested = wheelIntPtr(10) // far from every rating — any leak would move a number
		s.Suggested = wheelIntPtr(1)
		aug[key], sep[key] = a, s
	}
	addWheelMonth(t, withSugg, "2026-08", aug)
	addWheelMonth(t, withSugg, "2026-09", sep)

	want := showWheel(t, plain, "")
	got := showWheel(t, withSugg, "")
	assert.Equal(t, want.Lines, got.Lines, "human output is identical with or without suggestions")
	assert.Equal(t, want.View.Pillars, got.View.Pillars, "ratings, deltas, trends, sparklines ignore suggestions")
	assert.Equal(t, want.View.Lowest, got.View.Lowest)
	assert.Equal(t, want.View.BiggestDrop, got.View.BiggestDrop)
	assertDiscordSafe(t, got.Lines)

	assert.Empty(t, want.View.Calibration.Suggested)
	assert.NotNil(t, want.View.Calibration.Suggested)
	require.Len(t, got.View.Calibration.Suggested, 8)
	for _, key := range observations.WheelPillarKeys() {
		assert.Equal(t, 1, got.View.Calibration.Suggested[key], "the shown month's suggestion, in calibration only")
		assert.Equal(t, sep[key].Score, pillarTrend(t, got.View, key).Score, "the score is the self-rating")
	}
}

// TestWheelShowVerbatim_StoredIntegers: show prints the stored ratings exactly
// as stored — the scale's ends included — unrounded and unsoftened, with no
// average or total (wheel.md §7.2).
func TestWheelShowVerbatim_StoredIntegers(t *testing.T) {
	r := bootedWheel(t)
	addWheelMonth(t, r, "2026-08", wheelRatings(10, 1, 9, 2, 3, 8, 7, 6))
	addWheelMonth(t, r, "2026-09", wheelRatings(1, 10, 2, 9, 3, 8, 7, 6))

	res := showWheel(t, r, "")
	assert.Equal(t, []string{
		"health: 1 (-9) █▁",
		"relationships: 10 (+9) ▁█",
		"career/work: 2 (-7) ▇▂",
		"finances: 9 (+7) ▂▇",
		"personal growth: 3 (0) ▃▃",
		"fun/recreation: 8 (0) ▆▆",
		"environment: 7 (0) ▆▆",
		"contribution: 6 (0) ▅▅",
	}, res.Lines[1:9])
	assert.Equal(t, "lowest: health (1)", res.Lines[9])
	assert.Equal(t, "biggest drop: health (-9)", res.Lines[10])
	for key, score := range map[string]int{"health": 1, "relationships": 10, "career": 2, "finances": 9} {
		assert.Equal(t, score, pillarTrend(t, res.View, key).Score)
	}
	assertDiscordSafe(t, res.Lines)
}

// TestWheelShow_RefusesMalformedStoredRating: a stored snapshot missing a
// rating (a hand-edited file) is refused rather than shown with a made-up
// number; an unreadable month file surfaces as a read error.
func TestWheelShow_RefusesMalformedStoredRating(t *testing.T) {
	r := bootedWheel(t)
	addWheelMonth(t, r, "2026-09", wheelRatings(6, 7, 5, 4, 7, 4, 6, 5))
	path := filepath.Join(r.store.Home(), "registries", "wheel", "wheel_2026-09.json")
	entry, found := readWheelFile(t, r, "2026-09")
	require.True(t, found)
	delete(entry.History[0].Pillars, observations.PillarFinances)
	b, err := json.Marshal(entry)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))

	_, err = r.ShowWheel(ShowWheelRequest{})
	require.ErrorContains(t, err, "finances has no valid 1–10 rating")
	require.ErrorContains(t, err, "nothing is shown rather than a made-up number")

	require.NoError(t, os.WriteFile(path, []byte("{bad"), 0o600))
	_, err = r.ShowWheel(ShowWheelRequest{})
	require.ErrorContains(t, err, "could not read the wheel")
	_, err = r.ListWheel()
	require.ErrorContains(t, err, "could not read the wheel")
}

// TestWheelListEmptyHistory: with no wheel recorded, list says `no prior
// month` and returns an empty (never null) month list — never an invented row
// (wheel.md §7.3).
func TestWheelListEmptyHistory(t *testing.T) {
	r := bootedWheel(t)
	res, err := r.ListWheel()
	require.NoError(t, err)
	assert.Equal(t, []string{"no prior month — no wheel recorded yet"}, res.Lines)
	b, err := json.Marshal(res.View)
	require.NoError(t, err)
	assert.JSONEq(t, `{"months": []}`, string(b))
}

// TestWheelList_MostRecentFirst: list names every recorded month, most recent
// first, with its latest receipt, its snapshot count when amended, and whether
// the vision was reviewed; a month file holding no snapshot is not a row
// (wheel.md §7.3).
func TestWheelList_MostRecentFirst(t *testing.T) {
	r := bootedWheel(t)
	addWheelMonth(t, r, "2026-07", wheelRatings(6, 7, 5, 4, 7, 4, 6, 5))
	_, err := r.AddWheel(AddWheelRequest{
		Month: "2026-08", Pillars: wheelRatings(6, 7, 5, 4, 7, 4, 6, 5), VisionReviewed: true, Now: wheelDay(),
	})
	require.NoError(t, err)
	addWheelMonth(t, r, "2026-09", wheelRatings(6, 7, 5, 4, 7, 4, 6, 5))
	_, err = r.AddWheel(AddWheelRequest{
		Month: "2026-09", Pillars: wheelRatings(6, 7, 5, 4, 7, 4, 6, 5), VisionReflection: "Synthetic.", Now: wheelDay(),
	})
	require.NoError(t, err)
	empty := observations.NewWheelEntry("2026-06", wheelDay().Format(time.RFC3339))
	b, err := json.Marshal(empty)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(r.store.Home(), "registries", "wheel", "wheel_2026-06.json"), b, 0o600))

	res, err := r.ListWheel()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"2026-09: wheel_2026_09_002 (2 snapshots) · vision reviewed",
		"2026-08: wheel_2026_08_001 · vision reviewed",
		"2026-07: wheel_2026_07_001",
	}, res.Lines)
	assertDiscordSafe(t, res.Lines)
	require.Len(t, res.View.Months, 3)
	top := res.View.Months[0]
	assert.Equal(t, WheelListMonth{
		Month: "2026-09", EntryID: "wheel_2026-09", ReceiptID: "wheel_2026_09_002",
		Snapshots: 2, RecordedAt: wheelDay().Format(time.RFC3339), VisionReviewed: true,
	}, top)
}

// TestWheelListNoModel: show and list are pure, deterministic, agent-free reads
// (architecture P9) — a booted router with no provider renders both, twice,
// identically, and neither touches the Ledger.
func TestWheelListNoModel(t *testing.T) {
	r := bootedWheel(t)
	addWheelMonth(t, r, "2026-08", wheelRatings(5, 7, 6, 6, 7, 4, 6, 5))
	addWheelMonth(t, r, "2026-09", wheelRatings(6, 7, 5, 4, 7, 4, 6, 5))
	path := filepath.Join(r.store.Home(), "registries", "wheel", "wheel_2026-09.json")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	first := showWheel(t, r, "")
	firstList, err := r.ListWheel()
	require.NoError(t, err)
	assert.Equal(t, first, showWheel(t, r, ""))
	secondList, err := r.ListWheel()
	require.NoError(t, err)
	assert.Equal(t, firstList, secondList)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a read writes nothing")
	all, err := r.store.ReadWheelAll()
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// wheelSetRatings builds every pillar of pillar set set from ratings in that
// set's canonical order — invented test data, independent of the set new
// writes record in.
func wheelSetRatings(t *testing.T, set int, scores ...int) map[string]observations.PillarScore {
	t.Helper()
	keys, ok := observations.WheelPillarKeysOf(set)
	require.True(t, ok, "pillar set %d", set)
	require.Len(t, scores, len(keys), "one rating per pillar of set %d", set)
	out := make(map[string]observations.PillarScore, len(keys))
	for i, key := range keys {
		out[key] = observations.PillarScore{Score: scores[i]}
	}
	return out
}

// writeRawWheelMonth writes one month file straight to disk, bypassing the add
// path, so the fixture keeps its pillar set whatever set new writes record in.
// Set 1 is written with no pillar_set at all under schema 1 — exactly as every
// month before pillar set 2 was stored; any other set is stamped under schema 2.
func writeRawWheelMonth(
	t *testing.T, r *Router, month string, set int, pillars map[string]observations.PillarScore,
) {
	t.Helper()
	schema, stamped := 1, 0
	if set != 1 {
		schema, stamped = 2, set
	}
	at := wheelDay().Format(time.RFC3339)
	entry := observations.WheelEntry{
		Key:    observations.WheelEntryKey(month),
		Kind:   observations.RegistryWheel,
		Schema: schema,
		Month:  month,
		History: []observations.WheelSnapshot{{
			ID:        observations.WheelReceiptID(month, 1),
			At:        at,
			Type:      observations.WheelEventSnapshot,
			PillarSet: stamped,
			Pillars:   pillars,
		}},
		CreatedAt: at,
		UpdatedAt: at,
	}
	b, err := json.Marshal(entry)
	require.NoError(t, err)
	if set == 1 {
		require.NotContains(t, string(b), "pillar_set", "a set-1 fixture carries no pillar_set")
	}
	require.NoError(t, r.store.ScaffoldWheel())
	path := filepath.Join(r.store.Home(), "registries", "wheel", "wheel_"+month+".json")
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

// withNote returns pillars with key's note (and, when given, its suggestion)
// set.
func withNote(pillars map[string]observations.PillarScore, key, note string, suggested *int) map[string]observations.PillarScore {
	p := pillars[key]
	p.Note = note
	p.Suggested = suggested
	pillars[key] = p
	return pillars
}

// TestWheelShow_PillarSetBoundary: a set-2 month shown against a set-1 prior
// month never errors and never shows a number that was not recorded — the
// seven unchanged pillars carry real deltas, the two new ones read `(new)` with
// a nil delta and a trend over the months that rated them, biggest drop leaves
// them out, lowest covers all nine, the header line names the change, and the
// retired pillar is not shown (wheel.md §7.2 "Across a pillar-set change").
func TestWheelShow_PillarSetBoundary(t *testing.T) {
	r := bootedWheel(t)
	aug := wheelSetRatings(t, 1, 5, 7, 6, 6, 6, 4, 6, 5)
	aug = withNote(aug, observations.PillarRelationships, "synthetic blended spoke note", wheelIntPtr(4))
	writeRawWheelMonth(t, r, "2026-08", 1, aug)
	// A low romance rating: compared against the retired relationships 7 it
	// would be the biggest drop (-5), so its absence from biggest drop is proof.
	sep := wheelSetRatings(t, 2, 6, 2, 7, 5, 3, 7, 4, 6, 5)
	sep = withNote(sep, observations.PillarFriends, "synthetic dinner note", nil)
	sep = withNote(sep, observations.PillarHealth, "", wheelIntPtr(7))
	writeRawWheelMonth(t, r, "2026-09", 2, sep)

	res, err := r.ShowWheel(ShowWheelRequest{})
	require.NoError(t, err, "a mixed window never errors")
	assert.Equal(t, []string{
		"wheel: 2026-09 (vs 2026-08)",
		"pillars changed since 2026-08: relationships → romance/partner + friends & family",
		"health: 6 (+1) ▄▅",
		"romance/partner: 2 (new) ▂",
		"friends & family: 7 (new) ▆",
		"career/work: 5 (-1) ▅▄",
		"finances: 3 (-3) ▅▃",
		"personal growth: 7 (+1) ▅▆",
		"fun/recreation: 4 (0) ▃▃",
		"environment: 6 (0) ▅▅",
		"contribution: 5 (0) ▄▄",
		"lowest: romance/partner (2)",
		"biggest drop: finances (-3)",
		"notes:",
		"- friends & family: synthetic dinner note",
		"vision reviewed: no",
	}, res.Lines)
	assertDiscordSafe(t, res.Lines)
	for _, line := range res.Lines {
		assert.False(t, strings.HasPrefix(line, "relationships:"), "no retired pillar line: %q", line)
		assert.NotContains(t, line, "blended", "a retired pillar's note is never carried: %q", line)
	}

	v := res.View
	assert.Equal(t, "2026-09", v.Month)
	assert.Equal(t, "2026-08", v.PriorMonth)
	assert.Equal(t, 2, v.PillarSet)
	assert.Equal(t, &WheelPillarsChanged{
		Since: "2026-08", From: []string{"relationships"}, To: []string{"romance", "friends"},
	}, v.PillarsChanged)
	assert.Equal(t, []string{"2026-08", "2026-09"}, v.Months)
	require.Len(t, v.Pillars, 9)
	keys := make([]string, len(v.Pillars))
	for i, p := range v.Pillars {
		keys[i] = p.Pillar
	}
	assert.Equal(t, []string{
		"health", "romance", "friends", "career", "finances", "growth", "fun", "environment", "contribution",
	}, keys, "the shown month's set decides the lines, in its canonical order")

	for _, key := range []string{observations.PillarRomance, observations.PillarFriends} {
		p := pillarTrend(t, v, key)
		assert.Nil(t, p.Delta, "%s is new since the prior month — no delta", key)
		require.Len(t, p.Trend, 2, "the trend stays aligned to months")
		assert.Nil(t, p.Trend[0], "%s was not rated in the set-1 month", key)
		require.NotNil(t, p.Trend[1])
		assert.Equal(t, p.Score, *p.Trend[1])
		assert.Len(t, []rune(p.Sparkline), 1, "%s sparkline covers only the month that rated it", key)
	}
	deltas := 0
	for _, p := range v.Pillars {
		if p.Delta != nil {
			deltas++
			require.Len(t, p.Trend, 2)
			assert.NotNil(t, p.Trend[0], "%s was rated in both months", p.Pillar)
		}
	}
	assert.Equal(t, 7, deltas, "the seven unchanged pillars carry real deltas")
	assert.Equal(t, 1, *pillarTrend(t, v, observations.PillarHealth).Delta)
	assert.Equal(t, -3, *pillarTrend(t, v, observations.PillarFinances).Delta)
	assert.Equal(t, []WheelPillarDelta{{Pillar: "finances", Delta: -3}}, v.BiggestDrop)
	assert.Equal(t, []WheelPillarScore{{Pillar: "romance", Score: 2}}, v.Lowest)
	assert.Equal(t, map[string]int{"health": 7}, v.Calibration.Suggested,
		"calibration is the shown month's own — the retired pillar's suggestion is not carried")

	b, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(b),
		`"pillar_set":2,"pillars_changed":{"since":"2026-08","from":["relationships"],"to":["romance","friends"]}`)
	assert.Contains(t, string(b), `"pillar":"romance","label":"romance/partner","score":2,"delta":null,"trend":[null,2]`)
	assert.NotContains(t, string(b), `"pillar":"relationships"`)
}

// TestWheelShow_PillarSetWindow: in a set-1 → set-2 → set-2 window the newest
// month's deltas are against the middle set-2 month — real deltas on the new
// pillars too — each new pillar's trend is null for the set-1 month and its
// sparkline skips it, the seven unchanged pillars span all three months, and
// no change line is printed (the set did not change since the prior month).
func TestWheelShow_PillarSetWindow(t *testing.T) {
	r := bootedWheel(t)
	writeRawWheelMonth(t, r, "2026-07", 1, wheelSetRatings(t, 1, 4, 6, 5, 5, 6, 3, 7, 4))
	writeRawWheelMonth(t, r, "2026-08", 2, wheelSetRatings(t, 2, 5, 3, 6, 5, 6, 6, 4, 7, 4))
	writeRawWheelMonth(t, r, "2026-09", 2, wheelSetRatings(t, 2, 6, 6, 7, 6, 5, 7, 4, 6, 5))

	res := showWheel(t, r, "")
	assert.Equal(t, []string{
		"wheel: 2026-09 (vs 2026-08)",
		"health: 6 (+1) ▃▄▅",
		"romance/partner: 6 (+3) ▃▅",
		"friends & family: 7 (+1) ▅▆",
		"career/work: 6 (+1) ▄▄▅",
		"finances: 5 (-1) ▄▅▄",
		"personal growth: 7 (+1) ▅▅▆",
		"fun/recreation: 4 (0) ▃▃▃",
		"environment: 6 (-1) ▆▆▅",
		"contribution: 5 (+1) ▃▃▄",
		"lowest: fun/recreation (4)",
		"biggest drop: finances (-1), environment (-1)",
		"vision reviewed: no",
	}, res.Lines)
	assertDiscordSafe(t, res.Lines)

	v := res.View
	assert.Nil(t, v.PillarsChanged, "the set did not change since the prior month")
	assert.Equal(t, 2, v.PillarSet)
	assert.Equal(t, []string{"2026-07", "2026-08", "2026-09"}, v.Months)
	romance := pillarTrend(t, v, observations.PillarRomance)
	require.NotNil(t, romance.Delta)
	assert.Equal(t, 3, *romance.Delta, "the delta is against the middle set-2 month")
	assert.Equal(t, []*int{nil, wheelIntPtr(3), wheelIntPtr(6)}, romance.Trend)
	assert.Len(t, []rune(romance.Sparkline), 2, "the sparkline skips the set-1 month")
	assert.Equal(t, []*int{nil, wheelIntPtr(6), wheelIntPtr(7)}, pillarTrend(t, v, observations.PillarFriends).Trend)
	for _, p := range v.Pillars {
		if p.Pillar == observations.PillarRomance || p.Pillar == observations.PillarFriends {
			continue
		}
		assert.Len(t, []rune(p.Sparkline), 3, "%s spans all three months", p.Pillar)
		for i, n := range p.Trend {
			assert.NotNil(t, n, "%s was rated in %s", p.Pillar, v.Months[i])
		}
	}

	// The first set-2 month, shown on its own, is the boundary month.
	aug := showWheel(t, r, "2026-08")
	assert.Equal(t, []string{
		"wheel: 2026-08 (vs 2026-07)",
		"pillars changed since 2026-07: relationships → romance/partner + friends & family",
	}, aug.Lines[:2])
	assert.Equal(t, "romance/partner: 3 (new) ▃", aug.Lines[3])
	assert.Equal(t, "friends & family: 6 (new) ▅", aug.Lines[4])
}

// TestWheelShow_RefusesUnknownPillarSet: a month recorded in a pillar set this
// build does not know (a newer build's) is refused cleanly — naming the month
// and the receipt — rather than read in the wrong set; a month whose own window
// does not reach it is still shown.
func TestWheelShow_RefusesUnknownPillarSet(t *testing.T) {
	r := bootedWheel(t)
	writeRawWheelMonth(t, r, "2026-08", 1, wheelSetRatings(t, 1, 5, 7, 6, 6, 6, 4, 6, 5))
	writeRawWheelMonth(t, r, "2026-09", 3, wheelSetRatings(t, 2, 6, 3, 7, 5, 3, 7, 4, 6, 5))

	_, err := r.ShowWheel(ShowWheelRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not show the 2026-09 wheel")
	assert.Contains(t, err.Error(), "wheel_2026_09_001 was recorded in pillar set 3")
	assert.Contains(t, err.Error(), "recorded by a newer build")
	assert.Contains(t, err.Error(), "nothing is shown rather than a made-up number")

	writeRawWheelMonth(t, r, "2026-07", 3, wheelSetRatings(t, 2, 6, 3, 7, 5, 3, 7, 4, 6, 5))
	_, err = r.ShowWheel(ShowWheelRequest{Month: "2026-08"})
	require.ErrorContains(t, err, "could not show the 2026-07 wheel", "an unknown set anywhere in the window is refused")

	writeRawWheelMonth(t, r, "2026-07", 1, wheelSetRatings(t, 1, 4, 6, 5, 5, 6, 3, 7, 4))
	prior := showWheel(t, r, "2026-08")
	assert.Equal(t, "wheel: 2026-08 (vs 2026-07)", prior.Lines[0], "a window that never reaches the unknown set reads")
}

// TestWheelShow_Set1MonthAsStored: --month on a set-1 month, in a Ledger whose
// later months are set 2, renders exactly its eight pillars as stored —
// relationships included — with deltas against its own set-1 prior month, the
// sparkline ending there, and no change line (wheel.md §7.2).
func TestWheelShow_Set1MonthAsStored(t *testing.T) {
	r := bootedWheel(t)
	writeRawWheelMonth(t, r, "2026-06", 1, wheelSetRatings(t, 1, 5, 6, 7, 4, 5, 6, 7, 3))
	jul := wheelSetRatings(t, 1, 6, 8, 6, 4, 6, 5, 7, 4)
	jul = withNote(jul, observations.PillarRelationships, "synthetic blended spoke note", wheelIntPtr(9))
	writeRawWheelMonth(t, r, "2026-07", 1, jul)
	writeRawWheelMonth(t, r, "2026-08", 2, wheelSetRatings(t, 2, 6, 3, 7, 6, 4, 6, 5, 7, 4))
	writeRawWheelMonth(t, r, "2026-09", 2, wheelSetRatings(t, 2, 7, 4, 7, 6, 5, 6, 5, 7, 5))

	res := showWheel(t, r, "2026-07")
	assert.Equal(t, []string{
		"wheel: 2026-07 (vs 2026-06)",
		"health: 6 (+1) ▄▅",
		"relationships: 8 (+2) ▅▆",
		"career/work: 6 (-1) ▆▅",
		"finances: 4 (0) ▃▃",
		"personal growth: 6 (+1) ▄▅",
		"fun/recreation: 5 (-1) ▅▄",
		"environment: 7 (0) ▆▆",
		"contribution: 4 (+1) ▃▃",
		"lowest: finances (4), contribution (4)",
		"biggest drop: career/work (-1), fun/recreation (-1)",
		"notes:",
		"- relationships: synthetic blended spoke note",
		"vision reviewed: no",
	}, res.Lines)
	assertDiscordSafe(t, res.Lines)

	v := res.View
	assert.Equal(t, 1, v.PillarSet)
	assert.Nil(t, v.PillarsChanged)
	assert.Equal(t, []string{"2026-06", "2026-07"}, v.Months, "the window ends at the shown month")
	require.Len(t, v.Pillars, 8)
	rel := pillarTrend(t, v, observations.PillarRelationships)
	require.NotNil(t, rel.Delta)
	assert.Equal(t, 2, *rel.Delta)
	assert.Equal(t, wheelTrendOf(6, 8), rel.Trend)
	assert.Equal(t, map[string]int{"relationships": 9}, v.Calibration.Suggested, "a set-1 month's own calibration")
}

// TestWheelShow_PillarSetReversed: a set-1 month stored after a set-2 month
// (a set-2 backfill of an earlier month) is shown in its own set: the change
// line names the reverse change and relationships reads `(new)` — nothing is
// compared across the split.
func TestWheelShow_PillarSetReversed(t *testing.T) {
	r := bootedWheel(t)
	writeRawWheelMonth(t, r, "2026-08", 2, wheelSetRatings(t, 2, 5, 3, 6, 6, 6, 6, 4, 6, 5))
	writeRawWheelMonth(t, r, "2026-09", 1, wheelSetRatings(t, 1, 6, 7, 5, 6, 7, 4, 6, 5))

	res := showWheel(t, r, "")
	assert.Equal(t, "pillars changed since 2026-08: romance/partner + friends & family → relationships", res.Lines[1])
	assert.Equal(t, "relationships: 7 (new) ▆", res.Lines[3])
	assert.Equal(t, 1, res.View.PillarSet)
	assert.Nil(t, pillarTrend(t, res.View, observations.PillarRelationships).Delta)
	assert.Equal(t, &WheelPillarsChanged{
		Since: "2026-08", From: []string{"romance", "friends"}, To: []string{"relationships"},
	}, res.View.PillarsChanged)
}
