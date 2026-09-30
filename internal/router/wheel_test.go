package router

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
