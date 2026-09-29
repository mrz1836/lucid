package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
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

// TestGratitude_CLI_AutoMatchJSON: a phrase that misses the canonical key but is a
// clear tier-2 match bumps the existing entry automatically, and `--json` carries
// match_tier / match_score (gratitude.md §7.4) — while a plain canonical-key write
// keeps exactly its v1 keys, with no match attribution at all.
func TestGratitude_CLI_AutoMatchJSON(t *testing.T) {
	isolatedHome(t)

	first, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "a morning walk by a river", "--json")
	require.NoError(t, err)
	var v1 map[string]any
	require.NoError(t, json.Unmarshal([]byte(first), &v1))
	assert.NotContains(t, v1, "match_tier", "a create carries no match attribution")
	assert.NotContains(t, v1, "match_score")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "the morning walks by the river", "--json")
	require.NoError(t, err)
	var payload struct {
		ID         string  `json:"id"`
		Thing      string  `json:"thing"`
		Count      int     `json:"count"`
		Created    bool    `json:"created"`
		MatchTier  int     `json:"match_tier"`
		MatchScore float64 `json:"match_score"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, v1["id"], payload.ID, "the wording landed on the existing entry")
	assert.Equal(t, "a morning walk by a river", payload.Thing, "the canonical display is kept")
	assert.Equal(t, 2, payload.Count)
	assert.False(t, payload.Created)
	assert.Equal(t, 2, payload.MatchTier)
	assert.InDelta(t, 1.0, payload.MatchScore, 1e-9)

	human, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "morning walks by the river")
	require.NoError(t, err)
	assert.Contains(t, human, "by wording (tier 2)", "the ack says how it matched")
	assert.Len(t, gratitudeListEntries(t), 1, "no near-duplicate row was created")
}

// TestGratitude_CLI_AmbiguousRefusesNonInteractive: an ambiguous-band phrase (an
// exact tie between two entries) writes nothing and exits non-zero, printing the
// candidates and the two ways to resolve on stderr (gratitude.md §7.4); re-running
// with --into bumps the chosen entry.
func TestGratitude_CLI_AmbiguousRefusesNonInteractive(t *testing.T) {
	isolatedHome(t)
	addGratitudeCLI(t, "the walk to work")
	addGratitudeCLI(t, "a quiet home")
	before := gratitudeListEntries(t)
	require.Len(t, before, 2)

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "the walk home")
	require.Error(t, err, "an ambiguous match is a non-zero exit")
	assert.Empty(t, out, "nothing is acknowledged")
	for _, e := range before {
		assert.Contains(t, stderr, e.ID, "each candidate is named")
	}
	assert.Contains(t, stderr, "--into <id>")
	assert.Contains(t, stderr, "--new")
	assert.Contains(t, stderr, "nothing was saved")

	after := gratitudeListEntries(t)
	require.Len(t, after, 2, "never silently created")
	for _, e := range after {
		assert.Equal(t, 1, e.Count, "never silently merged")
	}

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "the walk home", "--into", before[0].ID)
	require.NoError(t, err, "--into resolves the suggestion")
}

// TestGratitude_CLI_NewFlag: `add --new` starts a new entry where the wording
// would otherwise auto-bump an existing one (gratitude.md §3), and cannot combine
// with --into or --count — each contradiction is a clean error that writes nothing.
func TestGratitude_CLI_NewFlag(t *testing.T) {
	isolatedHome(t)
	addGratitudeCLI(t, "morning coffee")
	id := gratitudeListEntries(t)[0].ID

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "coffee in the morning", "--new")
	require.NoError(t, err)
	assert.Contains(t, out, "Started tally for", "--new creates rather than matching by wording")
	require.Len(t, gratitudeListEntries(t), 2)

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "coffee", "--new", "--into", id)
	require.Error(t, err)
	assert.Contains(t, stderr, "--into and --new cannot be combined")

	_, stderr, err = runRoot(t, BuildInfo{Version: "dev"},
		"gratitude", "add", "warm sunlight", "--new", "--count", "5", "--first", "2026-02-01", "--last", "2026-08-01")
	require.Error(t, err)
	assert.Contains(t, stderr, "--new and --count cannot be combined")
	assert.Len(t, gratitudeListEntries(t), 2, "the refused adds wrote nothing")
}

// withGratitudeJudge injects p as the tier-3 judge for one test through the
// buildProvider seam (restoring the package's offline default after) and returns
// a pointer to the provider block the seam was last asked to build, so a test can
// assert the gratitude.match overrides reached it.
func withGratitudeJudge(t *testing.T, p provider.Provider) *config.ProviderConfig {
	t.Helper()
	prev := buildProvider
	var got config.ProviderConfig
	buildProvider = func(cfg config.ProviderConfig) (provider.Provider, error) {
		got = cfg
		return p, nil
	}
	t.Cleanup(func() { buildProvider = prev })
	return &got
}

// writeGratitudeMatch rewrites the isolated home's lucid.json with the
// documented defaults plus a mutated gratitude.match block.
func writeGratitudeMatch(t *testing.T, home string, mutate func(*config.GratitudeMatchConfig)) {
	t.Helper()
	cfg := config.Default()
	mutate(&cfg.Gratitude.Match)
	b, err := cfg.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(storage.New(home).ConfigPath(), b, 0o600))
}

// TestGratitudeProviderDisabledCLI: tier 3 is never load-bearing (gratitude.md
// §7.6). Out of the box it is off — an opt-in, since its default judge is hosted
// — so `add` completes on tiers 1–2 with no model built or called, no note, and
// "tier3": "disabled" under --json. Opted in with no model reachable (the
// package's offline default), `add` still completes — exit 0 on a write — and
// says so: the documented one-line note on stderr and "tier3": "unavailable",
// while stdout stays the clean ack or JSON; an ambiguous refusal prints the same
// stderr line. A first entry and a clear tier-2 match never needed the judge, so
// they carry no tier3 key. `list` completes either way.
func TestGratitudeProviderDisabledCLI(t *testing.T) {
	home := isolatedHome(t)

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "the walk to work", "--json")
	require.NoError(t, err)
	assert.NotContains(t, out, "tier3", "a first entry had nothing to judge against")
	assert.Empty(t, stderr)

	judge := &provider.Fake{}
	built := withGratitudeJudge(t, judge)
	out, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "a quiet home", "--json")
	require.NoError(t, err)
	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "disabled", view["tier3"], "tier 3 is off by default")
	assert.Empty(t, stderr, "switching tier 3 off is a choice, not a degradation")
	assert.Zero(t, judge.Calls())
	assert.Empty(t, built.Backend, "no judge is even built while tier 3 is off")

	writeGratitudeMatch(t, home, func(m *config.GratitudeMatchConfig) { m.Tier3Enabled = true })
	withGratitudeJudge(t, &provider.Fake{ExhaustErr: provider.ErrUnavailable})

	out, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "fresh bread")
	require.NoError(t, err, "a missing model is never an add failure")
	assert.Contains(t, out, "Started tally for")
	assert.Equal(t, gratitudeTier3UnavailableNote+"\n", stderr, "the degraded path is named on stderr")

	out, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "warm sunlight", "--json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &view), "stdout stays pure JSON")
	assert.Equal(t, "unavailable", view["tier3"])
	assert.Equal(t, true, view["created"])
	assert.Contains(t, stderr, gratitudeTier3UnavailableNote)

	_, stderr, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "the walk home")
	require.Error(t, err, "a tier-2 ambiguity still refuses without the model")
	assert.Contains(t, stderr, gratitudeTier3UnavailableNote)
	assert.Contains(t, stderr, "--into <id>")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "the walks to work", "--json")
	require.NoError(t, err)
	assert.NotContains(t, out, "tier3", "a clear tier-2 winner never needed the judge")

	listOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "list")
	require.NoError(t, err)
	assert.Contains(t, listOut, "4 gratitude entries")
}

// TestGratitudeTier3CLI: opted in, a plain `add` builds the tier-3 judge through
// the buildProvider seam from the provider block with the gratitude.match
// overrides (backend, model, and per-call bound — the endpoint inherited), and a
// clear by-meaning match of a zero-overlap phrase bumps the existing entry: the
// ack says "by meaning (tier 3)" and --json carries match_tier 3 and tier3
// "used" (gratitude.md §7.4–§7.5).
func TestGratitudeTier3CLI(t *testing.T) {
	home := isolatedHome(t)
	addGratitudeCLI(t, "the two wheels that carry me to work")
	id := gratitudeListEntries(t)[0].ID
	writeGratitudeMatch(t, home, func(m *config.GratitudeMatchConfig) { m.Tier3Enabled = true })

	judge := &provider.Fake{Script: []provider.Exchange{
		{Content: `{"matches": [{"n": 1, "score": 0.96}]}`},
		{Content: `{"matches": [{"n": 1, "score": 0.97}]}`},
	}}
	built := withGratitudeJudge(t, judge)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "my bike", "--json")
	require.NoError(t, err)
	var payload struct {
		ID         string  `json:"id"`
		Thing      string  `json:"thing"`
		Count      int     `json:"count"`
		Created    bool    `json:"created"`
		MatchTier  int     `json:"match_tier"`
		MatchScore float64 `json:"match_score"`
		Tier3      string  `json:"tier3"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, id, payload.ID)
	assert.Equal(t, "the two wheels that carry me to work", payload.Thing)
	assert.Equal(t, 2, payload.Count)
	assert.False(t, payload.Created)
	assert.Equal(t, 3, payload.MatchTier)
	assert.InDelta(t, 0.96, payload.MatchScore, 1e-9)
	assert.Equal(t, "used", payload.Tier3)

	def := config.Default()
	assert.Equal(t, def.Gratitude.Match.Tier3Backend, built.Backend, "tier3_backend overrides provider.backend")
	assert.Equal(t, def.Gratitude.Match.Tier3Model, built.Model, "tier3_model overrides provider.model")
	assert.Equal(t, def.Gratitude.Match.Tier3TimeoutSeconds, built.TimeoutSeconds, "the per-call bound")
	assert.Equal(t, def.Provider.Endpoint, built.Endpoint, "the endpoint is inherited")

	human, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "cycling in")
	require.NoError(t, err)
	assert.Contains(t, human, "by meaning (tier 3)")
	assert.Empty(t, stderr)
	assert.Equal(t, 2, judge.Calls())
	assert.Len(t, gratitudeListEntries(t), 1, "no near-duplicate row was created")
}

// runRootWithStdin runs the root command like runRoot, with stdin fed from the
// given text — the answers a person would type.
func runRootWithStdin(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errBuf.String(), err
}

// withGratitudeTerminal stands a terminal in for stdin for one test, so an
// ambiguous-band add asks its question (the package default is never a terminal).
func withGratitudeTerminal(t *testing.T) {
	t.Helper()
	prev := gratitudeStdinIsTerminal
	gratitudeStdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { gratitudeStdinIsTerminal = prev })
}

// seedGratitudeTie tallies two entries that "the walk home" ties exactly at the
// tier-2 floor (0.5 each) and returns their ids in suggestion order — a tie
// ranks by stable id — so a test knows which candidate the question leads with.
func seedGratitudeTie(t *testing.T) []string {
	t.Helper()
	addGratitudeCLI(t, "the walk to work")
	addGratitudeCLI(t, "a quiet home")
	entries := gratitudeListEntries(t)
	require.Len(t, entries, 2)
	ids := []string{entries[0].ID, entries[1].ID}
	slices.Sort(ids)
	return ids
}

// gratitudeCounts maps each live entry's id to its count.
func gratitudeCounts(t *testing.T) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, e := range gratitudeListEntries(t) {
		counts[e.ID] = e.Count
	}
	return counts
}

// TestGratitudeAddJSONRefuseAndDefer: under --json an ambiguous-band add refuses
// and defers (gratitude.md §7.4, ADR-0012 §4) — it writes nothing, exits 1, and
// its stdout is exactly the documented suggestion payload: status, thing, band,
// match_tier, the candidates (id, wording, score; best first), the two ways to
// resolve, saved false, and tier3. The sentence form is not printed as well, and
// --json never asks, even on a terminal with an answer waiting. Re-running with
// --into bumps the chosen candidate (no match attribution — the caller chose);
// re-running with --new starts a new entry.
func TestGratitudeAddJSONRefuseAndDefer(t *testing.T) {
	isolatedHome(t)
	ids := seedGratitudeTie(t)
	withGratitudeTerminal(t) // --json is a machine caller: never asked, terminal or not

	out, stderr, err := runRootWithStdin(t, "y\n", "gratitude", "add", "the walk home", "--json")
	require.Error(t, err, "a deferred choice is a failed exit")
	assert.Equal(t, ExitErr, exitCodeForError(err), "the deferred choice exits 1")
	assert.Empty(t, stderr, "no question, no sentence: the payload is the answer")

	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &keys), "stdout is pure JSON")
	assert.ElementsMatch(t,
		[]string{"status", "thing", "band", "match_tier", "candidates", "resolve", "saved", "tier3"},
		slices.Collect(maps.Keys(keys)), "exactly the documented fields")

	type candidate struct {
		ID    string  `json:"id"`
		Thing string  `json:"thing"`
		Score float64 `json:"score"`
	}
	var payload struct {
		Status     string      `json:"status"`
		Thing      string      `json:"thing"`
		Band       string      `json:"band"`
		MatchTier  int         `json:"match_tier"`
		Candidates []candidate `json:"candidates"`
		Resolve    []string    `json:"resolve"`
		Saved      *bool       `json:"saved"`
		Tier3      string      `json:"tier3"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&payload))
	assert.Equal(t, "suggestion", payload.Status)
	assert.Equal(t, "the walk home", payload.Thing)
	assert.Equal(t, "ambiguous", payload.Band)
	assert.Equal(t, 2, payload.MatchTier, "tier 2's band produced it")
	require.Len(t, payload.Candidates, 2)
	gotIDs := []string{payload.Candidates[0].ID, payload.Candidates[1].ID}
	assert.Equal(t, ids, gotIDs, "best first, a tie by id")
	things := map[string]string{}
	for _, c := range payload.Candidates {
		assert.InDelta(t, 0.5, c.Score, 1e-9)
		things[c.ID] = c.Thing
	}
	assert.ElementsMatch(t, []string{"the walk to work", "a quiet home"}, []string{things[ids[0]], things[ids[1]]})
	assert.Equal(t, []string{"--into <id>", "--new"}, payload.Resolve)
	require.NotNil(t, payload.Saved)
	assert.False(t, *payload.Saved)
	assert.Equal(t, "disabled", payload.Tier3, "tier 3 is off by default, and the payload says so")

	assert.Equal(t, map[string]int{ids[0]: 1, ids[1]: 1}, gratitudeCounts(t), "nothing was written")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "the walk home", "--into", ids[1], "--json")
	require.NoError(t, err, "--into resolves the suggestion")
	var bumped map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &bumped))
	assert.Equal(t, ids[1], bumped["id"])
	assert.InDelta(t, 2, bumped["count"], 1e-9)
	assert.NotContains(t, bumped, "match_tier", "a chosen bump carries no match attribution")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", "the walk home", "--new", "--json")
	require.NoError(t, err, "--new resolves the suggestion")
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &created))
	assert.Equal(t, true, created["created"])
	assert.Len(t, gratitudeListEntries(t), 3)
}

// TestGratitudeAddInteractiveSuggestion: on a terminal an ambiguous-band add asks
// the documented question on stderr and writes only on the answer (gratitude.md
// §7.4): y bumps the candidate it names, a listed number picks that candidate, n
// starts a new entry, and q or the end of input cancels with nothing written and
// exit 1. An unrecognized answer — a blank line included — names the choices and
// asks again. A bump answered here lands exactly as --into would: tonight's
// wording joins aka[], with no match attribution and the plain tally ack.
func TestGratitudeAddInteractiveSuggestion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stdin   string
		bumped  int // index into the suggestion-ordered ids; -1 for none
		created bool
		retries int // how many answers were not recognized
	}{
		{name: "y bumps the named candidate", stdin: "y\n", bumped: 0},
		{name: "a number picks another candidate", stdin: "2\n", bumped: 1},
		{name: "n starts a new entry", stdin: "n\n", bumped: -1, created: true},
		{name: "q cancels", stdin: "q\n", bumped: -1},
		{name: "end of input cancels", stdin: "", bumped: -1},
		{name: "unrecognized answers ask again", stdin: "maybe\n\n3\n Y \n", bumped: 0, retries: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatedHome(t)
			ids := seedGratitudeTie(t)
			withGratitudeTerminal(t)

			out, stderr, err := runRootWithStdin(t, tc.stdin, "gratitude", "add", "the walk home")
			assert.True(t, strings.HasPrefix(stderr, fmt.Sprintf(
				"Did you mean to bump %s: %q?\n  [y] bump it   [n] start a new entry   [2] %s: %q   [q] cancel\n",
				ids[0], gratitudeThing(t, ids[0]), ids[1], gratitudeThing(t, ids[1]),
			)), "the documented question, on stderr: %q", stderr)
			assert.Equal(t, tc.retries, strings.Count(stderr, "Answer y, n, 2, or q."))
			assert.NotContains(t, stderr, "re-run with --into", "a person is asked, not told to re-run")

			counts := gratitudeCounts(t)
			switch {
			case tc.bumped >= 0:
				require.NoError(t, err)
				target := ids[tc.bumped]
				assert.Equal(t, 2, counts[target])
				assert.Equal(t, 1, counts[ids[1-tc.bumped]])
				assert.Len(t, counts, 2, "no new entry")
				ack := fmt.Sprintf("Tallied %q (×2) as `", gratitudeThing(t, target))
				assert.True(t, strings.HasPrefix(out, ack),
					"the plain tally ack — the person made the call, so no match attribution: %q", out)
				assert.NotContains(t, out, "matched")
				assert.Contains(t, gratitudeAka(t, target), "the walk home", "tonight's wording joins aka[] as --into records it")
			case tc.created:
				require.NoError(t, err)
				assert.Contains(t, out, `Started tally for "the walk home" (×1)`)
				assert.Len(t, counts, 3)
			default:
				require.ErrorIs(t, err, errGratitudeAskCanceled)
				assert.Equal(t, ExitErr, exitCodeForError(err), "a canceled choice exits 1")
				assert.Empty(t, out)
				assert.Contains(t, stderr, "canceled; nothing was saved")
				assert.Equal(t, map[string]int{ids[0]: 1, ids[1]: 1}, counts, "nothing was written")
			}
		})
	}
}

// TestGratitudeAddAsksOnlyOnATerminal: the question is asked only when a person
// can answer it (gratitude.md §7.4). Off a terminal the add refuses with the
// suggestion sentence on stderr, even with answers piped in; with the phrase
// itself read from stdin (--body-file -) there is no one left to ask, so it
// refuses too, even on a terminal. Neither reads an answer or writes anything.
func TestGratitudeAddAsksOnlyOnATerminal(t *testing.T) {
	isolatedHome(t)
	ids := seedGratitudeTie(t)

	_, stderr, err := runRootWithStdin(t, "y\n", "gratitude", "add", "the walk home")
	require.Error(t, err)
	assert.NotContains(t, stderr, "Did you mean", "a pipe is not a person")
	assert.Contains(t, stderr, "re-run with --into <id>")

	withGratitudeTerminal(t)
	_, stderr, err = runRootWithStdin(t, "the walk home\n", "gratitude", "add", "--body-file", "-")
	require.Error(t, err)
	assert.NotContains(t, stderr, "Did you mean", "stdin carried the phrase, not a person")
	assert.Contains(t, stderr, "re-run with --into <id>")

	assert.Equal(t, map[string]int{ids[0]: 1, ids[1]: 1}, gratitudeCounts(t), "nothing was written")
}

// gratitudeThing returns a live entry's display wording by id.
func gratitudeThing(t *testing.T, id string) string {
	t.Helper()
	for _, e := range gratitudeListEntries(t) {
		if e.ID == id {
			return e.Thing
		}
	}
	require.Failf(t, "no live entry", "%s", id)
	return ""
}

// gratitudeAka returns a live entry's aka[] wordings by id.
func gratitudeAka(t *testing.T, id string) []string {
	t.Helper()
	for _, e := range gratitudeListEntries(t) {
		if e.ID == id {
			return e.Aka
		}
	}
	require.Failf(t, "no live entry", "%s", id)
	return nil
}
