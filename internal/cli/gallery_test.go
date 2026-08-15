package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGallery_Registered confirms the read-only timeline verb is on the spine
// and self-documents its window + subject flags in --help.
func TestGallery_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	assert.True(t, got["gallery"], "gallery verb not registered")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gallery", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "gallery")
	assert.Contains(t, out, "Read-only", "the help names it read-only")
	assert.Contains(t, out, "--since")
	assert.Contains(t, out, "--until")
	assert.Contains(t, out, "--to")
}

// TestGallery_EmptyStore prints the calm fallback when nothing is stored yet.
func TestGallery_EmptyStore(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gallery")
	require.NoError(t, err)
	assert.Contains(t, out, "No media in view")
}

// TestGallery_JSONProjection proves --json emits the full nine-field sidecar
// projection plus the derived stored_path, all present on the wire (AC-10).
func TestGallery_JSONProjection(t *testing.T) {
	isolatedHome(t)
	path := writeTempFile(t, "baseline.jpg", []byte("\xff\xd8\xff synthetic baseline bytes"))
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "attach", path, "--caption", "morning baseline")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gallery", "--json")
	require.NoError(t, err)

	var view galleryView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.Len(t, view.Items, 1)
	it := view.Items[0]
	assert.NotEmpty(t, it.ID)
	assert.Len(t, it.SHA256, 64, "sha256 is a full 64-hex-char digest")
	assert.Equal(t, "baseline.jpg", it.OriginalFilename)
	assert.NotEmpty(t, it.CapturedAt)
	assert.NotEmpty(t, it.LogicalDay)
	assert.Equal(t, "morning baseline", it.Caption)
	assert.Contains(t, it.RawEntryID, "raw_")
	assert.NotEmpty(t, it.Source)
	assert.NotEmpty(t, it.StoredPath)

	// Every one of the nine sidecar fields (including the nullable alt) plus the
	// derived stored_path is present in the wire shape — the stable projection.
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	require.Len(t, raw.Items, 1)
	for _, k := range []string{
		"id", "sha256", "original_filename", "captured_at", "logical_day",
		"caption", "alt", "raw_entry_id", "source", "stored_path",
	} {
		_, ok := raw.Items[0][k]
		assert.Truef(t, ok, "projection is missing %q", k)
	}
}

// TestGallery_PlainColumns proves the plain output shows logical_day + caption +
// path and is Discord-friendly (no markdown table) (AC-10).
func TestGallery_PlainColumns(t *testing.T) {
	isolatedHome(t)
	path := writeTempFile(t, "progress.jpg", []byte("\xff\xd8\xff synthetic progress bytes"))
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "attach", path, "--caption", "week two", "--json")
	require.NoError(t, err)
	var att attachJSON
	require.NoError(t, json.Unmarshal([]byte(out), &att))

	got, _, err := runRoot(t, BuildInfo{Version: "dev"}, "gallery")
	require.NoError(t, err)
	assert.Contains(t, got, att.Day, "the logical day is shown")
	assert.Contains(t, got, "week two", "the caption is shown")
	assert.Contains(t, got, att.StoredPath, "the stored path is shown")
	assert.NotContains(t, got, "|", "no markdown table (Discord rule)")
}

// TestGallery_ReadOnlyByteIdentical proves gallery writes nothing: the media
// tree and the link ledger are byte-identical before and after a run — the
// stronger sanctuary check (AC-11). It exercises both the whole-store and the
// subject-filtered paths so neither read leaves a trace.
func TestGallery_ReadOnlyByteIdentical(t *testing.T) {
	home := isolatedHome(t)

	// Seed synthetic media and one link so both the media tree and the link
	// ledger are non-empty when we snapshot them.
	path := writeTempFile(t, "spring.jpg", []byte("\xff\xd8\xff synthetic spring bytes"))
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "attach", path, "--caption", "spring baseline", "--json")
	require.NoError(t, err)
	var att attachJSON
	require.NoError(t, json.Unmarshal([]byte(out), &att))
	mediaID := filepath.Base(att.StoredPath)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "link", mediaID, "--to", "day:2026-05-01")
	require.NoError(t, err)

	before := galleryHashTree(t, home)
	require.NotEmpty(t, before, "the snapshot must cover the seeded media + ledger")

	// Both read paths: the whole store and the subject-filtered browse.
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gallery")
	require.NoError(t, err)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "gallery", "--to", "day:2026-05-01", "--json")
	require.NoError(t, err)

	after := galleryHashTree(t, home)
	assert.Equal(t, before, after,
		"gallery is read-only: the media tree + link ledger are byte-identical before and after")
}

// galleryHashTree snapshots the media tree and the link ledger under home as a
// map of home-relative path → sha256, so a byte-identical read-only assertion
// compares content, not just file counts. A missing store contributes nothing
// rather than erroring.
func galleryHashTree(t *testing.T, home string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	roots := []string{
		filepath.Join(home, "media"),
		filepath.Join(home, "links", "links.jsonl"),
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			rel, rerr := filepath.Rel(home, p)
			if rerr != nil {
				return rerr
			}
			sum := sha256.Sum256(b)
			snap[rel] = hex.EncodeToString(sum[:])
			return nil
		})
		require.NoError(t, err)
	}
	return snap
}
