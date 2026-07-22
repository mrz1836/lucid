package witnessreport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/notify"
)

// checkGolden compares got against the committed golden file, or rewrites the
// golden when UPDATE_GOLDEN is set (the standard regenerate-then-review loop).
// The golden files live under fixtures/ alongside the synthetic day-record
// inputs, because this repo gitignores testdata/ (the fuzz corpus) and the
// render contract's expected output must be committed. The embed goldens carry
// a `.golden` extension, not `.json`: they capture the exact bytes of Go's
// json.MarshalIndent (HTML-escaped `&`, struct-order keys, 2-space indent),
// which the repo's JSON formatter (`magex format:check`) would rewrite into its
// own canonical form — a `.json` golden would fail the byte comparison after
// every format pass. The `.golden` suffix keeps them out of that glob.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("fixtures", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoErrorf(t, err, "missing golden %s — regenerate with UPDATE_GOLDEN=1", path)
	assert.Equal(t, string(want), got, "render drifted from golden %s", name)
}

// embedJSON marshals a rendered embed to indented JSON (its Discord wire form,
// via Embed.MarshalJSON) so a golden file captures the exact bytes that would
// leave the machine.
func embedJSON(t *testing.T, e notify.Embed) string {
	t.Helper()
	b, err := json.MarshalIndent(e, "", "  ")
	require.NoError(t, err)
	return string(b)
}

// TestRender_GoldenByWeek pins the embed + markdown render of each synthetic
// week to a committed golden. These reports carry no model prose (the
// deterministic core leaves the narrative slots empty), so every rendered byte
// is deterministic — the honest numbers, the faults fallback line, the asks, and
// any watch-outs.
func TestRender_GoldenByWeek(t *testing.T) {
	for _, name := range []string{"strong-week", "quiet-week", "miss-heavy-week"} {
		t.Run(name, func(t *testing.T) {
			r, _ := buildFromFixture(t, name+".json")
			checkGolden(t, name+".embed.golden", embedJSON(t, RenderEmbed(r)))
			checkGolden(t, name+".md", RenderMarkdown(r))
		})
	}
}

// TestRenderEmbed_Deterministic: the same report renders byte-identical bytes on
// every call — the property that makes the golden test meaningful.
func TestRenderEmbed_Deterministic(t *testing.T) {
	r1, _ := buildFromFixture(t, "miss-heavy-week.json")
	r2, _ := buildFromFixture(t, "miss-heavy-week.json")
	assert.Equal(t, RenderEmbed(r1), RenderEmbed(r2))
	assert.Equal(t, RenderMarkdown(r1), RenderMarkdown(r2))
}

// TestRenderEmbed_RiskColor maps each risk band to its sidebar color: a strong
// week is green, a quiet week amber (soft watch-out), and a miss-heavy week red
// (a hard signal).
func TestRenderEmbed_RiskColor(t *testing.T) {
	strong, _ := buildFromFixture(t, "strong-week.json")
	quiet, _ := buildFromFixture(t, "quiet-week.json")
	heavy, _ := buildFromFixture(t, "miss-heavy-week.json")

	assert.Equal(t, colorClear, RenderEmbed(strong).Color, "a clean week is green")
	assert.Equal(t, colorCaution, RenderEmbed(quiet).Color, "a quiet week is amber")
	assert.Equal(t, colorRisk, RenderEmbed(heavy).Color, "a miss-heavy week is red")
}

// TestRenderEmbed_ModelProseFillsSlots: when the compose pass has filled the
// narrative slots, they land in the description (the warm read) and the Faults /
// Progress fields — while the numbers, asks, and watch-outs stay deterministic.
func TestRenderEmbed_ModelProseFillsSlots(t *testing.T) {
	r, _ := buildFromFixture(t, "miss-heavy-week.json")
	r.Narrative = "A mixed week, honestly read."
	r.Faults = "Two days slipped midweek — a wobble, not a collapse."
	r.Progress = "The back half closed strong."

	e := RenderEmbed(r)
	assert.Equal(t, "A mixed week, honestly read.", e.Description)
	assert.Equal(t, "Two days slipped midweek — a wobble, not a collapse.", fieldValue(t, e, fieldFaults))
	assert.Equal(t, "The back half closed strong.", fieldValue(t, e, fieldProgress))
	// Numbers stay deterministic — the model never owns them.
	assert.Contains(t, fieldValue(t, e, fieldStreak), "adherence")
}

// TestRenderEmbed_FallbackFaultsAndNoProgress: a deterministic report (no model
// prose) still renders a complete, non-empty card — the Faults field degrades to
// an honest numbers line and the optional Progress field is omitted, never left
// empty (Discord rejects an empty field value).
func TestRenderEmbed_FallbackFaultsAndNoProgress(t *testing.T) {
	heavy, _ := buildFromFixture(t, "miss-heavy-week.json")
	e := RenderEmbed(heavy)
	assert.Empty(t, e.Description, "no model narrative means no description")
	assert.Equal(t, "Missed 4 of 7 decided days this week.", fieldValue(t, e, fieldFaults))
	assert.False(t, hasField(e, fieldProgress), "an empty progress field is omitted, not blank")

	strong, _ := buildFromFixture(t, "strong-week.json")
	es := RenderEmbed(strong)
	assert.Equal(t, "No misses this week — the chain held.", fieldValue(t, es, fieldFaults))
	assert.False(t, hasField(es, fieldWatchOuts), "a clean week omits the watch-outs field")
	for _, f := range es.Fields {
		assert.NotEmptyf(t, f.Value, "field %q must never carry an empty value", f.Name)
	}
}

// TestRenderEmbed_RampWeek: with no decided day yet the streak and this-week
// values frame the build honestly rather than reporting a hollow 0%.
func TestRenderEmbed_RampWeek(t *testing.T) {
	r := Report{
		ISOWeek:   "2026-W28",
		Adherence: engine.Window{Length: 30, DaysAccounted: 2, Completed: 2},
		Week:      engine.Window{Length: 7, DaysAccounted: 2, Completed: 2},
		Asks:      []string{askGeneric},
	}
	e := RenderEmbed(r)
	assert.Contains(t, fieldValue(t, e, fieldStreak), "no decided day yet")
	assert.Contains(t, fieldValue(t, e, fieldThisWeek), "none decided yet")
}

// TestRenderMarkdown_WithProse: the markdown fallback mirrors the embed —
// the warm read, the optional Progress section, and the Watch-outs section all
// appear when the report carries them.
func TestRenderMarkdown_WithProse(t *testing.T) {
	r, _ := buildFromFixture(t, "miss-heavy-week.json")
	r.Narrative = "A mixed week, honestly read."
	r.Progress = "The back half held."

	md := RenderMarkdown(r)
	assert.Contains(t, md, "A mixed week, honestly read.")
	assert.Contains(t, md, "**"+fieldProgress+"**\nThe back half held.")
	assert.Contains(t, md, "**"+fieldWatchOuts+"**")
	assert.Contains(t, md, "_"+footerNote+"_")
}

// fieldValue returns the value of the named embed field, failing the test when
// it is absent.
func fieldValue(t *testing.T, e notify.Embed, name string) string {
	t.Helper()
	for _, f := range e.Fields {
		if f.Name == name {
			return f.Value
		}
	}
	t.Fatalf("embed has no field %q", name)
	return ""
}

// hasField reports whether the embed carries a field with the given name.
func hasField(e notify.Embed, name string) bool {
	for _, f := range e.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}
