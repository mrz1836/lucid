package companion

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// fixedDate is a stable Monday (2026-07-20) so the header weekday is
// deterministic across runs.
func fixedDate() time.Time {
	return time.Date(2026, 7, 20, 6, 0, 0, 0, time.UTC)
}

// sampleMorningBriefing is a fully-populated morning Briefing used by the golden
// test: a status panel, one context section with a freshness stamp, and both
// model slots filled.
func sampleMorningBriefing() Briefing {
	return Briefing{
		Mode: ModeMorning,
		Date: fixedDate(),
		Panel: []string{
			"⛓️ 5-day streak · 83% adherence (20/24 decided)",
			"📊 Error budget · 2/3 isolated misses left · 4d to gate",
		},
		Sections: []Section{
			{
				Emoji: "🫀",
				Label: "Body & state",
				Meta:  "as logged 2026-07-19",
				Lines: []string{"mood 7 — steady", "sleep 6.5h"},
			},
		},
		Interpretation: "Steady week. The streak holds; adherence is comfortably above floor.",
		Actions:        []string{"Run the morning chain before email.", "Log a mood check at noon."},
	}
}

// TestRender_MorningGolden pins the exact bytes of a full morning message so the
// scaffold's structure — header, panel, section, `•` bullets, and the
// read slot in the forward-looking order — is a hard contract, and asserts the
// render is byte-stable across repeated calls.
func TestRender_MorningGolden(t *testing.T) {
	want := strings.Join([]string{
		"☀️ **Morning** · Monday, Jul 20",
		"",
		"⛓️ 5-day streak · 83% adherence (20/24 decided)",
		"📊 Error budget · 2/3 isolated misses left · 4d to gate",
		"",
		"🫀 **Body & state** · as logged 2026-07-19",
		"• mood 7 — steady",
		"• sleep 6.5h",
		"",
		"🧭 **The read**",
		"Steady week. The streak holds; adherence is comfortably above floor.",
		"",
		"🌅 **Morning routine**",
		"• Run the morning chain before email.",
		"• Log a mood check at noon.",
	}, "\n")

	got := Render(sampleMorningBriefing())
	assert.Equal(t, want, got, "morning scaffold renders the exact contract bytes")
	assert.Equal(t, got, Render(sampleMorningBriefing()), "Render is byte-stable across calls")
}

// TestRender_NightOrdering confirms the night window is a distinct close-out
// ritual: the day read-back (sections) leads before the status panel, the
// interpretation slot is suppressed, and the action uses close-out framing —
// never the morning ordering or labels.
func TestRender_NightOrdering(t *testing.T) {
	b := sampleMorningBriefing()
	b.Mode = ModeNight

	got := Render(b)

	assert.True(t, strings.HasPrefix(got, "🌙 **Night** · Monday, Jul 20"), "night header leads")
	assert.NotContains(t, got, "🕯️ **Examen**", "night suppresses the separate examen/read section")
	assert.NotContains(t, got, "Steady week. The streak holds", "night does not render the interpretation slot")
	assert.Contains(t, got, "🌒 **Close-out**", "night action is the close-out")
	assert.NotContains(t, got, "🧭 **The read**", "night does not use the morning read header")
	assert.NotContains(t, got, "▶️ **Next**", "night does not use the morning next header")
	assert.NotContains(t, got, "― ― ―", "night omits divider lines for a compact close-out")

	// Close-out ordering: the read-back section comes before the numbers panel,
	// the reverse of the morning hero-first order.
	sectionIdx := strings.Index(got, "🫀 **Body & state**")
	panelIdx := strings.Index(got, "⛓️ 5-day streak")
	require.Positive(t, sectionIdx)
	require.Positive(t, panelIdx)
	assert.Less(t, sectionIdx, panelIdx, "night leads with the day read-back, then the panel")

	// The morning of the same briefing puts the panel first — proving the
	// ordering genuinely differs by window.
	morning := Render(sampleMorningBriefing())
	mSection := strings.Index(morning, "🫀 **Body & state**")
	mPanel := strings.Index(morning, "⛓️ 5-day streak")
	assert.Less(t, mPanel, mSection, "morning leads with the panel, then the context")
}

// TestRender_OmitsEmptySections drops a section with no bullet lines and leaves
// no dangling structural chrome when a whole region is empty.
func TestRender_OmitsEmptySections(t *testing.T) {
	b := sampleMorningBriefing()
	b.Sections = []Section{
		{Emoji: "🫀", Label: "Body & state", Meta: "as logged 2026-07-19", Lines: []string{"mood 7"}},
		{Emoji: "📌", Label: "Commitments", Meta: "as logged 2026-07-10", Lines: nil}, // empty → omitted
	}
	got := Render(b)
	assert.Contains(t, got, "🫀 **Body & state**")
	assert.NotContains(t, got, "Commitments", "an empty section leaves no stray header")

	// A briefing with no sections at all still has only blank-line separators —
	// never horizontal divider chrome.
	noSections := sampleMorningBriefing()
	noSections.Sections = nil
	out := Render(noSections)
	assert.NotContains(t, out, "― ― ―", "morning omits divider lines entirely")
}

// TestRender_OmitsEmptySlots renders cleanly when the model slots are empty —
// no interpretation header without prose, no next header without actions.
func TestRender_OmitsEmptySlots(t *testing.T) {
	b := sampleMorningBriefing()
	b.Interpretation = ""
	b.Actions = nil
	got := Render(b)
	assert.NotContains(t, got, "🧭 **The read**", "no interpretation header without prose")
	assert.NotContains(t, got, "▶️ **Next**", "morning has no generic next header")
	assert.Contains(t, got, "⛓️ 5-day streak", "the deterministic panel still renders")
}

// TestRender_VerdictIsLastGroup confirms the Engine verdict, when present, is
// appended as the final group after everything else.
func TestRender_VerdictIsLastGroup(t *testing.T) {
	b := sampleMorningBriefing()
	b.Verdict = "last night was a miss. Tonight is a must."
	got := Render(b)
	assert.True(t, strings.HasSuffix(got, "\n\nlast night was a miss. Tonight is a must."),
		"the verdict is the last group, blank-line-separated")
}

// TestBuildStatusPanel_CompactDecidedDay: a decided-day panel is the compact
// streak+adherence and error-budget pair — never the full metrics dump (no
// per-anchor days-since, no longer gate rollups).
func TestBuildStatusPanel_CompactDecidedDay(t *testing.T) {
	m := engine.Metrics{
		CurrentStreak: 5,
		LongestStreak: 12,
		Adherence:     engine.Window{Length: 30, Adherence: 0.83, Completed: 20, Decided: 24, DaysAccounted: 26},
		ErrorBudget:   engine.ErrorBudget{Budget: 3, Burn: 1, Remaining: 2},
	}
	gate := 4
	st := engine.Status{DaysToNextGate: &gate, StormState: engine.StormNone}

	panel := buildStatusPanel(m, st)
	require.LessOrEqual(t, len(panel), 4, "the panel is compact — at most four lines")
	require.Len(t, panel, 2, "a clean decided day is exactly the streak + budget pair")

	joined := strings.Join(panel, "\n")
	assert.Contains(t, joined, "5-day streak")
	assert.Contains(t, joined, "83% adherence (20/24 decided)")
	assert.Contains(t, joined, "Error budget · 2/3 isolated misses left")
	assert.Contains(t, joined, "4d to gate")
	assert.NotContains(t, joined, "Days since", "the panel is not the full metrics dump")
	assert.NotContains(t, joined, "60-day", "no longer gate rollups in the panel")
	assert.NotContains(t, joined, "longest", "no longest-streak co-number in the compact panel")
}

// TestBuildStatusPanel_EarlyRampBuilds frames the build (no hollow 0%) before
// any day is decided.
func TestBuildStatusPanel_EarlyRampBuilds(t *testing.T) {
	m := engine.Metrics{
		Adherence:   engine.Window{Length: 30, Completed: 2, Decided: 0, DaysAccounted: 3},
		ErrorBudget: engine.ErrorBudget{Budget: 3, Remaining: 3},
	}
	panel := buildStatusPanel(m, engine.Status{})
	joined := strings.Join(panel, "\n")
	assert.Contains(t, joined, "Building · 2 completed of 3 accounted — no decided day yet")
	assert.NotContains(t, joined, "0% adherence", "no hollow percentage before a decided day")
	assert.NotContains(t, joined, "streak ·", "no streak framing before a decided day")
}

// TestBuildStatusPanel_AmbientOnlyWhenHold surfaces the consecutive-miss and
// standing-storm lines only when they hold, and the over-budget note only when
// the budget is exceeded — a clean day carries none of them.
func TestBuildStatusPanel_AmbientOnlyWhenHold(t *testing.T) {
	m := engine.Metrics{
		CurrentStreak: 0,
		Adherence:     engine.Window{Length: 30, Adherence: 0.4, Completed: 8, Decided: 20, DaysAccounted: 22},
		ErrorBudget:   engine.ErrorBudget{Budget: 3, Burn: 5, Remaining: 0, Exceeded: true},
	}
	st := engine.Status{ConsecutiveMisses: 2, StormState: engine.StormStandingState}

	panel := buildStatusPanel(m, st)
	require.Len(t, panel, 4, "streak+adherence, budget, misses, storm — all four hold")
	require.LessOrEqual(t, len(panel), 4, "still capped at four lines when everything holds")

	joined := strings.Join(panel, "\n")
	assert.Contains(t, joined, "Consecutive misses · 2")
	assert.Contains(t, joined, "Storm standing")
	assert.Contains(t, joined, "(over — gates hold)")

	// A clean day drops all three ambient signals.
	clean := buildStatusPanel(m, engine.Status{StormState: engine.StormNone})
	cleanJoined := strings.Join(clean, "\n")
	assert.NotContains(t, cleanJoined, "Consecutive misses")
	assert.NotContains(t, cleanJoined, "Storm standing")
}

// TestFreshnessStamp covers the "as logged <date>" format, the stale flag past
// the threshold, and the graceful degrade of an unparseable date.
func TestFreshnessStamp(t *testing.T) {
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)

	// One day old: fresh, no flag.
	fresh := freshnessStamp(observations.Event{LogicalDate: "2026-07-19"}, now)
	assert.Equal(t, "as logged 2026-07-19", fresh)

	// Exactly at the threshold (2 days): still fresh — the flag is strictly older.
	boundary := freshnessStamp(observations.Event{LogicalDate: "2026-07-18"}, now)
	assert.Equal(t, "as logged 2026-07-18", boundary, "the threshold day is not yet stale")

	// Past the threshold: stale flag appended.
	stale := freshnessStamp(observations.Event{LogicalDate: "2026-07-17"}, now)
	assert.Equal(t, "as logged 2026-07-17 · stale", stale)

	// Unparseable date: the stamp renders as-is with no stale flag (no crying wolf).
	bad := freshnessStamp(observations.Event{LogicalDate: "not-a-date"}, now)
	assert.Equal(t, "as logged not-a-date", bad)
}

// TestParseSlots_Success parses both slots, stripping the action bullet markers.
func TestParseSlots_Success(t *testing.T) {
	resp := strings.Join([]string{
		"ignored preamble",
		"%%INTERPRETATION%%",
		"You held the line today.",
		"Momentum is real.",
		"%%ACTIONS%%",
		"- Journal before bed.",
		"• Set out tomorrow's clothes.",
		"",
	}, "\n")

	interp, actions, ok := parseSlots(resp)
	assert.True(t, ok)
	assert.Equal(t, "You held the line today.\nMomentum is real.", interp)
	assert.Equal(t, []string{"Journal before bed.", "Set out tomorrow's clothes."}, actions)
}

// TestParseSlots_MissingDelimiter falls back to the whole trimmed reply as the
// interpretation with no actions — plain prose is a valid scaffold, not a
// failure.
func TestParseSlots_MissingDelimiter(t *testing.T) {
	resp := "  Just a warm paragraph with no delimiters at all.  "
	interp, actions, ok := parseSlots(resp)
	assert.False(t, ok, "no interpretation delimiter is the fallback path")
	assert.Equal(t, "Just a warm paragraph with no delimiters at all.", interp)
	assert.Nil(t, actions)
}

// TestParseSlots_InterpretationOnly returns the interpretation and no actions
// when only the first delimiter is present.
func TestParseSlots_InterpretationOnly(t *testing.T) {
	resp := "%%INTERPRETATION%%\nA quiet, steady day.\n"
	interp, actions, ok := parseSlots(resp)
	assert.True(t, ok)
	assert.Equal(t, "A quiet, steady day.", interp)
	assert.Empty(t, actions)
}

// TestParseSlots_StrayActionsBeforeInterp ignores an %%ACTIONS%% delimiter that
// appears before the interpretation one, folding it into the interpretation
// body rather than mis-parsing actions.
func TestParseSlots_StrayActionsBeforeInterp(t *testing.T) {
	resp := "%%ACTIONS%%\n- stray\n%%INTERPRETATION%%\nThe real read.\n"
	interp, actions, ok := parseSlots(resp)
	assert.True(t, ok)
	assert.Equal(t, "The real read.", interp)
	assert.Empty(t, actions, "a stray actions delimiter before the interpretation yields no actions")
}
