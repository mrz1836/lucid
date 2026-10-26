package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/rep"
)

// readRepEntries walks the rep tree under home and returns every appended entry
// (reps and retirement markers), so a CLI test can assert what actually landed
// on disk. It skips the surface-state projection (surface_state.json is not a
// rep_*.jsonl day file).
func readRepEntries(t *testing.T, home string) []rep.Rep {
	t.Helper()
	var out []rep.Rep
	root := filepath.Join(home, "reps")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return filepath.SkipDir
			}
			return werr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "rep_") || !strings.HasSuffix(p, ".jsonl") {
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
			e, uerr := rep.UnmarshalRepLine([]byte(line))
			if uerr != nil {
				return uerr
			}
			out = append(out, e)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return out
}

// repListPayload is the `rep list --json` shape the CLI tests decode.
type repListPayload struct {
	Count int `json:"count"`
	Reps  []struct {
		ID    string `json:"id"`
		Text  string `json:"text"`
		Solo  string `json:"solo"`
		Level int    `json:"level"`
		State string `json:"state"`
	} `json:"reps"`
}

func listReps(t *testing.T, extra ...string) repListPayload {
	t.Helper()
	args := append([]string{"rep", "list", "--json"}, extra...)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoError(t, err)
	var payload repListPayload
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	return payload
}

// TestRep_CLI_AddReturnsReceipt: `rep add` prints the receipt id and the entry
// lands on disk verbatim, with its solo variant and level.
func TestRep_CLI_AddReturnsReceipt(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"rep", "add", "Ask one question in a meeting", "--solo", "Send the question afterward", "--level", "2")
	require.NoError(t, err)
	assert.Contains(t, out, "Added rep as `rep_")

	entries := readRepEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "Ask one question in a meeting", entries[0].Text)
	assert.Equal(t, "Send the question afterward", entries[0].Solo)
	assert.Equal(t, 2, entries[0].Level)
	assert.Equal(t, rep.SourceRep, entries[0].Source)
	assert.Equal(t, rep.Schema, entries[0].Schema)
	assert.Equal(t, rep.StateActive, entries[0].State)
	assert.True(t, strings.HasPrefix(entries[0].ID, "rep_"), "receipt id has the rep_ prefix")
	assert.Contains(t, out, entries[0].ID, "the ack carries the stored id")
}

// TestRep_CLI_AddJoinsUnquotedText: an unquoted multi-word rep is joined into a
// single text field; no --solo / --level leaves them empty / unlevelled.
func TestRep_CLI_AddJoinsUnquotedText(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "add", "Try", "one", "new", "route")
	require.NoError(t, err)

	entries := readRepEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "Try one new route", entries[0].Text)
	assert.Empty(t, entries[0].Solo, "no --solo leaves the solo variant empty")
	assert.Equal(t, 0, entries[0].Level, "no --level leaves the rep unlevelled")
}

// TestRep_CLI_RequiresText: a bare `rep add` is a usage error and nothing is
// written.
func TestRep_CLI_RequiresText(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "add")
	require.Error(t, err)
	assert.Empty(t, readRepEntries(t, home), "an add with no text writes nothing")
}

// TestRep_CLI_LevelFlagValidation: an explicitly given --level must be a positive
// whole number — 0, a negative, or a non-integer is a usage error that writes
// nothing — while a valid level is stored as given.
func TestRep_CLI_LevelFlagValidation(t *testing.T) {
	home := isolatedHome(t)

	for _, bad := range [][]string{
		{"--level", "0"},
		{"--level=-1"},
		{"--level", "-1"},
		{"--level", "two"},
	} {
		args := append([]string{"rep", "add", "A practice"}, bad...)
		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
		require.Errorf(t, err, "%v must be refused", bad)
		assert.Equalf(t, ExitUsage, exitCodeForError(err), "%v is a usage error", bad)
	}
	assert.Empty(t, readRepEntries(t, home), "a refused level writes nothing")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "add", "A practice", "--level", "6")
	require.NoError(t, err)
	entries := readRepEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, 6, entries[0].Level)
}

// TestRep_CLI_ListJSONShape: `list --json` emits {count, reps:[…]} with the folded
// live pool, exposing ids, solo and level.
func TestRep_CLI_ListJSONShape(t *testing.T) {
	isolatedHome(t)

	empty := listReps(t)
	assert.Equal(t, 0, empty.Count)
	assert.NotNil(t, empty.Reps, "an empty pool is reps: [], never null")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "add", "First practice", "--solo", "Alone version", "--level", "4")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "rep", "add", "Second practice")
	require.NoError(t, err)

	payload := listReps(t)
	assert.Equal(t, 2, payload.Count)
	require.Len(t, payload.Reps, 2)
	assert.Equal(t, "First practice", payload.Reps[0].Text)
	assert.Equal(t, "Alone version", payload.Reps[0].Solo)
	assert.Equal(t, 4, payload.Reps[0].Level)
	assert.Equal(t, rep.StateActive, payload.Reps[0].State)
	assert.True(t, strings.HasPrefix(payload.Reps[0].ID, "rep_"))
	assert.Equal(t, 0, payload.Reps[1].Level)
}

// TestRep_CLI_ListActiveDefaultAllIncludesRetired: after retiring a rep the
// default list drops it and --all (or --include-retired) restores it.
func TestRep_CLI_ListActiveDefaultAllIncludesRetired(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "add", "Keep me")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "rep", "add", "Retire me")
	require.NoError(t, err)

	var retireID string
	for _, e := range listReps(t).Reps {
		if e.Text == "Retire me" {
			retireID = e.ID
		}
	}
	require.NotEmpty(t, retireID)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "retire", retireID)
	require.NoError(t, err)
	assert.Contains(t, out, "Retired `"+retireID+"`")

	assert.Equal(t, 1, listReps(t).Count, "the retired rep drops out of the default list")
	all := listReps(t, "--all")
	assert.Equal(t, 2, all.Count, "the audit view keeps the retired rep")
	assert.Equal(t, 2, listReps(t, "--include-retired").Count, "--include-retired is an alias of --all")
	for _, e := range all.Reps {
		if e.ID == retireID {
			assert.Equal(t, rep.StateRetired, e.State)
		}
	}

	human, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "list", "--all")
	require.NoError(t, err)
	assert.Contains(t, human, "(retired)")
}

// TestRep_CLI_SurfaceReturnsOnePickIdempotentWithinDay: surface returns exactly
// one rep and the same pick on a repeated same-day call.
func TestRep_CLI_SurfaceReturnsOnePickIdempotentWithinDay(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "add", "First practice")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "rep", "add", "Second practice")
	require.NoError(t, err)

	first, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "surface")
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(strings.TrimSpace(first), "\n")+1, "surface prints exactly one line")

	second, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "surface")
	require.NoError(t, err)
	assert.Equal(t, first, second, "a repeated same-day surface returns the same pick")
}

// TestRep_CLI_SurfaceShowsSoloVariant: the prose line carries the level and the
// solo variant after `Solo:`.
func TestRep_CLI_SurfaceShowsSoloVariant(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"rep", "add", "Ask one question in a meeting", "--solo", "Send the question afterward", "--level", "3")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "surface")
	require.NoError(t, err)
	assert.Equal(t, "Ask one question in a meeting (level 3) — Solo: Send the question afterward", strings.TrimSpace(out))
}

// TestRep_CLI_SurfaceEmptyPool: surface on an empty pool is a clean hint (and a
// null rep under --json).
func TestRep_CLI_SurfaceEmptyPool(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "surface")
	require.NoError(t, err)
	assert.Contains(t, out, "No reps to surface yet")

	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "surface", "--json")
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &payload))
	assert.JSONEq(t, "null", string(payload["rep"]))
}

// TestRep_CLI_SurfaceJSONShape: `surface --json` emits exactly {day, rep:{…}},
// the rep carrying its solo variant and level.
func TestRep_CLI_SurfaceJSONShape(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"rep", "add", "Only practice", "--solo", "Only practice, alone", "--level", "5")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "surface", "--json")
	require.NoError(t, err)

	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &keys))
	assert.Len(t, keys, 2, "surface --json carries exactly day and rep")

	var payload struct {
		Day string `json:"day"`
		Rep *struct {
			ID    string `json:"id"`
			Text  string `json:"text"`
			Solo  string `json:"solo"`
			Level int    `json:"level"`
		} `json:"rep"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.NotEmpty(t, payload.Day)
	require.NotNil(t, payload.Rep)
	assert.Equal(t, "Only practice", payload.Rep.Text)
	assert.Equal(t, "Only practice, alone", payload.Rep.Solo)
	assert.Equal(t, 5, payload.Rep.Level)
	assert.True(t, strings.HasPrefix(payload.Rep.ID, "rep_"))
}

// TestRep_CLI_DayBackdatesExplicitDate: `--day @YYYY-MM-DD` files the entry under
// that literal civil day.
func TestRep_CLI_DayBackdatesExplicitDate(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"rep", "add", "Backdated practice", "--day", "@2026-06-01")
	require.NoError(t, err)

	entries := readRepEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "2026-06-01", entries[0].LogicalDate)
	assert.Equal(t, "rep_2026_06_01_001", entries[0].ID)
}

// TestRep_CLI_DayStrictRejectWritesNothing: a `--day` token the grammar cannot
// read is a clean refusal that names the accepted forms on stderr and writes
// nothing (strict tier).
func TestRep_CLI_DayStrictRejectWritesNothing(t *testing.T) {
	home := isolatedHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"},
		"rep", "add", "A practice", "--day", "@yesterdya")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
	assert.Empty(t, out)
	assert.Contains(t, errOut, "could not read the day", "the reason reaches the user, not just the exit code")
	assert.Empty(t, readRepEntries(t, home), "a refused day writes nothing")
}

// TestRep_CLI_RetireUnknownRejected: retiring an id no rep holds is an error
// printed to stderr, and nothing is appended.
func TestRep_CLI_RetireUnknownRejected(t *testing.T) {
	home := isolatedHome(t)

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "rep", "retire", "rep_2026_06_01_999")
	require.Error(t, err)
	assert.NotEmpty(t, errOut, "the reason reaches stderr")
	assert.Empty(t, readRepEntries(t, home), "a rejected retire writes nothing")
}
