package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// backupTo runs `lucid backup --out <path>` and returns the archive path so the
// restore tests have a real archive to work from.
func backupTo(t *testing.T, path string) {
	t.Helper()
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "backup", "--out", path)
	require.NoError(t, err)
}

// TestRestoreCmd_RoundTrip: backup, wipe the raw tree, restore via --in, and the
// entry returns byte-for-byte.
func TestRestoreCmd_RoundTrip(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)

	require.NoError(t, os.RemoveAll(filepath.Join(home, "raw")))
	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out)
	require.NoError(t, err)
	assert.Contains(t, stdout, "Restored")

	b, rerr := os.ReadFile(filepath.Join(home, "raw", "raw_2026_07_29_09_00.md"))
	require.NoError(t, rerr)
	assert.Equal(t, "# entry\nhello\n", string(b))
}

// TestRestoreCmd_RoundTripCarriesMediaAndLinks is SC-14's actual promise, proven
// end-to-end through the shipped verbs and the shipped deploy.BackupManifest():
// a capture's binary and the append-only link that points at it both survive a
// backup and restore into a fresh home, so a restored Ledger yields coherent
// links whose binaries still exist. Seeding via `attach --to` and archiving via
// the real `backup` command means a manifest that ever stopped covering media/
// or links/ would fail here, not pass vacuously.
func TestRestoreCmd_RoundTripCarriesMediaAndLinks(t *testing.T) {
	src := isolatedHome(t)
	body := []byte("\xff\xd8\xff synthetic image bytes")
	path := writeTempFile(t, "trailhead.jpg", body)

	// Capture the media and link it to a subject in one flow.
	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"attach", path, "--to", "day:2020-01-01", "--json")
	require.NoError(t, err)
	var att attachJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &att))
	require.Len(t, att.Linked, 1, "the attach linked one subject")
	mediaID := filepath.Base(att.StoredPath)

	// The link and its binary both live in the source home before the backup.
	srcLinks, err := storage.New(src).LinksForMedia(mediaID)
	require.NoError(t, err)
	require.Len(t, srcLinks, 1)

	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)

	// Restore into a fresh home — nothing carries over except the archive.
	dst := filepath.Join(t.TempDir(), ".lucid")
	t.Setenv("LUCID_HOME", dst)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out)
	require.NoError(t, err)

	// The binary survives, byte-for-byte, reachable by its media id.
	rec, found, err := storage.New(dst).ReadMediaByID(mediaID)
	require.NoError(t, err)
	require.True(t, found, "the media sidecar restored")
	got, err := os.ReadFile(rec.StoredPath)
	require.NoError(t, err)
	assert.Equal(t, body, got, "the restored binary is byte-identical")

	// The folded link survives and still points at the same subject.
	restored, err := storage.New(dst).LinksForMedia(mediaID)
	require.NoError(t, err)
	require.Len(t, restored, 1, "the folded link restored")
	assert.Equal(t, "day", restored[0].Subject.Kind)
	assert.Equal(t, "2020-01-01", restored[0].Subject.Key)
}

// TestRestoreCmd_RoundTripCarriesSecretCatalog proves the names-only secret
// reference catalog is primary data that survives a backup and restore into a
// fresh home — a manifest that ever stopped covering secrets/ would fail here,
// not pass vacuously. The catalog carries handles, notes, and creation times
// only; there is no secret material to lose, so what must survive is exactly the
// live folded reference.
func TestRestoreCmd_RoundTripCarriesSecretCatalog(t *testing.T) {
	isolatedHome(t)
	now := time.Date(2026, time.August, 9, 15, 30, 0, 0, time.UTC)
	withClock(t, now)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"secret", "add", "FIXTURE_SECRET", "--note", "synthetic reference")
	require.NoError(t, err)

	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)

	// Restore into a fresh home — nothing carries over except the archive.
	dst := filepath.Join(t.TempDir(), ".lucid")
	t.Setenv("LUCID_HOME", dst)
	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out)
	require.NoError(t, err)

	// The live catalog survives: handle, note, and creation time intact.
	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "secret", "list", "--json")
	require.NoError(t, err)
	var refs []storage.SecretRef
	require.NoError(t, json.Unmarshal([]byte(stdout), &refs))
	require.Equal(t, []storage.SecretRef{{
		Name:      "FIXTURE_SECRET",
		Note:      "synthetic reference",
		CreatedAt: now.Format(time.RFC3339),
	}}, refs)
}

// TestRestoreCmd_PositionalPath: the archive may be passed positionally.
func TestRestoreCmd_PositionalPath(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)

	require.NoError(t, os.RemoveAll(filepath.Join(home, "raw")))
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", out)
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(home, "raw", "raw_2026_07_29_09_00.md"))
	require.NoError(t, statErr)
}

// TestRestoreCmd_JSON: `--json` decodes to the documented payload.
func TestRestoreCmd_JSON(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)
	require.NoError(t, os.RemoveAll(filepath.Join(home, "raw")))

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out, "--json")
	require.NoError(t, err)
	var payload restoreJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload))
	assert.Equal(t, "restore", payload.Command)
	assert.Equal(t, out, payload.Path)
	assert.Equal(t, 1, payload.Files)
	assert.Positive(t, payload.Bytes)
}

// TestRestoreCmd_OccupiedRefusalThenForce: restore refuses an occupied home,
// then overlays it with --force.
func TestRestoreCmd_OccupiedRefusalThenForce(t *testing.T) {
	home := isolatedHome(t)
	seedRawEntry(t, home)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	backupTo(t, out)

	// The raw entry still occupies the home.
	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
	assert.Contains(t, stderr, "--force", "the refusal is printed for the user, not just returned")

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out, "--force")
	require.NoError(t, err)
}

// TestRestoreCmd_EmptyArchive: restoring an archive with no in-scope files is a
// clean no-op that says so.
func TestRestoreCmd_EmptyArchive(t *testing.T) {
	isolatedHome(t) // never scaffolded → the manifest trees are all absent
	out := filepath.Join(t.TempDir(), "empty.tar.gz")
	backupTo(t, out)

	stdout, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", out)
	require.NoError(t, err)
	assert.Contains(t, stdout, "Nothing to restore")
}

// TestRestoreCmd_InputResolution: neither/both input forms are refused with a
// clear message.
func TestRestoreCmd_InputResolution(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "restore")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name the archive")

	_, _, err = runRoot(t, BuildInfo{Version: "dev"}, "restore", "--in", "a.tar.gz", "b.tar.gz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}
