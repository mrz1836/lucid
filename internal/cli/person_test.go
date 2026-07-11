package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// personSeed is a deterministic instant the /person CLI fixtures anchor on, so
// the rendered dates (and the --json first_seen_at) are stable across runs.
func personSeed() time.Time { return time.Date(2026, time.May, 5, 19, 42, 11, 0, time.UTC) }

// writePersonRecord writes a people/<key>.json record directly, mirroring
// seedStormClauses: the CLI seeds the store shape it needs (a match, an aka
// shared by two referents for §P-2) before the command boots. Scaffold leaves
// existing entries untouched, so the seed survives bootedRouter.
func writePersonRecord(t *testing.T, home, key, display string, aka, entryRefs []string, seen time.Time) {
	t.Helper()
	dir := filepath.Join(home, "people")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	ts := seen.Format(time.RFC3339)
	rec := map[string]any{
		"person_key":    key,
		"display_name":  display,
		"aka":           aka,
		"first_seen_at": ts,
		"last_seen_at":  ts,
		"entry_refs":    entryRefs,
		"notes":         nil,
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, key+".json"), b, 0o600))
}

// writeOffLimits marks the given person keys off-limits by writing the registry
// directly at the Ledger root. Scaffold never touches off_limits.json, so the
// seed survives.
func writeOffLimits(t *testing.T, home string, keys ...string) {
	t.Helper()
	b, err := json.MarshalIndent(map[string]any{"person_keys": keys}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "off_limits.json"), b, 0o600))
}

// TestPersonCLI_NoMatch is §P-1: an unknown name is a read outcome, not an
// error — the empty-state prose on stdout and a clean exit 0.
func TestPersonCLI_NoMatch(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "Nobody")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, "No one by that name yet")
}

// TestPersonCLI_NoMatchJSON: the --json view carries matched:false with an empty
// candidates array (never null) and still exits 0.
func TestPersonCLI_NoMatchJSON(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "Nobody", "--json")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))

	var v personView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.Equal(t, "Nobody", v.Query)
	assert.False(t, v.Matched)
	assert.False(t, v.MultipleMatches)
	assert.NotNil(t, v.Candidates)
	assert.Empty(t, v.Candidates)
	// candidates must serialize as [] not null so a harness can index it.
	assert.Contains(t, out, `"candidates": []`)
}

// TestPersonCLI_SingleMatch: a single match renders the full view and exits 0.
func TestPersonCLI_SingleMatch(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_m", "M.", []string{"M.", "M"}, []string{"raw_2026_05_05_19_42"}, personSeed())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "M.")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, "M.")
	assert.Contains(t, out, "Mentioned in 1 entry")
}

// TestPersonCLI_SingleMatchJSON: the --json view carries matched:true and the
// resolved person_key.
func TestPersonCLI_SingleMatchJSON(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_m", "M.", []string{"M.", "M"}, []string{"raw_2026_05_05_19_42"}, personSeed())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "M.", "--json")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))

	var v personView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.True(t, v.Matched)
	assert.False(t, v.MultipleMatches)
	assert.False(t, v.OffLimits)
	assert.Equal(t, "person_m", v.PersonKey)
}

// TestPersonCLI_MultiMatch is §P-2: a name shared by two records lists the
// candidates and exits 0 — ambiguity is a read outcome, not an error.
func TestPersonCLI_MultiMatch(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"A.", "Alex"}, []string{"raw_2026_05_05_19_42"}, personSeed())
	writePersonRecord(t, home, "person_a-andy", "Andy", []string{"A.", "Andy"}, []string{"raw_2026_05_06_08_10"}, personSeed().Add(24*time.Hour))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "A.")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, "more than one person")
	assert.Contains(t, out, "Alex")
	assert.Contains(t, out, "Andy")
}

// TestPersonCLI_MultiMatchJSON: the --json view carries multiple_matches:true
// with both candidates (sorted by person_key) and their first-seen dates.
func TestPersonCLI_MultiMatchJSON(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"A.", "Alex"}, []string{"raw_2026_05_05_19_42"}, personSeed())
	writePersonRecord(t, home, "person_a-andy", "Andy", []string{"A.", "Andy"}, []string{"raw_2026_05_06_08_10"}, personSeed().Add(24*time.Hour))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "A.", "--json")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))

	var v personView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.False(t, v.Matched)
	assert.True(t, v.MultipleMatches)
	require.Len(t, v.Candidates, 2)
	assert.Equal(t, "person_a-alex", v.Candidates[0].PersonKey)
	assert.Equal(t, "Alex", v.Candidates[0].DisplayName)
	assert.Equal(t, personSeed().Format("2006-01-02"), v.Candidates[0].FirstSeenAt)
	assert.Equal(t, "person_a-andy", v.Candidates[1].PersonKey)
}

// TestPersonCLI_OffLimits is §P-3: an off-limits person renders the
// raw-record-only view and exits 0, with nothing derived.
func TestPersonCLI_OffLimits(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_m", "M.", []string{"M."}, []string{"raw_2026_05_05_19_42"}, personSeed())
	writeOffLimits(t, home, "person_m")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "M.", "--json")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))

	var v personView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.True(t, v.Matched)
	assert.True(t, v.OffLimits)
	assert.Contains(t, v.Text, "off-limits to inference")
	assert.Contains(t, v.Text, "raw record only")
	assert.NotContains(t, v.Text, "accepted insights")
}

// TestPersonCLI_JoinsSpacedName proves trailing args are joined: a two-word
// display name resolves only when "Mary Jane" is treated as one query (a
// non-joining verb would pass just "Mary" → no match).
func TestPersonCLI_JoinsSpacedName(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_m-mary", "Mary Jane", []string{"Mary Jane"}, []string{"raw_2026_05_05_19_42"}, personSeed())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "Mary", "Jane")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, "Mary Jane")
	assert.Contains(t, out, "Mentioned in 1 entry")
}

// TestPersonCLI_NoArgUsage: a bare `lucid person` is a cobra usage error (exit
// 2), distinct from the read-never-fails contract.
func TestPersonCLI_NoArgUsage(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestPersonCLI_CorruptRecordSurfaced: a genuine read failure (a malformed
// people record) surfaces as a real error / exit 1 — a read outcome is exit 0,
// but a real I/O failure is not.
func TestPersonCLI_CorruptRecordSurfaced(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, "people")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "person_m.json"), []byte("{bad"), 0o600))

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "M.")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
}

// TestPersonCLI_BootError: a Ledger that cannot be scaffolded surfaces the boot
// error rather than a false read.
func TestPersonCLI_BootError(t *testing.T) {
	unscaffoldableHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "M.")
	require.Error(t, err)
}
