package witnessreport

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// TestCompose_ProviderDownFallsBack: a provider timeout or unavailable backend
// yields the full deterministic report with Fallback set — only the warmth is
// lost, never the numbers, watch-outs, or asks (the companion precedent).
func TestCompose_ProviderDownFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"timeout", fmt.Errorf("backend: %w", provider.ErrTimeout)},
		{"unavailable", fmt.Errorf("backend: %w", provider.ErrUnavailable)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := composerDeps(t, "miss-heavy-week.json", "", provider.Exchange{Err: tc.err})

			r, err := c.Compose(context.Background(), reportNow())
			require.NoError(t, err, "a provider-down path is a fallback, not an error")

			assert.True(t, r.Fallback)
			assert.False(t, r.UsedLLM)
			assert.False(t, r.SafetyTripped)
			assert.Empty(t, r.Narrative, "no warmth on the fallback path")
			// The deterministic report lands whole.
			assert.Equal(t, 4, r.WeekMisses)
			assert.Equal(t, []string{
				"Missed 4 of 7 decided days this week.",
				"30-day adherence at 33% — below the 80% line.",
				"Error budget spent — 6 isolated misses against a budget of 4.",
			}, r.WatchOuts)
			assert.Equal(t, []string{askMidweekCheckIn, askChainHeld}, r.Asks)
		})
	}
}

// TestCompose_EmptyReplyFallsBack: a model reply with no usable slot content is
// treated like a provider-down path — the deterministic report lands, Fallback
// set, rather than an empty narrative being delivered.
func TestCompose_EmptyReplyFallsBack(t *testing.T) {
	c, _ := composerDeps(t, "quiet-week.json", "", provider.Exchange{Content: "   \n  \n"})

	r, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	assert.True(t, r.Fallback)
	assert.False(t, r.UsedLLM)
	assert.Empty(t, r.Narrative)
	assert.True(t, r.LowSignal, "the quiet week still posts its thin-logging watch-out")
}

// TestCompose_CuratedAsksOverride is the Q4-C guard: when the operator's curated
// asks file carries any asks, they override both the auto-drafted asks and the
// model's refined ones. Nothing is invented — the operator's own words win.
func TestCompose_CuratedAsksOverride(t *testing.T) {
	curated := "# my asks this week\n- Ask me about the writing block.\n- Nudge me to book the labs.\n"
	asksFile := writeFile(t, t.TempDir(), "asks.md", curated)
	c, _ := composerDeps(t, "miss-heavy-week.json", asksFile, provider.Exchange{Content: wellFormedReply})

	r, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	assert.True(t, r.UsedLLM, "the prose still lands even when asks are curated")
	assert.Equal(t, []string{
		"Ask me about the writing block.",
		"Nudge me to book the labs.",
	}, r.Asks, "curated asks override both the drafted and the model-refined asks")
}

// TestCompose_CuratedAsksOverrideOnFallback: the curated asks win even when the
// model is down — the deterministic fallback report carries the operator's asks,
// not the auto-drafted ones.
func TestCompose_CuratedAsksOverrideOnFallback(t *testing.T) {
	asksFile := writeFile(t, t.TempDir(), "asks.md", "- Just check in on me.\n")
	c, _ := composerDeps(t, "miss-heavy-week.json", asksFile,
		provider.Exchange{Err: fmt.Errorf("down: %w", provider.ErrUnavailable)})

	r, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)

	assert.True(t, r.Fallback)
	assert.Equal(t, []string{"Just check in on me."}, r.Asks)
}

// TestCompose_UnreadableCuratedAsksDegradesQuietly: a set-but-unreadable curated
// path never fails the report — it degrades to the auto-drafted asks so the
// witness post still lands.
func TestCompose_UnreadableCuratedAsksDegradesQuietly(t *testing.T) {
	c, _ := composerDeps(t, "miss-heavy-week.json", "/no/such/asks.md",
		provider.Exchange{Content: wellFormedReply})

	r, err := c.Compose(context.Background(), reportNow())
	require.NoError(t, err)
	// The model's refined asks apply (signal present, no curated override took).
	assert.Equal(t, []string{"Check in on me Wednesday.", "Ask how the back half went."}, r.Asks)
}
