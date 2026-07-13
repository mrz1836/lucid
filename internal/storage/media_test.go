package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syntheticMedia builds a well-formed attachment with synthetic bytes (no real
// content) attributed to the logical day of [fixedTime]. It mirrors
// syntheticRaw: a deterministic fixture the media tests vary from.
func syntheticMedia(caption, original string, content []byte) MediaAttachment {
	return MediaAttachment{
		Bytes:            content,
		OriginalFilename: original,
		CapturedAt:       fixedTime(),
		LogicalDay:       "2026-07-05",
		Caption:          caption,
		Source:           "cli",
		RawEntryID:       "raw_2026_07_05_18_41",
	}
}

// hashOf returns the hex sha256 of b — the reader's independent integrity
// recompute the round-trip test compares against.
func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestWriteMedia_RoundTripAndSha256(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	content := []byte("\xff\xd8\xff\xe0synthetic jpeg bytes") // opaque, not real image data
	rec, err := a.WriteMedia(syntheticMedia("Handwritten Notes, page 1", "IMG_4823.jpg", content))
	require.NoError(t, err)

	// Stored under the logical-day shard, LUCID_HOME-relative, name derived.
	assert.Equal(t, "2026-07-05-handwritten-notes-page-1.jpg", rec.ID)
	wantPath := filepath.Join(home, "media", "2026", "07", rec.ID)
	assert.Equal(t, wantPath, rec.StoredPath)
	assert.True(t, strings.HasPrefix(rec.StoredPath, home), "store must live under LUCID_HOME")

	// The stored bytes are byte-for-byte the input, and the recorded hash matches.
	onDisk, err := os.ReadFile(rec.StoredPath)
	require.NoError(t, err)
	assert.Equal(t, content, onDisk)
	assert.Equal(t, hashOf(content), rec.SHA256)

	// Metadata fields carry provenance + linkage.
	assert.Equal(t, "IMG_4823.jpg", rec.OriginalFilename)
	assert.Equal(t, "2026-07-05", rec.LogicalDay)
	assert.Equal(t, "cli", rec.Source)
	assert.Equal(t, "raw_2026_07_05_18_41", rec.RawEntryID)

	// The sidecar sits beside the binary and re-decodes to the same record.
	back, err := a.ReadMediaForDay("2026-07-05")
	require.NoError(t, err)
	require.Len(t, back, 1)
	assert.Equal(t, rec.ID, back[0].ID)
	assert.Equal(t, rec.SHA256, back[0].SHA256)
	assert.Equal(t, rec.StoredPath, back[0].StoredPath)
	// A reader re-hashing the file detects no drift.
	assert.Equal(t, back[0].SHA256, hashOf(onDisk))
}

func TestWriteMedia_NonImageStoredOpaquely(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	pdf := []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\nopaque pdf body bytes\n%%EOF")
	rec, err := a.WriteMedia(syntheticMedia("scanned page", "scan.pdf", pdf))
	require.NoError(t, err)

	assert.Equal(t, ".pdf", filepath.Ext(rec.ID), "original extension preserved")
	onDisk, err := os.ReadFile(rec.StoredPath)
	require.NoError(t, err)
	assert.Equal(t, pdf, onDisk, "any binary is stored byte-for-byte, no transcode")
	assert.Equal(t, hashOf(pdf), rec.SHA256)
}

func TestWriteMedia_SlugFromBasenameWhenNoCaption(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	rec, err := a.WriteMedia(syntheticMedia("", "My Vacation Photo.HEIC", []byte("x")))
	require.NoError(t, err)
	// Slug from the basename (extension dropped), lowercased + hyphenated;
	// the original extension is preserved verbatim.
	assert.Equal(t, "2026-07-05-my-vacation-photo.HEIC", rec.ID)
}

func TestWriteMedia_SlugFallbackForPunctuationOnlyName(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	rec, err := a.WriteMedia(syntheticMedia("", "___.png", []byte("x")))
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05-attachment.png", rec.ID)
}

func TestWriteMedia_SlugTruncatedToMax(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	long := strings.Repeat("a", maxMediaSlugLen+40)
	rec, err := a.WriteMedia(syntheticMedia(long, "x.jpg", []byte("x")))
	require.NoError(t, err)
	slug := strings.TrimSuffix(strings.TrimPrefix(rec.ID, "2026-07-05-"), ".jpg")
	assert.Len(t, slug, maxMediaSlugLen)
}

func TestWriteMedia_SameDaySlugCollisionAppendsN(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	first, err := a.WriteMedia(syntheticMedia("progress photo", "a.jpg", []byte("one")))
	require.NoError(t, err)
	second, err := a.WriteMedia(syntheticMedia("progress photo", "b.jpg", []byte("two")))
	require.NoError(t, err)
	third, err := a.WriteMedia(syntheticMedia("progress photo", "c.jpg", []byte("three")))
	require.NoError(t, err)

	assert.Equal(t, "2026-07-05-progress-photo.jpg", first.ID)
	assert.Equal(t, "2026-07-05-progress-photo_2.jpg", second.ID)
	assert.Equal(t, "2026-07-05-progress-photo_3.jpg", third.ID)

	// All three survive independently; the first is never overwritten.
	assert.Equal(t, []byte("one"), mustRead(t, first.StoredPath))
	assert.Equal(t, []byte("two"), mustRead(t, second.StoredPath))

	recs, err := a.ReadMediaForDay("2026-07-05")
	require.NoError(t, err)
	assert.Len(t, recs, 3)
}

func TestWriteMedia_JSONOriginalDoesNotClobberSidecar(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	// A `.json` original: the binary is `…-export.json`, its sidecar is
	// `…-export.json.json` — distinct, no clobber. The bytes are opaque (not the
	// sidecar's schema).
	original := []byte("opaque .json original bytes")
	rec, err := a.WriteMedia(syntheticMedia("", "export.json", original))
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05-export.json", rec.ID)

	binPath := rec.StoredPath
	sidecarPath := binPath + ".json"
	assert.FileExists(t, binPath)
	assert.FileExists(t, sidecarPath)

	// The binary still holds the user's bytes, not the sidecar.
	assert.Equal(t, original, mustRead(t, binPath))

	// The day read counts exactly one attachment, not two (the `.json` binary
	// is not mistaken for a sidecar).
	recs, err := a.ReadMediaForDay("2026-07-05")
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "2026-07-05-export.json", recs[0].ID)
}

func TestWriteMedia_FromSourcePath(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	src := filepath.Join(t.TempDir(), "drop.png")
	want := []byte("bytes from a file on disk")
	require.NoError(t, os.WriteFile(src, want, 0o600))

	att := syntheticMedia("a whiteboard", "drop.png", nil)
	att.SourcePath = src
	rec, err := a.WriteMedia(att)
	require.NoError(t, err)
	assert.Equal(t, want, mustRead(t, rec.StoredPath))
	assert.Equal(t, hashOf(want), rec.SHA256)
}

func TestWriteMedia_MissingSourceLeavesNothing(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	att := syntheticMedia("x", "gone.jpg", nil)
	att.SourcePath = filepath.Join(t.TempDir(), "does-not-exist.jpg")
	_, err := a.WriteMedia(att)
	require.Error(t, err)

	// Nothing was written: no shard, no stray files (error-states §St-1).
	_, statErr := os.Stat(filepath.Join(home, "media", "2026"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestWriteMedia_SidecarSchemaShape(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	// With a caption: caption present, alt null.
	rec, err := a.WriteMedia(syntheticMedia("captioned", "a.jpg", []byte("x")))
	require.NoError(t, err)
	fields := decodeSidecarFile(t, rec.StoredPath+".json")
	assert.Equal(t, rec.ID, fields["id"])
	assert.Equal(t, rec.SHA256, fields["sha256"])
	assert.Equal(t, "a.jpg", fields["original_filename"])
	assert.Equal(t, "2026-07-05", fields["logical_day"])
	assert.Equal(t, "captioned", fields["caption"])
	assert.Equal(t, "raw_2026_07_05_18_41", fields["raw_entry_id"])
	assert.Equal(t, "cli", fields["source"])
	require.Contains(t, fields, "alt")
	assert.Nil(t, fields["alt"], "alt renders as JSON null when unset")

	// Without a caption: the field is omitted on disk (the drop-it path).
	rec2, err := a.WriteMedia(syntheticMedia("", "b.jpg", []byte("y")))
	require.NoError(t, err)
	fields2 := decodeSidecarFile(t, rec2.StoredPath+".json")
	assert.NotContains(t, fields2, "caption", "empty caption is absent on disk")
}

func TestWriteMedia_AltRendersWhenSet(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	att := syntheticMedia("c", "a.jpg", []byte("x"))
	att.Alt = "a person at a whiteboard"
	rec, err := a.WriteMedia(att)
	require.NoError(t, err)
	fields := decodeSidecarFile(t, rec.StoredPath+".json")
	assert.Equal(t, "a person at a whiteboard", fields["alt"])
}

func TestWriteMedia_Permissions(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	rec, err := a.WriteMedia(syntheticMedia("perms", "a.jpg", []byte("x")))
	require.NoError(t, err)

	fi, err := os.Stat(rec.StoredPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "stored binary is owner-only")

	di, err := os.Stat(filepath.Dir(rec.StoredPath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), di.Mode().Perm(), "shard dir is owner-only")
}

func TestWriteMedia_ValidationErrors(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	base := syntheticMedia("x", "a.jpg", []byte("x"))
	cases := map[string]func(MediaAttachment) MediaAttachment{
		"no content":        func(m MediaAttachment) MediaAttachment { m.Bytes = nil; m.SourcePath = ""; return m },
		"no original name":  func(m MediaAttachment) MediaAttachment { m.OriginalFilename = ""; return m },
		"zero captured_at":  func(m MediaAttachment) MediaAttachment { m.CapturedAt = time.Time{}; return m },
		"no source":         func(m MediaAttachment) MediaAttachment { m.Source = ""; return m },
		"bad logical day":   func(m MediaAttachment) MediaAttachment { m.LogicalDay = "not-a-date"; return m },
		"empty logical day": func(m MediaAttachment) MediaAttachment { m.LogicalDay = ""; return m },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := a.WriteMedia(mutate(base))
			require.Error(t, err)
		})
	}
}

func TestWriteMedia_ShardCreateFailure(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	// A regular file where the media dir must live makes MkdirAll fail — both
	// for the on-demand shard create and for ScaffoldMedia.
	require.NoError(t, os.WriteFile(filepath.Join(home, "media"), []byte("x"), 0o600))

	_, err := a.WriteMedia(syntheticMedia("x", "a.jpg", []byte("y")))
	require.Error(t, err)
	require.Error(t, a.ScaffoldMedia())
}

func TestWriteMedia_SidecarCollisionCleansUpBinary(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	shard := filepath.Join(home, "media", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	// Pre-place the sidecar for the first candidate but not its binary — an
	// anomaly that must not leave an orphan binary behind (St-1).
	require.NoError(t, os.WriteFile(filepath.Join(shard, "2026-07-05-notes.jpg.json"), []byte("{}"), 0o600))

	_, err := a.WriteMedia(syntheticMedia("notes", "x.jpg", []byte("body")))
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(shard, "2026-07-05-notes.jpg"), "the briefly-written binary is removed")
}

func TestReadMediaForDay_MissingShardIsEmpty(t *testing.T) {
	a := New(t.TempDir())
	recs, err := a.ReadMediaForDay("2026-07-05")
	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestReadMediaForDay_BadDate(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.ReadMediaForDay("nope")
	require.Error(t, err)
}

func TestReadMediaForDay_SkipsNonSidecarAndCorrupt(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	good, err := a.WriteMedia(syntheticMedia("keep", "a.jpg", []byte("x")))
	require.NoError(t, err)
	shard := filepath.Dir(good.StoredPath)

	// A `.json` original with no sidecar: not a stored attachment.
	require.NoError(t, os.WriteFile(filepath.Join(shard, "2026-07-05-stray.json"), []byte(`{"foo":1}`), 0o600))
	// A binary + a corrupt sidecar: parse fails, skipped (not a read failure).
	require.NoError(t, os.WriteFile(filepath.Join(shard, "2026-07-05-broken.dat"), []byte("bin"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "2026-07-05-broken.dat.json"), []byte("{not json"), 0o600))
	// A binary + a sidecar whose id disagrees with its filename: skipped.
	require.NoError(t, os.WriteFile(filepath.Join(shard, "2026-07-05-mismatch.dat"), []byte("bin"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(shard, "2026-07-05-mismatch.dat.json"), []byte(`{"id":"wrong"}`), 0o600))

	recs, err := a.ReadMediaForDay("2026-07-05")
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, good.ID, recs[0].ID)
}

func TestReadMediaForDay_PrefixFiltersOtherDays(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	m1 := syntheticMedia("day five", "a.jpg", []byte("x"))
	_, err := a.WriteMedia(m1)
	require.NoError(t, err)

	// Same month shard, different day — must not appear in the day-five read.
	m2 := syntheticMedia("day six", "b.jpg", []byte("y"))
	m2.LogicalDay = "2026-07-06"
	_, err = a.WriteMedia(m2)
	require.NoError(t, err)

	five, err := a.ReadMediaForDay("2026-07-05")
	require.NoError(t, err)
	require.Len(t, five, 1)
	assert.Equal(t, "2026-07-05", five[0].LogicalDay)

	six, err := a.ReadMediaForDay("2026-07-06")
	require.NoError(t, err)
	require.Len(t, six, 1)
	assert.Equal(t, "2026-07-06", six[0].LogicalDay)
}

func TestListMedia(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	// Empty store: no records, no error.
	recs, err := a.ListMedia()
	require.NoError(t, err)
	assert.Empty(t, recs)

	m1 := syntheticMedia("alpha", "a.jpg", []byte("x"))
	_, err = a.WriteMedia(m1)
	require.NoError(t, err)
	m2 := syntheticMedia("bravo", "b.jpg", []byte("y"))
	m2.LogicalDay = "2026-08-01"
	_, err = a.WriteMedia(m2)
	require.NoError(t, err)

	recs, err = a.ListMedia()
	require.NoError(t, err)
	require.Len(t, recs, 2, "walks the whole tree across month shards")
	assert.Equal(t, "2026-07-05-alpha.jpg", recs[0].ID)
	assert.Equal(t, "2026-08-01-bravo.jpg", recs[1].ID)
}

func TestScaffoldMedia_Idempotent(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	require.NoError(t, a.ScaffoldMedia())
	require.NoError(t, a.ScaffoldMedia()) // second run makes no change and no error

	fi, err := os.Stat(filepath.Join(home, "media"))
	require.NoError(t, err)
	assert.True(t, fi.IsDir())
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
}

// mustRead reads a file or fails the test.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

// decodeSidecarFile reads a sidecar JSON file into a generic map so the test
// can assert exact on-disk shape (presence, null, values).
func decodeSidecarFile(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}
