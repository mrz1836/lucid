package storage

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/focus"
)

// newFocusStore returns a scaffolded adapter over an isolated temp home so the
// real ~/.lucid/ is never touched.
func newFocusStore(t *testing.T) *Adapter {
	t.Helper()
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldFocus())
	return a
}

// synthFocusEntry returns a synthetic active entry. Every focus item in these
// tests is invented — real focus items live only in the private Ledger (focus.md
// §6).
func synthFocusEntry(text, success, logicalDate string) focus.Focus {
	return focus.Focus{
		Text: text, SuccessCriterion: success, State: focus.StateActive,
		RecordedAt: "2026-08-23T21:45:10-04:00", LogicalDate: logicalDate,
		Source: focus.SourceFocus,
	}
}

func (a *Adapter) focusDayPathT(t *testing.T, date string) string {
	t.Helper()
	p, err := a.focusDayPath(date)
	require.NoError(t, err)
	return p
}

func (a *Adapter) focusDayBytes(t *testing.T, date string) []byte {
	t.Helper()
	b, err := os.ReadFile(a.focusDayPathT(t, date))
	require.NoError(t, err)
	return b
}

func TestScaffoldFocus_Idempotent(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldFocus())

	info, statErr := os.Stat(a.focusDir())
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "focus/ tree exists after scaffold")

	// A second scaffold changes nothing.
	require.NoError(t, a.ScaffoldFocus())
}

func TestAppendFocus_SeqSingleLineAndDefaults(t *testing.T) {
	a := newFocusStore(t)

	f1, err := a.AppendFocus(synthFocusEntry("Take the stairs today", "I skipped the elevator", "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "focus_2026_08_23_001", f1.ID)
	assert.Equal(t, focus.Schema, f1.Schema)
	assert.Equal(t, focus.StateActive, f1.State)

	f2, err := a.AppendFocus(synthFocusEntry("Read ten pages", "", "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "focus_2026_08_23_002", f2.ID)

	// A different logical day starts its own file at seq 1.
	f3, err := a.AppendFocus(synthFocusEntry("Water the plants", "", "2026-08-24"))
	require.NoError(t, err)
	assert.Equal(t, "focus_2026_08_24_001", f3.ID)

	// An empty source/state defaults to the documented provenance and active.
	bare := synthFocusEntry("Ask a question in standup", "I raised my hand", "2026-08-23")
	bare.Source = ""
	bare.State = ""
	f4, err := a.AppendFocus(bare)
	require.NoError(t, err)
	assert.Equal(t, focus.SourceFocus, f4.Source)
	assert.Equal(t, focus.StateActive, f4.State)

	// The day file holds one whole JSON line per entry, newline-terminated.
	body := a.focusDayBytes(t, "2026-08-23")
	assert.True(t, strings.HasSuffix(string(body), "\n"))
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	require.Len(t, lines, 3)

	// A missing logical_date is rejected; an empty text is rejected.
	_, err = a.AppendFocus(focus.Focus{Text: "x"})
	require.Error(t, err)
	_, err = a.AppendFocus(synthFocusEntry("", "no text", "2026-08-23"))
	require.Error(t, err)
}

// TestReadFocusDay_SkipsMalformedAndSeqIgnoresIt: a truncated line is skipped and
// counted; the next id derives from max well-formed seq + 1, never the line count
// (focus.md §2; error-states JSONL corruption).
func TestReadFocusDay_SkipsMalformedAndSeqIgnoresIt(t *testing.T) {
	a := newFocusStore(t)
	_, err := a.AppendFocus(synthFocusEntry("Take the stairs today", "I skipped the elevator", "2026-08-23"))
	require.NoError(t, err)

	// Inject a truncated line directly into the day file.
	require.NoError(t, appendLineFsync(a.focusDayPathT(t, "2026-08-23"), []byte(`{"id":"focus_2026_08_23_002","sch`)))

	entries, skipped, err := a.ReadFocusDay("2026-08-23")
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the malformed line is skipped")
	assert.Equal(t, 1, skipped, "and its count reported")

	// Seq derivation ignores the malformed line: next id is 002, not 003.
	next, err := a.AppendFocus(synthFocusEntry("Read ten pages", "", "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "focus_2026_08_23_002", next.ID)

	// A missing day is not an error.
	empty, skipped, err := a.ReadFocusDay("2026-09-09")
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.Zero(t, skipped)
}

// TestRetireFocus_FoldsAndLeavesOriginalByteIdentical: JSONL lines are never
// rewritten; retire is a new appended event whose refs.retires names its target,
// and readers mark the target retired without dropping it (focus.md §2).
func TestRetireFocus_FoldsAndLeavesOriginalByteIdentical(t *testing.T) {
	a := newFocusStore(t)
	orig, err := a.AppendFocus(synthFocusEntry("Take the stairs today", "I skipped the elevator", "2026-08-23"))
	require.NoError(t, err)
	other, err := a.AppendFocus(synthFocusEntry("Read ten pages", "", "2026-08-23"))
	require.NoError(t, err)

	before := a.focusDayBytes(t, "2026-08-23")
	firstLine := strings.SplitN(string(before), "\n", 2)[0]

	// The retirement event is backdated onto a later day file to prove folding is
	// cross-day: it still retires the earlier item.
	marker, err := a.RetireFocus(orig.ID, "2026-08-24", "2026-08-24T09:00:00-04:00")
	require.NoError(t, err)
	assert.Equal(t, "focus_2026_08_24_001", marker.ID)
	assert.True(t, marker.IsRetirement())

	after := a.focusDayBytes(t, "2026-08-23")
	assert.Equal(t, firstLine, strings.SplitN(string(after), "\n", 2)[0],
		"the retired item's original line stays byte-identical")
	assert.True(t, bytes.HasPrefix(after, before), "the original day file is only appended to, never rewritten")

	// The item view keeps both work-ons, sorted by id, with state resolved; the
	// marker itself is not an item.
	items, err := a.ReadFocus()
	require.NoError(t, err)
	assert.Equal(t, []string{orig.ID, other.ID}, focusIDs(items), "both items preserved; marker folded out")
	byID := map[string]focus.Focus{}
	for _, f := range items {
		byID[f.ID] = f
	}
	assert.Equal(t, focus.StateRetired, byID[orig.ID].State, "retired item stays with retired state")
	assert.Equal(t, focus.StateActive, byID[other.ID].State)

	// Retiring an unknown or already-retired id is a clean error, nothing written.
	_, err = a.RetireFocus("focus_2026_08_23_999", "2026-08-24", "2026-08-24T09:00:00-04:00")
	require.Error(t, err, "unknown id rejected")
	_, err = a.RetireFocus(orig.ID, "2026-08-25", "2026-08-25T09:00:00-04:00")
	require.Error(t, err, "double retire rejected")

	// ReadFocusByID is a raw lookup — the retired item is still on disk unchanged.
	got, found, err := a.ReadFocusByID(orig.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Take the stairs today", got.Text)

	// A malformed / absent id is a clean not-found, never an error.
	_, found, err = a.ReadFocusByID("not-a-focus-id")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestReadFocus_SortsAcrossDays(t *testing.T) {
	a := newFocusStore(t)
	_, err := a.AppendFocus(synthFocusEntry("t1", "", "2026-08-25"))
	require.NoError(t, err)
	_, err = a.AppendFocus(synthFocusEntry("t2", "", "2026-08-23"))
	require.NoError(t, err)
	_, err = a.AppendFocus(synthFocusEntry("t3", "", "2026-08-24"))
	require.NoError(t, err)

	items, err := a.ReadFocus()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"focus_2026_08_23_001", "focus_2026_08_24_001", "focus_2026_08_25_001",
	}, focusIDs(items), "read is sorted by id (time order) across day files")
}

// TestSurfaceFocus_RotationAndIdempotence exercises the whole surface contract
// (focus.md §4): empty pool is clean, cold start picks the lowest id, repeated
// same-day calls are idempotent, and successive days rotate least-recently-surfaced.
func TestSurfaceFocus_RotationAndIdempotence(t *testing.T) {
	a := newFocusStore(t)

	// An empty pool is a clean "nothing to surface," never an error, and writes no
	// projection.
	_, ok, err := a.SurfaceFocus("2026-08-23")
	require.NoError(t, err)
	assert.False(t, ok)
	_, statErr := os.Stat(a.focusSurfaceStatePath())
	assert.True(t, os.IsNotExist(statErr), "no surface-state file is written for an empty pool")

	for _, f := range []focus.Focus{
		synthFocusEntry("a", "", "2026-08-23"),
		synthFocusEntry("b", "", "2026-08-23"),
		synthFocusEntry("c", "", "2026-08-23"),
	} {
		_, aerr := a.AppendFocus(f)
		require.NoError(t, aerr)
	}

	// Cold start: the lowest id wins the tie (all unsurfaced).
	p1, ok, err := a.SurfaceFocus("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "focus_2026_08_23_001", p1.ID)

	// The projection is written at the tree root, separate from the entry stream.
	_, statErr = os.Stat(a.focusSurfaceStatePath())
	require.NoError(t, statErr)

	// Within-day idempotence: a repeated same-day call returns the same pick and
	// does not advance the rotation.
	p1again, ok, err := a.SurfaceFocus("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, p1.ID, p1again.ID)

	// Next day rotates to the next least-recently-surfaced (001 is now surfaced;
	// 002 and 003 are unsurfaced → lowest id 002).
	p2, ok, err := a.SurfaceFocus("2026-08-24")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "focus_2026_08_23_002", p2.ID)

	p3, ok, err := a.SurfaceFocus("2026-08-25")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "focus_2026_08_23_003", p3.ID)

	// All surfaced once: the least-recently-surfaced (001, shown on 08-23) wins.
	p4, ok, err := a.SurfaceFocus("2026-08-26")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "focus_2026_08_23_001", p4.ID)
}

// TestSurfaceFocus_FreshEntryEntersRotation: an entry never surfaced sorts before
// any surfaced entry, so a freshly added focus is picked next.
func TestSurfaceFocus_FreshEntryEntersRotation(t *testing.T) {
	a := newFocusStore(t)
	_, err := a.AppendFocus(synthFocusEntry("a", "", "2026-08-23"))
	require.NoError(t, err)

	p1, ok, err := a.SurfaceFocus("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "focus_2026_08_23_001", p1.ID)

	// Add a second focus after the first was surfaced.
	_, err = a.AppendFocus(synthFocusEntry("b", "", "2026-08-24"))
	require.NoError(t, err)

	// The new, unsurfaced entry enters the rotation ahead of the surfaced one.
	p2, ok, err := a.SurfaceFocus("2026-08-24")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "focus_2026_08_24_001", p2.ID)
}

// TestSurfaceFocus_SkipsRetired: a retirement event removes the item from the
// active pool, so surface never shows a retired focus.
func TestSurfaceFocus_SkipsRetired(t *testing.T) {
	a := newFocusStore(t)
	orig, err := a.AppendFocus(synthFocusEntry("a", "", "2026-08-23"))
	require.NoError(t, err)
	other, err := a.AppendFocus(synthFocusEntry("b", "", "2026-08-23"))
	require.NoError(t, err)

	_, err = a.RetireFocus(orig.ID, "2026-08-23", "2026-08-23T22:00:00-04:00")
	require.NoError(t, err)

	// The active pool is {other(002)}; cold start picks it, never the retired 001.
	p1, ok, err := a.SurfaceFocus("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, other.ID, p1.ID)
	assert.NotEqual(t, orig.ID, p1.ID)
}

// TestSurfaceFocus_RepickWhenRecordedPickRetired: if today's recorded pick is
// retired after it was chosen, the same-day surface re-picks a live active item
// rather than returning the retired one.
func TestSurfaceFocus_RepickWhenRecordedPickRetired(t *testing.T) {
	a := newFocusStore(t)
	first, err := a.AppendFocus(synthFocusEntry("a", "", "2026-08-23"))
	require.NoError(t, err)
	second, err := a.AppendFocus(synthFocusEntry("b", "", "2026-08-23"))
	require.NoError(t, err)

	p1, ok, err := a.SurfaceFocus("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, first.ID, p1.ID)

	// Retire the recorded pick mid-day, then surface again the same day.
	_, err = a.RetireFocus(first.ID, "2026-08-23", "2026-08-23T22:00:00-04:00")
	require.NoError(t, err)

	p2, ok, err := a.SurfaceFocus("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, second.ID, p2.ID, "a retired recorded pick is replaced by a live one")
}

// TestFocusSurfaceState_ResilientToCorrupt: the projection is rebuildable and
// disposable — a corrupt file resets rotation memory rather than erroring.
func TestFocusSurfaceState_ResilientToCorrupt(t *testing.T) {
	a := newFocusStore(t)
	_, err := a.AppendFocus(synthFocusEntry("a", "", "2026-08-23"))
	require.NoError(t, err)

	require.NoError(t, ensureDir(a.focusDir(), "focus"))
	require.NoError(t, os.WriteFile(a.focusSurfaceStatePath(), []byte("{not json"), filePerm))

	// A corrupt projection cold-starts cleanly.
	p, ok, err := a.SurfaceFocus("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "focus_2026_08_23_001", p.ID)
}

func focusIDs(fs []focus.Focus) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.ID
	}
	return out
}
