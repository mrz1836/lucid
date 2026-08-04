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

// TestAttach_YesterdayPreRollover pins the other half of the pre-dawn window:
// a 02:00 catch-up means the day before the one just lived. A bare capture at
// that hour files under 2026-07-04 (above), so @yesterday steps back from that
// logical day to 2026-07-03 rather than collapsing onto it.
func TestAttach_YesterdayPreRollover(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	preDawn := time.Date(2026, time.July, 5, 2, 0, 0, 0, time.UTC)
	path := writeTempFile(t, "whiteboard.jpg", []byte("whiteboard snapshot"))

	res, err := r.Attach(AttachRequest{Path: path, DayArg: "@yesterday", Now: preDawn})
	require.NoError(t, err)

	assert.Equal(t, "2026-07-03", res.Day, "@yesterday steps back from the current logical day")

	recs := mustListMedia(t, a)
	require.Len(t, recs, 1)
	assert.Equal(t, "2026-07-03", recs[0].LogicalDay, "the sidecar files under the attributed day")

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "approximate", doc.Fields["occurred_at_precision"])
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

// TestAttach_FutureDayWritesNothing refuses a day that has not happened yet:
// the realistic typo (a wrong year, transposed digits) would otherwise file a
// capture silently far ahead. Nothing lands (error-states.md §St-1).
func TestAttach_FutureDayWritesNothing(t *testing.T) {
	r, a, home := newBootedRouter(t)
	path := writeTempFile(t, "x.jpg", []byte("x"))

	_, err := r.Attach(AttachRequest{Path: path, Now: fixedNow(), DayArg: "@2027-08-01"})
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

// TestResolveCaptureDay covers the day-resolution branches directly — the one
// backdating grammar every capture verb shares.
func TestResolveCaptureDay(t *testing.T) {
	now := fixedNow()

	occ, prec, day, err := resolveCaptureDay("", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", day)
	assert.Equal(t, storage.PrecisionExact, prec)
	assert.Equal(t, now, occ)

	_, prec, day, err = resolveCaptureDay("@yesterday", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-04", day)
	assert.Equal(t, storage.PrecisionApproximate, prec)

	// The bare (no-@) form is accepted too.
	_, _, day, err = resolveCaptureDay("yesterday", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-04", day)

	_, prec, day, err = resolveCaptureDay("2026-06-30", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-06-30", day)
	assert.Equal(t, storage.PrecisionApproximate, prec)

	// Before the 04:00 rollover, @yesterday steps back from the current
	// *logical* day: at 02:00 on the 5th a bare capture files under the 4th, so
	// a 02:00 catch-up means the day before the one just lived — the 3rd.
	preDawn := time.Date(2026, time.July, 5, 2, 0, 0, 0, time.UTC)
	_, prec, day, err = resolveCaptureDay("@yesterday", preDawn)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-03", day, "@yesterday steps back from the logical day, not the civil one")
	assert.Equal(t, storage.PrecisionApproximate, prec)

	// The ceiling is the current civil date, inclusive — today always resolves.
	_, _, day, err = resolveCaptureDay("2026-07-05", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", day)

	// A day that has not happened yet is refused before anything is written.
	_, _, futureDay, futureErr := resolveCaptureDay("@2027-08-01", now)
	require.Error(t, futureErr)
	assert.Contains(t, futureErr.Error(), "nothing was saved")
	assert.Empty(t, futureDay)

	_, _, badDay, badErr := resolveCaptureDay("@notaday", now)
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

// TestResolveCaptureWhen_SharedGrammar covers the forms the capture verbs
// gained by routing --day through the shared resolver: a trailing time, the
// partial dates, a bare clock time, and a clock range (which is the one form
// that yields an end, so the record can honor `occurred_at_end`).
func TestResolveCaptureWhen_SharedGrammar(t *testing.T) {
	now := fixedNow()

	// A trailing time promotes the day to exact precision at that hour.
	when, err := resolveCaptureWhen("@yesterday 19:30", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-04", when.LogicalDate)
	assert.Equal(t, storage.PrecisionExact, when.Precision)
	assert.Equal(t, 19, when.OccurredAt.Hour())
	assert.Equal(t, 30, when.OccurredAt.Minute())

	// A partial date snaps to the period's first instant, approximate.
	when, err = resolveCaptureWhen("2014", now)
	require.NoError(t, err)
	assert.Equal(t, "2014-01-01", when.LogicalDate)
	assert.Equal(t, storage.PrecisionApproximate, when.Precision)

	when, err = resolveCaptureWhen("2014-09", now)
	require.NoError(t, err)
	assert.Equal(t, "2014-09-01", when.LogicalDate)
	assert.Equal(t, storage.PrecisionApproximate, when.Precision)

	// A bare clock time is today at that time, exact.
	when, err = resolveCaptureWhen("09:15", now)
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", when.LogicalDate)
	assert.Equal(t, storage.PrecisionExact, when.Precision)
	assert.Nil(t, when.End)

	// A clock range is the only form with an end — it must survive to the
	// record, or `range` precision would name a span the entry never states.
	when, err = resolveCaptureWhen("09:00-11:00", now)
	require.NoError(t, err)
	assert.Equal(t, storage.PrecisionRange, when.Precision)
	require.NotNil(t, when.End)
	assert.Equal(t, 9, when.OccurredAt.Hour())
	assert.Equal(t, 11, when.End.Hour())
}

// TestResolveCaptureWhen_RejectionsNameTheProblem proves the two strict-tier
// refusals stay legible: an unreadable token lists the accepted forms, and a
// future day names the day it resolved to. Both say nothing was saved.
func TestResolveCaptureWhen_RejectionsNameTheProblem(t *testing.T) {
	now := fixedNow()

	_, err := resolveCaptureWhen("@yesterdya", now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the day")
	assert.Contains(t, err.Error(), "@yesterday", "the message teaches the grammar it just refused")
	assert.Contains(t, err.Error(), "nothing was saved")

	_, err = resolveCaptureWhen("@2027-08-01", now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2027-08-01", "the refusal names the day it resolved to")
	assert.Contains(t, err.Error(), "that day has not happened yet")
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestLogAttach_BootstrapModeStampsHistorical proves `bootstrap` follows the
// mode rather than the entry path: a capture made while historical-entry mode
// is on is flagged as historical, and stops being flagged after
// `/bootstrap done`. Before this, a bulk history load via `lucid log --day`
// wrote entries the flag called ordinary.
func TestLogAttach_BootstrapModeStampsHistorical(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	photo := writeTempFile(t, "beach.jpg", []byte("synthetic-bytes"))

	// Off by default: an ordinary capture is not historical.
	before, err := r.Log(LogRequest{Text: "an ordinary entry", Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, rawBootstrapFlag(t, a, before.RawID))

	_, err = r.Bootstrap(BootstrapRequest{Done: false})
	require.NoError(t, err)

	logged, err := r.Log(LogRequest{Text: "a story from years ago", Now: fixedNow(), DayArg: "2014"})
	require.NoError(t, err)
	assert.True(t, rawBootstrapFlag(t, a, logged.RawID),
		"a capture made in bootstrap mode is flagged historical")

	attached, err := r.Attach(AttachRequest{Path: photo, Now: fixedNow(), DayArg: "2014"})
	require.NoError(t, err)
	assert.True(t, rawBootstrapFlag(t, a, attached.RawID))

	// `/bootstrap done` ends it — the next capture is ordinary again.
	_, err = r.Bootstrap(BootstrapRequest{Done: true})
	require.NoError(t, err)

	after, err := r.Log(LogRequest{Text: "back to today", Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, rawBootstrapFlag(t, a, after.RawID))
}

// rawBootstrapFlag reads the `bootstrap` frontmatter flag off a written raw
// entry — every raw entry carries one, so a missing value is a failure rather
// than a false.
func rawBootstrapFlag(t *testing.T, a *storage.Adapter, rawID string) bool {
	t.Helper()
	doc, err := a.ReadRaw(rawID)
	require.NoError(t, err)
	flag, ok := doc.Fields["bootstrap"].(bool)
	require.True(t, ok, "raw entry %s carries no bootstrap flag", rawID)
	return flag
}
