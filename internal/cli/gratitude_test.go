package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// addGratitudeCLI runs `lucid gratitude add <thing>` and fails the test on error.
func addGratitudeCLI(t *testing.T, thing string) {
	t.Helper()
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", thing)
	require.NoError(t, err)
}

// TestGratitudeList: `list` renders the tally sorted by count (most-returned-to
// first) with a stable id per entry (AC-7). coffee is tallied twice and water
// once, so coffee sorts above water.
func TestGratitudeList(t *testing.T) {
	isolatedHome(t)

	addGratitudeCLI(t, "my morning coffee")
	addGratitudeCLI(t, "my morning coffee")
	addGratitudeCLI(t, "clean drinking water")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "list")
	require.NoError(t, err)

	assert.Contains(t, out, "2 gratitude entries")
	assert.Contains(t, out, "my morning coffee")
	assert.Contains(t, out, "clean drinking water")
	assert.Contains(t, out, "×2", "the repeated thing shows its running count")
	assert.Contains(t, out, "gratitude_", "each row shows its stable id")
	assert.Less(t, strings.Index(out, "my morning coffee"), strings.Index(out, "clean drinking water"),
		"count-desc: the ×2 thing sorts above the ×1 thing")
}

// TestGratitudeListJSON: `list --json` emits {count, entries:[{id,thing,count,
// first,last,aka}]} with the folded live tally (AC-9).
func TestGratitudeListJSON(t *testing.T) {
	isolatedHome(t)

	addGratitudeCLI(t, "my morning coffee")
	addGratitudeCLI(t, "my morning coffee")
	addGratitudeCLI(t, "clean drinking water")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "list", "--json")
	require.NoError(t, err)

	var payload struct {
		Count   int `json:"count"`
		Entries []struct {
			ID    string   `json:"id"`
			Thing string   `json:"thing"`
			Aka   []string `json:"aka"`
			Count int      `json:"count"`
			First string   `json:"first"`
			Last  string   `json:"last"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, 2, payload.Count)
	require.Len(t, payload.Entries, 2)
	assert.Equal(t, "my morning coffee", payload.Entries[0].Thing)
	assert.Equal(t, 2, payload.Entries[0].Count)
	assert.True(t, strings.HasPrefix(payload.Entries[0].ID, "gratitude_"), "the stable id is the entry key")
	assert.NotEmpty(t, payload.Entries[0].Last, "the folded last date is present")
	assert.NotNil(t, payload.Entries[0].Aka, "aka renders as an array, never null")
}

// TestGratitude_CLI_AddReturnsReceipt: `add` prints the receipt id in its ack.
func TestGratitude_CLI_AddReturnsReceipt(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "my morning coffee")
	require.NoError(t, err)
	assert.Contains(t, out, "grat_", "the ack carries the receipt id")
	assert.Contains(t, out, "my morning coffee")
}

// TestGratitude_CLI_AddJSON: `add --json` emits the receipt, the stable id, and
// the resulting tally.
func TestGratitude_CLI_AddJSON(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "my morning coffee", "--json")
	require.NoError(t, err)

	var payload struct {
		Receipt string `json:"receipt"`
		ID      string `json:"id"`
		Thing   string `json:"thing"`
		Count   int    `json:"count"`
		Created bool   `json:"created"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.True(t, strings.HasPrefix(payload.Receipt, "grat_"))
	assert.True(t, strings.HasPrefix(payload.ID, "gratitude_"))
	assert.NotEqual(t, payload.Receipt, payload.ID, "receipt and stable id are distinct")
	assert.Equal(t, 1, payload.Count)
	assert.True(t, payload.Created)
}

// TestGratitude_CLI_ListEmpty: `list` on a cold home is a clean hint, not a crash.
func TestGratitude_CLI_ListEmpty(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "No gratitude tallied yet")
}

// TestGratitude_CLI_DayBackdatesExplicitDate: `--day @YYYY-MM-DD` files the
// occurrence under that literal civil day, reflected in the tally's last date.
func TestGratitude_CLI_DayBackdatesExplicitDate(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"gratitude", "add", "a walk outside", "--day", "2026-06-01")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "list", "--json")
	require.NoError(t, err)

	var payload struct {
		Entries []struct {
			First string `json:"first"`
			Last  string `json:"last"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	require.Len(t, payload.Entries, 1)
	assert.Equal(t, "2026-06-01", payload.Entries[0].Last)
	assert.Equal(t, "2026-06-01", payload.Entries[0].First)
}

// TestGratitude_CLI_DayStrictRejectWritesNothing: a `--day` token the grammar
// cannot read is a clean refusal that reaches stderr and writes nothing. Execute
// renders no returned error, so the reason must reach stderr, not just the exit
// code.
func TestGratitude_CLI_DayStrictRejectWritesNothing(t *testing.T) {
	isolatedHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"},
		"gratitude", "add", "my morning coffee", "--day", "@yesterdya")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
	assert.Contains(t, err.Error(), "nothing was saved")
	assert.Empty(t, out)
	assert.Contains(t, errOut, "could not read the day", "the reason reaches the user, not just the exit code")

	listOut, _, lerr := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "list")
	require.NoError(t, lerr)
	assert.Contains(t, listOut, "No gratitude tallied yet", "a refused day writes nothing")
}
