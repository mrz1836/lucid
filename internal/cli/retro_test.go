package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/retro"
)

// readRetroEvents walks the retro tree under home and returns every appended
// event, so a CLI test can assert what actually landed on disk.
func readRetroEvents(t *testing.T, home string) []retro.Retro {
	t.Helper()
	var out []retro.Retro
	root := filepath.Join(home, "retro")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return filepath.SkipDir
			}
			return werr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "retro_") || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			ev, uerr := retro.UnmarshalLine([]byte(line))
			if uerr != nil {
				return uerr
			}
			out = append(out, ev)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return out
}

// TestRetroCmd_HasAllSubcommands guards the retro command tree: a dropped
// registration would ship a missing verb silently, since the group itself still
// renders. The everyday surface is the five subcommands park/list/show/resolve/
// defer; the one-time migration `import` is registered too but carries
// Hidden: true (asserted by TestRetro_CLI_ImportHidden).
func TestRetroCmd_HasAllSubcommands(t *testing.T) {
	got := map[string]bool{}
	for _, c := range newRetroCmd().Commands() {
		got[c.Name()] = true
	}
	for _, name := range []string{"park", "list", "show", "resolve", "defer", "import"} {
		assert.Truef(t, got[name], "retro group missing %s", name)
	}
}

// TestRetro_CLI_ParkReturnsBothIdentities: `retro park` prints the minted R-NNN
// and the receipt, and the event lands on disk verbatim as an open park.
func TestRetro_CLI_ParkReturnsBothIdentities(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"retro", "park", "Revisit whether the Sunday walk opens with deferred items")
	require.NoError(t, err)
	assert.Contains(t, out, "Parked as R-001")
	assert.Contains(t, out, "receipt retro_event_", "the ack names both identities")

	events := readRetroEvents(t, home)
	require.Len(t, events, 1)
	assert.Equal(t, "R-001", events[0].ID)
	assert.Equal(t, retro.EventPark, events[0].EventType)
	assert.Equal(t, retro.StatusOpen, events[0].Status)
	assert.Equal(t, "Revisit whether the Sunday walk opens with deferred items", events[0].Item)
	assert.Equal(t, retro.SourceRetro, events[0].Source)
	assert.Equal(t, retro.Schema, events[0].Schema)
	assert.True(t, strings.HasPrefix(events[0].EventID, "retro_event_"), "the receipt has the retro_event_ prefix")
}

// TestRetro_CLI_ParkJoinsUnquotedText: an unquoted multi-word item is joined into
// a single verbatim item field.
func TestRetro_CLI_ParkJoinsUnquotedText(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "park", "Try", "a", "shorter", "Gate", "cadence")
	require.NoError(t, err)

	events := readRetroEvents(t, home)
	require.Len(t, events, 1)
	assert.Equal(t, "Try a shorter Gate cadence", events[0].Item)
}

// TestRetro_CLI_ParkRequiresItem: a bare `retro park` is a usage error and
// nothing is written.
func TestRetro_CLI_ParkRequiresItem(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "park")
	require.Error(t, err)
	assert.Empty(t, readRetroEvents(t, home), "a park with no item writes nothing")
}

// TestRetroParkFlags exercises the flexibility flags (AC-11): --source stored
// verbatim, --day backdating the parked date, and --json emitting the minted
// R-NNN, the receipt, and the folded item as a structured object.
func TestRetroParkFlags(t *testing.T) {
	home := isolatedHome(t)

	// --json emits {id, event_id, item:{…}} with the folded park.
	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"retro", "park", "Experiment with a two-column layout", "--source", "chat", "--json")
	require.NoError(t, err)

	var payload struct {
		ID      string `json:"id"`
		EventID string `json:"event_id"`
		Item    struct {
			ID         string `json:"id"`
			ParkedDate string `json:"parked_date"`
			Source     string `json:"source"`
			Item       string `json:"item"`
			Status     string `json:"status"`
		} `json:"item"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, "R-001", payload.ID)
	assert.True(t, strings.HasPrefix(payload.EventID, "retro_event_"))
	assert.Equal(t, "R-001", payload.Item.ID)
	assert.Equal(t, "chat", payload.Item.Source, "--source is stored verbatim")
	assert.Equal(t, "Experiment with a two-column layout", payload.Item.Item)
	assert.Equal(t, retro.StatusOpen, payload.Item.Status)

	// --day backdates the parked date to a literal civil day.
	_, _, err = runRoot(t, BuildInfo{Version: "dev"},
		"retro", "park", "Backdated item", "--day", "2026-06-01")
	require.NoError(t, err)

	events := readRetroEvents(t, home)
	var backdated *retro.Retro
	for i := range events {
		if events[i].Item == "Backdated item" {
			backdated = &events[i]
		}
	}
	require.NotNil(t, backdated)
	assert.Equal(t, "2026-06-01", backdated.LogicalDate, "--day sets the parked date")
	assert.Equal(t, "R-002", backdated.ID, "the second park mints the next id regardless of backdating")
	assert.True(t, strings.HasPrefix(backdated.EventID, "retro_event_2026_06_01_"),
		"a backdated park's receipt encodes the logical date")
}

// TestRetro_CLI_DayStrictRejectWritesNothing: a `--day` token the grammar cannot
// read is a clean refusal that names the accepted forms on stderr and writes
// nothing (strict tier).
func TestRetro_CLI_DayStrictRejectWritesNothing(t *testing.T) {
	home := isolatedHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"},
		"retro", "park", "Item", "--day", "@yesterdya")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
	assert.Empty(t, out)
	assert.Contains(t, errOut, "could not read the day", "the reason reaches the user, not just the exit code")
	assert.Empty(t, readRetroEvents(t, home), "a refused day writes nothing")
}

// retroListItems parses the `retro list --json` payload's item ids for an
// end-to-end CLI assertion.
func retroListItems(t *testing.T, args ...string) []string {
	t.Helper()
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, append([]string{"retro", "list"}, args...)...)
	require.NoError(t, err)
	var payload struct {
		Count int `json:"count"`
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	require.Len(t, payload.Items, payload.Count)
	ids := make([]string, 0, len(payload.Items))
	for _, it := range payload.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// TestRetro_CLI_ListShowResolveDefer exercises the whole read+lifecycle surface
// end-to-end: default list is open ∪ deferred; resolve leaves it and returns under
// --resolved/--all; defer stays; show renders a single item; the transition acks
// name both identities.
func TestRetro_CLI_ListShowResolveDefer(t *testing.T) {
	isolatedHome(t)

	for _, item := range []string{"Open item", "Defer me", "Resolve me"} {
		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "park", item)
		require.NoError(t, err)
	}

	// defer R-002, resolve R-003.
	deferOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "defer", "R-002", "not", "now")
	require.NoError(t, err)
	assert.Contains(t, deferOut, "Deferred R-002")
	assert.Contains(t, deferOut, "receipt retro_event_", "the defer ack names both identities")

	resolveOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "resolve", "R-003", "did", "the", "thing")
	require.NoError(t, err)
	assert.Contains(t, resolveOut, "Resolved R-003")
	assert.Contains(t, resolveOut, "receipt retro_event_")

	// Default list: open + deferred; resolved excluded.
	assert.Equal(t, []string{"R-001", "R-002"}, retroListItems(t, "--json"))
	// --all: open/deferred then resolved.
	assert.Equal(t, []string{"R-001", "R-002", "R-003"}, retroListItems(t, "--all", "--json"))
	// --resolved: only R-003.
	assert.Equal(t, []string{"R-003"}, retroListItems(t, "--resolved", "--json"))

	// show R-002 renders the deferred item with its reason (--json exposes fields).
	showOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "show", "R-002", "--json")
	require.NoError(t, err)
	var shown struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		DeferReason string `json:"defer_reason"`
	}
	require.NoError(t, json.Unmarshal([]byte(showOut), &shown))
	assert.Equal(t, "R-002", shown.ID)
	assert.Equal(t, retro.StatusDeferred, shown.Status)
	assert.Equal(t, "not now", shown.DeferReason)
}

// TestRetro_CLI_TransitionUnknownIDIsCleanError: resolving/deferring/showing an id
// no park minted is a clean error on stderr with a non-zero exit and no write.
func TestRetro_CLI_TransitionUnknownIDIsCleanError(t *testing.T) {
	home := isolatedHome(t)

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "resolve", "R-404", "nope")
	require.Error(t, err)
	assert.Contains(t, errOut, "R-404")
	assert.Empty(t, readRetroEvents(t, home), "resolving an unknown id writes nothing")

	_, errOut, err = runRoot(t, BuildInfo{Version: "dev"}, "retro", "show", "R-404")
	require.Error(t, err)
	assert.Contains(t, errOut, "R-404")
}

// TestRetro_CLI_ListEmpty: `list` on a cold home is a clean hint, not a crash.
func TestRetro_CLI_ListEmpty(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "No parked items yet")
}

// TestRetro_CLI_ImportHidden: the one-time `import` verb carries Hidden: true and
// never appears in `lucid retro --help` (retro.md §6), mirroring the hidden
// `pet migrate-self`.
func TestRetro_CLI_ImportHidden(t *testing.T) {
	child, _, err := newRetroCmd().Find([]string{"import"})
	require.NoError(t, err)
	require.NotNil(t, child)
	assert.True(t, child.Hidden, "import is a hidden migration tool, not an everyday verb")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "--help")
	require.NoError(t, err)
	assert.NotContains(t, out, "import", "the hidden import verb is absent from --help")
}

// TestRetro_CLI_ImportFromFile: `retro import --file <json>` reproduces a synthetic
// queue from a folded-item JSON array — exact ids, dates, sources, statuses,
// resolutions, and defer-reasons — and a subsequent everyday park continues at
// imported-max+1, all end-to-end through the CLI (AC-12).
func TestRetro_CLI_ImportFromFile(t *testing.T) {
	home := isolatedHome(t)

	fixture := `[
	  {"id":"R-001","parked_date":"2026-07-10","source":"raw_2026_07_10_22_55","item":"Resolved sample item","status":"resolved","resolution":"adopted it","resolved_date":"2026-07-20"},
	  {"id":"R-002","parked_date":"2026-07-12","source":"chat","item":"Deferred sample item","status":"deferred","defer_reason":"someday"},
	  {"id":"R-003","parked_date":"2026-07-15","source":"retro","item":"Open sample item","status":"open"}
	]`
	path := writeTempFile(t, "retro-items.json", []byte(fixture))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "import", "--file", path)
	require.NoError(t, err)
	assert.Contains(t, out, "Imported 3 parked item(s)")

	// The migrated set matches the fixture exactly via list --all --json. The
	// --all view groups open ∪ deferred (ascending) ahead of resolved, so R-001
	// (resolved) sorts into the trailing resolved section — assert the full set
	// regardless of that grouping.
	assert.ElementsMatch(t, []string{"R-001", "R-002", "R-003"}, retroListItems(t, "--all", "--json"))

	events := readRetroEvents(t, home)
	require.Len(t, events, 5, "3 parks + 1 resolve + 1 defer")
	byID := map[string][]retro.Retro{}
	for _, ev := range events {
		byID[ev.ID] = append(byID[ev.ID], ev)
	}
	// R-001: an open park followed by a resolve carrying the original resolution/date.
	require.Len(t, byID["R-001"], 2)
	var resolve *retro.Retro
	for i := range byID["R-001"] {
		if byID["R-001"][i].EventType == retro.EventResolve {
			resolve = &byID["R-001"][i]
		}
	}
	require.NotNil(t, resolve)
	assert.Equal(t, "adopted it", resolve.Resolution)
	assert.Equal(t, "2026-07-20", resolve.LogicalDate, "the resolve is attributed to the original resolved-date")

	// A subsequent everyday park continues at imported-max+1 = R-004.
	parkOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "park", "Parked after import")
	require.NoError(t, err)
	assert.Contains(t, parkOut, "Parked as R-004")
}

// TestRetro_CLI_ImportRequiresFile: `import` with no --file, or a path that does
// not exist, is a clean error that imports nothing.
func TestRetro_CLI_ImportRequiresFile(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "import")
	require.Error(t, err, "--file is required")

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "retro", "import", "--file", filepath.Join(home, "nope.json"))
	require.Error(t, err)
	assert.Contains(t, errOut, "could not read the import file")
	assert.Empty(t, readRetroEvents(t, home), "a missing import file writes nothing")
}
