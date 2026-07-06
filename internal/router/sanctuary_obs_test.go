package router

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSanctuaryDenylist_NamesObservationTrees: the runtime denylist names the
// observation and registry trees, and slice construction fails closed against
// them in both directions (Sanctuary / P3). No agent may read observations/ or
// registries/ (observations-module.md §"Binding rules inherited").
func TestSanctuaryDenylist_NamesObservationTrees(t *testing.T) {
	deny := SanctuaryDenylist()
	assert.Contains(t, deny, "observations/")
	assert.Contains(t, deny, "registries/")

	for _, p := range []string{
		"observations/2026/07/obs_2026_07_02.jsonl",
		"registries/places/place_a-river.json",
		"./observations",
		"registries",
	} {
		assert.Falsef(t, PathAllowedForAgent(p), "an agent slice must never include %q", p)
	}
	// A lookalike path outside the sanctuary is still allowed.
	assert.True(t, PathAllowedForAgent("raw/2026/07/raw_2026_07_02_08_15.md"))
}

// TestAgentContracts_DenylistAndNoObservationInputs is the AC-7 doc grep: the
// agent-contracts denylist names observations/ and registries/, and no
// contract's Inputs section references those trees (they are agent-invisible).
func TestAgentContracts_DenylistAndNoObservationInputs(t *testing.T) {
	b, err := os.ReadFile("../../docs/mvp/agent-contracts.md")
	require.NoError(t, err, "agent-contracts.md must exist for the sanctuary grep")
	doc := string(b)

	assert.Contains(t, doc, "path-prefix denylist", "the denylist is named as the enforcement mechanism")
	assert.Contains(t, doc, "observations/", "the denylist names the observations tree")
	assert.Contains(t, doc, "registries/", "the denylist names the registries tree")

	// No contract's Inputs section may reference the sanctuary trees.
	lines := strings.Split(doc, "\n")
	inInputs := false
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "### ") {
			inInputs = trimmed == "### Inputs"
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			inInputs = false
			continue
		}
		if inInputs {
			assert.NotContainsf(t, ln, "observations/",
				"an agent Inputs section references observations/ (line %d) — sanctuary breach", i+1)
			assert.NotContainsf(t, ln, "registries/",
				"an agent Inputs section references registries/ (line %d) — sanctuary breach", i+1)
		}
	}
}
