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
