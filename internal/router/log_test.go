package router

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// fixedNow is a deterministic capture instant for the /log tests.
func fixedNow() time.Time {
	return time.Date(2026, time.July, 5, 18, 41, 39, 0, time.UTC)
}

// newBootedRouter returns a router over a fresh scaffolded + booted temp
// Ledger, plus the adapter and home path.
func newBootedRouter(t *testing.T) (*Router, *storage.Adapter, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".lucid")
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	r := New(a)
	_, err = r.Boot()
	require.NoError(t, err)
	return r, a, home
}

// countFiles returns the number of non-.keep entries under home/sub.
func countFiles(t *testing.T, home, sub string) int {
	t.Helper()
	var n int
	err := filepath.WalkDir(filepath.Join(home, sub), func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() != ".keep" {
			n++
		}
		return nil
	})
	require.NoError(t, err)
	return n
}

// TestLog_WritesValidRawEntry is acceptance case 2.1: a /log writes one
// valid raw entry with a sub-second ack and nothing under processed/ or
// insights/.
func TestLog_WritesValidRawEntry(t *testing.T) {
	r, a, home := newBootedRouter(t)

	res, err := r.Log(LogRequest{Text: "Quiet day. Read for an hour.", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "raw_2026_07_05_18_41", res.RawID)
	assert.False(t, res.EmptyBody)
	assert.Equal(t, "Saved as `raw_2026_07_05_18_41`.", res.Ack)

	// A valid raw file exists and round-trips.
	path := filepath.Join(home, "raw", "2026", "07", res.RawID+".md")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, storage.ValidateRawFrontmatter(content))
	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "# Entry\n\nQuiet day. Read for an hour.", doc.Body)

	// A session record was written; nothing under processed/ or insights/.
	assert.Equal(t, 1, countFiles(t, home, "sessions"))
	assert.Equal(t, 0, countFiles(t, home, "processed"))
	assert.Equal(t, 0, countFiles(t, home, "insights"))
}

// TestLog_EmptyBody is acceptance case 2.2: an empty /log is a valid
// entry whose ack notes the empty body (error-states.md §S-3) and which
// triggers no Structuring.
func TestLog_EmptyBody(t *testing.T) {
	r, _, home := newBootedRouter(t)

	res, err := r.Log(LogRequest{Text: "", Now: fixedNow()})
	require.NoError(t, err)
	assert.True(t, res.EmptyBody)
	assert.Contains(t, res.Ack, "body was empty")

	path := filepath.Join(home, "raw", "2026", "07", res.RawID+".md")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, storage.ValidateRawFrontmatter(content))
	assert.Equal(t, 0, countFiles(t, home, "processed"))
	assert.Equal(t, 0, countFiles(t, home, "insights"))
}

// TestLog_SameMinuteCollision is acceptance case 2.3: two /log in the
// same minute both land, the second carrying the _SS collision suffix.
func TestLog_SameMinuteCollision(t *testing.T) {
	r, _, home := newBootedRouter(t)
	now := fixedNow()

	first, err := r.Log(LogRequest{Text: "first", Now: now})
	require.NoError(t, err)
	second, err := r.Log(LogRequest{Text: "second", Now: now})
	require.NoError(t, err)

	assert.Equal(t, "raw_2026_07_05_18_41", first.RawID)
	assert.Equal(t, "raw_2026_07_05_18_41_39", second.RawID)
	assert.Equal(t, 2, countFiles(t, home, "raw"))
	assert.Equal(t, 2, countFiles(t, home, "sessions"))
}

// TestLog_DiskFull is acceptance case 2.4 / error-states.md §St-1: a
// write failure surfaces an explicit error, writes nothing, and a retry
// after the condition clears succeeds.
func TestLog_DiskFull(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, _, home := newBootedRouter(t)
	rawDir := filepath.Join(home, "raw")
	require.NoError(t, os.Chmod(rawDir, 0o500))

	_, err := r.Log(LogRequest{Text: "cannot land", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
	assert.Equal(t, 0, countFiles(t, home, "raw"), "nothing written under raw/")
	assert.Equal(t, 0, countFiles(t, home, "sessions"), "no dangling session")

	// Clear the condition and retry: the capture now lands.
	require.NoError(t, os.Chmod(rawDir, 0o700))
	res, err := r.Log(LogRequest{Text: "cannot land", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "raw_2026_07_05_18_41", res.RawID)
}

// TestLog_SessionWriteFailureSurfaces covers the branch where the raw
// entry lands but the session record cannot be written.
func TestLog_SessionWriteFailureSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, _, home := newBootedRouter(t)
	sessDir := filepath.Join(home, "sessions")
	require.NoError(t, os.Chmod(sessDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(sessDir, 0o700) })

	_, err := r.Log(LogRequest{Text: "raw lands, session fails", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session record")
}

// TestLog_DefaultsSourceAndClock covers the zero-Now / empty-Source
// defaults: a bare request still produces a well-formed entry sourced
// from the local surface.
func TestLog_DefaultsSourceAndClock(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	res, err := r.Log(LogRequest{Text: "no clock, no source"})
	require.NoError(t, err)
	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "cli", doc.Fields["source"])
}

// TestLog_LatencyUnderOneSecond samples capture latency against the S-1
// sub-second acknowledgement requirement.
func TestLog_LatencyUnderOneSecond(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	start := time.Now()
	_, err := r.Log(LogRequest{Text: "synthetic ack-latency test", Now: fixedNow()})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second)
}

// TestLog_AckStringsAreClean guards the exact ack copy for both the
// happy path and the empty-body note.
func TestLog_AckStringsAreClean(t *testing.T) {
	assert.Equal(t, "Saved as `raw_x`.", logAck("raw_x", false))
	assert.Contains(t, logAck("raw_x", true), "empty")
}
