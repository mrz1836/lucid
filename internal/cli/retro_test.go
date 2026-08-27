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

// TestRetroCmd_HasParkSubcommand guards the retro command tree: a dropped
// registration would ship a missing verb silently, since the group itself still
// renders. Later build stages add list/show/resolve/defer.
func TestRetroCmd_HasParkSubcommand(t *testing.T) {
	got := map[string]bool{}
	for _, c := range newRetroCmd().Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["park"], "retro group missing park")
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
