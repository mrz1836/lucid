package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eraRegistrySnapshot captures the on-disk eras registry as a filename→content
// map so a test can assert a path wrote nothing (byte-unchanged before/after) —
// the strict-creation contract (life-archive.md §4). A missing eras directory is
// an empty snapshot, not an error.
func eraRegistrySnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	dir := filepath.Join(home, "registries", "eras")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]string{}
	}
	require.NoError(t, err)
	snap := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, rerr)
		snap[e.Name()] = string(b)
	}
	return snap
}

// TestEra_Registered confirms the verb is on the spine, self-documents its
// subcommands, and that the write subcommands surface the range flags.
func TestEra_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["era"], "era verb not registered")

	// The parent self-documents its list/create/amend subcommands.
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "list")
	assert.Contains(t, out, "create")
	assert.Contains(t, out, "amend")

	// The mint path self-documents the range flags.
	createHelp, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "create", "--help")
	require.NoError(t, err)
	assert.Contains(t, createHelp, "--start")
	assert.Contains(t, createHelp, "--end")
}

// TestEra_CLI_CreateWithRange runs a create with a backdate-aware range through
// the CLI and confirms the --json carries the normalized bounds + precision.
func TestEra_CLI_CreateWithRange(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "create", "the college years", "--start", "2009", "--end", "2013-05-31", "--json")
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

// TestEra_CLI_AckHuman confirms the human ack path for a bare first mention
// (through the mint path).
func TestEra_CLI_AckHuman(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "create", "the road years")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded")
	assert.Contains(t, out, "era")
}

// TestEra_CLI_RequiresName confirms a bare `era` (no name, no subcommand) is a
// usage error.
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
		"era", "create", "synthetic bounded chapter", "--start", "2008-09", "--end", "2010-01")
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
		"era", "create", "synthetic open chapter", "--start", "2008-09")
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
			"era", "create", "synthetic end-only chapter", "--end", "2010-01")
		require.NoError(t, err)
		assert.Contains(t, out, "(until 2010-01)")
		assert.NotContains(t, out, "active")
	})

	t.Run("no-dates", func(t *testing.T) {
		isolatedHome(t)

		out, _, err := runRoot(t, BuildInfo{Version: "dev"},
			"era", "create", "synthetic dateless chapter")
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
		"era", "create", "synthetic status-free chapter", "--start", "2008-09", "--end", "2010-01")
	require.NoError(t, err)
	assert.NotContains(t, out, "active")
	assert.NotContains(t, out, "managed")
	assert.NotContains(t, out, "resolved")
}

// TestEra_CLI_JSONNoStatus confirms the machine surface is honest too: `era create
// --json` for a bounded era carries the chapter span plus its raw start/end bounds
// and no status field — so no era output surface (human or machine) presents
// "active". Output-only (Q5=A): the stored status placeholder is untouched, merely
// never surfaced. Asserts the surfaced output only; no storage-layer assertion is
// needed.
func TestEra_CLI_JSONNoStatus(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "create", "synthetic json chapter", "--start", "2008-09", "--end", "2010-01", "--json")
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

// TestEraCreate_Mints confirms `era create` is the mint path — it records a new
// chapter and writes exactly one era registry file.
func TestEraCreate_Mints(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "create", "synthetic mint chapter", "--start", "2019")
	require.NoError(t, err)
	assert.Contains(t, out, "Recorded")

	assert.Len(t, eraRegistrySnapshot(t, home), 1, "era create mints exactly one era")
}

// TestEraList_ReadOnly confirms `era list` enumerates existing chapters and
// writes nothing — the eras registry is byte-unchanged across the read (the
// read-only discovery contract, life-archive.md §4).
func TestEraList_ReadOnly(t *testing.T) {
	home := isolatedHome(t)

	// Seed two synthetic chapters through the mint path.
	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "create", "synthetic coast years", "--start", "2010", "--end", "2014")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"},
		"era", "create", "synthetic desert years", "--start", "2015")
	require.NoError(t, err)

	before := eraRegistrySnapshot(t, home)
	require.Len(t, before, 2)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "synthetic coast years")
	assert.Contains(t, out, "synthetic desert years")
	assert.Contains(t, out, "(2010 → 2014)")
	assert.Contains(t, out, "(ongoing since 2015)")

	// The read created nothing and mutated nothing.
	assert.Equal(t, before, eraRegistrySnapshot(t, home), "era list must not write")
}

// TestEraList_EmptyAndJSON confirms the honest-empty human copy on a thin store,
// that the read leaves no eras registry behind, and that the --json envelope
// carries the raw bounds + span for each row.
func TestEraList_EmptyAndJSON(t *testing.T) {
	home := isolatedHome(t)

	// Empty store: honest empty, and the read writes no eras registry.
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "No eras recorded yet.")
	assert.Empty(t, eraRegistrySnapshot(t, home))

	// The JSON envelope is present and empty on a thin store.
	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "era", "list", "--json")
	require.NoError(t, err)
	var view eraListView
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &view))
	assert.Empty(t, view.Eras)

	// Seed one and confirm the JSON row carries the raw bounds + span.
	_, _, err = runRoot(t, BuildInfo{Version: "dev"},
		"era", "create", "synthetic bounded chapter", "--start", "2008-09", "--end", "2010-01")
	require.NoError(t, err)

	jsonOut, _, err = runRoot(t, BuildInfo{Version: "dev"}, "era", "list", "--json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &view))
	require.Len(t, view.Eras, 1)
	assert.Equal(t, "synthetic bounded chapter", view.Eras[0].DisplayName)
	assert.Equal(t, "2008-09", view.Eras[0].Start)
	assert.Equal(t, "2010-01", view.Eras[0].End)
	assert.Equal(t, "2008-09 → 2010-01", view.Eras[0].Span)
}

// TestEraAmend_NonMatchingHardErrors proves the strict-creation gate: `era amend`
// AND the bare `era <name>` alias hard-error on a name that matches no existing
// chapter, point at `era create`, and leave the eras registry byte-unchanged —
// only `era create` mints (life-archive.md §4).
func TestEraAmend_NonMatchingHardErrors(t *testing.T) {
	home := isolatedHome(t)

	// Seed one real chapter so the eras registry exists to be compared against.
	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"era", "create", "synthetic anchor chapter", "--start", "2010")
	require.NoError(t, err)
	before := eraRegistrySnapshot(t, home)
	require.Len(t, before, 1)

	for _, args := range [][]string{
		{"era", "amend", "synthetic ghost chapter"},
		{"era", "synthetic ghost chapter"}, // the bare amend-only alias
	} {
		_, errOut, runErr := runRoot(t, BuildInfo{Version: "dev"}, args...)
		require.Errorf(t, runErr, "a non-matching amend must hard-error: %v", args)
		assert.Contains(t, errOut, "no era named", args)
		assert.Contains(t, errOut, "era create", args)
	}

	// Nothing was written — the eras registry is byte-unchanged.
	assert.Equal(t, before, eraRegistrySnapshot(t, home), "a rejected amend must write nothing")
}

// TestEraReservedNames proves reserved/flag-shaped words can never be minted or
// amended into a chapter (life-archive.md §4): `era create list`, `era amend ls`,
// and a leading-dash name are rejected, and nothing is written.
func TestEraReservedNames(t *testing.T) {
	home := isolatedHome(t)

	rejected := [][]string{
		{"era", "create", "list"},
		{"era", "amend", "ls"},
		{"era", "create", "show"},
		{"era", "create", "help"},
		{"era", "create", "--", "-1994"}, // a leading-dash name reaches the guard
		{"era", "create", "-x"},          // a shorthand-looking token: flag parsing rejects it
	}
	for _, args := range rejected {
		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
		require.Errorf(t, err, "reserved/flag-shaped name must be rejected: %v", args)
	}

	// None of them wrote an era.
	assert.Empty(t, eraRegistrySnapshot(t, home), "a rejected name must write nothing")
}
