package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/focus"
)

// readFocusEntries walks the focus tree under home and returns every appended
// entry (items and retirement markers), so a CLI test can assert what actually
// landed on disk. It skips the surface-state projection (surface_state.json is
// not a focus_*.jsonl day file).
func readFocusEntries(t *testing.T, home string) []focus.Focus {
	t.Helper()
	var out []focus.Focus
	root := filepath.Join(home, "focus")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return filepath.SkipDir
			}
			return werr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "focus_") || !strings.HasSuffix(p, ".jsonl") {
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
			f, uerr := focus.UnmarshalFocusLine([]byte(line))
			if uerr != nil {
				return uerr
			}
			out = append(out, f)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return out
}

// TestFocus_CLI_AddReturnsReceipt: `focus add` prints the receipt id and the
// entry lands on disk verbatim, with the success criterion (AC-2).
func TestFocus_CLI_AddReturnsReceipt(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"focus", "add", "Take a walk after lunch", "--success", "I stepped outside")
	require.NoError(t, err)
	assert.Contains(t, out, "Added focus as `focus_")

	entries := readFocusEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "Take a walk after lunch", entries[0].Text)
	assert.Equal(t, "I stepped outside", entries[0].SuccessCriterion)
	assert.Equal(t, focus.SourceFocus, entries[0].Source)
	assert.Equal(t, focus.Schema, entries[0].Schema)
	assert.Equal(t, focus.StateActive, entries[0].State)
	assert.True(t, strings.HasPrefix(entries[0].ID, "focus_"), "receipt id has the focus_ prefix")
}

// TestFocus_CLI_AddJoinsUnquotedText: an unquoted multi-word work-on is joined
// into a single text field (no quotes required).
func TestFocus_CLI_AddJoinsUnquotedText(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "add", "Take", "a", "short", "walk")
	require.NoError(t, err)

	entries := readFocusEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "Take a short walk", entries[0].Text)
	assert.Empty(t, entries[0].SuccessCriterion, "no --success leaves the criterion empty")
}

// TestFocus_CLI_RequiresText: a bare `focus add` is a usage error and nothing is
// written.
func TestFocus_CLI_RequiresText(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "add")
	require.Error(t, err)
	assert.Empty(t, readFocusEntries(t, home), "an add with no text writes nothing")
}

// TestFocus_CLI_ListJSONShape: `list --json` emits {count, focus:[…]} with the
// folded live pool, unmarshaling to the expected shape (AC-3).
func TestFocus_CLI_ListJSONShape(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "add", "First work-on")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "focus", "add", "Second work-on")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "list", "--json")
	require.NoError(t, err)

	var payload struct {
		Count int `json:"count"`
		Focus []struct {
			ID    string `json:"id"`
			Text  string `json:"text"`
			State string `json:"state"`
		} `json:"focus"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, 2, payload.Count)
	require.Len(t, payload.Focus, 2)
	assert.Equal(t, "First work-on", payload.Focus[0].Text)
	assert.Equal(t, focus.StateActive, payload.Focus[0].State)
	assert.True(t, strings.HasPrefix(payload.Focus[0].ID, "focus_"))
}

// TestFocus_CLI_ListActiveDefaultAllIncludesRetired: after retiring an item the
// default list drops it and --all restores it (AC-3, AC-5).
func TestFocus_CLI_ListActiveDefaultAllIncludesRetired(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "add", "Keep me")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "focus", "add", "Retire me")
	require.NoError(t, err)

	// Resolve the id of the second item from the JSON list, then retire it.
	listOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "list", "--json")
	require.NoError(t, err)
	var listed struct {
		Focus []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"focus"`
	}
	require.NoError(t, json.Unmarshal([]byte(listOut), &listed))
	var retireID string
	for _, f := range listed.Focus {
		if f.Text == "Retire me" {
			retireID = f.ID
		}
	}
	require.NotEmpty(t, retireID)

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "focus", "retire", retireID)
	require.NoError(t, err)

	active, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "list", "--json")
	require.NoError(t, err)
	var activeView struct {
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal([]byte(active), &activeView))
	assert.Equal(t, 1, activeView.Count, "the retired item drops out of the default list")

	all, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "list", "--all", "--json")
	require.NoError(t, err)
	var allView struct {
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal([]byte(all), &allView))
	assert.Equal(t, 2, allView.Count, "the audit view keeps the retired item")
}

// TestFocus_CLI_ListEmpty: `list` on a cold home is a clean hint, not a crash.
func TestFocus_CLI_ListEmpty(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "No active focus items")
}

// TestFocus_CLI_SurfaceReturnsOnePickIdempotentWithinDay: surface returns exactly
// one item and the same pick on a repeated same-day call (AC-4).
func TestFocus_CLI_SurfaceReturnsOnePickIdempotentWithinDay(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "add", "First work-on")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "focus", "add", "Second work-on")
	require.NoError(t, err)

	first, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "surface")
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(strings.TrimSpace(first), "\n")+1, "surface prints exactly one line")

	second, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "surface")
	require.NoError(t, err)
	assert.Equal(t, first, second, "a repeated same-day surface returns the same pick")
}

// TestFocus_CLI_SurfaceEmptyPool: surface on an empty pool is a clean hint.
func TestFocus_CLI_SurfaceEmptyPool(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "surface")
	require.NoError(t, err)
	assert.Contains(t, out, "No focus items to surface yet")
}

// TestFocus_CLI_SurfaceJSONShape: `surface --json` emits {day, focus:{…}}.
func TestFocus_CLI_SurfaceJSONShape(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "add", "Only work-on")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "surface", "--json")
	require.NoError(t, err)

	var payload struct {
		Day   string `json:"day"`
		Focus *struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"focus"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.NotEmpty(t, payload.Day)
	require.NotNil(t, payload.Focus)
	assert.Equal(t, "Only work-on", payload.Focus.Text)
}

// TestFocus_CLI_DayBackdatesExplicitDate: `--day @YYYY-MM-DD` files the entry
// under that literal civil day (AC-6).
func TestFocus_CLI_DayBackdatesExplicitDate(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"focus", "add", "Backdated work-on", "--day", "2026-06-01")
	require.NoError(t, err)

	entries := readFocusEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "2026-06-01", entries[0].LogicalDate)
	assert.Equal(t, "focus_2026_06_01_001", entries[0].ID)
}

// TestFocus_CLI_DayStrictRejectWritesNothing: a `--day` token the grammar cannot
// read is a clean refusal that names the accepted forms on stderr and writes
// nothing (AC-6, strict tier).
func TestFocus_CLI_DayStrictRejectWritesNothing(t *testing.T) {
	home := isolatedHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"},
		"focus", "add", "Work-on", "--day", "@yesterdya")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
	assert.Empty(t, out)
	assert.Contains(t, errOut, "could not read the day", "the reason reaches the user, not just the exit code")
	assert.Empty(t, readFocusEntries(t, home), "a refused day writes nothing")
}

// TestFocus_CLI_RetireUnknownRejected: retiring an id no item holds is an error
// printed to stderr, and nothing is appended.
func TestFocus_CLI_RetireUnknownRejected(t *testing.T) {
	home := isolatedHome(t)

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "retire", "focus_2026_06_01_999")
	require.Error(t, err)
	assert.NotEmpty(t, errOut, "the reason reaches stderr")
	assert.Empty(t, readFocusEntries(t, home), "a rejected retire writes nothing")
}
