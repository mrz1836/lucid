package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/retro"
)

// bootedRetro returns a booted router over a fresh scaffolded Ledger (day1/day2/
// edt live in reframe_test.go — same package).
func bootedRetro(t *testing.T) *Router {
	t.Helper()
	r := New(newScaffolded(t))
	_, err := r.Boot()
	require.NoError(t, err)
	return r
}

// bootedRetroWithHome is bootedRetro plus the Ledger home, so a test can read the
// raw day file on disk (the never-delete byte-retention check).
func bootedRetroWithHome(t *testing.T) (*Router, string) {
	t.Helper()
	home, a := lucidtest.Ledger(t)
	r := New(a)
	_, err := r.Boot()
	require.NoError(t, err)
	return r, home
}

// parkRetro parks one synthetic item and returns its minted R-NNN id.
func parkRetro(t *testing.T, r *Router, item string) string {
	t.Helper()
	res, err := r.ParkRetro(ParkRetroRequest{Item: item, Now: day1()})
	require.NoError(t, err)
	return res.View.ID
}

// TestRetroParkAppendsAndReturnsReceipt: park appends one event and returns a
// unique per-write receipt (retro_event_…) distinct from the parked-item id; a
// second park returns a different receipt and the next R-NNN (AC-3).
func TestRetroParkAppendsAndReturnsReceipt(t *testing.T) {
	r := bootedRetro(t)

	first, err := r.ParkRetro(ParkRetroRequest{Item: "Revisit the Sunday walk opener", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-001", first.View.ID)
	assert.True(t, strings.HasPrefix(first.View.EventID, "retro_event_"), "the receipt is a per-write id")
	assert.NotEqual(t, first.View.ID, first.View.EventID, "the parked-item id and the receipt are distinct identities")
	assert.Equal(t, retro.StatusOpen, first.View.Item.Status)
	assert.Equal(t, "Revisit the Sunday walk opener", first.View.Item.Item)
	assert.Contains(t, first.Ack, "R-001")
	assert.Contains(t, first.Ack, first.View.EventID, "the ack names both the item id and the receipt")

	second, err := r.ParkRetro(ParkRetroRequest{Item: "Try a shorter Gate cadence", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-002", second.View.ID, "the next park mints the next global id")
	assert.NotEqual(t, first.View.EventID, second.View.EventID, "each write returns a fresh receipt")
}

// TestRetroParkReturnsRID: park mints and returns a real R-NNN id (the echo
// contract holds structurally); an empty item is a loud failure that parks
// nothing (AC-4).
func TestRetroParkReturnsRID(t *testing.T) {
	r := bootedRetro(t)

	res, err := r.ParkRetro(ParkRetroRequest{Item: "Experiment with a two-column weekly layout", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-001", res.View.ID, "park returns the minted R-NNN")
	seq, ok := retro.ParseSeq(res.View.ID)
	assert.True(t, ok, "the returned id is a well-formed R-NNN")
	assert.Equal(t, 1, seq)

	// An empty item fails loudly and parks nothing — no id is echoed.
	_, err = r.ParkRetro(ParkRetroRequest{Item: "   ", Now: day1()})
	require.Error(t, err, "an empty item is a loud failure, never a silent no-op")

	// The failed park did not advance the counter: the next real park is still R-002.
	next, err := r.ParkRetro(ParkRetroRequest{Item: "A second real item", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-002", next.View.ID)
}

// TestRetroPark_SourceAndBackdating: --source is stored verbatim (default retro),
// and a strict --day backdates the parked date; a bad token parks nothing.
func TestRetroPark_SourceAndBackdating(t *testing.T) {
	r := bootedRetro(t)

	res, err := r.ParkRetro(ParkRetroRequest{Item: "From chat", Source: "chat", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "chat", res.View.Item.Source)

	// No --source defaults to the documented `retro` provenance.
	def, err := r.ParkRetro(ParkRetroRequest{Item: "Default source", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, retro.SourceRetro, def.View.Item.Source)

	// --day backdates the parked date to a literal civil day.
	back, err := r.ParkRetro(ParkRetroRequest{Item: "Backdated", DayArg: "2026-06-01", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "2026-06-01", back.View.Item.ParkedDate)

	// A --day the grammar cannot read is a clean refusal that parks nothing.
	_, err = r.ParkRetro(ParkRetroRequest{Item: "Bad day", DayArg: "@yesterdya", Now: day1()})
	require.Error(t, err)
	var refused *DayRejectedError
	assert.ErrorAs(t, err, &refused, "a bad --day is a DayRejectedError")
}

// TestRetroListDefaultShowsOpenAndDeferred: the default list is the Sunday walk —
// open ∪ deferred, ascending R-NNN — so a deferred item never vanishes from it; a
// resolved item leaves the default view but returns under --all / --resolved
// (AC-5).
func TestRetroListDefaultShowsOpenAndDeferred(t *testing.T) {
	r := bootedRetro(t)
	parkRetro(t, r, "Open item")      // R-001
	parkRetro(t, r, "To be deferred") // R-002
	parkRetro(t, r, "To be resolved") // R-003

	_, err := r.DeferRetro(DeferRetroRequest{ID: "R-002", Reason: "someday", Now: day2()})
	require.NoError(t, err)
	_, err = r.ResolveRetro(ResolveRetroRequest{ID: "R-003", Resolution: "did it", Now: day2()})
	require.NoError(t, err)

	// Default: open + deferred, ascending, with the resolved item excluded.
	def, err := r.ListRetro(ListRetroRequest{})
	require.NoError(t, err)
	require.Equal(t, 2, def.View.Count)
	assert.Equal(t, []string{"R-001", "R-002"}, retroIDs(def.View.Items))
	assert.Equal(t, retro.StatusOpen, def.View.Items[0].Status)
	assert.Equal(t, retro.StatusDeferred, def.View.Items[1].Status, "a deferred item stays in the default walk")

	// --all: open + deferred first, then the resolved item.
	all, err := r.ListRetro(ListRetroRequest{All: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"R-001", "R-002", "R-003"}, retroIDs(all.View.Items))
	assert.Equal(t, retro.StatusResolved, all.View.Items[2].Status, "resolved sorts after the open/deferred section")

	// --resolved: only the resolved audit trail.
	res, err := r.ListRetro(ListRetroRequest{Resolved: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"R-003"}, retroIDs(res.View.Items))

	// --resolved wins over --all (most specific filter).
	both, err := r.ListRetro(ListRetroRequest{All: true, Resolved: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"R-003"}, retroIDs(both.View.Items))
}

// TestRetroShowAllFields: `show <R-NNN>` returns a single folded item with every
// schema field — the base park fields plus the resolution/resolved-date of a
// resolved item and the defer-reason of a deferred one; an unknown id is a clean
// error (AC-6).
func TestRetroShowAllFields(t *testing.T) {
	r := bootedRetro(t)

	back, err := r.ParkRetro(ParkRetroRequest{Item: "Item one\nsecond paragraph", Source: "chat", DayArg: "2026-06-01", Now: day1()})
	require.NoError(t, err)
	require.Equal(t, "R-001", back.View.ID)
	parkRetro(t, r, "Item two") // R-002

	_, err = r.DeferRetro(DeferRetroRequest{ID: "R-001", Reason: "revisit next quarter", Now: day2()})
	require.NoError(t, err)
	_, err = r.ResolveRetro(ResolveRetroRequest{ID: "R-002", Resolution: "adopted it", Now: day2()})
	require.NoError(t, err)

	// The deferred item renders every base field plus its reason.
	deferred, err := r.ShowRetro("R-001")
	require.NoError(t, err)
	it := deferred.View
	assert.Equal(t, "R-001", it.ID)
	assert.Equal(t, "2026-06-01", it.ParkedDate)
	assert.Equal(t, "chat", it.Source)
	assert.Equal(t, "Item one\nsecond paragraph", it.Item, "the verbatim multi-line body is preserved")
	assert.Equal(t, retro.StatusDeferred, it.Status)
	assert.Equal(t, "revisit next quarter", it.DeferReason)
	joined := strings.Join(deferred.Lines, "\n")
	assert.Contains(t, joined, "R-001")
	assert.Contains(t, joined, "second paragraph", "the full body renders in show")
	assert.Contains(t, joined, "revisit next quarter")

	// The resolved item renders the resolution and resolved-date.
	resolved, err := r.ShowRetro("R-002")
	require.NoError(t, err)
	assert.Equal(t, retro.StatusResolved, resolved.View.Status)
	assert.Equal(t, "adopted it", resolved.View.Resolution)
	assert.NotEmpty(t, resolved.View.ResolvedDate, "a resolved item carries its resolved-date")

	// An unknown id is a clean error.
	_, err = r.ShowRetro("R-404")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "R-404")
}

// TestRetroResolveNeverDeletes: resolve is an append — the original park line is
// byte-retained on disk, the item leaves the default list, and it reappears under
// --resolved / --all with its resolution and resolved-date (AC-7).
func TestRetroResolveNeverDeletes(t *testing.T) {
	r, home := bootedRetroWithHome(t)

	// Park on day1 so the park lands in a known day file.
	res, err := r.ParkRetro(ParkRetroRequest{Item: "Resolve me", Source: "chat", Now: day1()})
	require.NoError(t, err)
	require.Equal(t, "R-001", res.View.ID)
	parkDate := res.View.Item.ParkedDate // 2026-07-02
	parkFile := filepath.Join(home, "retro",
		parkDate[0:4], parkDate[5:7], "retro_"+strings.ReplaceAll(parkDate, "-", "_")+".jsonl")
	before, err := os.ReadFile(parkFile)
	require.NoError(t, err)

	// Resolve on a later day: the transition lands in a different day file.
	tr, err := r.ResolveRetro(ResolveRetroRequest{ID: "R-001", Resolution: "handled it", Now: day2()})
	require.NoError(t, err)
	assert.Equal(t, "R-001", tr.View.ID)
	assert.Equal(t, retro.StatusResolved, tr.View.Item.Status)

	// The original park line is byte-identical — nothing was rewritten or deleted.
	after, err := os.ReadFile(parkFile)
	require.NoError(t, err)
	assert.Equal(t, before, after, "resolve never rewrites the original park line")

	// The resolved item leaves the default list but stays under --resolved / --all.
	def, err := r.ListRetro(ListRetroRequest{})
	require.NoError(t, err)
	assert.Empty(t, def.View.Items, "a resolved item leaves the default walk")

	resolved, err := r.ListRetro(ListRetroRequest{Resolved: true})
	require.NoError(t, err)
	require.Equal(t, []string{"R-001"}, retroIDs(resolved.View.Items))
	assert.Equal(t, "handled it", resolved.View.Items[0].Resolution)
	assert.NotEmpty(t, resolved.View.Items[0].ResolvedDate)
	assert.Equal(t, parkDate, resolved.View.Items[0].ParkedDate, "the parked date stays the park day, not the resolve day")
}

// TestRetroDeferSetsDeferredStatus: defer sets the first-class deferred status and
// records the reason, and the item stays visible in the default list (AC-8).
func TestRetroDeferSetsDeferredStatus(t *testing.T) {
	r := bootedRetro(t)
	parkRetro(t, r, "Defer me") // R-001

	tr, err := r.DeferRetro(DeferRetroRequest{ID: "R-001", Reason: "someday / maybe", Now: day2()})
	require.NoError(t, err)
	assert.Equal(t, "R-001", tr.View.ID)
	assert.Equal(t, retro.StatusDeferred, tr.View.Item.Status)
	assert.Equal(t, "someday / maybe", tr.View.Item.DeferReason)

	// Still in the default walk, carrying its reason.
	def, err := r.ListRetro(ListRetroRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"R-001"}, retroIDs(def.View.Items))
	assert.Equal(t, retro.StatusDeferred, def.View.Items[0].Status)
	assert.Equal(t, "someday / maybe", def.View.Items[0].DeferReason)

	// An empty reason is a loud failure that changes nothing.
	_, err = r.DeferRetro(DeferRetroRequest{ID: "R-001", Reason: "   ", Now: day2()})
	require.Error(t, err)

	// A defer of an unknown id is a clean error and appends nothing.
	_, err = r.DeferRetro(DeferRetroRequest{ID: "R-404", Reason: "nope", Now: day2()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "R-404")
}

// TestRetroTransitionReceiptsAndAcknowledgments: resolve/defer each mint a unique
// internal receipt, their acks name the affected R-NNN alongside that receipt, and
// neither advances the parked-item counter — a subsequent park consumes exactly
// the next id (AC-7, AC-8, and the identity split).
func TestRetroTransitionReceiptsAndAcknowledgments(t *testing.T) {
	r := bootedRetro(t)
	first := parkRetro(t, r, "First")   // R-001
	second := parkRetro(t, r, "Second") // R-002
	require.Equal(t, "R-001", first)
	require.Equal(t, "R-002", second)

	resolve, err := r.ResolveRetro(ResolveRetroRequest{ID: "R-001", Resolution: "did it", Now: day2()})
	require.NoError(t, err)
	deferRes, err := r.DeferRetro(DeferRetroRequest{ID: "R-002", Reason: "later", Now: day2()})
	require.NoError(t, err)

	// Each transition's receipt is a per-write internal id, distinct from the R-NNN.
	assert.True(t, strings.HasPrefix(resolve.View.EventID, "retro_event_"), "the resolve receipt is a per-write id")
	assert.True(t, strings.HasPrefix(deferRes.View.EventID, "retro_event_"), "the defer receipt is a per-write id")
	assert.NotEqual(t, resolve.View.EventID, deferRes.View.EventID, "each transition mints a fresh receipt")
	assert.NotEqual(t, resolve.View.ID, resolve.View.EventID, "the affected R-NNN and the receipt are distinct")

	// The ack names both the affected R-NNN and the write that recorded it.
	assert.Contains(t, resolve.Ack, "R-001")
	assert.Contains(t, resolve.Ack, resolve.View.EventID)
	assert.Contains(t, deferRes.Ack, "R-002")
	assert.Contains(t, deferRes.Ack, deferRes.View.EventID)

	// Neither transition advanced the parked-item counter: the next park is R-003.
	next, err := r.ParkRetro(ParkRetroRequest{Item: "Third", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-003", next.View.ID, "a transition never consumes an R-NNN slot")
}

// TestRetroImportExplicitIDs: the hidden one-time import reproduces a synthetic
// set with explicit ids exactly — sources, statuses, defer-reasons, resolutions,
// and dates all round-trip on `list --all` — gives every generated park/transition
// event a unique internal receipt, and makes a subsequent `park` continue at the
// next id after the imported item max (R-004), not after the raw event count
// (AC-12, AC-3).
func TestRetroImportExplicitIDs(t *testing.T) {
	r, home := bootedRetroWithHome(t)

	items := []retro.Item{
		{
			ID: "R-001", ParkedDate: "2026-07-10", Source: "raw_2026_07_10_22_55",
			Item:   "Resolved sample\n\nwith a second paragraph kept verbatim",
			Status: retro.StatusResolved, Resolution: "adopted it — the walk opens with deferred items", ResolvedDate: "2026-07-20",
		},
		{
			ID: "R-002", ParkedDate: "2026-07-12", Source: "chat",
			Item: "Deferred sample", Status: retro.StatusDeferred, DeferReason: "someday / after the quarter closes",
		},
		{
			ID: "R-003", ParkedDate: "2026-07-15", Source: "retro",
			Item: "Open sample", Status: retro.StatusOpen,
		},
	}

	res, err := r.ImportRetro(ImportRetroRequest{Items: items, Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, 3, res.View.Imported)
	assert.Equal(t, []string{"R-001", "R-002", "R-003"}, res.View.IDs)
	assert.Contains(t, res.Ack, "Imported 3 parked item(s)")

	// list --all reproduces every field exactly, ascending R-NNN.
	all, err := r.ListRetro(ListRetroRequest{All: true})
	require.NoError(t, err)
	require.Equal(t, 3, all.View.Count)
	byID := map[string]retro.Item{}
	for _, it := range all.View.Items {
		byID[it.ID] = it
	}

	r1 := byID["R-001"]
	assert.Equal(t, "2026-07-10", r1.ParkedDate)
	assert.Equal(t, "raw_2026_07_10_22_55", r1.Source, "the original source is stored verbatim")
	assert.Equal(t, "Resolved sample\n\nwith a second paragraph kept verbatim", r1.Item, "the multi-paragraph body is kept verbatim")
	assert.Equal(t, retro.StatusResolved, r1.Status)
	assert.Equal(t, "adopted it — the walk opens with deferred items", r1.Resolution)
	assert.Equal(t, "2026-07-20", r1.ResolvedDate, "the original resolved-date is preserved, not set to today")

	r2 := byID["R-002"]
	assert.Equal(t, "2026-07-12", r2.ParkedDate)
	assert.Equal(t, "chat", r2.Source)
	assert.Equal(t, retro.StatusDeferred, r2.Status)
	assert.Equal(t, "someday / after the quarter closes", r2.DeferReason)

	r3 := byID["R-003"]
	assert.Equal(t, retro.StatusOpen, r3.Status)
	assert.Equal(t, "retro", r3.Source)
	assert.Empty(t, r3.Resolution)
	assert.Empty(t, r3.DeferReason)

	// Every generated event (3 parks + 1 resolve + 1 defer = 5) carries a unique
	// per-write receipt distinct from the R-NNN item id.
	raw := readRetroRaw(t, home)
	require.Len(t, raw, 5, "one park per item plus one transition each for the resolved and deferred items")
	receipts := map[string]bool{}
	for _, ev := range raw {
		assert.Truef(t, strings.HasPrefix(ev.EventID, "retro_event_"), "receipt %q has the retro_event_ prefix", ev.EventID)
		assert.Falsef(t, receipts[ev.EventID], "receipt %q is unique across the import", ev.EventID)
		receipts[ev.EventID] = true
	}

	// The next everyday park continues at imported-max+1 = R-004, NOT after the
	// number of raw events (which would be R-006).
	next, err := r.ParkRetro(ParkRetroRequest{Item: "Parked after the import", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-004", next.View.ID, "the next park continues after the imported item max, not the event count")
}

// TestRetroImport_ValidationAbortsBeforeWriting: a structurally-impossible item
// (a resolved row with no resolution) aborts the whole import before any event is
// written, and an empty item set is a clean error — so a bad file never leaves a
// partial, non-idempotent store behind.
func TestRetroImport_ValidationAbortsBeforeWriting(t *testing.T) {
	r, home := bootedRetroWithHome(t)

	_, err := r.ImportRetro(ImportRetroRequest{Items: nil, Now: day1()})
	require.Error(t, err, "an empty import set is a clean error")

	bad := []retro.Item{
		{ID: "R-001", ParkedDate: "2026-07-10", Source: "chat", Item: "Good open item", Status: retro.StatusOpen},
		{ID: "R-002", ParkedDate: "2026-07-11", Source: "chat", Item: "Resolved but no resolution", Status: retro.StatusResolved, ResolvedDate: "2026-07-20"},
	}
	_, err = r.ImportRetro(ImportRetroRequest{Items: bad, Now: day1()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "R-002")
	assert.Empty(t, readRetroRaw(t, home), "a malformed item aborts the whole import — nothing is written")
}

// readRetroRaw walks the retro tree under home and returns every appended event,
// so a router test can assert receipt uniqueness and the exact write count on disk.
func readRetroRaw(t *testing.T, home string) []retro.Retro {
	t.Helper()
	var out []retro.Retro
	root := filepath.Join(home, "retro")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return filepath.SkipDir
			}
			return werr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "retro_") || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			ev, uerr := retro.UnmarshalLine([]byte(line))
			require.NoError(t, uerr)
			out = append(out, ev)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return out
}

// retroIDs projects the ids from a folded item slice, so a list assertion reads
// as the expected ascending R-NNN order.
func retroIDs(items []retro.Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}
