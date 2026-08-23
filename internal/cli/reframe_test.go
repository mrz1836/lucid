package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/reframes"
)

// readReframeEntries walks the reframes tree under home and returns every
// appended entry, so a CLI test can assert what actually landed on disk. It
// skips the surface-state projection (surface_state.json is not a
// reframe_*.jsonl day file).
func readReframeEntries(t *testing.T, home string) []reframes.Reframe {
	t.Helper()
	var out []reframes.Reframe
	root := filepath.Join(home, "reframes")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return filepath.SkipDir
			}
			return werr
		}
		if d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
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
			rf, uerr := reframes.UnmarshalReframeLine([]byte(line))
			if uerr != nil {
				return uerr
			}
			out = append(out, rf)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return out
}

// TestReframe_CLI_AddReturnsReceipt: `reframe add` prints the receipt id and the
// entry lands on disk verbatim (AC-N2).
func TestReframe_CLI_AddReturnsReceipt(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "add", "I can't do this", "I can learn this")
	require.NoError(t, err)
	assert.Contains(t, out, "Added reframe as `reframe_")

	entries := readReframeEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "I can't do this", entries[0].Catch)
	assert.Equal(t, "I can learn this", entries[0].Flip)
	assert.Equal(t, reframes.SourceReframe, entries[0].Source)
	assert.Equal(t, reframes.Schema, entries[0].Schema)
	assert.True(t, strings.HasPrefix(entries[0].ID, "reframe_"), "receipt id has the reframe_ prefix")
}

// TestReframe_CLI_RequiresBothArgs: a missing flip is a usage error (both
// positionals required), and nothing is written.
func TestReframe_CLI_RequiresBothArgs(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "add", "only a catch")
	require.Error(t, err)
	assert.Empty(t, readReframeEntries(t, home), "an incomplete add writes nothing")
}

// TestReframe_CLI_EmptyPairRejected: an explicitly empty flip is a clean error
// and nothing lands (reframes.md §3).
func TestReframe_CLI_EmptyPairRejected(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "add", "a catch", "")
	require.Error(t, err)
	assert.Empty(t, readReframeEntries(t, home), "an empty flip writes nothing")
}

// TestReframe_CLI_ListJSONShape: `list --json` emits {count, reframes:[…]} with
// the folded live pool, unmarshaling to the expected shape (AC-N3).
func TestReframe_CLI_ListJSONShape(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "add", "I always mess up", "I'm still practicing")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "reframe", "add", "This is too hard", "This is worth the effort")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "list", "--json")
	require.NoError(t, err)

	var payload struct {
		Count    int `json:"count"`
		Reframes []struct {
			ID    string `json:"id"`
			Catch string `json:"catch"`
			Flip  string `json:"flip"`
		} `json:"reframes"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, 2, payload.Count)
	require.Len(t, payload.Reframes, 2)
	assert.Equal(t, "I always mess up", payload.Reframes[0].Catch)
	assert.Equal(t, "I'm still practicing", payload.Reframes[0].Flip)
	assert.True(t, strings.HasPrefix(payload.Reframes[0].ID, "reframe_"))
}

// TestReframe_CLI_ListEmpty: `list` on a cold home is a clean hint, not a crash.
func TestReframe_CLI_ListEmpty(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "No reframes yet")
}

// TestReframe_CLI_SurfaceReturnsOnePickIdempotentWithinDay: surface returns
// exactly one reframe and the same pick on a repeated same-day call (AC-N4). The
// two calls fall in the same logical day, so the recorded pick is returned
// unchanged.
func TestReframe_CLI_SurfaceReturnsOnePickIdempotentWithinDay(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "add", "I can't do this", "I can learn this")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "reframe", "add", "This is too hard", "This is worth the effort")
	require.NoError(t, err)

	first, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "surface")
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(strings.TrimSpace(first), "\n")+1, "surface prints exactly one line")
	assert.Contains(t, first, "→")

	second, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "surface")
	require.NoError(t, err)
	assert.Equal(t, first, second, "a repeated same-day surface returns the same pick")
}

// TestReframe_CLI_SurfaceEmptyPool: surface on an empty pool is a clean hint.
func TestReframe_CLI_SurfaceEmptyPool(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "surface")
	require.NoError(t, err)
	assert.Contains(t, out, "No reframes to surface yet")
}

// TestReframe_CLI_SurfaceJSONShape: `surface --json` emits {day, reframe:{…}}.
func TestReframe_CLI_SurfaceJSONShape(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "add", "I can't do this", "I can learn this")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "reframe", "surface", "--json")
	require.NoError(t, err)

	var payload struct {
		Day     string `json:"day"`
		Reframe *struct {
			ID    string `json:"id"`
			Catch string `json:"catch"`
			Flip  string `json:"flip"`
		} `json:"reframe"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.NotEmpty(t, payload.Day)
	require.NotNil(t, payload.Reframe)
	assert.Equal(t, "I can't do this", payload.Reframe.Catch)
}

// TestReframe_CLI_DayBackdatesYesterday: `--day @yesterday` sets logical_date to
// the prior logical day while recorded_at stays the real write time (AC-N5).
func TestReframe_CLI_DayBackdatesYesterday(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"reframe", "add", "This is too hard", "This is worth the effort", "--day", "@yesterday")
	require.NoError(t, err)

	wantDay := observations.DateString(
		observations.LogicalBaseDate(time.Now(), observations.DefaultRolloverMin).AddDate(0, 0, -1),
	)
	entries := readReframeEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, wantDay, entries[0].LogicalDate, "logical_date is yesterday's logical day")
	assert.NotEmpty(t, entries[0].RecordedAt, "recorded_at is the real write time")
	assert.Contains(t, entries[0].ID, strings.ReplaceAll(wantDay, "-", "_"), "the id encodes the logical date")
}

// TestReframe_CLI_DayBackdatesExplicitDate: `--day @YYYY-MM-DD` files the entry
// under that literal civil day (AC-N5).
func TestReframe_CLI_DayBackdatesExplicitDate(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"reframe", "add", "I always mess up", "I'm still practicing", "--day", "2026-06-01")
	require.NoError(t, err)

	entries := readReframeEntries(t, home)
	require.Len(t, entries, 1)
	assert.Equal(t, "2026-06-01", entries[0].LogicalDate)
	assert.Equal(t, "reframe_2026_06_01_001", entries[0].ID)
}

// TestReframe_CLI_DayStrictRejectWritesNothing: a `--day` token the grammar
// cannot read is a clean refusal that names the accepted forms on stderr and
// writes nothing (AC-N5, strict tier). Execute renders no returned error, so the
// reason must reach stderr, not just the exit code.
func TestReframe_CLI_DayStrictRejectWritesNothing(t *testing.T) {
	home := isolatedHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"},
		"reframe", "add", "I can't do this", "I can learn this", "--day", "@yesterdya")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
	assert.Contains(t, err.Error(), "nothing was saved")
	assert.Empty(t, out)
	assert.Contains(t, errOut, "could not read the day", "the reason reaches the user, not just the exit code")
	assert.Empty(t, readReframeEntries(t, home), "a refused day writes nothing")
}

// TestReframe_CLI_DayFutureRejected: a future `--day` is refused, naming the day,
// and nothing lands.
func TestReframe_CLI_DayFutureRejected(t *testing.T) {
	home := isolatedHome(t)

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"},
		"reframe", "add", "x", "y", "--day", "2999-01-01")
	require.Error(t, err)
	assert.Contains(t, errOut, "2999-01-01")
	assert.Contains(t, errOut, "has not happened yet")
	assert.Empty(t, readReframeEntries(t, home), "a future day writes nothing")
}
