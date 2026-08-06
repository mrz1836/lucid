package storage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// --- raw.go ---

// TestCovReadRaw_ParseError: a raw file with unparseable frontmatter surfaces a
// parse error rather than a half-decoded document.
func TestCovReadRaw_ParseError(t *testing.T) {
	a, home := newRawAdapter(t)
	shard := filepath.Join(home, "raw", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "raw_2026_07_05_18_41.md"), []byte("not frontmatter"), 0o600))
	_, err := a.ReadRaw("raw_2026_07_05_18_41")
	assert.Error(t, err)
}

// TestCovEarliestRawDate_ReadErrors: a raw tree that is a file, an unreadable
// year, and an unreadable month shard each surface rather than reading empty.
func TestCovEarliestRawDate_ReadErrors(t *testing.T) {
	t.Run("raw is a file", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, os.WriteFile(filepath.Join(a.home, rawDirName), []byte("x"), 0o600))
		_, _, err := a.EarliestRawDate()
		assert.Error(t, err)
	})
	t.Run("unreadable year", func(t *testing.T) {
		skipIfRoot(t)
		a := New(t.TempDir())
		year := filepath.Join(a.home, rawDirName, "2026")
		require.NoError(t, os.MkdirAll(filepath.Join(year, "07"), 0o700))
		require.NoError(t, os.Chmod(year, 0o000))
		t.Cleanup(func() { _ = os.Chmod(year, 0o700) })
		_, _, err := a.EarliestRawDate()
		assert.Error(t, err)
	})
	t.Run("unreadable month shard", func(t *testing.T) {
		skipIfRoot(t)
		a := New(t.TempDir())
		month := filepath.Join(a.home, rawDirName, "2026", "07")
		require.NoError(t, os.MkdirAll(month, 0o700))
		require.NoError(t, os.Chmod(month, 0o000))
		t.Cleanup(func() { _ = os.Chmod(month, 0o700) })
		_, _, err := a.EarliestRawDate()
		assert.Error(t, err)
	})
}

// TestCovListRawIDsInRange_ShardReadError: an unreadable shard surfaces from the
// range enumeration.
func TestCovListRawIDsInRange_ShardReadError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	month := filepath.Join(a.home, rawDirName, "2026", "07")
	require.NoError(t, os.MkdirAll(month, 0o700))
	require.NoError(t, os.Chmod(month, 0o000))
	t.Cleanup(func() { _ = os.Chmod(month, 0o700) })
	_, err := a.ListRawIDsInRange("2026-07-01", "2026-07-31")
	assert.Error(t, err)
}

// TestCovWriteExcl_StatError: a non-ENOENT stat failure (a file where the parent
// dir belongs) surfaces rather than being read as "path free".
func TestCovWriteExcl_StatError(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "afile")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	_, err := writeExcl(filepath.Join(f, "child"), []byte("y"))
	assert.Error(t, err)
}

// --- insights.go ---

// TestCovWriteInsight_CreatedAtZero: a missing created_at is rejected before any
// id is allocated.
func TestCovWriteInsight_CreatedAtZero(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.WriteInsight(Insight{})
	assert.Error(t, err)
}

// TestCovWriteInsight_NextIDScanError: an unreadable insights dir surfaces from
// the next-id slot scan.
func TestCovWriteInsight_NextIDScanError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.insightsDir(), 0o700))
	require.NoError(t, os.Chmod(a.insightsDir(), 0o000))
	t.Cleanup(func() { _ = os.Chmod(a.insightsDir(), 0o700) })
	_, err := a.WriteInsight(validInsight())
	assert.Error(t, err)
}

// TestCovWriteInsight_ProvenanceValidationError: an insight with incomplete
// provenance is caught by the pre-write validator, so nothing is stored.
func TestCovWriteInsight_ProvenanceValidationError(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.WriteInsight(Insight{CreatedAt: insightNow()})
	assert.Error(t, err)
}

// TestCovExtractInsightStatement_HeadingOnly: a body that is only the heading
// yields the empty statement.
func TestCovExtractInsightStatement_HeadingOnly(t *testing.T) {
	assert.Empty(t, extractInsightStatement(insightHeading))
}

// TestCovValidateInsight_EmptyID: frontmatter without an id is rejected.
func TestCovValidateInsight_EmptyID(t *testing.T) {
	err := ValidateInsight([]byte("---\nstatus: accepted\n---\n\n# Insight\n"))
	assert.Error(t, err)
}

// TestCovListInsightIDs_Skip: a non-record directory entry is skipped by the id
// listing.
func TestCovListInsightIDs_Skip(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Join(a.insightsDir(), "nested"), 0o700))
	ids, err := a.ListInsightIDs()
	require.NoError(t, err)
	assert.Empty(t, ids)
}

// writeInvalidProvenanceInsight writes an insight that decodes cleanly but whose
// provenance no longer validates (empty raw_entry_ids) — the shape the recall
// updates re-validate against before rewriting.
func writeInvalidProvenanceInsight(t *testing.T, a *Adapter) string {
	t.Helper()
	ins := validInsight()
	ins.ID = "i_2026_05_05_a"
	ins.StatusHistory = []TimedEvent{{At: insightNow(), Kind: ResponseAccepted}}
	ins.Provenance.RawEntryIDs = nil // renders as [] → fails provenance validation
	content, err := renderInsight(ins)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(a.insightsDir(), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(a.insightsDir(), ins.ID+insightExt), content, 0o600))
	return ins.ID
}

// TestCovRewriteInsight_ValidationError: a recall status update re-validates and
// refuses to rewrite an insight whose provenance has gone missing.
func TestCovRewriteInsight_ValidationError(t *testing.T) {
	a := New(t.TempDir())
	id := writeInvalidProvenanceInsight(t, a)
	err := a.UpdateInsightStatus(id, RecallConfirmed, insightNow())
	assert.Error(t, err)
}

// TestCovSetInsightRule_ValidationError: setting a rule re-validates and refuses
// to rewrite an insight whose provenance has gone missing.
func TestCovSetInsightRule_ValidationError(t *testing.T) {
	a := New(t.TempDir())
	id := writeInvalidProvenanceInsight(t, a)
	err := a.SetInsightRule(id, "one idea at a time", insightNow())
	assert.Error(t, err)
}

// TestCovReadInsight_DecodeErrors: a malformed timestamp in rule_history,
// last_softened_at, or retired_at surfaces from the decoder rather than reading
// as a zero value.
func TestCovReadInsight_DecodeErrors(t *testing.T) {
	base := func() Insight {
		in := validInsight()
		in.ID = "i_2026_05_05_a"
		in.StatusHistory = []TimedEvent{{At: insightNow(), Kind: ResponseAccepted}}
		return in
	}
	cases := []struct {
		name     string
		mutate   func(*Insight)
		sentinel string
	}{
		{"rule_history", func(in *Insight) {
			r := "one idea"
			in.Rule = &r
			in.RuleHistory = []TimedEvent{{At: time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC), Kind: RuleKept}}
		}, "2027-01-02T03:04:05Z"},
		{"last_softened_at", func(in *Insight) {
			s := time.Date(2028, 1, 2, 3, 4, 5, 0, time.UTC)
			in.LastSoftenedAt = &s
		}, "2028-01-02T03:04:05Z"},
		{"retired_at", func(in *Insight) {
			r := time.Date(2029, 1, 2, 3, 4, 5, 0, time.UTC)
			in.RetiredAt = &r
		}, "2029-01-02T03:04:05Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(t.TempDir())
			in := base()
			tc.mutate(&in)
			content, err := renderInsight(in)
			require.NoError(t, err)
			corrupt := bytes.Replace(content, []byte(tc.sentinel), []byte("not-a-time"), 1)
			require.NotEqual(t, content, corrupt, "sentinel must be present to corrupt")
			require.NoError(t, os.MkdirAll(a.insightsDir(), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(a.insightsDir(), in.ID+insightExt), corrupt, 0o600))
			_, err = a.ReadInsight(in.ID)
			assert.Error(t, err)
		})
	}
}

// --- reflections.go ---

// TestCovReadInsightsWindow_ListError: an unreadable insights tree surfaces from
// the recall window read.
func TestCovReadInsightsWindow_ListError(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.WriteFile(a.insightsDir(), []byte("x"), 0o600))
	_, err := a.ReadInsightsWindow(time.Time{}, 0)
	assert.Error(t, err)
}

// TestCovLastStatusAt_Fallback: an insight with no status_history falls back to
// its created_at for recency ordering.
func TestCovLastStatusAt_Fallback(t *testing.T) {
	assert.Equal(t, insightNow(), lastStatusAt(Insight{CreatedAt: insightNow()}))
}

// TestCovReadAcceptedInsights_ReadError: a corrupt insight file surfaces from
// the accepted-insights read rather than being skipped.
func TestCovReadAcceptedInsights_ReadError(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.insightsDir(), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(a.insightsDir(), "i_2026_05_05_a.md"), []byte("not frontmatter"), 0o600))
	_, err := a.ReadAcceptedInsights(0)
	assert.Error(t, err)
}

// TestCovWriteReflection_EnsureDirError: a file where reflections/ belongs fails
// the dir guard.
func TestCovWriteReflection_EnsureDirError(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.WriteFile(a.reflectionsDir(), []byte("x"), 0o600))
	_, err := a.WriteReflection(reflectionSeed(insightNow(), nil, nil, "summary"))
	assert.Error(t, err)
}

// TestCovWriteReflection_WriteError: an unwritable reflections dir fails the
// record write after a clean read of the (absent) prior record.
func TestCovWriteReflection_WriteError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.reflectionsDir(), 0o700))
	require.NoError(t, os.Chmod(a.reflectionsDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.reflectionsDir(), 0o700) })
	_, err := a.WriteReflection(reflectionSeed(insightNow(), nil, nil, "summary"))
	assert.Error(t, err)
}

// TestCovReadReflection_ReadError: a reflection path that is a directory surfaces
// a read error rather than an absent-record nil.
func TestCovReadReflection_ReadError(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.MkdirAll(a.reflectionsDir(), 0o700))
	id := "reflection_2026_w19"
	mkdirAt(t, filepath.Join(a.reflectionsDir(), id+reflectionExt))
	_, err := a.ReadReflection(id)
	assert.Error(t, err)
}

// TestCovReflectionListReaders_ListError: an unreadable reflections tree surfaces
// from every reader that lists ids.
func TestCovReflectionListReaders_ListError(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.WriteFile(a.reflectionsDir(), []byte("x"), 0o600))

	_, err := a.ListReflectionIDs()
	require.Error(t, err)
	_, err = a.ReadReflections(0)
	require.Error(t, err)
	_, _, err = a.LatestReflectionCreatedAt()
	require.Error(t, err)
}

// --- media.go ---

// TestCovWriteMedia_WriteExclError: an unwritable day shard fails the binary
// write, leaving nothing on disk.
func TestCovWriteMedia_WriteExclError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	require.NoError(t, a.ScaffoldMedia())
	shard := filepath.Join(a.mediaDir(), "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.Chmod(shard, 0o500))
	t.Cleanup(func() { _ = os.Chmod(shard, 0o700) })
	_, err := a.WriteMedia(MediaAttachment{
		Bytes: []byte("x"), OriginalFilename: "p.txt", CapturedAt: fixedTime(),
		LogicalDay: "2026-07-02", Source: "cli",
	})
	assert.Error(t, err)
}

// TestCovListMedia_WalkError: an unreadable media subtree surfaces from the
// whole-tree walk.
func TestCovListMedia_WalkError(t *testing.T) {
	skipIfRoot(t)
	a := New(t.TempDir())
	require.NoError(t, a.ScaffoldMedia())
	sub := filepath.Join(a.mediaDir(), "2026")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	_, err := a.ListMedia()
	assert.Error(t, err)
}

// --- backup.go ---

// buildArchiveWithDir builds a tar.gz carrying one directory entry (dirName)
// alongside the regular files — for exercising untar's non-regular-entry skip.
func buildArchiveWithDir(t *testing.T, dirName string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dirName, Mode: 0o700}))
	for name, body := range files {
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

// TestCovBackupTo_CollectStatError: a non-ENOENT stat failure on a backup entry
// (its parent is a file) aborts the backup rather than skipping the entry.
func TestCovBackupTo_CollectStatError(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "raw"), []byte("x"), 0o600))
	var buf bytes.Buffer
	_, err := New(home).BackupTo(&buf, []BackupEntry{{Path: "raw/sub", IsDir: false}})
	assert.Error(t, err)
}

// TestCovBackupTo_WalkDirError: an unreadable subtree inside a walked directory
// entry aborts the backup.
func TestCovBackupTo_WalkDirError(t *testing.T) {
	skipIfRoot(t)
	home := t.TempDir()
	sub := filepath.Join(home, "raw", "2026")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	var buf bytes.Buffer
	_, err := New(home).BackupTo(&buf, []BackupEntry{{Path: "raw", IsDir: true}})
	assert.Error(t, err)
}

// TestCovBackupTo_OpenError: an unreadable regular file fails the tar copy.
func TestCovBackupTo_OpenError(t *testing.T) {
	skipIfRoot(t)
	home := t.TempDir()
	writeHomeFile(t, home, "raw/entry.md", "body")
	entry := filepath.Join(home, "raw", "entry.md")
	require.NoError(t, os.Chmod(entry, 0o000))
	t.Cleanup(func() { _ = os.Chmod(entry, 0o600) })
	var buf bytes.Buffer
	_, err := New(home).BackupTo(&buf, []BackupEntry{{Path: "raw", IsDir: true}})
	assert.Error(t, err)
}

// TestCovRestoreFrom_RootHasContentError: an unreadable allowed root aborts the
// occupancy check rather than being read as empty.
func TestCovRestoreFrom_RootHasContentError(t *testing.T) {
	skipIfRoot(t)
	home := t.TempDir()
	engineDir := filepath.Join(home, "engine")
	require.NoError(t, os.MkdirAll(filepath.Join(engineDir, "days"), 0o700))
	require.NoError(t, os.Chmod(engineDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(engineDir, 0o700) })
	arch := buildArchive(t, map[string]string{"raw/x.md": "y"})
	_, err := New(home).RestoreFrom(bytes.NewReader(arch), RestoreOptions{AllowedRoots: manifestRoots()})
	assert.Error(t, err)
}

// TestCovRestoreFrom_SkipsDirectoryEntry: a directory entry in the archive is
// skipped; the regular file alongside it restores.
func TestCovRestoreFrom_SkipsDirectoryEntry(t *testing.T) {
	arch := buildArchiveWithDir(t, "raw/", map[string]string{"raw/x.md": "y"})
	home := t.TempDir()
	rres, err := New(home).RestoreFrom(bytes.NewReader(arch), RestoreOptions{AllowedRoots: manifestRoots()})
	require.NoError(t, err)
	assert.Equal(t, []string{"raw/x.md"}, rres.Files, "the directory entry is skipped")
}

// TestCovRestoreFrom_EnsureDirError: an entry whose parent path is a file fails
// the on-demand parent creation.
func TestCovRestoreFrom_EnsureDirError(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "engine"), []byte("x"), 0o600))
	arch := buildArchive(t, map[string]string{"engine/sub/file.txt": "data"})
	_, err := New(home).RestoreFrom(bytes.NewReader(arch), RestoreOptions{Force: true, AllowedRoots: manifestRoots()})
	assert.Error(t, err)
}

// TestCovRestoreFrom_WriteCappedCreateError: an entry whose destination is an
// existing directory fails the capped write.
func TestCovRestoreFrom_WriteCappedCreateError(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "raw", "x.md"), 0o700))
	arch := buildArchive(t, map[string]string{"raw/x.md": "data"})
	_, err := New(home).RestoreFrom(bytes.NewReader(arch), RestoreOptions{Force: true, AllowedRoots: manifestRoots()})
	assert.Error(t, err)
}

// TestCovWriteWitnessContract_MarshalStable is a light guard that the contract
// write round-trips through the shared marshaller (kept beside the error tests).
func TestCovWriteWitnessContract_MarshalStable(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, a.WriteWitnessContract(engine.WitnessContract{}))
	got, err := a.ReadWitnessContract()
	require.NoError(t, err)
	assert.Equal(t, engine.WitnessContract{}, got)
}

// TestCovEarliestRawDateInShard_Missing: a shard that vanished between the walk
// and the read is empty, not an error.
func TestCovEarliestRawDateInShard_Missing(t *testing.T) {
	date, err := earliestRawDateInShard(filepath.Join(t.TempDir(), "ghost"))
	require.NoError(t, err)
	assert.Empty(t, date)
}

// TestCovRawIDsInShard_Missing: a shard that vanished between the walk and the
// read yields no ids and no error.
func TestCovRawIDsInShard_Missing(t *testing.T) {
	ids, err := rawIDsInShard(filepath.Join(t.TempDir(), "ghost"))
	require.NoError(t, err)
	assert.Empty(t, ids)
}

// TestCovReadAcceptedInsights_ListError: an unreadable insights tree surfaces
// from the id listing rather than reading as no accepted insights.
func TestCovReadAcceptedInsights_ListError(t *testing.T) {
	a := New(t.TempDir())
	require.NoError(t, os.WriteFile(a.insightsDir(), []byte("x"), 0o600))
	_, err := a.ReadAcceptedInsights(0)
	assert.Error(t, err)
}

// TestCovWriteTarEntry_Errors covers both the stat failure (a home-relative path
// with no file) and the copy failure (a directory opened as a regular file,
// whose read returns EISDIR).
func TestCovWriteTarEntry_Errors(t *testing.T) {
	t.Run("stat error", func(t *testing.T) {
		a := New(t.TempDir())
		tw := tar.NewWriter(&bytes.Buffer{})
		err := a.writeTarEntry(tw, "raw/ghost.md")
		assert.Error(t, err)
	})
	t.Run("copy error", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "raw", "adir"), 0o700))
		tw := tar.NewWriter(&bytes.Buffer{})
		err := New(home).writeTarEntry(tw, "raw/adir")
		assert.Error(t, err)
	})
}
