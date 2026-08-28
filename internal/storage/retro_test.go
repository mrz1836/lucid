package storage

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/retro"
)

// newRetroStore returns a scaffolded adapter over an isolated temp home so the
// real ~/.lucid/ is never touched.
func newRetroStore(t *testing.T) *Adapter {
	t.Helper()
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldRetro())
	return a
}

// synthParkEvent returns a synthetic park event with no id (the store auto-mints)
// on the given logical date. Every retro item in these tests is invented.
func synthParkEvent(item, source, logicalDate string) retro.Retro {
	return retro.Retro{
		EventType:   retro.EventPark,
		Item:        item,
		Status:      retro.StatusOpen,
		Source:      source,
		RecordedAt:  "2026-08-23T21:45:10-04:00",
		LogicalDate: logicalDate,
	}
}

func (a *Adapter) retroDayPathT(t *testing.T, date string) string {
	t.Helper()
	p, err := a.retroDayPath(date)
	require.NoError(t, err)
	return p
}

func TestScaffoldRetro_Idempotent(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldRetro())

	info, statErr := os.Stat(a.retroDir())
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "retro/ tree exists after scaffold")

	// A second scaffold changes nothing.
	require.NoError(t, a.ScaffoldRetro())
}

// TestRetroMintGlobalMonotonic proves the two independent minters (AC-10): a
// plain park stream yields R-001..R-003; every appended event (park, resolve,
// defer) carries a unique per-day receipt; and after an import of explicit
// R-001..R-028 with synthetic resolve/defer transitions the next auto-mint park
// is R-029 — because only park events feed the R-NNN max and transitions never
// advance it.
func TestRetroMintGlobalMonotonic(t *testing.T) {
	// Scenario A: three plain parks mint R-001..R-003, then R-004.
	a := newRetroStore(t)
	seen := map[string]bool{}
	for i, want := range []string{"R-001", "R-002", "R-003"} {
		ev, err := a.AppendRetroEvent(synthParkEvent("item", "retro", "2026-08-23"))
		require.NoError(t, err)
		assert.Equalf(t, want, ev.ID, "park %d mints the next global R-NNN", i+1)
		assert.Truef(t, strings.HasPrefix(ev.EventID, "retro_event_2026_08_23_"), "receipt is a per-day id, got %q", ev.EventID)
		assert.Falsef(t, seen[ev.EventID], "receipt %q is unique", ev.EventID)
		seen[ev.EventID] = true
	}
	next, err := a.AppendRetroEvent(synthParkEvent("item", "retro", "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "R-004", next.ID)

	// Scenario B: a fresh store imports explicit R-001..R-028 plus synthetic
	// resolve/defer transitions; the next auto-mint park is R-029.
	b := newRetroStore(t)
	receipts := map[string]bool{}
	for i := 1; i <= 28; i++ {
		id := retro.ID(i)
		park := synthParkEvent("imported item", "migration", "2026-08-01")
		park.ID = id // explicit id: the import path supplies it, no auto-mint
		got, aerr := b.AppendRetroEvent(park)
		require.NoError(t, aerr)
		assert.Equal(t, id, got.ID, "an explicit import id is kept, not re-minted")
		assert.Falsef(t, receipts[got.EventID], "receipt %q is unique across imports", got.EventID)
		receipts[got.EventID] = true
	}

	// Synthetic transitions reference existing items on a later day — they must
	// NOT advance the R-NNN counter, and each still gets its own receipt.
	resolve := retro.Retro{
		EventType: retro.EventResolve, ID: "R-005", Status: retro.StatusResolved, Source: retro.SourceRetro,
		Resolution: "did it", ResolvedDate: "2026-08-30", RecordedAt: "2026-08-30T09:00:00-04:00", LogicalDate: "2026-08-30",
	}
	rGot, err := b.AppendRetroEvent(resolve)
	require.NoError(t, err)
	assert.Equal(t, "R-005", rGot.ID, "a resolve references the item, mints no new id")
	assert.Falsef(t, receipts[rGot.EventID], "the resolve receipt is unique")
	receipts[rGot.EventID] = true

	defer2 := retro.Retro{
		EventType: retro.EventDefer, ID: "R-010", Status: retro.StatusDeferred, Source: retro.SourceRetro,
		DeferReason: "someday", RecordedAt: "2026-08-30T09:05:00-04:00", LogicalDate: "2026-08-30",
	}
	dGot, err := b.AppendRetroEvent(defer2)
	require.NoError(t, err)
	assert.Equal(t, "R-010", dGot.ID, "a defer references the item, mints no new id")
	assert.Falsef(t, receipts[dGot.EventID], "the defer receipt is unique")

	// The next auto-mint park continues at imported-max+1 = R-029, regardless of
	// how many transition events were written.
	park29, err := b.AppendRetroEvent(synthParkEvent("new item", "retro", "2026-08-31"))
	require.NoError(t, err)
	assert.Equal(t, "R-029", park29.ID, "the next park continues after the imported item max, not the event count")

	// The fold reflects the transitions: R-005 resolved, R-010 deferred, others open.
	items, err := b.ReadRetro()
	require.NoError(t, err)
	require.Len(t, items, 29)
	byID := map[string]retro.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	assert.Equal(t, retro.StatusResolved, byID["R-005"].Status)
	assert.Equal(t, retro.StatusDeferred, byID["R-010"].Status)
	assert.Equal(t, retro.StatusOpen, byID["R-001"].Status)
	assert.Equal(t, retro.StatusOpen, byID["R-029"].Status)
	// The list is ascending numeric R-NNN, so R-029 sorts after R-005, not lexically.
	assert.Equal(t, "R-001", items[0].ID)
	assert.Equal(t, "R-029", items[len(items)-1].ID)
}

// TestRetroAppend_SeqSingleLineAndDefaults: an append writes one whole JSONL
// line, defaults an empty source/status, and derives per-day receipts single-
// writer, ignoring a truncated line.
func TestRetroAppend_SeqSingleLineAndDefaults(t *testing.T) {
	a := newRetroStore(t)

	// An empty source/status defaults to the documented provenance and open.
	bare := synthParkEvent("a thing", "", "2026-08-23")
	bare.Status = ""
	e1, err := a.AppendRetroEvent(bare)
	require.NoError(t, err)
	assert.Equal(t, retro.SourceRetro, e1.Source)
	assert.Equal(t, retro.StatusOpen, e1.Status)
	assert.Equal(t, "retro_event_2026_08_23_001", e1.EventID)

	_, err = a.AppendRetroEvent(synthParkEvent("second", "retro", "2026-08-23"))
	require.NoError(t, err)

	// Inject a truncated line; the receipt derivation ignores it.
	require.NoError(t, appendLineFsync(a.retroDayPathT(t, "2026-08-23"), []byte(`{"event_id":"retro_event_2026_08_23_003","sch`)))
	e3, err := a.AppendRetroEvent(synthParkEvent("third", "retro", "2026-08-23"))
	require.NoError(t, err)
	assert.Equal(t, "retro_event_2026_08_23_003", e3.EventID, "next receipt is 003, not the line count")

	// The day file holds one whole JSON line per good append plus the injected one.
	body, err := os.ReadFile(a.retroDayPathT(t, "2026-08-23"))
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(string(body), "\n"))
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	require.Len(t, lines, 4)

	// A missing logical_date is rejected; an empty park item is rejected.
	_, err = a.AppendRetroEvent(retro.Retro{EventType: retro.EventPark, Item: "x"})
	require.Error(t, err)
	_, err = a.AppendRetroEvent(synthParkEvent("", "retro", "2026-08-23"))
	require.Error(t, err)
}

// TestReadRetro_FoldsAcrossDaysAndByID: resolve on a later day folds onto a park
// from an earlier day; the original park line is never rewritten; by-id lookup
// resolves the folded item; an unknown id is a clean not-found.
func TestReadRetro_FoldsAcrossDaysAndByID(t *testing.T) {
	a := newRetroStore(t)
	p1 := synthParkEvent("first", "chat", "2026-08-20")
	p1.ID = "R-001"
	_, err := a.AppendRetroEvent(p1)
	require.NoError(t, err)
	p2 := synthParkEvent("second", "retro", "2026-08-20")
	p2.ID = "R-002"
	_, err = a.AppendRetroEvent(p2)
	require.NoError(t, err)

	before, err := os.ReadFile(a.retroDayPathT(t, "2026-08-20"))
	require.NoError(t, err)

	// Resolve R-001 on a later day — cross-day fold.
	resolve := retro.Retro{
		EventType: retro.EventResolve, ID: "R-001", Status: retro.StatusResolved, Source: retro.SourceRetro,
		Resolution: "handled", ResolvedDate: "2026-08-25", RecordedAt: "2026-08-25T09:00:00-04:00", LogicalDate: "2026-08-25",
	}
	_, err = a.AppendRetroEvent(resolve)
	require.NoError(t, err)

	after, err := os.ReadFile(a.retroDayPathT(t, "2026-08-20"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "the parked items' original day file is never rewritten")

	items, err := a.ReadRetro()
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, []string{"R-001", "R-002"}, []string{items[0].ID, items[1].ID})
	assert.Equal(t, retro.StatusResolved, items[0].Status)
	assert.Equal(t, "handled", items[0].Resolution)
	assert.Equal(t, "2026-08-25", items[0].ResolvedDate)
	assert.Equal(t, "2026-08-20", items[0].ParkedDate, "the parked date stays the park day, not the resolve day")

	got, found, err := a.ReadRetroByID("R-001")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "first", got.Item)

	_, found, err = a.ReadRetroByID("R-404")
	require.NoError(t, err)
	assert.False(t, found, "an unknown id is a clean not-found, never an error")
}
