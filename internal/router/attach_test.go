package router

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// writeTempFile writes content to a fresh file under t.TempDir() and returns
// its path — the source file an attach test drops.
func writeTempFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

// sha256Hex returns the hex sha256 of b — the independent integrity recompute
// the attach tests compare against the stored sidecar.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// mustListMedia returns every stored media record, failing the test on error.
func mustListMedia(t *testing.T, a *storage.Adapter) []storage.MediaRecord {
	t.Helper()
	recs, err := a.ListMedia()
	require.NoError(t, err)
	return recs
}

// assertNothingWritten asserts a rejected attach left the Ledger untouched:
// no raw entry, no session, no media (error-states.md §St-1).
func assertNothingWritten(t *testing.T, a *storage.Adapter, home string) {
	t.Helper()
	assert.Equal(t, 0, countFiles(t, home, "raw"), "nothing written under raw/")
	assert.Equal(t, 0, countFiles(t, home, "sessions"), "no dangling session")
	assert.Empty(t, mustListMedia(t, a), "nothing written under media/")
}

// TestAttach_StoresMediaAndLinksRawEntry is the happy-path round-trip: an
// attach copies the file into the media store (sha256 recomputes), attributes
// it to the current logical day, links it to one immutable raw entry whose
// body references the stored path + caption, and returns a confirm-back ack.
func TestAttach_StoresMediaAndLinksRawEntry(t *testing.T) {
	r, a, home := newBootedRouter(t)
	content := []byte("\xff\xd8\xff synthetic jpeg bytes")
	path := writeTempFile(t, "IMG_4823.jpg", content)

	res, err := r.Attach(AttachRequest{
		Path:    path,
		Caption: "handwritten session notes, page 1",
		Now:     fixedNow(),
	})
	require.NoError(t, err)

	// Result surfaces exactly what was stored and where.
	assert.Equal(t, "2026-07-05", res.Day)
	assert.Equal(t, "raw_2026_07_05_18_41", res.RawID)
	assert.Equal(t, sha256Hex(content), res.SHA256)
	assert.Equal(t, "handwritten session notes, page 1", res.Caption)
	want := filepath.Join(home, "media", "2026", "07", "2026-07-05-handwritten-session-notes-page-1.jpg")
	assert.Equal(t, want, res.StoredPath)

	// The stored bytes are byte-identical and the sha matches.
	stored, err := os.ReadFile(res.StoredPath)
	require.NoError(t, err)
	assert.Equal(t, content, stored)
	assert.Equal(t, sha256Hex(content), res.SHA256)

	// The sidecar links back to the raw entry (the day-view/Mirror join).
	recs := mustListMedia(t, a)
	require.Len(t, recs, 1)
	assert.Equal(t, res.RawID, recs[0].RawEntryID, "sidecar links to the emitted raw entry")
	assert.Equal(t, "2026-07-05", recs[0].LogicalDay)
	assert.Equal(t, sha256Hex(content), recs[0].SHA256)
	assert.Equal(t, "IMG_4823.jpg", recs[0].OriginalFilename)

	// The raw entry body references the stored media path + caption verbatim.
	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t,
		"Media attachment media/2026/07/2026-07-05-handwritten-session-notes-page-1.jpg — handwritten session notes, page 1",
		doc.EntryText())
	assert.Equal(t, commandAttach, doc.Fields["command"])
	assert.Equal(t, "exact", doc.Fields["occurred_at_precision"])

	// The ack confirms what/where after the write lands.
	assert.Contains(t, res.Ack, "media/2026/07/2026-07-05-handwritten-session-notes-page-1.jpg")
	assert.Contains(t, res.Ack, "2026-07-05")
	assert.Contains(t, res.Ack, res.SHA256[:shaAckPrefixLen])
	assert.Contains(t, res.Ack, res.RawID)

	// One raw + one session; nothing structured (Sanctuary — no model ran).
	assert.Equal(t, 1, countFiles(t, home, "raw"))
	assert.Equal(t, 1, countFiles(t, home, "sessions"))
	assert.Equal(t, 0, countFiles(t, home, "processed"))
	assert.Equal(t, 0, countFiles(t, home, "insights"))
}

// TestAttach_NonImageBinaryStoredOpaquely proves any binary is stored
// content-agnostically (Q4=B): a PDF-ish blob round-trips with its original
// extension preserved and no type gate.
func TestAttach_NonImageBinaryStoredOpaquely(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	content := []byte("%PDF-1.7\n… scanned intake form bytes …\n%%EOF")
	path := writeTempFile(t, "scan.pdf", content)

	res, err := r.Attach(AttachRequest{Path: path, Caption: "clinic intake form", Now: fixedNow()})
	require.NoError(t, err)

	assert.Equal(t, ".pdf", filepath.Ext(res.StoredPath))
	stored, err := os.ReadFile(res.StoredPath)
	require.NoError(t, err)
	assert.Equal(t, content, stored)
	assert.Equal(t, sha256Hex(content), res.SHA256)
	recs := mustListMedia(t, a)
	require.Len(t, recs, 1)
	assert.Equal(t, "scan.pdf", recs[0].OriginalFilename)
}

// TestAttach_Yesterday attributes the media to the prior logical day via the
// observations @yesterday backdate — approximate precision on the raw entry.
func TestAttach_Yesterday(t *testing.T) {
	r, a, home := newBootedRouter(t)
	path := writeTempFile(t, "before.png", []byte("day-0 photo"))

	res, err := r.Attach(AttachRequest{Path: path, DayArg: "@yesterday", Now: fixedNow()})
	require.NoError(t, err)

	assert.Equal(t, "2026-07-04", res.Day)
	assert.Equal(t, filepath.Join(home, "media", "2026", "07"), filepath.Dir(res.StoredPath))

	recs := mustListMedia(t, a)
	require.Len(t, recs, 1)
	assert.Equal(t, "2026-07-04", recs[0].LogicalDay)

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "approximate", doc.Fields["occurred_at_precision"])
}

// TestAttach_ExplicitDate honors @YYYY-MM-DD backdating.
func TestAttach_ExplicitDate(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	path := writeTempFile(t, "whiteboard.jpg", []byte("whiteboard"))

	res, err := r.Attach(AttachRequest{Path: path, DayArg: "@2026-07-01", Now: fixedNow()})
	require.NoError(t, err)

	assert.Equal(t, "2026-07-01", res.Day)
	recs := mustListMedia(t, a)
	require.Len(t, recs, 1)
	assert.Equal(t, "2026-07-01", recs[0].LogicalDay)
}

// TestAttach_RolloverBeforeFour files a pre-dawn capture (02:00, before the
// 04:00 rollover) under the day just lived — the same boundary events use, no
// second clock.
func TestAttach_RolloverBeforeFour(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	preDawn := time.Date(2026, time.July, 5, 2, 0, 0, 0, time.UTC)
	path := writeTempFile(t, "late.jpg", []byte("2am capture"))

	res, err := r.Attach(AttachRequest{Path: path, Now: preDawn})
	require.NoError(t, err)

	assert.Equal(t, "2026-07-04", res.Day, "before 04:00 files under yesterday's logical day")
	recs := mustListMedia(t, a)
	require.Len(t, recs, 1)
	assert.Equal(t, "2026-07-04", recs[0].LogicalDay)

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "exact", doc.Fields["occurred_at_precision"], "the default (no --day) is an exact now")
}

// TestAttach_RecordsProvenance proves the harness provenance rides the session
// record and the media/raw source — the same fields /log carries, no new
// plumbing.
func TestAttach_RecordsProvenance(t *testing.T) {
	r, a, home := newBootedRouter(t)
	path := writeTempFile(t, "relayed.jpg", []byte("relayed by an assistant"))

	res, err := r.Attach(AttachRequest{
		Path:      path,
		Now:       fixedNow(),
		Source:    "discord",
		Harness:   " Discord ",
		ChannelID: "chan-1",
	})
	require.NoError(t, err)

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "discord", doc.Fields["source"], "raw source records the supplied harness token")

	sessionID, ok := doc.Fields["session_id"].(string)
	require.True(t, ok, "raw entry carries its session id")
	sess := readSessionMap(t, home, sessionID)
	assert.Equal(t, "discord", sess["harness"], "harness is normalized onto the session")
	assert.Equal(t, "chan-1", sess["channel_id"])

	recs := mustListMedia(t, a)
	require.Len(t, recs, 1)
	assert.Equal(t, "discord", recs[0].Source, "media sidecar records the normalized source")
}

// TestAttach_EmptyCaption keeps the frictionless "drop it" path: no caption,
// the slug falls back to the original basename, and the body omits the dash.
func TestAttach_EmptyCaption(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	path := writeTempFile(t, "artifact.bin", []byte("opaque artifact"))

	res, err := r.Attach(AttachRequest{Path: path, Now: fixedNow()})
	require.NoError(t, err)

	assert.Empty(t, res.Caption)
	assert.Equal(t, "2026-07-05-artifact.bin", filepath.Base(res.StoredPath), "slug from the original basename")

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "Media attachment media/2026/07/2026-07-05-artifact.bin", doc.EntryText())

	recs := mustListMedia(t, a)
	require.Len(t, recs, 1)
	assert.Empty(t, recs[0].Caption)
}

// TestAttach_MissingFile rejects an unreadable path with nothing written
// (error-states.md §St-1).
func TestAttach_MissingFile(t *testing.T) {
	r, a, home := newBootedRouter(t)

	_, err := r.Attach(AttachRequest{Path: filepath.Join(t.TempDir(), "nope.jpg"), Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
	assertNothingWritten(t, a, home)
}

// TestAttach_EmptyPath rejects a blank path before any write.
func TestAttach_EmptyPath(t *testing.T) {
	r, a, home := newBootedRouter(t)

	_, err := r.Attach(AttachRequest{Path: "   ", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a file path")
	assertNothingWritten(t, a, home)
}

// TestAttach_MalformedSourceWritesNothing proves an invalid source token is
// rejected before anything lands (never coerced to cli).
func TestAttach_MalformedSourceWritesNothing(t *testing.T) {
	r, a, home := newBootedRouter(t)
	path := writeTempFile(t, "x.jpg", []byte("x"))

	_, err := r.Attach(AttachRequest{Path: path, Now: fixedNow(), Source: "bad token!"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
	assertNothingWritten(t, a, home)
}

// TestAttach_MalformedHarnessWritesNothing proves a malformed harness token is
// rejected before anything lands, even though the harness only reaches the
// session record.
func TestAttach_MalformedHarnessWritesNothing(t *testing.T) {
	r, a, home := newBootedRouter(t)
	path := writeTempFile(t, "x.jpg", []byte("x"))

	_, err := r.Attach(AttachRequest{Path: path, Now: fixedNow(), Harness: "bad harness!"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
	assertNothingWritten(t, a, home)
}

// TestAttach_BadDayArgWritesNothing rejects an unparseable --day before any
// write.
func TestAttach_BadDayArgWritesNothing(t *testing.T) {
	r, a, home := newBootedRouter(t)
	path := writeTempFile(t, "x.jpg", []byte("x"))

	_, err := r.Attach(AttachRequest{Path: path, Now: fixedNow(), DayArg: "@notaday"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
	assertNothingWritten(t, a, home)
}

// TestAttach_RawWriteFailureSurfaces covers the branch where the media lands
// but the raw entry cannot be written (error-states.md §St-1: the error is
// explicit and surfaced, never swallowed).
func TestAttach_RawWriteFailureSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, _, home := newBootedRouter(t)
	rawDir := filepath.Join(home, "raw")
	require.NoError(t, os.Chmod(rawDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(rawDir, 0o700) })
	path := writeTempFile(t, "x.jpg", []byte("x"))

	_, err := r.Attach(AttachRequest{Path: path, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "raw entry")
}

// TestAttach_SessionWriteFailureSurfaces covers the branch where the media and
// raw entry land but the session record cannot be written.
func TestAttach_SessionWriteFailureSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, _, home := newBootedRouter(t)
	sessDir := filepath.Join(home, "sessions")
	require.NoError(t, os.Chmod(sessDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(sessDir, 0o700) })
	path := writeTempFile(t, "x.jpg", []byte("x"))

	_, err := r.Attach(AttachRequest{Path: path, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session record")
}

// TestMediaRelPath_Fallback proves a record outside the Ledger home falls back
// to the stored id rather than erroring.
func TestMediaRelPath_Fallback(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	rel := r.mediaRelPath(storage.MediaRecord{ID: "fallback.jpg", StoredPath: "relative/not/absolute.jpg"})
	assert.Equal(t, "fallback.jpg", rel)
}

// TestResolveAttachDay covers the day-resolution branches directly.
func TestResolveAttachDay(t *testing.T) {
	now := fixedNow()

	occ, prec, day, err := resolveAttachDay("", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", day)
	assert.Equal(t, storage.PrecisionExact, prec)
	assert.Equal(t, now, occ)

	_, prec, day, err = resolveAttachDay("@yesterday", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-04", day)
	assert.Equal(t, storage.PrecisionApproximate, prec)

	// The bare (no-@) form is accepted too.
	_, _, day, err = resolveAttachDay("yesterday", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-04", day)

	_, prec, day, err = resolveAttachDay("2026-06-30", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-06-30", day)
	assert.Equal(t, storage.PrecisionApproximate, prec)

	_, _, badDay, badErr := resolveAttachDay("@notaday", now)
	require.Error(t, badErr)
	assert.Empty(t, badDay)
}

// TestAttachHelpers guards the ack/body/short-sha copy directly.
func TestAttachHelpers(t *testing.T) {
	assert.Equal(t, "Media attachment media/x.jpg", attachBody("media/x.jpg", ""))
	assert.Equal(t, "Media attachment media/x.jpg — a note", attachBody("media/x.jpg", "a note"))
	assert.Equal(t, "raw_2026_07_05_18_41", predictRawID(fixedNow()))

	ack := attachAck("media/x.jpg", "2026-07-05", "0123456789abcdef0123", "raw_1")
	assert.Contains(t, ack, "media/x.jpg")
	assert.Contains(t, ack, "2026-07-05")
	assert.Contains(t, ack, "0123456789ab…")
	assert.Contains(t, ack, "raw_1")
	assert.Equal(t, "abc", shortSHA("abc"))
}
