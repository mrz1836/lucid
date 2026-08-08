package storage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backupManifest mirrors deploy.BackupManifest() so the storage tests exercise
// the real ADR-0002 set without importing deploy (the Sanctuary package stays
// deploy-free; the CLI owns that translation).
func backupManifest() []BackupEntry {
	return []BackupEntry{
		{Path: "raw", IsDir: true},
		{Path: "media", IsDir: true},
		{Path: "observations", IsDir: true},
		{Path: "registries", IsDir: true},
		{Path: "links", IsDir: true},
		{Path: "engine", IsDir: true, Exclude: []string{"engine/status.json"}},
		{Path: "projections/exports.log", IsDir: false},
	}
}

// manifestRoots is the deduped top-level set the restore allowlist checks.
func manifestRoots() []string {
	return []string{"raw", "media", "observations", "registries", "links", "engine", "projections"}
}

// writeHomeFile writes content at a home-relative slash path, creating parents.
func writeHomeFile(t *testing.T, home, rel, content string) {
	t.Helper()
	p := filepath.Join(home, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), dirPerm))
	require.NoError(t, os.WriteFile(p, []byte(content), filePerm))
}

// selfLogFixture is a four-state self.json: an active fact, a correction under
// the same id, a re-categorization (move) under a second id, and a retirement.
// It exists so the round-trip proves the whole grammar survives a backup, not
// just a single trivial record.
//
// The self store rides the manifest's whole-engine/ walk by construction — the
// "engine" entry is a directory that excludes only the derived status.json —
// so this fixture proves a free ride rather than new plumbing. Do NOT add an
// "engine/self.json" entry to backupManifest(): the file would then be listed
// twice.
const selfLogFixture = `{
  "version": 1,
  "history": [
    {
      "id": "self_2026_07_29_a",
      "key": "identity.generation",
      "value": "elder millennial",
      "recorded_at": "2026-07-29T09:00:00Z"
    },
    {
      "id": "self_2026_07_29_a",
      "key": "identity.generation",
      "value": "elder millennial (born 1984)",
      "since": "1984",
      "recorded_at": "2026-07-29T09:05:00Z"
    },
    {
      "id": "self_2026_07_29_b",
      "key": "body.handedness",
      "value": "left",
      "recorded_at": "2026-07-29T09:10:00Z"
    },
    {
      "id": "self_2026_07_29_b",
      "key": "misc.handedness",
      "value": "left",
      "recorded_at": "2026-07-29T09:15:00Z"
    },
    {
      "id": "self_2026_07_29_c",
      "key": "pref.coffee",
      "value": "black",
      "note": "no longer true",
      "recorded_at": "2026-07-29T09:20:00Z",
      "state": "retired"
    }
  ]
}
`

// seedBackupHome populates a home with a file in every manifest tree plus the
// excluded status, a rebuildable tree, and lucid.json — the latter three must
// never ride along in a backup. It returns the home and the expected
// backed-up-file → content map (exactly the manifest set).
func seedBackupHome(t *testing.T) (home string, want map[string]string) {
	t.Helper()
	home = t.TempDir()
	want = map[string]string{
		"raw/raw_2026_07_29_09_00.md": "# entry one\nbody\n",
		"raw/raw_2026_07_29_10_00.md": "# entry two\nbody\n",
		"observations/events.jsonl":   `{"kind":"micro"}` + "\n",
		"observations/config.json":    `{"enabled":true}`,
		"registries/injuries.json":    `[{"id":"inj_x"}]`,
		"registries/threads.json":     `[]`,
		"engine/chain.json":           `{"bell_time":"21:30"}`,
		"engine/days/2026-07-29.json": `{"closed":true}`,
		"engine/self.json":            selfLogFixture,
		"projections/exports.log":     "2026-07-29 export series\n",
	}
	for rel, content := range want {
		writeHomeFile(t, home, rel, content)
	}
	// Present on disk, deliberately outside the backup set:
	writeHomeFile(t, home, "engine/status.json", `{"derived":true}`) // excluded
	writeHomeFile(t, home, "processed/raw_2026_07_29_09_00.json", `{"x":1}`)
	writeHomeFile(t, home, "insights/ins_a.md", "# insight\n")
	writeHomeFile(t, home, "lucid.json", `{"schema":1}`)
	return home, want
}

// buildArchive builds a tar.gz from a name→body map for the restore-guard tests.
func buildArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Mode: 0o600, Size: int64(len(body)),
		}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// TestBackupRestore_RoundTrip is the core property: a populated home backs up to
// exactly the manifest set, restores byte-for-byte into a fresh home, and the
// excluded/rebuildable trees never appear on either side.
func TestBackupRestore_RoundTrip(t *testing.T) {
	srcHome, want := seedBackupHome(t)
	src := New(srcHome)

	var buf bytes.Buffer
	res, err := src.BackupTo(&buf, backupManifest())
	require.NoError(t, err)
	assert.Positive(t, res.Bytes)
	assert.Equal(t, int64(buf.Len()), res.Bytes, "reported bytes equal the sink size")
	assert.Len(t, res.Files, len(want))
	assert.True(t, sort.StringsAreSorted(res.Files), "file list is globally sorted")
	for rel := range want {
		assert.Contains(t, res.Files, rel)
	}
	for _, omitted := range []string{
		"engine/status.json", "processed/raw_2026_07_29_09_00.json",
		"insights/ins_a.md", "lucid.json",
	} {
		assert.NotContains(t, res.Files, omitted)
	}

	dstHome := t.TempDir()
	dst := New(dstHome)
	rres, err := dst.RestoreFrom(&buf, RestoreOptions{AllowedRoots: manifestRoots()})
	require.NoError(t, err)
	assert.Len(t, rres.Files, len(want))

	for rel, content := range want {
		abs := filepath.Join(dstHome, filepath.FromSlash(rel))
		got, rerr := os.ReadFile(abs)
		require.NoErrorf(t, rerr, "restored %s", rel)
		assert.Equal(t, content, string(got), rel)
		info, statErr := os.Stat(abs)
		require.NoError(t, statErr)
		assert.Equalf(t, filePerm, info.Mode().Perm(), "file perm for %s", rel)
	}
	// The omitted trees are absent from the restored home.
	for _, rel := range []string{"engine/status.json", "processed", "insights", "lucid.json"} {
		_, statErr := os.Stat(filepath.Join(dstHome, filepath.FromSlash(rel)))
		require.ErrorIsf(t, statErr, fs.ErrNotExist, "%s should be absent", rel)
	}
	// A restored directory carries the owner-only mode.
	di, statErr := os.Stat(filepath.Join(dstHome, "raw"))
	require.NoError(t, statErr)
	assert.Equal(t, dirPerm, di.Mode().Perm())
}

// TestBackupRestore_CarriesTheSelfStore names the self-facts property the
// generic round-trip proves generically: a decade of semantic memory backs up
// and restores byte-for-byte, across the store's whole grammar (an active
// fact, a correction, a move, and a retirement — see selfLogFixture).
//
// This costs no plumbing. The store lives at engine/self.json, and the
// manifest's "engine" entry is a whole-directory walk excluding only the
// derived status.json, so the file is in the backup set by construction. The
// test exists to keep that free ride honest if the manifest is ever narrowed.
func TestBackupRestore_CarriesTheSelfStore(t *testing.T) {
	srcHome, _ := seedBackupHome(t)

	var buf bytes.Buffer
	res, err := New(srcHome).BackupTo(&buf, backupManifest())
	require.NoError(t, err)
	assert.Contains(t, res.Files, "engine/self.json", "the self store is in the backup set")

	dstHome := t.TempDir()
	rres, err := New(dstHome).RestoreFrom(&buf, RestoreOptions{AllowedRoots: manifestRoots()})
	require.NoError(t, err)
	assert.Contains(t, rres.Files, "engine/self.json", "the self store is in the restore set")

	got, err := os.ReadFile(filepath.Join(dstHome, "engine", "self.json"))
	require.NoError(t, err)
	// Byte equality, deliberately not JSON equality: a semantic compare would
	// pass on a store whose formatting or record order had shifted in transit,
	// and an append-only history is exactly the thing that must come back as
	// it went in.
	assert.True(t, bytes.Equal([]byte(selfLogFixture), got), "the restored self store is byte-identical")
}

// TestBackupTo_Deterministic pins the reproducibility property: two in-process
// backups of one unchanged tree are byte-identical.
func TestBackupTo_Deterministic(t *testing.T) {
	home, _ := seedBackupHome(t)
	a := New(home)
	var b1, b2 bytes.Buffer
	_, err := a.BackupTo(&b1, backupManifest())
	require.NoError(t, err)
	_, err = a.BackupTo(&b2, backupManifest())
	require.NoError(t, err)
	assert.True(t, bytes.Equal(b1.Bytes(), b2.Bytes()), "two backups of one tree match byte-for-byte")
}

// TestBackupTo_SkipsAbsentTrees: a fresh home with only raw/ present backs up
// just that tree — an absent manifest tree is skipped, never an error.
func TestBackupTo_SkipsAbsentTrees(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "raw/raw_x.md", "only raw\n")
	a := New(home)
	var buf bytes.Buffer
	res, err := a.BackupTo(&buf, backupManifest())
	require.NoError(t, err)
	assert.Equal(t, []string{"raw/raw_x.md"}, res.Files)
}

// TestBackupRestore_EmptyHome: a home with no manifest tree yields an empty
// archive that restores as a clean no-op.
func TestBackupRestore_EmptyHome(t *testing.T) {
	a := New(t.TempDir())
	var buf bytes.Buffer
	res, err := a.BackupTo(&buf, backupManifest())
	require.NoError(t, err)
	assert.Empty(t, res.Files)

	dst := New(t.TempDir())
	rres, err := dst.RestoreFrom(&buf, RestoreOptions{AllowedRoots: manifestRoots()})
	require.NoError(t, err)
	assert.Empty(t, rres.Files)
	assert.Zero(t, rres.Bytes)
}

// TestBackupTo_SkipsSymlinkRoot: a symlink standing in for a manifest tree is
// never followed out of ~/.lucid.
func TestBackupTo_SkipsSymlinkRoot(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.md"), []byte("x"), filePerm))
	require.NoError(t, os.Symlink(outside, filepath.Join(home, "raw")))
	writeHomeFile(t, home, "observations/e.jsonl", "{}\n")

	a := New(home)
	var buf bytes.Buffer
	res, err := a.BackupTo(&buf, backupManifest())
	require.NoError(t, err)
	assert.Equal(t, []string{"observations/e.jsonl"}, res.Files, "symlink root skipped")
}

// TestBackupTo_SkipsSymlinkWithinDir: an irregular file inside a walked tree is
// skipped (P2), so a backup carries only regular files.
func TestBackupTo_SkipsSymlinkWithinDir(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "raw/real.md", "real\n")
	require.NoError(t, os.Symlink("/etc/hosts", filepath.Join(home, "raw", "link.md")))

	a := New(home)
	var buf bytes.Buffer
	res, err := a.BackupTo(&buf, backupManifest())
	require.NoError(t, err)
	assert.Equal(t, []string{"raw/real.md"}, res.Files)
}

// TestBackupTo_ManifestTypeMismatch: BackupTo branches on the real on-disk type,
// not BackupEntry.IsDir — a file where the manifest expects a dir is archived as
// a single file, and a dir where it expects a file is walked.
func TestBackupTo_ManifestTypeMismatch(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "engine", "not-a-dir\n")                           // manifest says dir
	writeHomeFile(t, home, "projections/exports.log/inner.txt", "surprise\n") // manifest says file

	a := New(home)
	var buf bytes.Buffer
	res, err := a.BackupTo(&buf, backupManifest())
	require.NoError(t, err)
	assert.Contains(t, res.Files, "engine")
	assert.Contains(t, res.Files, "projections/exports.log/inner.txt")
}

// TestBackupTo_SkipsIrregularRoot: a manifest root that is a FIFO (not a dir,
// symlink, or regular file) is skipped — the default P2 guard.
func TestBackupTo_SkipsIrregularRoot(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(home, "raw"), 0o600))
	writeHomeFile(t, home, "observations/e.jsonl", "{}\n")

	a := New(home)
	var buf bytes.Buffer
	res, err := a.BackupTo(&buf, backupManifest())
	require.NoError(t, err)
	assert.Equal(t, []string{"observations/e.jsonl"}, res.Files, "FIFO root skipped")
}

// TestRestoreFrom_RejectsZipSlip: a "../" entry escaping the home is refused.
func TestRestoreFrom_RejectsZipSlip(t *testing.T) {
	arch := buildArchive(t, map[string]string{"../escape.md": "haxx"})
	dst := New(t.TempDir())
	_, err := dst.RestoreFrom(bytes.NewReader(arch), RestoreOptions{AllowedRoots: manifestRoots()})
	require.ErrorIs(t, err, ErrPathTraversal)
}

// TestRestoreFrom_RejectsAbsolutePath: an absolute entry is refused.
func TestRestoreFrom_RejectsAbsolutePath(t *testing.T) {
	arch := buildArchive(t, map[string]string{"/etc/evil": "haxx"})
	dst := New(t.TempDir())
	_, err := dst.RestoreFrom(bytes.NewReader(arch), RestoreOptions{AllowedRoots: manifestRoots()})
	require.ErrorIs(t, err, ErrPathTraversal)
}

// TestRestoreFrom_RejectsDisallowedRoot: an entry under a non-backup root is
// refused even though its path stays inside the home.
func TestRestoreFrom_RejectsDisallowedRoot(t *testing.T) {
	arch := buildArchive(t, map[string]string{"processed/raw_x.json": "{}"})
	dst := New(t.TempDir())
	_, err := dst.RestoreFrom(bytes.NewReader(arch), RestoreOptions{AllowedRoots: manifestRoots()})
	require.ErrorIs(t, err, ErrDisallowedRoot)
}

// TestWriteCapped_SizeCap drives the per-file cap directly with a tiny limit so
// the zip-bomb defense is exercised without a multi-hundred-MiB fixture; the
// over-cap file is removed.
func TestWriteCapped_SizeCap(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "f")
	_, err := writeCapped(strings.NewReader("0123456789"), dest, "raw/f", 4)
	require.ErrorIs(t, err, ErrFileTooLarge)
	_, statErr := os.Stat(dest)
	assert.ErrorIs(t, statErr, fs.ErrNotExist, "over-cap file is removed")
}

// errReader fails every read, exercising writeCapped's copy-error path.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

// TestWriteCapped_CopyError: a mid-copy read failure surfaces as an error.
func TestWriteCapped_CopyError(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "f")
	_, err := writeCapped(errReader{}, dest, "raw/f", 1000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extract")
}

// TestMaxBackupFileSize_Is500MiB pins the cap to the upgrade precedent.
func TestMaxBackupFileSize_Is500MiB(t *testing.T) {
	assert.Equal(t, int64(500*1024*1024), int64(maxBackupFileSize))
}

// TestRestoreFrom_GarbageGzip: a non-gzip stream errors rather than panics.
func TestRestoreFrom_GarbageGzip(t *testing.T) {
	dst := New(t.TempDir())
	_, err := dst.RestoreFrom(bytes.NewReader([]byte("not a gzip stream at all")), RestoreOptions{AllowedRoots: manifestRoots()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gzip")
}

// TestRestoreFrom_TruncatedArchive: a valid header cut short errors, not panics.
func TestRestoreFrom_TruncatedArchive(t *testing.T) {
	full := buildArchive(t, map[string]string{"raw/x.md": strings.Repeat("y", 8192)})
	trunc := full[:len(full)-64]
	dst := New(t.TempDir())
	_, err := dst.RestoreFrom(bytes.NewReader(trunc), RestoreOptions{AllowedRoots: manifestRoots()})
	require.Error(t, err)
}

// TestRestoreFrom_OccupiedHome: an occupied home is refused without --force and
// overlaid (never replaced) with it.
func TestRestoreFrom_OccupiedHome(t *testing.T) {
	arch := buildArchive(t, map[string]string{"raw/new.md": "new\n"})

	home := t.TempDir()
	writeHomeFile(t, home, "raw/existing.md", "old\n")
	dst := New(home)

	_, err := dst.RestoreFrom(bytes.NewReader(arch), RestoreOptions{AllowedRoots: manifestRoots()})
	require.ErrorIs(t, err, ErrHomeOccupied)

	rres, err := dst.RestoreFrom(bytes.NewReader(arch), RestoreOptions{Force: true, AllowedRoots: manifestRoots()})
	require.NoError(t, err)
	assert.Equal(t, []string{"raw/new.md"}, rres.Files)
	old, rerr := os.ReadFile(filepath.Join(home, "raw", "existing.md"))
	require.NoError(t, rerr)
	assert.Equal(t, "old\n", string(old), "overlay never deletes a pre-existing file")
}

// TestRestoreFrom_ScaffoldedEmptyHomeIsEmpty: a home holding only lucid.json and
// .keep markers reads as empty, so restore proceeds without --force.
func TestRestoreFrom_ScaffoldedEmptyHomeIsEmpty(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)

	arch := buildArchive(t, map[string]string{"raw/new.md": "new\n"})
	rres, err := a.RestoreFrom(bytes.NewReader(arch), RestoreOptions{AllowedRoots: manifestRoots()})
	require.NoError(t, err)
	assert.Equal(t, []string{"raw/new.md"}, rres.Files)
}

// failWriter fails every write past afterN bytes, so a backup's writer error
// surfaces from BackupTo rather than being swallowed.
type failWriter struct {
	afterN, n int
}

func (f *failWriter) Write(p []byte) (int, error) {
	f.n += len(p)
	if f.n > f.afterN {
		return 0, errors.New("sink is full")
	}
	return len(p), nil
}

// TestBackupTo_FailingWriterPropagates: a writer that fails mid-stream surfaces
// as an error, not a silent short backup.
func TestBackupTo_FailingWriterPropagates(t *testing.T) {
	home, _ := seedBackupHome(t)
	a := New(home)
	_, err := a.BackupTo(&failWriter{}, backupManifest())
	require.Error(t, err)
}
