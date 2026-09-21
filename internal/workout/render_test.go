package workout

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// sampleAnchor is today's floor as [BuildAnchor] projects it: three items in the
// program's own order, one of them accumulate-mode, on a week-1 program.
func sampleAnchor() Anchor {
	return Anchor{
		Week: 1,
		Items: []AnchorLine{
			{Name: "squats", Target: 50},
			{Name: "core", Target: 40},
			{Name: "easy push-ups", Target: 20, Mode: "accumulate"},
		},
	}
}

// sampleTrend is a populated insight trend: a live streak, one loaded part whose
// next-day response is rising and tracks higher load, and the concrete watch-out
// that surfaces. The retired flat fields (frequency/skipped/body) stay in the
// struct for --json but no longer render on the card.
func sampleTrend() Trend {
	return Trend{
		Streak:     5,
		WindowDays: 28,
		Sessions:   8,
		PainResponse: []PartTrend{
			{Part: "legs", Loaded: true, Direction: ResponseRising, PairedDays: 4},
		},
		LoadPattern: []PartPattern{
			{Part: "legs", Pattern: LoadPatternTracksHigher},
		},
		WatchOuts: []string{
			"legs — next-day pain/soreness rising across 4 logged days.",
		},
	}
}

// TestRenderGolden pins the exact bytes of a full ordinary-day message so the
// scaffold's structure — header, the three offerings, blank lines between
// offering bullets, the daily anchor, and the progress panel — is a hard
// contract, and asserts the render is byte-stable across repeated calls. There is
// no "Why" region on any path.
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
		emojiAnchor + " **Daily Anchor** · squats 50 · core 40 · easy push-ups 20 (accumulate) — week 1",
		"",
		emojiProgress + " **Progress**",
		workoutBullet + " 5-day streak",
		workoutBullet + " legs — next-day pain/soreness rising · tracks higher load",
		labelWatchOuts,
		workoutBullet + " legs — next-day pain/soreness rising across 4 logged days.",
	}, "\n")

	got := Render(sampleRecommendation(), sampleTrend(), sampleAnchor(), renderNow())
	assert.Equal(t, want, got, "the workout scaffold renders the exact contract bytes")
	assert.Equal(t, got, Render(sampleRecommendation(), sampleTrend(), sampleAnchor(), renderNow()), "Render is byte-stable across calls")
}

// TestRenderExactlyThreeOfferings proves the message always carries exactly three
// offering doors — recommended, easier, back off — never two or four.
func TestRenderExactlyThreeOfferings(t *testing.T) {
	t.Parallel()

	got := Render(sampleRecommendation(), sampleTrend(), sampleAnchor(), renderNow())

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

	got := Render(sampleRecommendation(), sampleTrend(), sampleAnchor(), renderNow())
	assert.NotContains(t, got, "|", "no markdown table pipes")
	assert.NotContains(t, got, "---", "no markdown table rule")
}

// TestRenderUnderAMinute proves the message stays inside a glanceable budget — a
// handful of short lines a reader clears in well under a minute.
func TestRenderUnderAMinute(t *testing.T) {
	t.Parallel()

	got := Render(sampleRecommendation(), sampleTrend(), sampleAnchor(), renderNow())
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
		rec    Recommendation
		tr     Trend
		anchor Anchor
	}{
		"ordinary":   {sampleRecommendation(), sampleTrend(), sampleAnchor()},
		"pain":       {painRec, sampleTrend(), sampleAnchor()},
		"emptytrend": {Recommendation{Primary: Card{Name: "Recovery + mobility", Load: LoadNone}, Reason: ""}, Trend{WindowDays: 28}, Anchor{Week: 1}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := Render(tc.rec, tc.tr, tc.anchor, renderNow())

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

	got := Render(rec, sampleTrend(), sampleAnchor(), renderNow())

	assert.Contains(t, got, "**Back off** — Back off — protect legs · gentle mobility only, no loaded work on legs")
	assert.Equal(t, 3, strings.Count(got, workoutBullet+" **"), "still exactly three offering doors with a hard stop")
}

// TestRenderRecoveryCardEasierDiffersFromRecommended proves the distinct-Easier
// guarantee end to end: a recovery-style card with no config easier variant still
// renders an Easier door whose text is distinct from Recommended and genuinely
// lighter, with exactly three offering doors preserved.
func TestRenderRecoveryCardEasierDiffersFromRecommended(t *testing.T) {
	t.Parallel()

	prog := recoveryOnlyProgram()
	require.NoError(t, prog.Validate())

	rec := Recommend(RecommendInput{Program: prog, Now: renderNow(), Loc: time.UTC})
	require.Equal(t, "recovery", rec.Primary.ID, "today resolves the recovery card")

	got := Render(rec, sampleTrend(), sampleAnchor(), renderNow())

	recommended := cardOffering(rec.Primary)
	easier := cardOffering(rec.Fallback)
	assert.NotEqual(t, recommended, easier, "Easier must never render identically to Recommended")
	assert.Contains(t, got, "**"+labelRecommended+"**")
	assert.Contains(t, got, "**"+labelEasier+"**")
	assert.Contains(t, got, "**"+labelBackOff+"**")
	assert.Equal(t, 3, strings.Count(got, workoutBullet+" **"), "still exactly three offering doors")
	assert.Less(t, len(rec.Fallback.Movements), len(rec.Primary.Movements),
		"the synthesized Easier is genuinely lighter than Recommended")
}

// TestRenderEmptyTrendAndAnchor proves the honest empty message: a program with
// no anchor items drops the whole anchor region (no dangling label), the streak
// frames the build, the insight panel adds no lines with nothing to read, and no
// horizontal divider chrome remains.
func TestRenderEmptyTrendAndAnchor(t *testing.T) {
	t.Parallel()

	rec := Recommendation{
		Primary:  Card{Name: "Recovery + mobility", Load: LoadNone, Movements: []string{"gentle mobility"}},
		Fallback: Card{Name: "Recovery + mobility", Load: LoadNone},
		Reason:   "",
	}
	got := Render(rec, Trend{WindowDays: 28}, Anchor{Week: 1}, renderNow())

	assert.NotContains(t, got, "Daily Anchor", "a program with no anchor items drops the whole region")
	assert.NotContains(t, got, "week 1", "the week index never renders without the items it labels")
	assert.NotContains(t, got, "― ― ―", "no horizontal divider chrome")
	assert.Contains(t, got, "Building — no active streak yet", "a zero streak frames the build, not a hollow 0-day")
	assert.NotContains(t, got, "had no logged session", "the skipped-day tally left the card")
	assert.NotContains(t, got, "Frequency", "the frequency direction left the card")
	assert.NotContains(t, got, "Watch-outs", "no watch-outs when nothing warrants one")
	assert.NotContains(t, got, "Body:", "no body line on the insight panel")
}

// TestRenderCarriesNoWhyRegion is the region-removal contract: no rendered
// message prints a "Why" line on any path — the ordinary calendar day, the
// recovery downshift (a real veto), or the pain hard stop. The deterministic
// Reason still exists on the Recommendation (it grounds the model's note and
// rides --json); it simply never renders as prose.
func TestRenderCarriesNoWhyRegion(t *testing.T) {
	t.Parallel()

	downshift := sampleRecommendation()
	downshift.Primary = Card{ID: "push", Name: "Push", Focus: []string{"chest"}, Load: LoadModerate}
	downshift.Vetoes = []string{"legs is inside its recovery window"}
	downshift.Reason = "Legs is still inside its recovery window, so today rotates to Push."

	pain := sampleRecommendation()
	pain.Primary = Card{ID: "recovery", Name: "Recovery + mobility", Load: LoadNone}
	pain.HardStop = &SafetyOption{Name: "Back off — protect legs", Movements: []string{"gentle mobility only"}}
	pain.Reason = "A pain signal on legs means backing off today."

	cases := map[string]Recommendation{
		"plain":     sampleRecommendation(),
		"downshift": downshift,
		"pain":      pain,
	}

	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := Render(rec, sampleTrend(), sampleAnchor(), renderNow())

			assert.NotContains(t, got, "**Why**", "the workout panel carries no Why region")
			assert.NotContains(t, got, rec.Reason, "the deterministic reason never renders as prose")
			require.NotEmpty(t, rec.Reason, "the Reason itself still exists for --json and model grounding")
		})
	}
}

// TestRenderAnchorFromExampleProgram proves the rendered region is the projection
// the program actually yields: on a week-2 day the synthetic program's
// targets_by_week override renders, accumulate-mode and all.
func TestRenderAnchorFromExampleProgram(t *testing.T) {
	t.Parallel()

	week2 := time.Date(2026, 1, 12, 12, 0, 0, 0, time.UTC) // day 7 — week 2 begins
	got := Render(sampleRecommendation(), sampleTrend(), BuildAnchor(ExampleProgram(), week2, time.UTC), renderNow())

	assert.Contains(t, got, emojiAnchor+" **Daily Anchor** · squats 55 · core 50 · easy push-ups 25 (accumulate) — week 2")
}

// TestRenderAnchorItemEdges proves one line never renders half-formed: an item
// with no target shows its bare name (the program simply gave no count), and an
// unnamed item is dropped rather than rendering a floating number.
func TestRenderAnchorItemEdges(t *testing.T) {
	t.Parallel()

	got := renderAnchor(Anchor{Week: 3, Items: []AnchorLine{
		{Name: "PT app", Target: 6},
		{Name: "walk"},
		{Name: "  ", Target: 99},
	}})

	assert.Equal(t, emojiAnchor+" **Daily Anchor** · PT app 6 · walk — week 3", got)
}

// TestRenderAnchorUnits proves the non-rep anchor forms render cleanly — a
// sets×hold, a standalone hold, a target with a unit, and a bare set count —
// while a plain rep count is unchanged, and none of the old dangling-number
// artifacts (`x45s 5`, `(min) 5`, a bare `2`) survive. The `(accumulate)` mode
// marker still trails the resolved quantity.
func TestRenderAnchorUnits(t *testing.T) {
	t.Parallel()

	got := renderAnchor(Anchor{Week: 1, Items: []AnchorLine{
		{Name: "wall sit", Sets: 5, HoldSeconds: 45},
		{Name: "balance hold", HoldSeconds: 45},
		{Name: "breathing", Target: 5, Unit: "min"},
		{Name: "shoulder band", Sets: 2},
		{Name: "mobility", HoldSeconds: 30, Mode: "accumulate"},
		{Name: "PT app", Target: 6},
	}})

	want := emojiAnchor + " **Daily Anchor** · wall sit 5x45s · balance hold 45s · " +
		"breathing 5 min · shoulder band 2 sets · mobility 30s (accumulate) · PT app 6 — week 1"
	assert.Equal(t, want, got, "each non-rep form renders in the documented shape")
	assert.Contains(t, got, "wall sit 5x45s")
	assert.Contains(t, got, "balance hold 45s")
	assert.Contains(t, got, "breathing 5 min")
	assert.Contains(t, got, "shoulder band 2 sets")
	assert.Contains(t, got, "PT app 6", "a plain rep count still renders as a bare number")
	assert.NotContains(t, got, "x45s 5", "no sets×hold dangling-rep artifact")
	assert.NotContains(t, got, "(min)", "a unit is never rendered as a mode-style parenthesis")
}

// TestRenderAnchorDropsAllBlankItems proves a region whose every item is unnamed
// renders nothing at all rather than a header with an empty list.
func TestRenderAnchorDropsAllBlankItems(t *testing.T) {
	t.Parallel()

	assert.Empty(t, renderAnchor(Anchor{Week: 2, Items: []AnchorLine{{Target: 50}}}))
	assert.Empty(t, renderAnchor(Anchor{Week: 2}), "no items renders no region")
}

// TestRenderProgressInsightPanel proves the panel is the insight surface, not the
// old flat dashboard: it keeps the one slim streak line and the per-part next-day
// pain-response signal with its load-vs-pain pattern, and it no longer prints the
// frequency direction, the skipped-day tally, or the flat "Body:" line.
func TestRenderProgressInsightPanel(t *testing.T) {
	t.Parallel()

	got := Render(sampleRecommendation(), sampleTrend(), sampleAnchor(), renderNow())

	assert.Contains(t, got, workoutBullet+" 5-day streak", "the one slim streak line stays")
	assert.Contains(t, got, "legs — next-day pain/soreness rising", "the per-part next-day trend renders")
	assert.Contains(t, got, "tracks higher load", "the load-vs-pain pattern renders alongside it")
	assert.NotContains(t, got, "Frequency", "the frequency direction left the card")
	assert.NotContains(t, got, "had no logged session", "the skipped-day tally left the card")
	assert.NotContains(t, got, "Body:", "the flat body line left the card")
}

// TestRenderProgressInsufficientData proves the card never invents a direction
// from too-few pairs and never prints a per-part zero line: a part with no paired
// days yet is dropped entirely, and parts that have begun accumulating pairs fold
// into one compact "still building" line — nearest first, the rest rolled into
// "+N more" — instead of one identical noise line each.
func TestRenderProgressInsufficientData(t *testing.T) {
	t.Parallel()

	tr := sampleTrend()
	tr.PainResponse = []PartTrend{
		{Part: "knee", Loaded: true, PairedDays: 2, Insufficient: true},
		{Part: "core", Loaded: true, PairedDays: 1, Insufficient: true},
		{Part: "shoulder", Loaded: true, PairedDays: 0, Insufficient: true},
	}
	tr.LoadPattern = nil
	tr.WatchOuts = nil

	got := Render(sampleRecommendation(), tr, sampleAnchor(), renderNow())
	assert.Contains(t, got, "Next-day reads still building · knee 2/3, core 1/3",
		"partial parts fold into one compact line, nearest first")
	assert.NotContains(t, got, "shoulder", "a part with no paired days yet takes no line")
	assert.NotContains(t, got, "logged days, reading builds with more",
		"the per-part zero-line noise is gone")
	assert.NotContains(t, got, "no tracked increase", "an insufficient part shows no load-pattern claim")
}

// TestRenderProgressBuildingSummaryCaps proves the still-building summary names at
// most maxBuildingParts parts and rolls the remainder into a "+N more" so the line
// stays one glance of signal, never the per-part wall it replaced.
func TestRenderProgressBuildingSummaryCaps(t *testing.T) {
	t.Parallel()

	tr := sampleTrend()
	tr.PainResponse = []PartTrend{
		{Part: "abs", Loaded: true, PairedDays: 2, Insufficient: true},
		{Part: "back", Loaded: true, PairedDays: 2, Insufficient: true},
		{Part: "core", Loaded: true, PairedDays: 1, Insufficient: true},
		{Part: "legs", Loaded: true, PairedDays: 1, Insufficient: true},
		{Part: "shoulder", Loaded: true, PairedDays: 1, Insufficient: true},
	}
	tr.LoadPattern = nil
	tr.WatchOuts = nil

	got := Render(sampleRecommendation(), tr, sampleAnchor(), renderNow())
	assert.Contains(t, got, "Next-day reads still building · abs 2/3, back 2/3, core 1/3, +2 more",
		"the summary caps at maxBuildingParts and rolls the rest into +N more")
}

// TestRenderProgressCheckpointScaffold proves the checkpoint scaffold renders only
// when a qualifying session supplies one, as relative-time marks (right after /
// ~12h / ~24h) computed off the logged session — never a fixed calendar day, never
// a done/pending tracker.
func TestRenderProgressCheckpointScaffold(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, Render(sampleRecommendation(), sampleTrend(), sampleAnchor(), renderNow()),
		"right after", "no scaffold without a qualifying session")

	session := mustTime(t, "2026-07-19T18:30:00Z")
	tr := sampleTrend()
	tr.Checkpoints = &CheckpointScaffold{
		Subject:   "the loaded area",
		Label:     "Post-workout check-in",
		Copy:      "note how it feels",
		SessionAt: session,
		Checkpoints: []Checkpoint{
			{OffsetHours: 0, At: session},
			{OffsetHours: 12, At: session.Add(12 * time.Hour)},
			{OffsetHours: 24, At: session.Add(24 * time.Hour)},
		},
	}

	got := Render(sampleRecommendation(), tr, sampleAnchor(), renderNow())
	assert.Contains(t, got,
		workoutBullet+" Post-workout check-in · the loaded area — right after / ~12h / ~24h (note how it feels)",
		"the scaffold renders relative-time marks off the logged session")
}

// TestRenderProgressWatchOuts proves the watch-outs render under the ⚠️ Watch-outs
// label as plain bullets, and the label is absent when nothing warrants a
// watch-out.
func TestRenderProgressWatchOuts(t *testing.T) {
	t.Parallel()

	got := Render(sampleRecommendation(), sampleTrend(), sampleAnchor(), renderNow())
	assert.Contains(t, got, labelWatchOuts)
	assert.Contains(t, got, workoutBullet+" legs — next-day pain/soreness rising across 4 logged days.")

	quiet := sampleTrend()
	quiet.WatchOuts = nil
	assert.NotContains(t, Render(sampleRecommendation(), quiet, sampleAnchor(), renderNow()), "Watch-outs",
		"a clean read shows no watch-out label")
}

// TestCardTitleFallbacks proves the display title prefers the name, then the id,
// then a neutral recovery label — so a title-less card never renders blank.
func TestCardTitleFallbacks(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Legs + hips", cardTitle(Card{Name: "Legs + hips", ID: "legs"}))
	assert.Equal(t, "legs", cardTitle(Card{ID: "legs"}), "an unnamed card titles by id")
	assert.Equal(t, "Recovery + mobility", cardTitle(Card{}), "a bare card titles by the recovery fallback")
}
