package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedTime is a deterministic capture instant used across the raw and
// session tests. UTC keeps the derived id and RFC3339 rendering stable
// regardless of the host TZ.
func fixedTime() time.Time {
	return time.Date(2026, time.July, 5, 18, 41, 39, 0, time.UTC)
}

// syntheticRaw is the fixture generator for raw-entry tests: it builds a
// well-formed /log entry with synthetic content (no real names or
// identifiers) recorded at now.
func syntheticRaw(now time.Time, body string) RawEntry {
	return RawEntry{
		RecordedAt:          now,
		OccurredAt:          now,
		OccurredAtPrecision: PrecisionExact,
		Source:              "cli",
		Command:             "/log",
		Body:                body,
	}
}

// rawMarkdownFiles returns every raw entry file under home/raw, sorted.
func rawMarkdownFiles(t *testing.T, home string) []string {
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

// newRawAdapter returns an adapter over a fresh scaffolded temp Ledger.
func newRawAdapter(t *testing.T) (*Adapter, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".lucid")
	a := New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	return a, home
}

func TestWriteRaw_WritesImmutableEntry(t *testing.T) {
	a, home := newRawAdapter(t)
	now := fixedTime()

	res, err := a.WriteRaw(syntheticRaw(now, "Quiet day. Read for an hour."))
	require.NoError(t, err)
	assert.Equal(t, "raw_2026_07_05_18_41", res.RawID)
	assert.Equal(t, "session_2026_07_05_18_41", res.SessionID)

	// The file lives under the YYYY/MM shard and validates.
	path := filepath.Join(home, "raw", "2026", "07", res.RawID+".md")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, ValidateRawFrontmatter(content))

	// ReadRaw round-trips the frontmatter and body.
	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "/log", doc.Fields["command"])
	assert.Equal(t, res.SessionID, doc.Fields["session_id"])
	assert.Equal(t, "# Entry\n\nQuiet day. Read for an hour.", doc.Body)
}

func TestWriteRaw_UsesSuppliedSessionID(t *testing.T) {
	a, _ := newRawAdapter(t)
	e := syntheticRaw(fixedTime(), "body")
	e.SessionID = "session_opened_earlier"

	res, err := a.WriteRaw(e)
	require.NoError(t, err)
	assert.Equal(t, "session_opened_earlier", res.SessionID, "an explicit session id is used verbatim")

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "session_opened_earlier", doc.Fields["session_id"])
}

func TestWriteRaw_AgentVersionsIntakeNull(t *testing.T) {
	a, home := newRawAdapter(t)
	res, err := a.WriteRaw(syntheticRaw(fixedTime(), "body"))
	require.NoError(t, err)

	path := filepath.Join(home, "raw", "2026", "07", res.RawID+".md")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "agent_versions:\n  intake: null")
}

func TestWriteRaw_SameMinuteCollisionAppendsSeconds(t *testing.T) {
	a, home := newRawAdapter(t)
	now := fixedTime()

	first, err := a.WriteRaw(syntheticRaw(now, "first"))
	require.NoError(t, err)
	second, err := a.WriteRaw(syntheticRaw(now, "second"))
	require.NoError(t, err)

	assert.Equal(t, "raw_2026_07_05_18_41", first.RawID)
	assert.Equal(t, "raw_2026_07_05_18_41_39", second.RawID, "second same-minute id carries the _SS suffix")
	assert.Len(t, rawMarkdownFiles(t, home), 2)

	// Immutability: the first file is untouched by the second write.
	firstDoc, err := a.ReadRaw(first.RawID)
	require.NoError(t, err)
	assert.Equal(t, "# Entry\n\nfirst", firstDoc.Body)
}

func TestWriteRaw_ThreeSameSecondAppendsCounter(t *testing.T) {
	a, _ := newRawAdapter(t)
	now := fixedTime()

	for i := 0; i < 2; i++ {
		_, err := a.WriteRaw(syntheticRaw(now, "n"))
		require.NoError(t, err)
	}
	third, err := a.WriteRaw(syntheticRaw(now, "n"))
	require.NoError(t, err)
	assert.Equal(t, "raw_2026_07_05_18_41_39_1", third.RawID)
}

func TestWriteRaw_EmptyBody(t *testing.T) {
	a, home := newRawAdapter(t)
	res, err := a.WriteRaw(syntheticRaw(fixedTime(), ""))
	require.NoError(t, err)

	path := filepath.Join(home, "raw", "2026", "07", res.RawID+".md")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, ValidateRawFrontmatter(content), "empty-body entry still validates")
	assert.True(t, strings.HasSuffix(string(content), "# Entry\n"), "no body after the heading")

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "# Entry", doc.Body)
}

func TestWriteRaw_RangePrecisionWritesOccurredAtEnd(t *testing.T) {
	a, home := newRawAdapter(t)
	now := fixedTime()
	end := now.Add(2 * time.Hour)
	e := syntheticRaw(now, "spanning event")
	e.OccurredAtPrecision = PrecisionRange
	e.OccurredAtEnd = &end

	res, err := a.WriteRaw(e)
	require.NoError(t, err)
	path := filepath.Join(home, "raw", "2026", "07", res.RawID+".md")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "occurred_at_precision: range")
	assert.Contains(t, string(content), "occurred_at_end:")
}

func TestWriteRaw_ValidationErrors(t *testing.T) {
	a, home := newRawAdapter(t)
	now := fixedTime()

	base := func() RawEntry { return syntheticRaw(now, "b") }
	cases := map[string]func(RawEntry) RawEntry{
		"zero recorded_at": func(e RawEntry) RawEntry { e.RecordedAt = time.Time{}; return e },
		"zero occurred_at": func(e RawEntry) RawEntry { e.OccurredAt = time.Time{}; return e },
		"bad precision":    func(e RawEntry) RawEntry { e.OccurredAtPrecision = "someday"; return e },
		"empty source":     func(e RawEntry) RawEntry { e.Source = ""; return e },
		"empty command":    func(e RawEntry) RawEntry { e.Command = ""; return e },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := a.WriteRaw(mutate(base()))
			require.Error(t, err)
		})
	}
	assert.Empty(t, rawMarkdownFiles(t, home), "a rejected entry writes nothing")
}

func TestReadRaw_MalformedID(t *testing.T) {
	a, _ := newRawAdapter(t)
	_, err := a.ReadRaw("not-a-raw-id")
	require.Error(t, err)
}

func TestReadRaw_MissingFile(t *testing.T) {
	a, _ := newRawAdapter(t)
	_, err := a.ReadRaw("raw_2026_07_05_18_41")
	require.Error(t, err)
}

// TestWriteRaw_UnwritableTree is the storage-level view of error-states.md
// §St-1: when the raw tree cannot be written, WriteRaw surfaces the
// failure and leaves nothing behind.
func TestWriteRaw_UnwritableTree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a, home := newRawAdapter(t)
	rawDir := filepath.Join(home, "raw")
	require.NoError(t, os.Chmod(rawDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(rawDir, 0o700) })

	_, err := a.WriteRaw(syntheticRaw(fixedTime(), "body"))
	require.Error(t, err)
	assert.Empty(t, rawMarkdownFiles(t, home))
}

// TestWriteRaw_ShardReadOnly exercises the file-create failure inside an
// existing shard (the writeExcl error path): the shard directory is
// present but read-only.
func TestWriteRaw_ShardReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a, home := newRawAdapter(t)
	shard := filepath.Join(home, "raw", "2026", "07")
	require.NoError(t, os.MkdirAll(shard, 0o700))
	require.NoError(t, os.Chmod(shard, 0o500))
	t.Cleanup(func() { _ = os.Chmod(shard, 0o700) })

	_, err := a.WriteRaw(syntheticRaw(fixedTime(), "body"))
	require.Error(t, err)
}
