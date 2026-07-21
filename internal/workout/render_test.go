package workout

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/lucid/internal/agents/safety"
)

// renderNow is the stable Monday (2026-07-20) the golden test pins so the header
// weekday is deterministic across runs.
func renderNow() time.Time {
	return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
}

// sampleRecommendation is an ordinary-day recommendation (no pain hard stop): a
// hard legs card, its easier variant, and a plain calendar reason.
func sampleRecommendation() Recommendation {
	return Recommendation{
		Primary: Card{
			ID: "legs", Name: "Legs + hips", Focus: []string{"legs"}, Load: LoadHard,
			Movements: []string{"goblet squat", "hip hinge"},
		},
		Fallback: Card{
			ID: "legs_easier", Name: "Easy legs", Load: LoadLight,
			Movements: []string{"bodyweight squat", "glute bridge"},
		},
		Reason: "On the program calendar today: Legs + hips.",
	}
}

// sampleTrend is a populated trend: a live streak, an up week, a skipped-day
// count, and one body-response signal.
func sampleTrend() Trend {
	sore := 6
	return Trend{
		Streak:      5,
		WindowDays:  28,
		Sessions:    8,
		ThisWeek:    3,
		PriorWeek:   2,
		Direction:   DirectionUp,
		SkippedDays: 20,
		BodyResponse: []BodySignal{
			{Part: "legs", Soreness: &sore, AsOf: "2026-07-19"},
		},
	}
}

// TestRenderGolden pins the exact bytes of a full ordinary-day message so the
// scaffold's structure — header, the three offerings, blank lines between
// offering bullets, the progress panel, and the reason — is a hard contract, and asserts the
// render is byte-stable across repeated calls.
func TestRenderGolden(t *testing.T) {
	t.Parallel()

	want := strings.Join([]string{
		emojiHeader + " **Workout** · Monday, Jul 20",
		"",
		emojiOfferings + " **Today's options**",
		workoutBullet + " **Recommended** — Legs + hips · legs · goblet squat, hip hinge",
		"",
		workoutBullet + " **Easier** — Easy legs · bodyweight squat, glute bridge",
		"",
		workoutBullet + " **Back off** — a lighter day is always fine — gentle mobility, an easy walk, or simply rest",
		"",
		emojiProgress + " **Progress**",
		workoutBullet + " 5-day streak",
		workoutBullet + " Frequency ↗ up · 3 this week vs 2 the week before",
		workoutBullet + " 20 of the last 28 days had no logged session",
		workoutBullet + " Body: legs soreness 6",
		"",
		emojiReason + " **Why** — On the program calendar today: Legs + hips.",
	}, "\n")

	got := Render(sampleRecommendation(), sampleTrend(), renderNow())
	assert.Equal(t, want, got, "the workout scaffold renders the exact contract bytes")
	assert.Equal(t, got, Render(sampleRecommendation(), sampleTrend(), renderNow()), "Render is byte-stable across calls")
}

// TestRenderExactlyThreeOfferings proves the message always carries exactly three
// offering doors — recommended, easier, back off — never two or four.
func TestRenderExactlyThreeOfferings(t *testing.T) {
	t.Parallel()

	got := Render(sampleRecommendation(), sampleTrend(), renderNow())

	assert.Contains(t, got, "**"+labelRecommended+"**")
	assert.Contains(t, got, "**"+labelEasier+"**")
	assert.Contains(t, got, "**"+labelBackOff+"**")
	// Offering doors are the only `• **…` bold bullets; the panel bullets are plain.
	assert.Equal(t, 3, strings.Count(got, workoutBullet+" **"), "exactly three offering doors")
}

// TestRenderNoMarkdownTables proves the layout uses no markdown tables — a chat
// surface renders them as raw text.
func TestRenderNoMarkdownTables(t *testing.T) {
	t.Parallel()

	got := Render(sampleRecommendation(), sampleTrend(), renderNow())
	assert.NotContains(t, got, "|", "no markdown table pipes")
	assert.NotContains(t, got, "---", "no markdown table rule")
}

// TestRenderUnderAMinute proves the message stays inside a glanceable budget — a
// handful of short lines a reader clears in well under a minute.
func TestRenderUnderAMinute(t *testing.T) {
	t.Parallel()

	got := Render(sampleRecommendation(), sampleTrend(), renderNow())
	lines := strings.Count(got, "\n") + 1
	assert.LessOrEqual(t, lines, 30, "the message is a short glance, not a wall of text")
	assert.Less(t, len([]rune(got)), 900, "the message stays under a minute of reading")
	assert.Less(t, len(strings.Fields(got)), 140, "well under ~200 words a minute of reading allows")
}

// TestRenderVoiceGuard is the boundary guard: every rendered message — ordinary,
// pain-hard-stop, and an honest empty one — carries no coaching-imperative or
// phrase-blocklist token (product-principles.md §6).
func TestRenderVoiceGuard(t *testing.T) {
	t.Parallel()

	painRec := sampleRecommendation()
	painRec.Primary = Card{ID: "recovery", Name: "Recovery + mobility", Load: LoadNone, Movements: []string{"gentle mobility"}}
	painRec.HardStop = &SafetyOption{
		Name:      "Back off — protect legs",
		Movements: []string{"gentle mobility only, no loaded work on legs", "stop entirely if it feels sharp"},
		Reason:    "A pain signal on legs is a reason to rest it today rather than train through it.",
	}
	painRec.Reason = "A pain signal on legs means backing off today — an easy recovery session is the safe choice."

	cases := map[string]struct {
		rec Recommendation
		tr  Trend
	}{
		"ordinary":   {sampleRecommendation(), sampleTrend()},
		"pain":       {painRec, sampleTrend()},
		"emptytrend": {Recommendation{Primary: Card{Name: "Recovery + mobility", Load: LoadNone}, Reason: ""}, Trend{WindowDays: 28}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := Render(tc.rec, tc.tr, renderNow())

			assert.NotContains(t, got, "not medical advice")
			assert.False(t, safety.MatchesBlocklist(got), "the rendered message carries no phrase-blocklist token")
			lower := strings.ToLower(got)
			assert.NotContains(t, lower, "you should", "no coaching imperative")
			assert.NotContains(t, lower, "you always", "no flattening overclaim")
		})
	}
}

// TestRenderPainHardStopBecomesBackOffDoor proves that when the recommendation
// carries a pain hard stop, the third door renders that named safety option (with
// its movements), still exactly three offerings.
func TestRenderPainHardStopBecomesBackOffDoor(t *testing.T) {
	t.Parallel()

	rec := sampleRecommendation()
	rec.HardStop = &SafetyOption{
		Name:      "Back off — protect legs",
		Movements: []string{"gentle mobility only, no loaded work on legs"},
	}

	got := Render(rec, sampleTrend(), renderNow())

	assert.Contains(t, got, "**Back off** — Back off — protect legs · gentle mobility only, no loaded work on legs")
	assert.Equal(t, 3, strings.Count(got, workoutBullet+" **"), "still exactly three offering doors with a hard stop")
}

// TestRenderEmptyTrendAndReason proves the honest empty message: the reason region
// is dropped (no dangling divider), the streak frames the build, the body line is
// absent, and no horizontal divider chrome remains.
func TestRenderEmptyTrendAndReason(t *testing.T) {
	t.Parallel()

	rec := Recommendation{
		Primary:  Card{Name: "Recovery + mobility", Load: LoadNone, Movements: []string{"gentle mobility"}},
		Fallback: Card{Name: "Recovery + mobility", Load: LoadNone},
		Reason:   "",
	}
	got := Render(rec, Trend{WindowDays: 28}, renderNow())

	assert.NotContains(t, got, "**Why**", "an empty reason drops the whole region")
	assert.NotContains(t, got, "― ― ―", "no horizontal divider chrome")
	assert.Contains(t, got, "Building — no active streak yet", "a zero streak frames the build, not a hollow 0-day")
	assert.Contains(t, got, "0 of the last 28 days had no logged session")
	assert.NotContains(t, got, "Body:", "no body line when nothing is logged")
}

// TestRenderBodyResponseFormatsPainAndSoreness proves a part reporting both
// soreness and pain renders both, slash-joined.
func TestRenderBodyResponseFormatsPainAndSoreness(t *testing.T) {
	t.Parallel()

	sore, pain := 4, 7
	tr := sampleTrend()
	tr.BodyResponse = []BodySignal{{Part: "shoulder", Soreness: &sore, Pain: &pain, AsOf: "2026-07-19"}}

	got := Render(sampleRecommendation(), tr, renderNow())
	assert.Contains(t, got, "Body: shoulder soreness 4/pain 7")
}

// TestRenderFrequencyArrows proves each direction renders its glyph — the down
// arrow in particular (the golden pins up, the empty trend pins flat).
func TestRenderFrequencyArrows(t *testing.T) {
	t.Parallel()

	down := sampleTrend()
	down.Direction = DirectionDown
	down.ThisWeek, down.PriorWeek = 1, 3
	assert.Contains(t, Render(sampleRecommendation(), down, renderNow()), "Frequency ↘ down · 1 this week vs 3 the week before")

	flat := sampleTrend()
	flat.Direction = DirectionFlat
	assert.Contains(t, Render(sampleRecommendation(), flat, renderNow()), "Frequency → flat")
}

// TestRenderBodyResponseOmitsEmptySignal proves a body signal with neither
// soreness nor pain contributes no body line — the panel drops it rather than
// printing a bare part name.
func TestRenderBodyResponseOmitsEmptySignal(t *testing.T) {
	t.Parallel()

	tr := sampleTrend()
	tr.BodyResponse = []BodySignal{{Part: "legs", AsOf: "2026-07-19"}} // no soreness, no pain
	assert.NotContains(t, Render(sampleRecommendation(), tr, renderNow()), "Body:", "an empty signal renders no body line")
}

// TestCardTitleFallbacks proves the display title prefers the name, then the id,
// then a neutral recovery label — so a title-less card never renders blank.
func TestCardTitleFallbacks(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Legs + hips", cardTitle(Card{Name: "Legs + hips", ID: "legs"}))
	assert.Equal(t, "legs", cardTitle(Card{ID: "legs"}), "an unnamed card titles by id")
	assert.Equal(t, "Recovery + mobility", cardTitle(Card{}), "a bare card titles by the recovery fallback")
}
