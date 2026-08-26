package storage

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/reframes"
)

// newReframeStore returns a scaffolded adapter over an isolated temp home so the
// real ~/.lucid/ is never touched (plan.md Approach §"Isolated test home").
func newReframeStore(t *testing.T) *Adapter {
	t.Helper()
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldReframes())
	return a
}

// synthReframe returns a synthetic entry. Every reframe in these tests is
// invented — real reframes live only in the private Ledger (reframes.md §6).
func synthReframe(catch, flip, logicalDate string) reframes.Reframe {
	return reframes.Reframe{
		Catch: catch, Flip: flip,
		RecordedAt: "2026-08-23T21:45:10-04:00", LogicalDate: logicalDate,
		Source: reframes.SourceReframe,
	}
}

func (a *Adapter) reframeDayPathT(t *testing.T, date string) string {
	t.Helper()
	p, err := a.reframeDayPath(date)
	require.NoError(t, err)
	return p
}

func (a *Adapter) reframeDayBytes(t *testing.T, date string) []byte {
	t.Helper()
	b, err := os.ReadFile(a.reframeDayPathT(t, date))
	require.NoError(t, err)
	return b
}

func TestScaffoldReframes_Idempotent(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldReframes())

	info, statErr := os.Stat(a.reframesDir())
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "reframes/ tree exists after scaffold")

	// A second scaffold changes nothing.
	require.NoError(t, a.ScaffoldReframes())
}

func TestAppendReframe_SeqSingleLineAndDefaults(t *testing.T) {
	a := newReframeStore(t)

	r1, err := a.AppendReframe(synthReframe("I can't do this", "I can learn this", "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "reframe_2026_08_23_001", r1.ID)
	assert.Equal(t, reframes.Schema, r1.Schema)

	r2, err := a.AppendReframe(synthReframe("I always mess up", "I'm still practicing", "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "reframe_2026_08_23_002", r2.ID)

	// A different logical day starts its own file at seq 1.
	r3, err := a.AppendReframe(synthReframe("This is too hard", "This is worth the effort", "2026-08-24"))
	require.NoError(t, err)
	assert.Equal(t, "reframe_2026_08_24_001", r3.ID)

	// An empty source defaults to the documented `reframe` provenance.
	noSource := synthReframe("I have to be perfect", "I get to grow", "2026-08-23")
	noSource.Source = ""
	r4, err := a.AppendReframe(noSource)
	require.NoError(t, err)
	assert.Equal(t, reframes.SourceReframe, r4.Source)

	// The day file holds one whole JSON line per entry, newline-terminated.
	body := a.reframeDayBytes(t, "2026-08-23")
	assert.True(t, strings.HasSuffix(string(body), "\n"))
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	require.Len(t, lines, 3)

	// A missing logical_date is rejected; an empty catch/flip is rejected.
	_, err = a.AppendReframe(reframes.Reframe{Catch: "x", Flip: "y"})
	require.Error(t, err)
	_, err = a.AppendReframe(synthReframe("", "flip only", "2026-08-23"))
	require.Error(t, err)
}

// TestReadReframesDay_SkipsMalformedAndSeqIgnoresIt: a truncated line is skipped
// and counted; the next id derives from max well-formed seq + 1, never the line
// count (reframes.md §2; error-states JSONL corruption).
func TestReadReframesDay_SkipsMalformedAndSeqIgnoresIt(t *testing.T) {
	a := newReframeStore(t)
	_, err := a.AppendReframe(synthReframe("I can't do this", "I can learn this", "2026-08-23"))
	require.NoError(t, err)

	// Inject a truncated line directly into the day file.
	require.NoError(t, appendLineFsync(a.reframeDayPathT(t, "2026-08-23"), []byte(`{"id":"reframe_2026_08_23_002","sch`)))

	entries, skipped, err := a.ReadReframesDay("2026-08-23")
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the malformed line is skipped")
	assert.Equal(t, 1, skipped, "and its count reported")

	// Seq derivation ignores the malformed line: next id is 002, not 003.
	next, err := a.AppendReframe(synthReframe("I always mess up", "I'm still practicing", "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "reframe_2026_08_23_002", next.ID)

	// A missing day is not an error.
	empty, skipped, err := a.ReadReframesDay("2026-09-09")
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.Zero(t, skipped)
}

// TestAppendReframe_CorrectionFoldsAndLeavesOriginalByteIdentical: JSONL lines
// are never rewritten; a correction is a new appended entry whose refs.corrects
// names its target, and readers drop the superseded entry (reframes.md §2).
func TestAppendReframe_CorrectionFoldsAndLeavesOriginalByteIdentical(t *testing.T) {
	a := newReframeStore(t)
	orig, err := a.AppendReframe(synthReframe("I can't do this", "I can learn this", "2026-08-23"))
	require.NoError(t, err)
	other, err := a.AppendReframe(synthReframe("I always mess up", "I'm still practicing", "2026-08-23"))
	require.NoError(t, err)

	before := a.reframeDayBytes(t, "2026-08-23")
	firstLine := strings.SplitN(string(before), "\n", 2)[0]

	// The correction is backdated onto a later day file to prove folding is
	// cross-day: it still supersedes the earlier entry.
	correction := synthReframe("I can't do this", "I choose to learn this", "2026-08-24")
	correction.Refs = map[string]any{"corrects": orig.ID}
	corr, err := a.AppendReframe(correction)
	require.NoError(t, err)

	after := a.reframeDayBytes(t, "2026-08-23")
	assert.Equal(t, firstLine, strings.SplitN(string(after), "\n", 2)[0],
		"the corrected entry's original line stays byte-identical")
	assert.True(t, bytes.HasPrefix(after, before), "the original day file is only appended to, never rewritten")

	// The live pool folds the supersede: orig drops out, the correction and the
	// unrelated entry remain, sorted by id across day files.
	live, err := a.ReadReframes()
	require.NoError(t, err)
	ids := reframeIDs(live)
	assert.Equal(t, []string{other.ID, corr.ID}, ids, "superseded entry omitted; result sorted by id across days")

	// ReadReframeByID is a raw lookup — the superseded original is still on disk.
	got, found, err := a.ReadReframeByID(orig.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "I can learn this", got.Flip)

	// A malformed / absent id is a clean not-found, never an error.
	_, found, err = a.ReadReframeByID("not-a-reframe-id")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestReadReframes_SortsAcrossDays(t *testing.T) {
	a := newReframeStore(t)
	_, err := a.AppendReframe(synthReframe("c1", "f1", "2026-08-25"))
	require.NoError(t, err)
	_, err = a.AppendReframe(synthReframe("c2", "f2", "2026-08-23"))
	require.NoError(t, err)
	_, err = a.AppendReframe(synthReframe("c3", "f3", "2026-08-24"))
	require.NoError(t, err)

	live, err := a.ReadReframes()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"reframe_2026_08_23_001", "reframe_2026_08_24_001", "reframe_2026_08_25_001",
	}, reframeIDs(live), "read is sorted by id (time order) across day files")
}

// TestSurfaceReframe_RotationAndIdempotence exercises the whole surface contract
// (reframes.md §4): empty pool is clean, cold start picks the lowest id, repeated
// same-day calls are idempotent, and successive days rotate least-recently-surfaced.
func TestSurfaceReframe_RotationAndIdempotence(t *testing.T) {
	a := newReframeStore(t)

	// An empty pool is a clean "nothing to surface," never an error, and writes
	// no projection.
	_, ok, err := a.SurfaceReframe("2026-08-23")
	require.NoError(t, err)
	assert.False(t, ok)
	_, statErr := os.Stat(a.surfaceStatePath())
	assert.True(t, os.IsNotExist(statErr), "no surface-state file is written for an empty pool")

	for _, r := range []reframes.Reframe{
		synthReframe("a", "A", "2026-08-23"),
		synthReframe("b", "B", "2026-08-23"),
		synthReframe("c", "C", "2026-08-23"),
	} {
		_, aerr := a.AppendReframe(r)
		require.NoError(t, aerr)
	}

	// Cold start: the lowest id wins the tie (all unsurfaced).
	p1, ok, err := a.SurfaceReframe("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "reframe_2026_08_23_001", p1.ID)

	// The projection is written at the tree root, separate from the entry stream.
	_, statErr = os.Stat(a.surfaceStatePath())
	require.NoError(t, statErr)

	// Within-day idempotence: a repeated same-day call returns the same pick and
	// does not advance the rotation.
	p1again, ok, err := a.SurfaceReframe("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, p1.ID, p1again.ID)

	// Next day rotates to the next least-recently-surfaced (001 is now surfaced;
	// 002 and 003 are unsurfaced → lowest id 002).
	p2, ok, err := a.SurfaceReframe("2026-08-24")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "reframe_2026_08_23_002", p2.ID)

	p3, ok, err := a.SurfaceReframe("2026-08-25")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "reframe_2026_08_23_003", p3.ID)

	// All surfaced once: the least-recently-surfaced (001, shown on 08-23) wins.
	p4, ok, err := a.SurfaceReframe("2026-08-26")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "reframe_2026_08_23_001", p4.ID)
}

// TestSurfaceReframe_FreshEntryEntersRotation: an entry never surfaced sorts
// before any surfaced entry, so a freshly added reframe is picked next.
func TestSurfaceReframe_FreshEntryEntersRotation(t *testing.T) {
	a := newReframeStore(t)
	_, err := a.AppendReframe(synthReframe("a", "A", "2026-08-23"))
	require.NoError(t, err)

	p1, ok, err := a.SurfaceReframe("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "reframe_2026_08_23_001", p1.ID)

	// Add a second reframe after the first was surfaced.
	_, err = a.AppendReframe(synthReframe("b", "B", "2026-08-24"))
	require.NoError(t, err)

	// The new, unsurfaced entry enters the rotation ahead of the surfaced one.
	p2, ok, err := a.SurfaceReframe("2026-08-24")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "reframe_2026_08_24_001", p2.ID)
}

// TestSurfaceReframe_SkipsSupersededPick: a correction removes the target from
// the pool, so surface never shows a superseded reframe.
func TestSurfaceReframe_SkipsSuperseded(t *testing.T) {
	a := newReframeStore(t)
	orig, err := a.AppendReframe(synthReframe("a", "A", "2026-08-23"))
	require.NoError(t, err)
	other, err := a.AppendReframe(synthReframe("b", "B", "2026-08-23"))
	require.NoError(t, err)

	corr := synthReframe("a", "A refined", "2026-08-23")
	corr.Refs = map[string]any{"corrects": orig.ID}
	corrRF, err := a.AppendReframe(corr)
	require.NoError(t, err)

	// The pool is {other(002), correction(003)} sorted by id; cold start picks 002.
	p1, ok, err := a.SurfaceReframe("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, other.ID, p1.ID)

	// Next day picks the correction, never the superseded original.
	p2, ok, err := a.SurfaceReframe("2026-08-24")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, corrRF.ID, p2.ID)
	assert.NotEqual(t, orig.ID, p2.ID)
}

// TestSurfaceState_ResilientToCorrupt: the projection is rebuildable and
// disposable — a corrupt file resets rotation memory rather than erroring.
func TestSurfaceState_ResilientToCorrupt(t *testing.T) {
	a := newReframeStore(t)
	_, err := a.AppendReframe(synthReframe("a", "A", "2026-08-23"))
	require.NoError(t, err)

	require.NoError(t, ensureDir(a.reframesDir(), "reframes"))
	require.NoError(t, os.WriteFile(a.surfaceStatePath(), []byte("{not json"), filePerm))

	// A corrupt projection cold-starts cleanly.
	p, ok, err := a.SurfaceReframe("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "reframe_2026_08_23_001", p.ID)
}

func reframeIDs(rfs []reframes.Reframe) []string {
	out := make([]string, len(rfs))
	for i, r := range rfs {
		out[i] = r.ID
	}
	return out
}
