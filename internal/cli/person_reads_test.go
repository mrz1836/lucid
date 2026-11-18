package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// person_reads_test.go covers the --json projection and the flags of the
// who-it-means-now read surfaces: `lucid person <name>` adds dormant,
// dormant_also and a candidate's dormant only when set, and `contact due --all`
// brings dormant people back, labeled. Synthetic people only: two people named
// "Sam" ("Sam" and "Sam Rivera").

// personJSON runs `lucid person <args> --json` and decodes it both into the
// view and into a generic map, so a test can check which keys are present.
func personJSON(t *testing.T, args ...string) (personView, map[string]any, string) {
	t.Helper()
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, append(append([]string{"person"}, args...), "--json")...)
	require.NoErrorf(t, err, "lucid person %s: %s", strings.Join(args, " "), stderr)
	var view personView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	return view, raw, out
}

// runOK runs one command and fails the test on any error.
func runOK(t *testing.T, args ...string) string {
	t.Helper()
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoErrorf(t, err, "lucid %s: %s", strings.Join(args, " "), stderr)
	return out
}

// TestPersonLookup_CLIDormantJSON: under --json the person view carries
// dormant: true for a dormant person, dormant_also for the dormant people who
// share the queried form, and dormant: true on a dormant §P-2 candidate — and
// none of those keys while no one is dormant, so such a Ledger projects
// exactly as before.
func TestPersonLookup_CLIDormantJSON(t *testing.T) {
	identityCLIHome(t)
	sam := identityCLIKey(t, "Sam")
	river := identityCLIKey(t, "Sam Rivera")

	_, raw, out := personJSON(t, "Sam")
	assert.NotContains(t, out, `"dormant`, "no dormant key while no one is dormant")
	assert.NotContains(t, raw, "dormant_also")

	runOK(t, "person", "dormant", sam)
	view, raw, _ := personJSON(t, "Sam")
	assert.True(t, view.Matched)
	assert.Equal(t, river, view.PersonKey, "the one active Sam by first name")
	assert.False(t, view.Dormant)
	assert.NotContains(t, raw, "dormant", "an active answer carries no dormant key")
	assert.Equal(t, []personIdentityRefView{{PersonKey: sam, DisplayName: "Sam"}}, view.DormantAlso)
	assert.Contains(t, view.Text, `"Sam" also names a dormant person: "Sam" (`+sam+`).`)

	own, raw, _ := personJSON(t, sam)
	assert.True(t, own.Dormant)
	assert.Equal(t, true, raw["dormant"])
	assert.NotContains(t, raw, "dormant_also")
	assert.Contains(t, own.Text, "\nDormant since ")

	// Three same-name records: the dormant Sam forces a second (-2), which goes
	// dormant and forces a third (-3); restoring the first leaves two active
	// exact holders beside a dormant one — a §P-2 list with the dormant last.
	runOK(t, "person", "create", "Sam")
	second := sam + "-2"
	runOK(t, "person", "dormant", second)
	runOK(t, "person", "create", "Sam")
	third := sam + "-3"
	runOK(t, "person", "dormant", sam, "--restore")

	list, raw, _ := personJSON(t, "Sam")
	require.True(t, list.MultipleMatches)
	keys := make([]string, 0, len(list.Candidates))
	flags := make([]bool, 0, len(list.Candidates))
	for _, c := range list.Candidates {
		keys = append(keys, c.PersonKey)
		flags = append(flags, c.Dormant)
	}
	assert.Equal(t, []string{sam, third, second}, keys, "active first by key, the dormant one last")
	assert.Equal(t, []bool{false, false, true}, flags)
	cands, ok := raw["candidates"].([]any)
	require.True(t, ok)
	for i, c := range cands {
		row, isMap := c.(map[string]any)
		require.True(t, isMap)
		_, has := row["dormant"]
		assert.Equal(t, i == 2, has, "only the dormant candidate carries the key (row %d)", i)
	}
	assert.True(t, strings.HasSuffix(list.Text, ", dormant)?"), list.Text)
}

// TestContactDue_ExcludesDormantUnlessAllCLI: `contact due` leaves a dormant
// person out of its groups and birthdays, and `contact due --all` (with or
// without --wrap) lists them where they fall, each line ending " (dormant)";
// under --json only their rows carry "dormant": true.
func TestContactDue_ExcludesDormantUnlessAllCLI(t *testing.T) {
	isolatedHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")
	zone := time.FixedZone("EDT", -4*3600)
	at := func(month time.Month, day int) time.Time { return time.Date(2026, month, day, 10, 0, 0, 0, zone) }

	withClock(t, at(time.October, 1))
	runOK(t, "person", "create", "Sam", "--dob", "1991-11-05")
	runOK(t, "person", "create", "Sam Rivera")
	withClock(t, at(time.October, 8))
	runOK(t, "contact", "every", "Sam", "4w")
	withClock(t, at(time.October, 20))
	runOK(t, "contact", "every", "Sam", "Rivera", "1w")
	withClock(t, at(time.November, 3))
	runOK(t, "person", "dormant", "Sam")
	withClock(t, at(time.November, 4))

	plain := runOK(t, "contact", "due")
	assert.NotContains(t, plain, "- Sam (every")
	assert.NotContains(t, plain, "Birthdays")
	assert.NotContains(t, plain, "dormant")
	assert.Contains(t, plain, "- Sam Rivera (every week), not logged yet")
	assert.NotContains(t, runOK(t, "contact", "due", "--json"), "dormant")

	all := runOK(t, "contact", "due", "--all")
	assert.Contains(t, all, "- Sam (every 4 weeks), not logged yet (dormant)")
	assert.Contains(t, all, "- Sam — Thu Nov 5 (dormant)")
	assert.Contains(t, all, "- Sam Rivera (every week), not logged yet\n")

	allJSON := runOK(t, "contact", "due", "--all", "--json")
	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(allJSON), &view))
	due, ok := view["due_this_week"].([]any)
	require.True(t, ok)
	require.Len(t, due, 1)
	assert.Equal(t, true, due[0].(map[string]any)["dormant"])
	open, ok := view["still_open"].([]any)
	require.True(t, ok)
	require.Len(t, open, 1)
	assert.NotContains(t, open[0].(map[string]any), "dormant")
	birthdays, ok := view["birthdays"].([]any)
	require.True(t, ok)
	require.Len(t, birthdays, 1)
	assert.Equal(t, true, birthdays[0].(map[string]any)["dormant"])

	wrap := runOK(t, "contact", "due", "--wrap", "--all")
	assert.Contains(t, wrap, "- Sam (every 4 weeks), not logged yet (dormant)")
	assert.NotContains(t, runOK(t, "contact", "due", "--wrap"), "- Sam (every")
}
