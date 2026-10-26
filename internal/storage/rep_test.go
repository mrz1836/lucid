package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/rep"
)

// newRepStore returns a scaffolded adapter over an isolated temp home so the
// real ~/.lucid/ is never touched.
func newRepStore(t *testing.T) *Adapter {
	t.Helper()
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldReps())
	return a
}

// synthRepEntry returns a synthetic active entry. Every rep in these tests is
// invented — real reps live only in the private Ledger (reps.md §7).
func synthRepEntry(text, solo string, level int, logicalDate string) rep.Rep {
	return rep.Rep{
		Text: text, Solo: solo, Level: level, State: rep.StateActive,
		RecordedAt: "2026-08-23T21:45:10-04:00", LogicalDate: logicalDate,
		Source: rep.SourceRep,
	}
}

func (a *Adapter) repDayPathT(t *testing.T, date string) string {
	t.Helper()
	p, err := a.repDayPath(date)
	require.NoError(t, err)
	return p
}

func (a *Adapter) repDayBytes(t *testing.T, date string) []byte {
	t.Helper()
	b, err := os.ReadFile(a.repDayPathT(t, date))
	require.NoError(t, err)
	return b
}

// repTreeHashes returns a content hash for every regular file under the rep
// tree, keyed by its path relative to the tree root.
func repTreeHashes(t *testing.T, a *Adapter) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(a.repDir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(a.repDir(), path)
		if rerr != nil {
			return rerr
		}
		out[rel] = sha256.Sum256(b)
		return nil
	})
	require.NoError(t, err)
	return out
}

func TestScaffoldReps_Idempotent(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldReps())

	info, statErr := os.Stat(a.repDir())
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "reps/ tree exists after scaffold")
	assert.Equal(t, filepath.Join(a.home, "reps"), a.repDir())

	// A second scaffold changes nothing, including an existing projection.
	require.NoError(t, os.WriteFile(a.repSurfaceStatePath(), []byte(`{"schema":1}`), filePerm))
	require.NoError(t, a.ScaffoldReps())
	b, err := os.ReadFile(a.repSurfaceStatePath())
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema":1}`, string(b), "scaffold leaves the projection untouched")
}

func TestAppendRep_SeqSingleLineAndDefaults(t *testing.T) {
	a := newRepStore(t)

	r1, err := a.AppendRep(synthRepEntry("Ask one question in a meeting", "Write the question down and send it afterward", 2, "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "rep_2026_08_23_001", r1.ID)
	assert.Equal(t, rep.Schema, r1.Schema)
	assert.Equal(t, rep.StateActive, r1.State)

	// The day file lands at reps/YYYY/MM/rep_YYYY_MM_DD.jsonl.
	assert.Equal(t,
		filepath.Join(a.home, "reps", "2026", "08", "rep_2026_08_23.jsonl"),
		a.repDayPathT(t, "2026-08-23"))

	r2, err := a.AppendRep(synthRepEntry("Try one new route home", "", 0, "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "rep_2026_08_23_002", r2.ID)

	// A different logical day starts its own file at seq 1.
	r3, err := a.AppendRep(synthRepEntry("Read one page aloud", "", 3, "2026-08-24"))
	require.NoError(t, err)
	assert.Equal(t, "rep_2026_08_24_001", r3.ID)

	// An empty source/state defaults to the documented provenance and active.
	bare := synthRepEntry("Cook one new recipe", "", 0, "2026-08-23")
	bare.Source = ""
	bare.State = ""
	r4, err := a.AppendRep(bare)
	require.NoError(t, err)
	assert.Equal(t, rep.SourceRep, r4.Source)
	assert.Equal(t, rep.StateActive, r4.State)

	// The day file holds one whole JSON line per entry, newline-terminated, and
	// each line round-trips with solo and level always present.
	body := a.repDayBytes(t, "2026-08-23")
	assert.True(t, strings.HasSuffix(string(body), "\n"))
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	require.Len(t, lines, 3)
	first, err := rep.UnmarshalRepLine([]byte(lines[0]))
	require.NoError(t, err)
	assert.Equal(t, "Write the question down and send it afterward", first.Solo)
	assert.Equal(t, 2, first.Level)
	assert.Contains(t, lines[1], `"solo":""`)
	assert.Contains(t, lines[1], `"level":0`)

	// A missing logical_date, an empty text, and a negative level are rejected
	// before any write.
	_, err = a.AppendRep(rep.Rep{Text: "x"})
	require.Error(t, err)
	_, err = a.AppendRep(synthRepEntry("", "no text", 0, "2026-08-23"))
	require.Error(t, err)
	_, err = a.AppendRep(synthRepEntry("Try one new route home", "", -1, "2026-08-23"))
	require.Error(t, err)
	assert.Equal(t, body, a.repDayBytes(t, "2026-08-23"), "a rejected append writes nothing")
}

// TestReadRepDay_SkipsMalformedAndSeqIgnoresIt: a truncated line is skipped and
// counted; the next id derives from max well-formed seq + 1, never the line count
// (reps.md §2; error-states JSONL corruption).
func TestReadRepDay_SkipsMalformedAndSeqIgnoresIt(t *testing.T) {
	a := newRepStore(t)
	_, err := a.AppendRep(synthRepEntry("Ask one question in a meeting", "", 2, "2026-08-23"))
	require.NoError(t, err)

	// Inject a truncated line directly into the day file.
	require.NoError(t, appendLineFsync(a.repDayPathT(t, "2026-08-23"), []byte(`{"id":"rep_2026_08_23_002","sch`)))

	entries, skipped, err := a.ReadRepDay("2026-08-23")
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the malformed line is skipped")
	assert.Equal(t, 1, skipped, "and its count reported")

	// Seq derivation ignores the malformed line: next id is 002, not 003.
	next, err := a.AppendRep(synthRepEntry("Try one new route home", "", 0, "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "rep_2026_08_23_002", next.ID)

	// The folded view skips the malformed line too.
	all, err := a.ReadReps()
	require.NoError(t, err)
	assert.Equal(t, []string{"rep_2026_08_23_001", "rep_2026_08_23_002"}, repEntryIDs(all))

	// A missing day is not an error.
	empty, skipped, err := a.ReadRepDay("2026-09-09")
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.Zero(t, skipped)

	// A malformed date is rejected before any read.
	_, _, err = a.ReadRepDay("../../etc")
	require.Error(t, err)
}

func TestReadReps_SortsAcrossDays(t *testing.T) {
	a := newRepStore(t)
	_, err := a.AppendRep(synthRepEntry("t1", "", 0, "2026-08-25"))
	require.NoError(t, err)
	_, err = a.AppendRep(synthRepEntry("t2", "", 0, "2026-08-23"))
	require.NoError(t, err)
	_, err = a.AppendRep(synthRepEntry("t3", "", 0, "2026-09-24"))
	require.NoError(t, err)
	_, err = a.AppendRep(synthRepEntry("t4", "", 0, "2026-08-24"))
	require.NoError(t, err)

	reps, err := a.ReadReps()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"rep_2026_08_23_001", "rep_2026_08_24_001", "rep_2026_08_25_001", "rep_2026_09_24_001",
	}, repEntryIDs(reps), "read is sorted by id (time order) across day and month files")

	// An empty tree reads as an empty pool, not an error.
	fresh := New(t.TempDir())
	none, err := fresh.ReadReps()
	require.NoError(t, err)
	assert.Empty(t, none)
}

// TestRetireRep_FoldsAndLeavesOriginalByteIdentical: JSONL lines are never
// rewritten; retire is a new appended event whose refs.retires names its target,
// and readers mark the target retired without dropping it (reps.md §2, §5).
func TestRetireRep_FoldsAndLeavesOriginalByteIdentical(t *testing.T) {
	a := newRepStore(t)
	orig, err := a.AppendRep(synthRepEntry("Ask one question in a meeting", "Write the question down and send it afterward", 2, "2026-08-23"))
	require.NoError(t, err)
	other, err := a.AppendRep(synthRepEntry("Try one new route home", "", 0, "2026-08-23"))
	require.NoError(t, err)

	before := a.repDayBytes(t, "2026-08-23")
	firstLine := strings.SplitN(string(before), "\n", 2)[0]

	// The retirement event lands on a later day file to prove folding is
	// cross-day: it still retires the earlier rep.
	marker, err := a.RetireRep(orig.ID, "2026-08-24", "2026-08-24T09:00:00-04:00")
	require.NoError(t, err)
	assert.Equal(t, "rep_2026_08_24_001", marker.ID)
	assert.True(t, marker.IsRetirement())
	target, ok := marker.Retires()
	require.True(t, ok)
	assert.Equal(t, orig.ID, target, "the event names its target in refs.retires")

	after := a.repDayBytes(t, "2026-08-23")
	assert.Equal(t, before, after, "the retired rep's day file is byte-identical")
	assert.Equal(t, firstLine, strings.SplitN(string(after), "\n", 2)[0],
		"the retired rep's original line stays byte-identical")

	// The rep view keeps both reps, sorted by id, with state resolved; the marker
	// itself is not a rep.
	reps, err := a.ReadReps()
	require.NoError(t, err)
	assert.Equal(t, []string{orig.ID, other.ID}, repEntryIDs(reps), "both reps preserved; marker folded out")
	byID := map[string]rep.Rep{}
	for _, r := range reps {
		byID[r.ID] = r
	}
	assert.Equal(t, rep.StateRetired, byID[orig.ID].State, "retired rep stays with retired state")
	assert.Equal(t, "Write the question down and send it afterward", byID[orig.ID].Solo, "a retired rep keeps its content")
	assert.Equal(t, rep.StateActive, byID[other.ID].State)

	// The retired rep leaves the active pool.
	assert.Equal(t, []string{other.ID}, repEntryIDs(activeReps(reps)))

	// ReadRepByID is a raw lookup — the retired rep is still on disk unchanged.
	got, found, err := a.ReadRepByID(orig.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Ask one question in a meeting", got.Text)
	assert.Equal(t, rep.StateActive, got.State, "the raw line keeps its write-time state")

	// A malformed / absent id is a clean not-found, never an error.
	_, found, err = a.ReadRepByID("not-a-rep-id")
	require.NoError(t, err)
	assert.False(t, found)
	_, found, err = a.ReadRepByID("rep_2026_08_23_999")
	require.NoError(t, err)
	assert.False(t, found)
}

// TestRetireRep_RejectsUnknownAndDouble: retiring an unknown or already-retired
// id is a clean error and appends nothing (reps.md §5).
func TestRetireRep_RejectsUnknownAndDouble(t *testing.T) {
	a := newRepStore(t)
	orig, err := a.AppendRep(synthRepEntry("Ask one question in a meeting", "", 2, "2026-08-23"))
	require.NoError(t, err)

	snapshot := repTreeHashes(t, a)
	_, err = a.RetireRep("rep_2026_08_23_999", "2026-08-24", "2026-08-24T09:00:00-04:00")
	require.Error(t, err, "unknown id rejected")
	assert.Contains(t, err.Error(), "no rep")
	_, err = a.RetireRep("", "2026-08-24", "2026-08-24T09:00:00-04:00")
	require.Error(t, err, "empty id rejected")
	assert.Equal(t, snapshot, repTreeHashes(t, a), "a rejected retire appends nothing")

	_, err = a.RetireRep(orig.ID, "2026-08-24", "2026-08-24T09:00:00-04:00")
	require.NoError(t, err)

	snapshot = repTreeHashes(t, a)
	_, err = a.RetireRep(orig.ID, "2026-08-25", "2026-08-25T09:00:00-04:00")
	require.Error(t, err, "double retire rejected")
	assert.Contains(t, err.Error(), "already retired")
	// A retirement marker's own id is not a rep either.
	_, err = a.RetireRep("rep_2026_08_24_001", "2026-08-25", "2026-08-25T09:00:00-04:00")
	require.Error(t, err, "a retirement marker cannot itself be retired")
	assert.Equal(t, snapshot, repTreeHashes(t, a), "a rejected double retire appends nothing")
}

// TestSurfaceRep_RotationAndIdempotence exercises the whole surface contract
// (reps.md §4): empty pool is clean, cold start picks the lowest id, repeated
// same-day calls are idempotent, and successive days rotate
// least-recently-surfaced, so no rep repeats until the pool has cycled.
func TestSurfaceRep_RotationAndIdempotence(t *testing.T) {
	a := newRepStore(t)

	// An empty pool is a clean "nothing to surface," never an error, and writes no
	// projection.
	_, ok, err := a.SurfaceRep("2026-08-23")
	require.NoError(t, err)
	assert.False(t, ok)
	_, statErr := os.Stat(a.repSurfaceStatePath())
	assert.True(t, os.IsNotExist(statErr), "no surface-state file is written for an empty pool")

	// Levels are deliberately unordered: rotation never reads level.
	for _, r := range []rep.Rep{
		synthRepEntry("a", "solo a", 5, "2026-08-23"),
		synthRepEntry("b", "", 7, "2026-08-23"),
		synthRepEntry("c", "solo c", 0, "2026-08-23"),
	} {
		_, aerr := a.AppendRep(r)
		require.NoError(t, aerr)
	}

	// Cold start: the lowest id wins the tie (all unsurfaced), and the surfaced
	// entry carries its solo variant.
	p1, ok, err := a.SurfaceRep("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "rep_2026_08_23_001", p1.ID)
	assert.Equal(t, "solo a", p1.Solo)
	assert.Equal(t, 5, p1.Level)

	// The projection is written at the tree root, separate from the entry stream.
	_, statErr = os.Stat(a.repSurfaceStatePath())
	require.NoError(t, statErr)

	// Within-day idempotence: a repeated same-day call returns the same pick and
	// does not advance the rotation.
	stateBefore, err := os.ReadFile(a.repSurfaceStatePath())
	require.NoError(t, err)
	p1again, ok, err := a.SurfaceRep("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, p1.ID, p1again.ID)
	stateAfter, err := os.ReadFile(a.repSurfaceStatePath())
	require.NoError(t, err)
	assert.Equal(t, stateBefore, stateAfter, "a same-day surface does not advance the projection")

	// Successive days rotate through every rep once before any repeats.
	cycleDays := []string{"2026-08-24", "2026-08-25"}
	cycle := make([]string, 0, len(cycleDays))
	for _, day := range cycleDays {
		p, pok, perr := a.SurfaceRep(day)
		require.NoError(t, perr)
		require.True(t, pok)
		cycle = append(cycle, p.ID)
	}
	assert.Equal(t, []string{"rep_2026_08_23_002", "rep_2026_08_23_003"}, cycle)

	// All surfaced once: the least-recently-surfaced (001, shown on 08-23) wins,
	// and the next cycle replays the first one's order.
	replayDays := []string{"2026-08-26", "2026-08-27", "2026-08-28"}
	replay := make([]string, 0, len(replayDays))
	for _, day := range replayDays {
		p, pok, perr := a.SurfaceRep(day)
		require.NoError(t, perr)
		require.True(t, pok)
		replay = append(replay, p.ID)
	}
	assert.Equal(t, []string{"rep_2026_08_23_001", "rep_2026_08_23_002", "rep_2026_08_23_003"}, replay)

	// A freshly added rep is unsurfaced, so it enters the rotation next.
	fresh, err := a.AppendRep(synthRepEntry("d", "", 4, "2026-08-28"))
	require.NoError(t, err)
	p, ok, err := a.SurfaceRep("2026-08-29")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, fresh.ID, p.ID, "an unsurfaced rep sorts ahead of every surfaced one")
}

// TestSurfaceRep_SkipsRetired: a retirement event removes the rep from the
// active pool, so surface never shows a retired rep.
func TestSurfaceRep_SkipsRetired(t *testing.T) {
	a := newRepStore(t)
	orig, err := a.AppendRep(synthRepEntry("a", "", 0, "2026-08-23"))
	require.NoError(t, err)
	other, err := a.AppendRep(synthRepEntry("b", "", 0, "2026-08-23"))
	require.NoError(t, err)

	_, err = a.RetireRep(orig.ID, "2026-08-23", "2026-08-23T22:00:00-04:00")
	require.NoError(t, err)

	// The active pool is {other(002)}; it surfaces every day, never the retired 001.
	for _, day := range []string{"2026-08-23", "2026-08-24", "2026-08-25"} {
		p, ok, serr := a.SurfaceRep(day)
		require.NoError(t, serr)
		require.True(t, ok)
		assert.Equal(t, other.ID, p.ID)
	}

	// Retiring the last active rep leaves nothing to surface — a clean no-pick.
	_, err = a.RetireRep(other.ID, "2026-08-26", "2026-08-26T09:00:00-04:00")
	require.NoError(t, err)
	_, ok, err := a.SurfaceRep("2026-08-26")
	require.NoError(t, err)
	assert.False(t, ok, "an all-retired pool is nothing to surface")
}

// TestSurfaceRep_RepickWhenRecordedPickRetired: if today's recorded pick is
// retired after it was chosen, the same-day surface re-picks a live active rep
// rather than returning the retired one.
func TestSurfaceRep_RepickWhenRecordedPickRetired(t *testing.T) {
	a := newRepStore(t)
	first, err := a.AppendRep(synthRepEntry("a", "", 0, "2026-08-23"))
	require.NoError(t, err)
	second, err := a.AppendRep(synthRepEntry("b", "", 0, "2026-08-23"))
	require.NoError(t, err)

	p1, ok, err := a.SurfaceRep("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, first.ID, p1.ID)

	// Retire the recorded pick mid-day, then surface again the same day.
	_, err = a.RetireRep(first.ID, "2026-08-23", "2026-08-23T22:00:00-04:00")
	require.NoError(t, err)

	p2, ok, err := a.SurfaceRep("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, second.ID, p2.ID, "a retired recorded pick is replaced by a live one")
}

// TestRepSurfaceState_SeparateFromEntries: rotation state lives only in the
// separate projection reps/surface_state.json, never on the entries (reps.md §4).
// Three surfaces across two day keys leave every entry file byte-identical; the
// only file created or changed is the projection, whose top-level keys are
// exactly schema, last_surfaced, and today.
func TestRepSurfaceState_SeparateFromEntries(t *testing.T) {
	a := newRepStore(t)
	for _, r := range []rep.Rep{
		synthRepEntry("a", "solo a", 2, "2026-08-22"),
		synthRepEntry("b", "", 0, "2026-08-23"),
		synthRepEntry("c", "solo c", 3, "2026-08-23"),
	} {
		_, err := a.AppendRep(r)
		require.NoError(t, err)
	}
	before := repTreeHashes(t, a)
	require.NotContains(t, before, surfaceStateFile, "no projection before the first surface")

	for _, day := range []string{"2026-08-23", "2026-08-23", "2026-08-24"} {
		_, ok, err := a.SurfaceRep(day)
		require.NoError(t, err)
		require.True(t, ok)
	}

	after := repTreeHashes(t, a)
	var changed []string
	for path, sum := range after {
		if prev, ok := before[path]; !ok || prev != sum {
			changed = append(changed, path)
		}
	}
	for path := range before {
		_, ok := after[path]
		assert.Truef(t, ok, "entry file %s still exists", path)
	}
	assert.Equal(t, []string{surfaceStateFile}, changed,
		"surface creates or changes only reps/surface_state.json; every entry file is byte-identical")

	b, err := os.ReadFile(a.repSurfaceStatePath())
	require.NoError(t, err)
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &top))
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	assert.Equal(t, []string{"last_surfaced", "schema", "today"}, keys,
		"the projection holds only rotation bookkeeping")

	var st surfaceState
	require.NoError(t, json.Unmarshal(b, &st))
	assert.Equal(t, surfaceStateSchema, st.Schema)
	require.NotNil(t, st.Today)
	assert.Equal(t, "2026-08-24", st.Today.Day)
	assert.Equal(t, map[string]string{
		"rep_2026_08_22_001": "2026-08-23",
		"rep_2026_08_23_001": "2026-08-24",
	}, st.LastSurfaced)

	// The entries carry no rotation state of their own.
	for _, day := range []string{"2026-08-22", "2026-08-23"} {
		body := a.repDayBytes(t, day)
		assert.NotContains(t, string(body), "last_surfaced")
		assert.NotContains(t, string(body), "today")
	}
}

// TestRepSurfaceState_ResilientToCorrupt: the projection is rebuildable and
// disposable — a corrupt or deleted file resets rotation memory rather than
// erroring, and no rep is ever lost.
func TestRepSurfaceState_ResilientToCorrupt(t *testing.T) {
	a := newRepStore(t)
	_, err := a.AppendRep(synthRepEntry("a", "", 0, "2026-08-23"))
	require.NoError(t, err)
	_, err = a.AppendRep(synthRepEntry("b", "", 0, "2026-08-23"))
	require.NoError(t, err)

	require.NoError(t, ensureDir(a.repDir(), "rep"))
	require.NoError(t, os.WriteFile(a.repSurfaceStatePath(), []byte("{not json"), filePerm))

	// A corrupt projection cold-starts cleanly.
	p, ok, err := a.SurfaceRep("2026-08-23")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "rep_2026_08_23_001", p.ID)

	// Deleting the projection is also a clean cold start.
	require.NoError(t, os.Remove(a.repSurfaceStatePath()))
	p, ok, err = a.SurfaceRep("2026-08-24")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "rep_2026_08_23_001", p.ID, "rotation memory reset; the pool is intact")

	reps, err := a.ReadReps()
	require.NoError(t, err)
	assert.Len(t, reps, 2, "no rep is lost when the projection is discarded")
	assert.True(t, bytes.HasSuffix(a.repDayBytes(t, "2026-08-23"), []byte("\n")))
}

func repEntryIDs(rs []rep.Rep) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}
