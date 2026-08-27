package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEra_Registered confirms the verb is on the spine and self-documents its
// range flags.
func TestEra_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["era"], "era verb not registered")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "era")
	assert.Contains(t, out, "--start")
	assert.Contains(t, out, "--end")
}

// TestEra_CLI_CreateWithRange runs a create with a backdate-aware range through
// the CLI and confirms the --json carries the normalized bounds + precision.
func TestEra_CLI_CreateWithRange(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "the college years", "--start", "2009", "--end", "2013-05-31", "--json")
	require.NoError(t, err)

	var view registryWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "era", view.Kind)
	assert.True(t, view.Created)
	assert.Equal(t, "the college years", view.DisplayName)
	assert.Equal(t, "2009", view.Fields["start"])
	assert.Equal(t, "approximate", view.Fields["start_precision"])
	assert.Equal(t, "2013-05-31", view.Fields["end"])
}

// TestEra_CLI_AckHuman confirms the human ack path for a bare first mention.
func TestEra_CLI_AckHuman(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "the road years")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded")
	assert.Contains(t, out, "era")
}

// TestEra_CLI_RequiresName confirms a bare `era` is a usage error.
func TestEra_CLI_RequiresName(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestEra_CLI_AckEndedSpan confirms a bounded era acks its chapter span with the
// arrow separator — (start → end) — and never a status word. An era is a chapter,
// not a graded state (life-archive.md §4), so an ended chapter must not read as
// "active". Synthetic data only (external-repo firewall).
func TestEra_CLI_AckEndedSpan(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "synthetic bounded chapter", "--start", "2008-09", "--end", "2010-01")
	require.NoError(t, err)
	assert.Contains(t, out, "(2008-09 → 2010-01)")
	assert.NotContains(t, out, "active")
}

// TestEra_CLI_AckOngoing confirms an open-ended era (a start, no end) acks
// "(ongoing since <start>)" — the still-running-chapter wording (life-archive.md
// §4) — with no status word.
func TestEra_CLI_AckOngoing(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "synthetic open chapter", "--start", "2008-09")
	require.NoError(t, err)
	assert.Contains(t, out, "(ongoing since 2008-09)")
	assert.NotContains(t, out, "active")
}

// TestEra_CLI_AckDegenerate covers the two degenerate spans: an end-only era acks
// "(until <end>)", and an era with no dates at all acks with no trailing
// parenthetical (life-archive.md §4).
func TestEra_CLI_AckDegenerate(t *testing.T) {
	t.Run("end-only", func(t *testing.T) {
		isolatedHome(t)

		out, _, err := runRoot(t, BuildInfo{Version: "dev"},
			"era", "synthetic end-only chapter", "--end", "2010-01")
		require.NoError(t, err)
		assert.Contains(t, out, "(until 2010-01)")
		assert.NotContains(t, out, "active")
	})

	t.Run("no-dates", func(t *testing.T) {
		isolatedHome(t)

		out, _, err := runRoot(t, BuildInfo{Version: "dev"},
			"era", "synthetic dateless chapter")
		require.NoError(t, err)
		// No dates → no trailing parenthetical at all.
		assert.NotContains(t, out, "(")
		assert.NotContains(t, out, "active")
	})
}

// TestEra_CLI_AckNoStatusWord confirms a bounded era ack surfaces no status
// vocabulary at all — the "(active)" placeholder the old ack leaked is gone
// (life-archive.md §4).
func TestEra_CLI_AckNoStatusWord(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "synthetic status-free chapter", "--start", "2008-09", "--end", "2010-01")
	require.NoError(t, err)
	assert.NotContains(t, out, "active")
	assert.NotContains(t, out, "managed")
	assert.NotContains(t, out, "resolved")
}

// TestEra_CLI_JSONNoStatus confirms the machine surface is honest too: `era --json`
// for a bounded era carries the chapter span plus its raw start/end bounds and no
// status field — so no era output surface (human or machine) presents "active".
// Output-only (Q5=A): the stored status placeholder is untouched, merely never
// surfaced. Asserts the surfaced output only; no storage-layer assertion is needed.
func TestEra_CLI_JSONNoStatus(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "synthetic json chapter", "--start", "2008-09", "--end", "2010-01", "--json")
	require.NoError(t, err)

	var view eraWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "era", view.Kind)
	assert.Equal(t, "2008-09", view.Start)
	assert.Equal(t, "2010-01", view.End)
	assert.Equal(t, "2008-09 → 2010-01", view.Span)

	// No status field is emitted for an era, so no surface can present "active".
	assert.NotContains(t, out, "\"status\"")
	assert.NotContains(t, out, "active")
}
