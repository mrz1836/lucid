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

	for range 2 {
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

// TestEarliestRawDate_EmptyLedger: a Ledger with nothing captured reports no
// earliest date rather than an error. This is the first-ever-reflection case —
// the caller falls back to a bare calendar window instead of inventing a start.
func TestEarliestRawDate_EmptyLedger(t *testing.T) {
	a, _ := newRawAdapter(t)

	date, ok, err := a.EarliestRawDate()
	require.NoError(t, err, "an empty raw tree is not an error")
	assert.False(t, ok)
	assert.Empty(t, date)
}

// TestEarliestRawDate_NoRawTree covers the harder empty case: the raw/
// directory does not exist at all (an unscaffolded home), which must still be
// a clean miss rather than a read error.
func TestEarliestRawDate_NoRawTree(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), ".lucid"))

	date, ok, err := a.EarliestRawDate()
	require.NoError(t, err, "an absent raw tree is not an error")
	assert.False(t, ok)
	assert.Empty(t, date)
}

// TestEarliestRawDate_AcrossMonthsAndYears is the case a per-shard search would
// get wrong: entries spread over two months and two years must resolve to the
// single oldest date, not the oldest within some shard.
func TestEarliestRawDate_AcrossMonthsAndYears(t *testing.T) {
	a, _ := newRawAdapter(t)

	for _, at := range []time.Time{
		time.Date(2026, time.March, 15, 9, 5, 0, 0, time.UTC),
		time.Date(2025, time.November, 20, 21, 30, 0, 0, time.UTC),
		time.Date(2026, time.March, 1, 7, 0, 0, 0, time.UTC),
		time.Date(2025, time.December, 2, 12, 0, 0, 0, time.UTC),
	} {
		_, err := a.WriteRaw(syntheticRaw(at, "synthetic entry"))
		require.NoError(t, err)
	}

	date, ok, err := a.EarliestRawDate()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2025-11-20", date)
}

// TestEarliestRawDate_IgnoresStrayNames proves a foreign file in the tree is
// skipped rather than answered with. The adversarial fixture is
// raw_0001_01_01.md: it looks like a raw id and carries a date far older than
// any real entry, so a naive prefix match would return year 1 and send the
// reflection window back to antiquity.
func TestEarliestRawDate_IgnoresStrayNames(t *testing.T) {
	a, home := newRawAdapter(t)

	_, err := a.WriteRaw(syntheticRaw(time.Date(2026, time.June, 10, 8, 0, 0, 0, time.UTC), "synthetic entry"))
	require.NoError(t, err)

	shard := filepath.Join(home, "raw", "2026", "06")
	for _, name := range []string{"raw_0001_01_01.md", "notes.md", "raw_not_a_date_here_x.md", "README.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(shard, name), []byte("stray"), 0o600))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(shard, "raw_1999_01_01"), 0o700))

	date, ok, err := a.EarliestRawDate()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2026-06-10", date, "only well-formed raw ids count")
}

// TestEarliestRawDate_UnreadableShard surfaces a real read failure instead of
// silently reporting no entries — a permission problem must not look like an
// empty Ledger and quietly widen the reflection window.
func TestEarliestRawDate_UnreadableShard(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a, home := newRawAdapter(t)

	_, err := a.WriteRaw(syntheticRaw(fixedTime(), "synthetic entry"))
	require.NoError(t, err)

	shard := filepath.Join(home, "raw", "2026", "07")
	require.NoError(t, os.Chmod(shard, 0o000))
	t.Cleanup(func() { _ = os.Chmod(shard, 0o700) })

	_, ok, err := a.EarliestRawDate()
	require.Error(t, err)
	assert.False(t, ok)
}

// seedRawAt writes one synthetic raw entry recorded on the given civil date
// (at a fixed hour, so the derived id is deterministic) and returns the id
// the adapter assigned.
func seedRawAt(t *testing.T, a *Adapter, date string) string {
	t.Helper()
	day, err := time.Parse("2006-01-02", date)
	require.NoError(t, err)
	res, err := a.WriteRaw(syntheticRaw(day.Add(9*time.Hour), "synthetic entry"))
	require.NoError(t, err)
	return res.RawID
}

// TestListRawIDsInRange_NoRawTree is the unscaffolded-home case: an absent
// raw/ tree is an empty window, not a read error.
func TestListRawIDsInRange_NoRawTree(t *testing.T) {
	a := New(filepath.Join(t.TempDir(), ".lucid"))

	ids, err := a.ListRawIDsInRange("2026-01-01", "2026-12-31")
	require.NoError(t, err, "an absent raw tree is not an error")
	assert.Empty(t, ids)
}

// TestListRawIDsInRange_EmptyLedger covers the scaffolded-but-never-captured
// Ledger: the tree exists and holds nothing.
func TestListRawIDsInRange_EmptyLedger(t *testing.T) {
	a, _ := newRawAdapter(t)

	ids, err := a.ListRawIDsInRange("2026-01-01", "2026-12-31")
	require.NoError(t, err)
	assert.Empty(t, ids)
}

// TestListRawIDsInRange_SelectsWindowAndSorts seeds entries before, inside
// and after the window out of chronological order: only the in-window ids
// come back, and they come back sorted.
func TestListRawIDsInRange_SelectsWindowAndSorts(t *testing.T) {
	a, _ := newRawAdapter(t)

	before := seedRawAt(t, a, "2026-02-28")
	late := seedRawAt(t, a, "2026-03-20")
	early := seedRawAt(t, a, "2026-03-05")
	after := seedRawAt(t, a, "2026-04-02")

	ids, err := a.ListRawIDsInRange("2026-03-01", "2026-03-31")
	require.NoError(t, err)
	assert.Equal(t, []string{early, late}, ids, "in-window ids only, chronological")
	assert.NotContains(t, ids, before)
	assert.NotContains(t, ids, after)
}

// TestListRawIDsInRange_BoundsAreInclusive pins the contract at the edges:
// an entry falling exactly on since and one falling exactly on until are
// both in the window.
func TestListRawIDsInRange_BoundsAreInclusive(t *testing.T) {
	a, _ := newRawAdapter(t)

	first := seedRawAt(t, a, "2026-03-01")
	middle := seedRawAt(t, a, "2026-03-15")
	last := seedRawAt(t, a, "2026-03-31")

	ids, err := a.ListRawIDsInRange("2026-03-01", "2026-03-31")
	require.NoError(t, err)
	assert.Equal(t, []string{first, middle, last}, ids)

	single, err := a.ListRawIDsInRange("2026-03-15", "2026-03-15")
	require.NoError(t, err)
	assert.Equal(t, []string{middle}, single, "a one-day window is not an empty window")
}

// TestListRawIDsInRange_AcrossMonthsAndYears is the case a shard-guessing
// walk would get wrong: a window spanning a month and a year boundary must
// collect entries from every raw/YYYY/MM shard it crosses.
func TestListRawIDsInRange_AcrossMonthsAndYears(t *testing.T) {
	a, _ := newRawAdapter(t)

	decLast := seedRawAt(t, a, "2025-12-31")
	janFirst := seedRawAt(t, a, "2026-01-01")
	janLast := seedRawAt(t, a, "2026-01-31")
	febFirst := seedRawAt(t, a, "2026-02-01")

	ids, err := a.ListRawIDsInRange("2025-12-31", "2026-02-01")
	require.NoError(t, err)
	assert.Equal(t, []string{decLast, janFirst, janLast, febFirst}, ids)

	acrossYear, err := a.ListRawIDsInRange("2025-12-31", "2026-01-01")
	require.NoError(t, err)
	assert.Equal(t, []string{decLast, janFirst}, acrossYear, "the year boundary is not a wall")
}

// TestListRawIDsInRange_IgnoresStrayNames proves a foreign name in the tree
// is skipped rather than raised or answered with. The window deliberately
// starts at year one so the stray raw_0001_01_01.md would be in range if it
// parsed at all — it is excluded because it is malformed, not because it is
// old. The directory fixture covers the other half: a name that would parse
// as a raw id but is not a file.
func TestListRawIDsInRange_IgnoresStrayNames(t *testing.T) {
	a, home := newRawAdapter(t)

	want := seedRawAt(t, a, "2026-06-10")

	shard := filepath.Join(home, "raw", "2026", "06")
	for _, name := range []string{"raw_0001_01_01.md", "notes.md", "raw_not_a_date_here_x.md", "README.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(shard, name), []byte("stray"), 0o600))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(shard, "raw_1999_01_01_00_00"), 0o700))

	ids, err := a.ListRawIDsInRange("0001-01-01", "2026-06-30")
	require.NoError(t, err, "a stray file must never break a read")
	assert.Equal(t, []string{want}, ids, "only well-formed raw ids count")
}

// TestListRawIDsInRange_CollisionSuffixedIDs covers the same-minute
// collision forms (raw_..._SS and raw_..._SS_N): they carry more id
// components than the minute-precision form, so they must still parse, and
// their zero-padded shape must still sort chronologically.
func TestListRawIDsInRange_CollisionSuffixedIDs(t *testing.T) {
	a, _ := newRawAdapter(t)
	now := fixedTime()

	written := make([]string, 0, 3)
	for range 3 {
		res, err := a.WriteRaw(syntheticRaw(now, "synthetic entry"))
		require.NoError(t, err)
		written = append(written, res.RawID)
	}
	require.Equal(t, []string{
		"raw_2026_07_05_18_41",
		"raw_2026_07_05_18_41_39",
		"raw_2026_07_05_18_41_39_1",
	}, written, "the collision ladder is the fixture under test")

	ids, err := a.ListRawIDsInRange("2026-07-05", "2026-07-05")
	require.NoError(t, err)
	assert.Equal(t, written, ids, "every collision form is enumerated, in order")
}

// TestListRawIDsInRange_InvalidBounds: a malformed or inverted bound is an
// error naming the expected form, never a silently empty window — a typo
// must not read as "nothing captured".
func TestListRawIDsInRange_InvalidBounds(t *testing.T) {
	a, _ := newRawAdapter(t)
	seedRawAt(t, a, "2026-03-15")

	cases := map[string][2]string{
		"unpadded since":    {"2026-7-1", "2026-07-31"},
		"unpadded until":    {"2026-07-01", "2026-7-31"},
		"no separators":     {"20260701", "2026-07-31"},
		"empty since":       {"", "2026-07-31"},
		"empty until":       {"2026-07-01", ""},
		"non-digit since":   {"20x6-07-01", "2026-07-31"},
		"wrong separator":   {"2026/07/01", "2026-07-31"},
		"timestamp since":   {"2026-07-01T00:00:00Z", "2026-07-31"},
		"since after until": {"2026-07-31", "2026-07-01"},
	}
	for name, bounds := range cases {
		t.Run(name, func(t *testing.T) {
			ids, err := a.ListRawIDsInRange(bounds[0], bounds[1])
			require.Error(t, err)
			assert.Nil(t, ids)
			assert.Contains(t, err.Error(), "storage:")
		})
	}
}

// TestListRawIDsInRange_UnreadableShard surfaces a real read failure rather
// than reporting an empty window: a permission problem must not look like a
// stretch of the record with nothing in it.
func TestListRawIDsInRange_UnreadableShard(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a, home := newRawAdapter(t)
	seedRawAt(t, a, "2026-07-05")

	shard := filepath.Join(home, "raw", "2026", "07")
	require.NoError(t, os.Chmod(shard, 0o000))
	t.Cleanup(func() { _ = os.Chmod(shard, 0o700) })

	ids, err := a.ListRawIDsInRange("2026-07-01", "2026-07-31")
	require.Error(t, err)
	assert.Empty(t, ids)
}

// TestListRawIDsInRange_UnreadableWalk covers the two directory levels above
// the shard. An unreadable raw/ or raw/YYYY must surface too: the walk has
// three places it can fail, and only the deepest one is obvious.
func TestListRawIDsInRange_UnreadableWalk(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	for name, rel := range map[string][]string{
		"raw root": {"raw"},
		"year dir": {"raw", "2026"},
	} {
		t.Run(name, func(t *testing.T) {
			a, home := newRawAdapter(t)
			seedRawAt(t, a, "2026-07-05")

			dir := filepath.Join(append([]string{home}, rel...)...)
			require.NoError(t, os.Chmod(dir, 0o000))
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

			ids, err := a.ListRawIDsInRange("2026-07-01", "2026-07-31")
			require.Error(t, err)
			assert.Empty(t, ids)
		})
	}
}
