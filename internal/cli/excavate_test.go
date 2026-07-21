package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExcavate_Registered confirms the read-only selection verb is on the spine
// and self-documents in --help.
func TestExcavate_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["excavate"], "excavate verb not registered")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "excavate", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "excavate")
	assert.Contains(t, out, "Read-only", "the help names it read-only")
}

// TestExcavate_EmptyStore prints the calm fallback when there is nothing to
// excavate yet — an honest empty result, no model call.
func TestExcavate_EmptyStore(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "excavate")
	require.NoError(t, err)
	assert.Contains(t, out, "Nothing to excavate")
}

// TestExcavate_ThinInjuryJSON proves the injury track selects a bare injury and
// emits its gaps + generic prompts in the stable --json shape.
func TestExcavate_ThinInjuryJSON(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "injury", "left knee")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "excavate", "--json")
	require.NoError(t, err)

	var view excavateView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.True(t, view.Found)
	assert.Equal(t, "injury", view.Track)
	assert.Equal(t, "left knee", view.DisplayName)
	assert.Len(t, view.Gaps, 9, "a bare injury is missing every convention field")
	assert.Len(t, view.Prompts, 10, "a lead-in plus one prompt per gap")
	assert.NotEmpty(t, view.Key)
}

// TestExcavate_StoryTrackText proves the story track is selected from an era when
// no injury has gaps, and renders Discord-friendly text (no markdown table).
func TestExcavate_StoryTrackText(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "wild summer", "--start", "2010-06-01")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "excavate")
	require.NoError(t, err)
	assert.Contains(t, out, "story track")
	assert.Contains(t, out, "wild summer")
	assert.Contains(t, out, "•", "prompts render as bullets, not a table")
	assert.NotContains(t, out, "|", "no markdown table in Discord output")
}

// TestExcavate_ReadOnly proves the surface writes nothing: the Ledger file count
// is identical before and after a run over a seeded store.
func TestExcavate_ReadOnly(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "injury", "left knee")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "era", "wild summer")
	require.NoError(t, err)

	before := countHomeFiles(t, home, "")
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "excavate")
	require.NoError(t, err)
	assert.Equal(t, before, countHomeFiles(t, home, ""), "excavate writes nothing")
}
