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

// gratitudeListEntries runs `gratitude list --json` and returns the folded rows.
func gratitudeListEntries(t *testing.T) []struct {
	ID    string   `json:"id"`
	Thing string   `json:"thing"`
	Aka   []string `json:"aka"`
	Count int      `json:"count"`
	First string   `json:"first"`
	Last  string   `json:"last"`
} {
	t.Helper()
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
	return payload.Entries
}

// TestGratitude_CLI_AddInto: `add --into <id>` bumps a specific entry by its
// stable id regardless of wording, keeping the canonical display and folding the
// new wording into aka[] (AC-5). An unknown id is a clean error.
func TestGratitude_CLI_AddInto(t *testing.T) {
	isolatedHome(t)

	addGratitudeCLI(t, "a roof over my head")
	entries := gratitudeListEntries(t)
	require.Len(t, entries, 1)
	id := entries[0].ID

	bumpOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "my house", "--into", id)
	require.NoError(t, err)
	assert.Contains(t, bumpOut, "×2", "the targeted entry's count bumps")

	entries = gratitudeListEntries(t)
	require.Len(t, entries, 1, "the differently-worded bump created no new entry")
	assert.Equal(t, "a roof over my head", entries[0].Thing, "the canonical wording is kept")
	assert.Equal(t, 2, entries[0].Count)
	assert.Contains(t, entries[0].Aka, "my house", "tonight's wording joins aka[]")

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "my house", "--into", "gratitude_nope")
	require.Error(t, err, "an --into id naming no live entry is a clean error")
}

// TestGratitude_CLI_Merge: `merge <src> <dst>` folds a duplicate into the
// canonical entry and drops the source from the active tally (AC-6).
func TestGratitude_CLI_Merge(t *testing.T) {
	isolatedHome(t)

	addGratitudeCLI(t, "a roof over my head")
	addGratitudeCLI(t, "my house")

	entries := gratitudeListEntries(t)
	require.Len(t, entries, 2)
	ids := map[string]string{}
	for _, e := range entries {
		ids[e.Thing] = e.ID
	}
	src, dst := ids["my house"], ids["a roof over my head"]
	require.NotEmpty(t, src)
	require.NotEmpty(t, dst)

	mergeOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "merge", src, dst)
	require.NoError(t, err)
	assert.Contains(t, mergeOut, "Merged")
	assert.Contains(t, mergeOut, "grat_", "the merge prints its receipt id")

	entries = gratitudeListEntries(t)
	require.Len(t, entries, 1, "the merged-away source is omitted from the active tally")
	assert.Equal(t, dst, entries[0].ID)
	assert.Equal(t, 2, entries[0].Count, "the target absorbs the source's count")
}

// TestGratitude_CLI_Import: `import <thing> --count N --first --last` seeds a
// pre-counted row faithfully (AC-10). The equivalent `add --count …` alias
// routes to the same seed path.
func TestGratitude_CLI_Import(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"gratitude", "import", "clean drinking water", "--count", "22", "--first", "2025-11-02", "--last", "2026-08-20")
	require.NoError(t, err)
	assert.Contains(t, out, "Seeded")
	assert.Contains(t, out, "×22")
	assert.Contains(t, out, "grat_2026_08_20_", "the seed receipt encodes its last date")

	entries := gratitudeListEntries(t)
	require.Len(t, entries, 1)
	assert.Equal(t, 22, entries[0].Count)
	assert.Equal(t, "2025-11-02", entries[0].First)
	assert.Equal(t, "2026-08-20", entries[0].Last)

	// The `add --count …` alias routes to the seed path (a distinct entry here).
	aliasOut, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"gratitude", "add", "warm sunlight", "--count", "5", "--first", "2026-02-01", "--last", "2026-08-01")
	require.NoError(t, err)
	assert.Contains(t, aliasOut, "Seeded")
	assert.Contains(t, aliasOut, "×5")

	// --into and --count cannot be combined.
	_, _, err = runRoot(t, BuildInfo{Version: "dev"},
		"gratitude", "add", "warm sunlight", "--into", "gratitude_x", "--count", "5", "--first", "2026-02-01", "--last", "2026-08-01")
	require.Error(t, err, "--into and --count are mutually exclusive")

	// A malformed date is a clean error.
	_, _, err = runRoot(t, BuildInfo{Version: "dev"},
		"gratitude", "import", "a walk outside", "--count", "3", "--first", "nope", "--last", "2026-08-01")
	require.Error(t, err)
}
