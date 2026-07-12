package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawFileCount returns the number of raw entry files under home/raw.
func rawFileCount(t *testing.T, home string) int {
	t.Helper()
	var n int
	err := filepath.WalkDir(filepath.Join(home, "raw"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			n++
		}
		return nil
	})
	require.NoError(t, err)
	return n
}

// keepOnly asserts a Mirror subdirectory contains only its .keep marker
// (no capture wrote into it).
func keepOnly(t *testing.T, home, sub string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, sub))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, ".keep", entries[0].Name())
}

// TestLog_CLI_FreshHost runs `lucid log` on an un-scaffolded home: it
// auto-scaffolds, captures the entry, and acks — capture never blocks on
// setup.
func TestLog_CLI_FreshHost(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "Quiet day. Read for an hour.")
	require.NoError(t, err)
	assert.Contains(t, out, "Saved as `raw_")

	assert.Equal(t, 1, rawFileCount(t, home))
	keepOnly(t, home, "processed")
	keepOnly(t, home, "insights")
}

// TestLog_CLI_MultiWordArgs joins positional args into the entry body.
func TestLog_CLI_MultiWordArgs(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "read", "for", "an", "hour")
	require.NoError(t, err)

	files := findMarkdown(t, home)
	require.Len(t, files, 1)
	b, err := os.ReadFile(files[0])
	require.NoError(t, err)
	assert.Contains(t, string(b), "read for an hour")
}

// TestLog_CLI_EmptyBody runs `lucid log` with no text: a valid entry is
// still saved and the ack notes the empty body.
func TestLog_CLI_EmptyBody(t *testing.T) {
	home := isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log")
	require.NoError(t, err)
	assert.Contains(t, out, "body was empty")
	assert.Equal(t, 1, rawFileCount(t, home))
}

// TestLog_CLI_BootWarnings confirms a clip warning from an out-of-range
// lucid.json is surfaced on stderr during a `lucid log`.
func TestLog_CLI_BootWarnings(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)

	cfgPath := filepath.Join(home, "lucid.json")
	b, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	m["recent_window"] = 999
	edited, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, edited, 0o600))

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "hi")
	require.NoError(t, err)
	assert.Contains(t, errOut, "warning:")
	assert.Contains(t, errOut, "clipped to 14")
}

// TestLog_CLI_WriteError confirms a write failure propagates as a
// non-nil command error (error-states.md §St-1) through the CLI.
func TestLog_CLI_WriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)

	rawDir := filepath.Join(home, "raw")
	require.NoError(t, os.Chmod(rawDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(rawDir, 0o700) })

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "log", "nope")
	require.Error(t, err)
}

// findMarkdown returns every .md file under home/raw.
func findMarkdown(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(home, "raw"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			out = append(out, p)
		}
		return nil
	})
	require.NoError(t, err)
	return out
}

// readOnlyRaw returns the content of the single raw markdown entry under
// home/raw, failing if there is not exactly one.
func readOnlyRaw(t *testing.T, home string) string {
	t.Helper()
	files := findMarkdown(t, home)
	require.Len(t, files, 1)
	b, err := os.ReadFile(files[0])
	require.NoError(t, err)
	return string(b)
}

// readOnlySession decodes the single session record under home/sessions into a
// map so individual provenance fields can be asserted.
func readOnlySession(t *testing.T, home string) map[string]any {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "sessions"))
	require.NoError(t, err)
	var records []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			records = append(records, e.Name())
		}
	}
	require.Len(t, records, 1)
	b, err := os.ReadFile(filepath.Join(home, "sessions", records[0]))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

// TestLog_CLI_SourceFlag confirms `--source` records a non-cli harness token on
// the raw entry, proving the accept surface is wired end to end (AC-1).
func TestLog_CLI_SourceFlag(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "--source", "discord", "relayed capture")
	require.NoError(t, err)

	assert.Contains(t, readOnlyRaw(t, home), "source: discord")
}

// TestLog_CLI_SourceEnv confirms LUCID_SOURCE alone (no flag) sets the raw
// source, so a harness can attribute captures purely through the environment.
func TestLog_CLI_SourceEnv(t *testing.T) {
	home := isolatedHome(t)
	t.Setenv(envSource, "discord")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "relayed capture")
	require.NoError(t, err)

	assert.Contains(t, readOnlyRaw(t, home), "source: discord")
}

// TestLog_CLI_SourceFlagBeatsEnv confirms flag > env precedence: an explicit
// --source wins over LUCID_SOURCE.
func TestLog_CLI_SourceFlagBeatsEnv(t *testing.T) {
	home := isolatedHome(t)
	t.Setenv(envSource, "fromenv")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "--source", "fromflag", "relayed capture")
	require.NoError(t, err)

	raw := readOnlyRaw(t, home)
	assert.Contains(t, raw, "source: fromflag")
	assert.NotContains(t, raw, "source: fromenv")
}

// TestLog_CLI_BareStillDefaultsToCLI confirms backward compatibility (AC-5): a
// bare `lucid log` with no flags or env still writes source: cli and omits the
// optional agent/model provenance.
func TestLog_CLI_BareStillDefaultsToCLI(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "quiet terminal capture")
	require.NoError(t, err)

	assert.Contains(t, readOnlyRaw(t, home), "source: cli")

	sess := readOnlySession(t, home)
	assert.Equal(t, "cli", sess["harness"])
	assert.Equal(t, "cli", sess["channel_id"])
	_, hasAgent := sess["agent"]
	assert.False(t, hasAgent, "agent is omitted for a plain terminal capture")
	_, hasModel := sess["model"]
	assert.False(t, hasModel, "model is omitted for a plain terminal capture")
}

// TestLog_CLI_ProvenanceClusterOnSession confirms --harness/--agent/--model/
// --channel/--thread land on the session record's provenance cluster (AC-2,
// AC-4).
func TestLog_CLI_ProvenanceClusterOnSession(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(
		t, BuildInfo{Version: "dev"}, "log",
		"--harness", "discord",
		"--agent", "agent-x",
		"--model", "model-y",
		"--channel", "channel-z",
		"--thread", "thread-1",
		"relayed by an assistant",
	)
	require.NoError(t, err)

	sess := readOnlySession(t, home)
	assert.Equal(t, "discord", sess["harness"])
	assert.Equal(t, "agent-x", sess["agent"])
	assert.Equal(t, "model-y", sess["model"])
	assert.Equal(t, "channel-z", sess["channel_id"])
	assert.Equal(t, "thread-1", sess["thread_id"])
}

// TestLog_CLI_ProvenanceEnvFallback confirms every provenance field also
// resolves from its LUCID_* env fallback when no flag is passed.
func TestLog_CLI_ProvenanceEnvFallback(t *testing.T) {
	home := isolatedHome(t)
	t.Setenv(envHarness, "discord")
	t.Setenv(envAgent, "agent-x")
	t.Setenv(envModel, "model-y")
	t.Setenv(envChannel, "channel-z")
	t.Setenv(envThread, "thread-1")

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "relayed via env")
	require.NoError(t, err)

	sess := readOnlySession(t, home)
	assert.Equal(t, "discord", sess["harness"])
	assert.Equal(t, "agent-x", sess["agent"])
	assert.Equal(t, "model-y", sess["model"])
	assert.Equal(t, "channel-z", sess["channel_id"])
	assert.Equal(t, "thread-1", sess["thread_id"])
}

// TestLog_CLI_MalformedSourceRejected confirms a malformed --source token is
// rejected honestly (never coerced to cli) and leaves nothing on disk (AC-8
// via the router, exercised through the CLI accept surface).
func TestLog_CLI_MalformedSourceRejected(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "log", "--source", "bad token!", "should not land")
	require.Error(t, err)

	assert.Equal(t, 0, rawFileCount(t, home), "nothing written under raw/")

	entries, readErr := os.ReadDir(filepath.Join(home, "sessions"))
	require.NoError(t, readErr)
	for _, e := range entries {
		assert.Falsef(t, strings.HasSuffix(e.Name(), ".json"), "no dangling session record, found %s", e.Name())
	}
}
