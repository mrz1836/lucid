package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

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

// TestGratitudeStdinIsTerminal: the real terminal check behind the ambiguous-band
// question. The null device is a character device, so the looser
// stdinIsInteractive says yes to it — but a caller running `</dev/null` has
// nobody to answer, so an add must refuse and defer rather than ask.
func TestGratitudeStdinIsTerminal(t *testing.T) {
	null, err := os.Open(os.DevNull)
	require.NoError(t, err)
	t.Cleanup(func() { _ = null.Close() })
	assert.True(t, stdinIsInteractive(null), "the null device is a character device")
	assert.False(t, stdinIsTerminal(null), "the null device is not a person")

	path := t.TempDir() + "/phrase.txt"
	require.NoError(t, os.WriteFile(path, []byte("a walk outside\n"), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	assert.False(t, stdinIsTerminal(file), "a regular file is not a person")

	assert.False(t, stdinIsTerminal(strings.NewReader("y\n")), "a reader is not a person")
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

// gratitudeReconcileJSON is the documented `reconcile --json` shape
// (gratitude.md §7.9); decoding with unknown fields disallowed pins it.
type gratitudeReconcileJSON struct {
	Proposals []struct {
		Source      string  `json:"source"`
		SourceThing string  `json:"source_thing"`
		Target      string  `json:"target"`
		TargetThing string  `json:"target_thing"`
		Score       float64 `json:"score"`
		MatchTier   int     `json:"match_tier"`
		Band        string  `json:"band"`
		WillApply   bool    `json:"will_apply"`
		Command     string  `json:"command"`
	} `json:"proposals"`
	Tier3   string `json:"tier3"`
	Applied []struct {
		Receipt string `json:"receipt"`
		Source  string `json:"source"`
		Target  string `json:"target"`
	} `json:"applied"`
}

// decodeReconcileJSON decodes a reconcile --json payload strictly and checks the
// two arrays are arrays, never null.
func decodeReconcileJSON(t *testing.T, out string) gratitudeReconcileJSON {
	t.Helper()
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	assert.Equal(t, "[", string(raw["proposals"][:1]), "proposals is an array, never null")
	assert.Equal(t, "[", string(raw["applied"][:1]), "applied is an array, never null")
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	var v gratitudeReconcileJSON
	require.NoError(t, dec.Decode(&v))
	return v
}

// seedReconcileCLI tallies a clear duplicate by wording (a plural started with
// --new beside its singular) and a close call (an exact tie by wording), and
// returns the entry ids by wording.
func seedReconcileCLI(t *testing.T) map[string]string {
	t.Helper()
	addGratitudeCLI(t, "my morning coffee")
	addGratitudeCLI(t, "my morning coffee")
	addGratitudeCLI(t, "the walk to work")
	addGratitudeCLI(t, "the walk to work") // counts, not same-second creation times, decide each fold's direction
	for _, thing := range []string{"morning coffees", "the walk home"} {
		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "add", thing, "--new")
		require.NoError(t, err)
	}
	ids := map[string]string{}
	for _, e := range gratitudeListEntries(t) {
		ids[e.Thing] = e.ID
	}
	require.Len(t, ids, 4)
	return ids
}

// TestGratitudeReconcileCLI: `lucid gratitude reconcile` is a dry run by default
// (gratitude.md §7.9) — it prints the proposals, a clear match marked fold and a
// close call marked look with its merge command, and writes nothing; `--json`
// emits the documented shape; `--apply --json` folds exactly the marked pair
// through merge, returning its receipt, and leaves the close call alone.
func TestGratitudeReconcileCLI(t *testing.T) {
	isolatedHome(t)
	ids := seedReconcileCLI(t)
	coffee, coffees := ids["my morning coffee"], ids["morning coffees"]
	home, work := ids["the walk home"], ids["the walk to work"]

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "reconcile")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Contains(t, out, "2 possible duplicates (dry run — nothing was changed):")
	assert.Contains(t, out, fmt.Sprintf("fold  %s %q into %s %q", coffees, "morning coffees", coffee, "my morning coffee"))
	assert.Contains(t, out, fmt.Sprintf("look  %s %q into %s %q", home, "the walk home", work, "the walk to work"))
	assert.Contains(t, out, fmt.Sprintf("lucid gratitude merge %s %s", home, work))
	assert.Contains(t, out, "Meaning match is off — proposed by wording only.")
	assert.Len(t, gratitudeListEntries(t), 4, "a dry run folds nothing")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "reconcile", "--json")
	require.NoError(t, err)
	dry := decodeReconcileJSON(t, out)
	require.Len(t, dry.Proposals, 2)
	assert.Equal(t, coffees, dry.Proposals[0].Source)
	assert.Equal(t, coffee, dry.Proposals[0].Target)
	assert.Equal(t, "high", dry.Proposals[0].Band)
	assert.True(t, dry.Proposals[0].WillApply)
	assert.Equal(t, 2, dry.Proposals[0].MatchTier)
	assert.Equal(t, "ambiguous", dry.Proposals[1].Band)
	assert.False(t, dry.Proposals[1].WillApply)
	assert.Equal(t, fmt.Sprintf("lucid gratitude merge %s %s", home, work), dry.Proposals[1].Command)
	assert.Equal(t, "disabled", dry.Tier3)
	assert.Empty(t, dry.Applied)

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "reconcile", "--apply", "--json")
	require.NoError(t, err)
	applied := decodeReconcileJSON(t, out)
	require.Len(t, applied.Applied, 1, "only the pair marked fold is applied")
	assert.Equal(t, coffees, applied.Applied[0].Source)
	assert.Equal(t, coffee, applied.Applied[0].Target)
	assert.Regexp(t, `^grat_\d{4}_\d{2}_\d{2}_\d{3,}$`, applied.Applied[0].Receipt, "each fold returns its receipt")
	assert.Empty(t, applied.Tier3, "an apply never consults the model")

	counts := gratitudeCounts(t)
	assert.Equal(t, map[string]int{coffee: 3, home: 1, work: 2}, counts,
		"the plural folded into the coffee entry; the close call was left alone")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "reconcile", "--apply")
	require.NoError(t, err)
	assert.Contains(t, out, "Nothing to fold")
	assert.Contains(t, out, "Worth a look, not folded:")
}

// TestGratitudeReconcileCLIJudge: opted in to tier 3, a dry run builds the judge
// through the buildProvider seam and lists its by-meaning pair as advisory;
// `--apply` never builds or calls it (gratitude.md §7.6, §7.9), and folds no
// tier-3 pair.
func TestGratitudeReconcileCLIJudge(t *testing.T) {
	home := isolatedHome(t)
	addGratitudeCLI(t, "the two wheels that carry me to work")
	addGratitudeCLI(t, "my bike")
	writeGratitudeMatch(t, home, func(m *config.GratitudeMatchConfig) { m.Tier3Enabled = true })

	judge := &provider.Fake{Script: []provider.Exchange{{Content: `{"pairs": [{"a": 1, "b": 2, "score": 0.96}]}`}}}
	built := withGratitudeJudge(t, judge)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "reconcile", "--json")
	require.NoError(t, err)
	v := decodeReconcileJSON(t, out)
	assert.Equal(t, "used", v.Tier3)
	require.Len(t, v.Proposals, 1)
	assert.Equal(t, 3, v.Proposals[0].MatchTier)
	assert.False(t, v.Proposals[0].WillApply, "a by-meaning pair is advisory")
	assert.Equal(t, 1, judge.Calls())
	assert.Equal(t, config.Default().Gratitude.Match.Tier3Backend, built.Backend, "built with the gratitude.match overrides")

	unused := &provider.Fake{}
	builtOnApply := withGratitudeJudge(t, unused)
	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gratitude", "reconcile", "--apply", "--json")
	require.NoError(t, err)
	v = decodeReconcileJSON(t, out)
	assert.Empty(t, v.Applied)
	assert.Empty(t, v.Tier3)
	assert.Zero(t, unused.Calls(), "an apply never calls the judge")
	assert.Empty(t, builtOnApply.Backend, "an apply never even builds it")
	assert.Len(t, gratitudeListEntries(t), 2, "no tier-3 pair is folded")
}

// gratitudeCLIOK runs one `lucid` invocation that must succeed and returns its
// stdout.
func gratitudeCLIOK(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoErrorf(t, err, "lucid %s: %s", strings.Join(args, " "), errOut)
	return out
}

// gratitudeAddID tallies one thing (with any extra flags) under --json and
// returns the stable id it landed on.
func gratitudeAddID(t *testing.T, thing string, flags ...string) string {
	t.Helper()
	out := gratitudeCLIOK(t, append([]string{"gratitude", "add", thing, "--json"}, flags...)...)
	var view struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	return view.ID
}

// gratitudeListJSON is the documented `list --json` shape with the outward-
// expression fields (gratitude.md §6, §8).
type gratitudeListJSON struct {
	Count   int `json:"count"`
	Entries []struct {
		ID     string `json:"id"`
		Thing  string `json:"thing"`
		People []struct {
			PersonKey     string `json:"person_key"`
			DisplayName   string `json:"display_name"`
			OffLimits     bool   `json:"off_limits"`
			LastExpressed string `json:"last_expressed"`
		} `json:"people"`
	} `json:"entries"`
	Reminders []map[string]any `json:"reminders"`
}

// TestGratitudeThankCLI: `thank <id> --person <subject>` prints a receipt-bearing
// ack, emits exactly {receipt, id, thing, count, person_key, date} under --json
// with the count unchanged (gratitude.md §8), and refuses — on stderr, with a
// non-zero exit and nothing saved — a missing --person, an unknown person, and an
// unknown id.
func TestGratitudeThankCLI(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, time.Date(2026, time.July, 10, 21, 45, 0, 0, time.UTC))
	writePersonRecord(t, home, "person_a-river", "Sam Rivera", []string{"Sam Rivera", "Sam"}, nil, personSeed())
	id := gratitudeAddID(t, "coffee with Sam on the porch")
	gratitudeAddID(t, "coffee with Sam on the porch")

	out := gratitudeCLIOK(t, "gratitude", "thank", id, "--person", "Sam")
	assert.Contains(t, out, "Noted that you told Sam Rivera about \"coffee with Sam on the porch\" (2026-07-10) as `grat_2026_07_10_003`")
	assert.Contains(t, out, "the tally stays ×2")

	out = gratitudeCLIOK(t, "gratitude", "thank", id, "--person", "person_a-river", "--day", "@yesterday", "--json")
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	var view struct {
		Receipt   string `json:"receipt"`
		ID        string `json:"id"`
		Thing     string `json:"thing"`
		Count     int    `json:"count"`
		PersonKey string `json:"person_key"`
		Date      string `json:"date"`
	}
	require.NoError(t, dec.Decode(&view), "exactly the documented keys")
	assert.Equal(t, "grat_2026_07_09_004", view.Receipt)
	assert.Equal(t, id, view.ID)
	assert.Equal(t, "coffee with Sam on the porch", view.Thing)
	assert.Equal(t, 2, view.Count, "expressing gratitude never moves the tally")
	assert.Equal(t, "person_a-river", view.PersonKey)
	assert.Equal(t, "2026-07-09", view.Date)

	before := gratitudeFileSnapshot(t, home)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"gratitude", "thank", id}, "needs --person"},
		{[]string{"gratitude", "thank", id, "--person", "Robin Nobody"}, "no one matches"},
		{[]string{"gratitude", "thank", "gratitude_z-nowhere", "--person", "Sam"}, "no live gratitude entry"},
	} {
		stdout, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, tc.args...)
		require.Errorf(t, err, "lucid %s", strings.Join(tc.args, " "))
		assert.Empty(t, stdout)
		assert.Contains(t, errOut, tc.want, "the reason reaches stderr")
		assert.Contains(t, errOut, "nothing was saved")
	}
	assert.Equal(t, before, gratitudeFileSnapshot(t, home), "a refused thank writes nothing")
}

// gratitudeFileSnapshot reads every gratitude record byte for byte.
func gratitudeFileSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	dir := home + "/registries/gratitude"
	des, err := os.ReadDir(dir)
	require.NoError(t, err)
	files := make(map[string]string, len(des))
	for _, de := range des {
		b, rerr := os.ReadFile(dir + "/" + de.Name())
		require.NoError(t, rerr)
		files[de.Name()] = string(b)
	}
	return files
}

// TestGratitudeAddPersonCLI: `add --person <subject>` links the person onto the
// entry and reports person_key under --json (gratitude.md §3, §8); an empty
// --person, an unknown person, and --person with the --count seed are clean
// refusals that save nothing.
func TestGratitudeAddPersonCLI(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-river", "Sam Rivera", []string{"Sam Rivera"}, nil, personSeed())

	out := gratitudeCLIOK(t, "gratitude", "add", "coffee with Sam on the porch", "--person", "Sam Rivera", "--json")
	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "person_a-river", view["person_key"])
	assert.Equal(t, true, view["created"])

	out = gratitudeCLIOK(t, "gratitude", "add", "coffee with Sam on the porch")
	assert.NotContains(t, out, "Linked to", "an add without --person keeps its ack")
	out = gratitudeCLIOK(t, "gratitude", "add", "coffee with Sam on the porch", "--person", "person_a-river")
	assert.Contains(t, out, "Linked to Sam Rivera.")

	var list gratitudeListJSON
	require.NoError(t, json.Unmarshal([]byte(gratitudeCLIOK(t, "gratitude", "list", "--json")), &list))
	require.Len(t, list.Entries, 1)
	require.Len(t, list.Entries[0].People, 1, "linked once")
	assert.Equal(t, "person_a-river", list.Entries[0].People[0].PersonKey)
	assert.Empty(t, list.Entries[0].People[0].LastExpressed, "linking is not expressing")

	before := gratitudeFileSnapshot(t, home)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"gratitude", "add", "a new thing", "--person", " "}, "--person needs a person name or key"},
		{[]string{"gratitude", "add", "a new thing", "--person", "Robin Nobody"}, "no one matches"},
		{[]string{"gratitude", "add", "a new thing", "--person", "Sam Rivera", "--count", "3", "--first", "2026-01-01", "--last", "2026-02-01"}, "--person and --count cannot be combined"},
	} {
		_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, tc.args...)
		require.Errorf(t, err, "lucid %s", strings.Join(tc.args, " "))
		assert.Contains(t, errOut, tc.want)
		assert.Contains(t, errOut, "nothing was saved")
	}
	assert.Equal(t, before, gratitudeFileSnapshot(t, home), "a refused add writes nothing")
}

// TestGratitudeListPeopleJSON: `list --json` always carries a people array on
// every entry and a top-level reminders array — [] when unlinked or empty,
// never null (gratitude.md §6, §8).
func TestGratitudeListPeopleJSON(t *testing.T) {
	isolatedHome(t)
	out := gratitudeCLIOK(t, "gratitude", "list", "--json")
	assert.Contains(t, out, `"reminders": []`, "an empty tally still carries the array")

	addGratitudeCLI(t, "clean drinking water")
	out = gratitudeCLIOK(t, "gratitude", "list", "--json")
	assert.Contains(t, out, `"people": []`)
	assert.Contains(t, out, `"reminders": []`)
	assert.NotContains(t, out, "null")
}

// seedGratitudeReminders links five entries, each to its own person and tallied
// on its own day (most recent last), and returns the people by key → name.
func seedGratitudeReminders(t *testing.T, home string) map[string]string {
	t.Helper()
	people := []struct{ key, name, thing string }{
		{"person_a-river", "Sam Rivera", "coffee with Sam on the porch"},
		{"person_b-stone", "Alex Stone", "the long call with Alex"},
		{"person_c-field", "Robin Field", "a letter from Robin"},
		{"person_e-lark", "Jo Lark", "a ride home from Jo"},
		{"person_g-reed", "Kit Reed", "Kit fixing the bike"},
	}
	names := make(map[string]string, len(people))
	for i, p := range people {
		writePersonRecord(t, home, p.key, p.name, []string{p.name}, nil, personSeed())
		gratitudeAddID(t, p.thing, "--person", p.key, "--day", fmt.Sprintf("@2026-07-0%d", i+1))
		names[p.key] = p.name
	}
	return names
}

// gratitudeReminderSection returns the human `list` output from the reminder
// header on — "" when no reminder is offered.
func gratitudeReminderSection(out string) string {
	i := strings.Index(out, "You might tell:")
	if i < 0 {
		return ""
	}
	return out[i:]
}

// TestGratitudeReminderNoScore: the reminder is gentle and optional
// (gratitude.md §0, §8) — at most three plain lines, the same wording however
// long a person has gone untold, and no completion percentage, streak, quota,
// "unthanked" backlog, overdue language, or count of how many others there are,
// in the human view or under --json (whose reminder objects carry exactly the
// four documented keys and nothing numeric).
func TestGratitudeReminderNoScore(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, time.Date(2026, time.July, 10, 21, 45, 0, 0, time.UTC))
	seedGratitudeReminders(t, home)
	// Told Jo long ago; Jo's entry has been tallied since — offered again, in the
	// same words as a person never told.
	gratitudeCLIOK(t, "gratitude", "thank", gratitudeAddID(t, "a ride home from Jo"), "--person", "person_e-lark", "--day", "@2026-06-01")
	gratitudeAddID(t, "a ride home from Jo")

	out := gratitudeCLIOK(t, "gratitude", "list")
	section := gratitudeReminderSection(out)
	require.NotEmpty(t, section, "list offers reminders")
	lines := strings.Split(strings.TrimRight(section, "\n"), "\n")
	require.Len(t, lines, 4, "a header and at most three lines — five linked people, three offered")
	for _, line := range lines[1:] {
		assert.Regexp(t, `^  [A-Z][a-z]+ [A-Z][a-z]+ — "[^"]+" \(gratitude_[a-z0-9-]+\)$`, line,
			"one fixed, quiet shape: no dates, counts, or escalation")
	}
	assert.Contains(t, lines[1], "Jo Lark", "the entry tallied most recently comes first")

	lower := strings.ToLower(out)
	for _, banned := range []string{
		"%", "percent", "streak", "quota", "unthanked", "backlog", "overdue", "complete",
		"remaining", "more", "others", "left to", "score", "goal", "haven't", "days since", "still",
	} {
		assert.NotContainsf(t, lower, banned, "the list never renders %q", banned)
	}
	for _, line := range lines[1:] {
		wording := regexp.MustCompile(` \(gratitude_[^)]*\)$`).ReplaceAllString(line, "")
		assert.NotRegexp(t, `\d`, wording, "a reminder line carries no number beyond the entry id")
	}

	var list struct {
		Count     int                 `json:"count"`
		Entries   []json.RawMessage   `json:"entries"`
		Reminders []map[string]string `json:"reminders"`
	}
	raw := gratitudeCLIOK(t, "gratitude", "list", "--json")
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &top))
	assert.ElementsMatch(t, []string{"count", "entries", "reminders"}, slices.Collect(maps.Keys(top)),
		"no top-level total, backlog, or progress field")
	require.NoError(t, json.Unmarshal([]byte(raw), &list), "every reminder field is a string — nothing numeric")
	require.Len(t, list.Reminders, 3)
	for _, rm := range list.Reminders {
		assert.ElementsMatch(t, []string{"person_key", "display_name", "entry_id", "thing"}, slices.Collect(maps.Keys(rm)))
	}
}

// TestGratitudeReminderExcludesOffLimits: a person marked off-limits may be
// linked and thanked — the link still shows on the entry, flagged off_limits —
// but a reminder never names them, in the human view or under --json, even when
// they are the most recently tallied (gratitude.md §8).
func TestGratitudeReminderExcludesOffLimits(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, time.Date(2026, time.July, 10, 21, 45, 0, 0, time.UTC))
	writePersonRecord(t, home, "person_a-river", "Sam Rivera", []string{"Sam Rivera"}, nil, personSeed())
	writePersonRecord(t, home, "person_d-vale", "Dana Vale", []string{"Dana Vale"}, nil, personSeed())
	writeOffLimits(t, home, "person_d-vale")

	sam := gratitudeAddID(t, "coffee with Sam on the porch", "--person", "person_a-river", "--day", "@2026-07-01")
	dana := gratitudeAddID(t, "the walks with Dana", "--person", "Dana Vale", "--day", "@2026-07-02")
	shared := gratitudeAddID(t, "the dinner the three of us cooked", "--person", "person_d-vale", "--day", "@2026-07-03")
	gratitudeCLIOK(t, "gratitude", "add", "the dinner the three of us cooked", "--into", shared, "--person", "person_a-river", "--day", "@2026-07-03")
	out := gratitudeCLIOK(t, "gratitude", "thank", dana, "--person", "Dana Vale", "--day", "@2026-07-01")
	assert.Contains(t, out, "Noted that you told Dana Vale", "thanking an off-limits person is allowed")

	human := gratitudeCLIOK(t, "gratitude", "list")
	section := gratitudeReminderSection(human)
	require.NotEmpty(t, section)
	assert.NotContains(t, section, "Dana", "an off-limits person is never named in a reminder")
	assert.Contains(t, section, "Sam Rivera")
	assert.Contains(t, human, "· with Dana Vale (told 2026-07-01) (off-limits)", "the link itself still shows, flagged")

	var list gratitudeListJSON
	require.NoError(t, json.Unmarshal([]byte(gratitudeCLIOK(t, "gratitude", "list", "--json")), &list))
	require.Len(t, list.Reminders, 2, "Sam, for both entries he is linked to — never Dana")
	for _, rm := range list.Reminders {
		assert.Equal(t, "person_a-river", rm["person_key"])
	}
	assert.Equal(t, []any{shared, sam}, []any{list.Reminders[0]["entry_id"], list.Reminders[1]["entry_id"]},
		"most recently tallied first")
	for _, e := range list.Entries {
		for _, p := range e.People {
			assert.Equal(t, p.PersonKey == "person_d-vale", p.OffLimits, "the off-limits flag rides on the link")
		}
	}
}
