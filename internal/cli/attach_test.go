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

// writeTempFile drops content at a fresh file in a temp dir and returns its
// path — the artifact `lucid attach` copies into the media store.
func writeTempFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

// mediaFileCount returns the number of stored binaries under home/media
// (excluding the .json sidecars) so a test can assert exactly one landed.
func mediaFileCount(t *testing.T, home string) int {
	t.Helper()
	var n int
	err := filepath.WalkDir(filepath.Join(home, "media"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !strings.HasSuffix(p, ".json") {
			n++
		}
		return nil
	})
	require.NoError(t, err)
	return n
}

// TestAttach_Registered confirms the verb is on the spine and self-documents in
// --help with its two capture flags (AC-1).
func TestAttach_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["attach"], "attach verb not registered")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "attach", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "attach")
	assert.Contains(t, out, "--caption")
	assert.Contains(t, out, "--day")
}

// TestAttach_CLI_FreshHost runs `lucid attach` on an un-scaffolded home: it
// auto-scaffolds the media store, copies the file, and acks with what was
// stored and where (AC-10) — end-to-end against an isolated LUCID_HOME.
func TestAttach_CLI_FreshHost(t *testing.T) {
	home := isolatedHome(t)
	path := writeTempFile(t, "photo.jpg", []byte("\xff\xd8\xff binary image bytes"))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "attach", path)
	require.NoError(t, err)
	assert.Contains(t, out, "Attached")
	assert.Contains(t, out, "sha256")
	assert.Contains(t, out, "logged as")

	assert.Equal(t, 1, mediaFileCount(t, home), "exactly one binary stored under media/")
}

// TestAttach_CLI_CaptionAndDay confirms --caption and --day parse and flow
// through: the caption rides the ack and the media attributes to yesterday
// (AC-10 + the shared rollover grammar, exercised via the CLI).
func TestAttach_CLI_CaptionAndDay(t *testing.T) {
	home := isolatedHome(t)
	path := writeTempFile(t, "page.pdf", []byte("%PDF-1.7 opaque non-image bytes"))

	out, _, err := runRoot(
		t, BuildInfo{Version: "dev"},
		"attach", path, "--caption", "handwritten notes", "--day", "@yesterday",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "Attached")
	assert.Equal(t, 1, mediaFileCount(t, home))
}

// TestAttach_CLI_JSON confirms the --json shape carries the fields a script
// needs to locate and verify the stored binary (AC-10).
func TestAttach_CLI_JSON(t *testing.T) {
	home := isolatedHome(t)
	path := writeTempFile(t, "artifact.bin", []byte("arbitrary opaque bytes"))

	out, _, err := runRoot(
		t, BuildInfo{Version: "dev"},
		"attach", path, "--caption", "whiteboard", "--json",
	)
	require.NoError(t, err)

	var res attachJSON
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	assert.NotEmpty(t, res.StoredPath)
	assert.Len(t, res.SHA256, 64, "sha256 is a full 64-hex-char digest in --json")
	assert.NotEmpty(t, res.Day)
	assert.Contains(t, res.RawID, "raw_")
	assert.Equal(t, "whiteboard", res.Caption)

	// The stored path the JSON reports is a real file on disk under this home.
	assert.True(t, strings.HasPrefix(res.StoredPath, home), "stored path is under LUCID_HOME")
	_, statErr := os.Stat(res.StoredPath)
	require.NoError(t, statErr)
}

// TestAttach_CLI_RequiresExactlyOnePath confirms ExactArgs(1): a bare `attach`
// and a two-arg `attach` are usage errors (exit 2), not runtime failures.
func TestAttach_CLI_RequiresExactlyOnePath(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "attach")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "attach", "a.jpg", "b.jpg")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestAttach_CLI_MissingFile confirms an unreadable path surfaces as a clean
// runtime error with nothing written (error-states.md §St-1), exercised through
// the CLI accept surface.
func TestAttach_CLI_MissingFile(t *testing.T) {
	home := isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "attach", filepath.Join(t.TempDir(), "nope.jpg"))
	require.Error(t, err)
	assert.Equal(t, 0, mediaFileCount(t, home), "nothing stored under media/ on a failed attach")
}

// TestAttach_CLI_ScaffoldMediaError confirms a media store that cannot be
// created — a regular file occupies its path — surfaces as a clean runtime
// error with nothing stored, exercising the ScaffoldMedia guard on the CLI
// attach path.
func TestAttach_CLI_ScaffoldMediaError(t *testing.T) {
	home := isolatedHome(t)
	require.NoError(t, os.MkdirAll(home, 0o700))
	// A regular file where the media/ dir must go makes its MkdirAll fail.
	require.NoError(t, os.WriteFile(filepath.Join(home, "media"), []byte("not a dir"), 0o600))

	path := writeTempFile(t, "photo.jpg", []byte("bytes"))
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "attach", path)
	require.Error(t, err)
}

// TestLog_HasNoMediaFlag guards AC-6: media is its own verb, so `lucid log`
// exposes no --media (or --attach) flag — the two capture concerns stay
// decoupled.
func TestLog_HasNoMediaFlag(t *testing.T) {
	logCmd := newLogCmd()
	assert.Nil(t, logCmd.Flags().Lookup("media"), "log must not carry a --media flag")
	assert.Nil(t, logCmd.Flags().Lookup("attach"), "log must not carry an --attach flag")
}
