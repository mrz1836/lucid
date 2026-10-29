package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/contact"
)

// newContactStore returns a scaffolded adapter over an isolated temp home so
// the real ~/.lucid/ is never touched.
func newContactStore(t *testing.T) *Adapter {
	t.Helper()
	a := New(t.TempDir())
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldContacts())
	return a
}

// synthContactRecordedAt is the fixed write time every synthetic event carries.
const synthContactRecordedAt = "2026-10-29T10:15:00-04:00"

// synthContact returns a synthetic contact event for a person key and logical
// day. Every key in these tests is invented — real people live only in the
// private Ledger.
func synthContact(key, day string) contact.Event {
	return contact.NewContact(key, day, synthContactRecordedAt, contact.ViaCall, "")
}

func (a *Adapter) contactDayPathT(t *testing.T, date string) string {
	t.Helper()
	p, err := a.contactDayPath(date)
	require.NoError(t, err)
	return p
}

func (a *Adapter) contactDayBytes(t *testing.T, date string) []byte {
	t.Helper()
	b, err := os.ReadFile(a.contactDayPathT(t, date))
	require.NoError(t, err)
	return b
}

func appendContactT(t *testing.T, a *Adapter, e contact.Event) contact.Event {
	t.Helper()
	stored, err := a.AppendContactEvent(e)
	require.NoError(t, err)
	return stored
}

func contactEventIDs(events []contact.Event) []string {
	ids := make([]string, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	return ids
}

// TestScaffoldContacts_Idempotent: scaffolding creates the tree once and a
// second run leaves everything under it — shards and the weekly receipt —
// byte-identical.
func TestScaffoldContacts_Idempotent(t *testing.T) {
	a := newContactStore(t)
	info, err := os.Stat(a.contactDir())
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	stored := appendContactT(t, a, synthContact("person_s-river", "2026-10-29"))
	require.NoError(t, a.WriteContactWeeklyReceipt(ContactWeeklyReceipt{Week: "2026-W44", MessageID: "msg-1"}))
	shard := a.contactDayBytes(t, "2026-10-29")
	receipt, err := os.ReadFile(a.contactWeeklyReceiptPath())
	require.NoError(t, err)

	require.NoError(t, a.ScaffoldContacts())
	assert.Equal(t, shard, a.contactDayBytes(t, "2026-10-29"), "a re-scaffold leaves the shard untouched")
	again, err := os.ReadFile(a.contactWeeklyReceiptPath())
	require.NoError(t, err)
	assert.Equal(t, receipt, again, "a re-scaffold leaves the receipt untouched")

	got, ok, err := a.ReadContactEventByID(stored.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, stored, got)
}

// TestAppendContactEvent_SlotIDsAndSingleLine: each append lands as one line in
// contacts/YYYY/MM/contact_YYYY_MM_DD.jsonl under the next letter slot; a
// backdated event files under its own day with its own sequence; and a damaged
// line never causes its slot to be minted again, nor does the line count steer
// the slot (ADR-0014 §2).
func TestAppendContactEvent_SlotIDsAndSingleLine(t *testing.T) {
	a := newContactStore(t)

	first := appendContactT(t, a, synthContact("person_s-river", "2026-10-29"))
	assert.Equal(t, "contact_2026_10_29_a", first.ID)
	assert.Equal(t, contact.Schema, first.Schema)
	assert.Equal(t, contact.SourceContact, first.Source)
	second := appendContactT(t, a, contact.NewSnooze("person_a-kim", "2026-10-29", synthContactRecordedAt))
	assert.Equal(t, "contact_2026_10_29_b", second.ID)

	wantPath := filepath.Join(a.contactDir(), "2026", "10", "contact_2026_10_29.jsonl")
	assert.Equal(t, wantPath, a.contactDayPathT(t, "2026-10-29"))
	body := a.contactDayBytes(t, "2026-10-29")
	assert.Equal(t, 2, bytes.Count(body, []byte("\n")), "one line per append")
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	wantFirst, err := first.MarshalLine()
	require.NoError(t, err)
	assert.Equal(t, string(wantFirst), lines[0], "the stored line is the event, verbatim")

	// A backdated contact files under its own day and starts that day's slots.
	back := appendContactT(t, a, synthContact("person_s-river", "2026-10-12"))
	assert.Equal(t, "contact_2026_10_12_a", back.ID)
	assert.Equal(t, 1, bytes.Count(a.contactDayBytes(t, "2026-10-12"), []byte("\n")), "the backdated day has its own shard")
	third := appendContactT(t, a, synthContact("person_j-lee", "2026-10-29"))
	assert.Equal(t, "contact_2026_10_29_c", third.ID, "the backdated write did not touch today's sequence")

	// A torn line that still names its id keeps that slot: `_d` is never
	// reissued as a second receipt.
	require.NoError(t, appendLineFsync(a.contactDayPathT(t, "2026-10-29"),
		[]byte(`{"id":"contact_2026_10_29_d","schema":1,"ty`)))
	afterTorn := appendContactT(t, a, synthContact("person_c-park", "2026-10-29"))
	assert.Equal(t, "contact_2026_10_29_e", afterTorn.ID, "a damaged line's slot is never reused")

	// Lines with no recoverable id do not count either way: the slot follows
	// the ids, never the line count.
	for range 3 {
		require.NoError(t, appendLineFsync(a.contactDayPathT(t, "2026-10-29"), []byte(`not json`)))
	}
	afterJunk := appendContactT(t, a, synthContact("person_r-moss", "2026-10-29"))
	assert.Equal(t, "contact_2026_10_29_f", afterJunk.ID)

	// Any id the caller set is replaced by the minted one.
	preset := synthContact("person_s-river", "2026-11-02")
	preset.ID = "contact_2026_11_02_q"
	minted := appendContactT(t, a, preset)
	assert.Equal(t, "contact_2026_11_02_a", minted.ID)

	// A malformed event is refused before anything is written.
	bad := synthContact("person_s-river", "2026-11-03")
	bad.Via = "email"
	_, err = a.AppendContactEvent(bad)
	require.ErrorIs(t, err, contact.ErrInvalidVia)
	assert.NoFileExists(t, a.contactDayPathT(t, "2026-11-03"))
	_, err = a.AppendContactEvent(contact.Event{Type: contact.TypeContact, PersonKey: "person_s-river"})
	require.Error(t, err, "an event without a logical_date is refused")
}

// TestContactSlotIndex_InvertsSlotLabel pins the slot reader to the shared
// grammar across the single-to-double letter boundary.
func TestContactSlotIndex_InvertsSlotLabel(t *testing.T) {
	for _, n := range []int{0, 1, 25, 26, 27, 51, 52, 701, 702} {
		got, ok := contactSlotIndex(contact.ID("2026-10-29", n))
		require.True(t, ok)
		assert.Equal(t, n, got, "slot %d", n)
	}
	for _, bad := range []string{"", "contact_2026_10_29_", "contact_2026_10_29_A", "rep_2026_10_29_001", "contact_2026_13_01_a"} {
		_, ok := contactSlotIndex(bad)
		assert.False(t, ok, "%q", bad)
	}
}

// TestAppendContactEvent_SlotsPastZ: the 27th event of a day is `_aa`, and the
// read order follows the slot, not the string (`_z` before `_aa`).
func TestAppendContactEvent_SlotsPastZ(t *testing.T) {
	a := newContactStore(t)
	var last contact.Event
	for range 28 {
		last = appendContactT(t, a, synthContact("person_s-river", "2026-10-29"))
	}
	assert.Equal(t, "contact_2026_10_29_ab", last.ID)

	all, err := a.ReadContactEvents()
	require.NoError(t, err)
	require.Len(t, all, 28)
	assert.Equal(t, "contact_2026_10_29_z", all[25].ID)
	assert.Equal(t, "contact_2026_10_29_aa", all[26].ID)
	assert.Equal(t, "contact_2026_10_29_ab", all[27].ID)
}

// TestReadContactEvents_SkipsMalformedSortsAcrossDays: the whole-tree read
// returns every decodable event across months and years in logical-day then
// slot order, skips damaged lines (counted by the day read), ignores the
// weekly receipt at the tree root, and reads a missing tree as empty.
func TestReadContactEvents_SkipsMalformedSortsAcrossDays(t *testing.T) {
	empty := New(t.TempDir())
	none, err := empty.ReadContactEvents()
	require.NoError(t, err)
	assert.Empty(t, none, "a Ledger with no contacts/ tree reads as the empty stream")

	a := newContactStore(t)
	// Appended out of chronological order, across a month and a year boundary.
	appendContactT(t, a, synthContact("person_s-river", "2026-11-02"))
	appendContactT(t, a, synthContact("person_a-kim", "2025-12-30"))
	appendContactT(t, a, synthContact("person_s-river", "2026-10-29"))
	appendContactT(t, a, contact.NewSnooze("person_a-kim", "2026-11-02", synthContactRecordedAt))
	require.NoError(t, appendLineFsync(a.contactDayPathT(t, "2026-10-29"), []byte(`{"id":"contact_2026_10_29_b","sch`)))
	require.NoError(t, a.WriteContactWeeklyReceipt(ContactWeeklyReceipt{Week: "2026-W45"}))

	all, err := a.ReadContactEvents()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"contact_2025_12_30_a",
		"contact_2026_10_29_a",
		"contact_2026_11_02_a",
		"contact_2026_11_02_b",
	}, contactEventIDs(all))

	day, skipped, err := a.ReadContactDay("2026-10-29")
	require.NoError(t, err)
	assert.Len(t, day, 1, "the damaged line is skipped")
	assert.Equal(t, 1, skipped, "and its count reported")

	missing, skipped, err := a.ReadContactDay("2026-09-09")
	require.NoError(t, err)
	assert.Empty(t, missing)
	assert.Zero(t, skipped)
	_, _, err = a.ReadContactDay("../../etc")
	require.Error(t, err, "a malformed date is rejected before any read")
}

// TestReadContactEvents_KeepsInvalidEventsForTheFold: a line that
// decodes but fails validation is returned as written — the fold skips it —
// and its id still holds its slot.
func TestReadContactEvents_KeepsInvalidEventsForTheFold(t *testing.T) {
	a := newContactStore(t)
	require.NoError(t, appendLineFsync(a.contactDayPathT(t, "2026-10-29"),
		[]byte(`{"id":"contact_2026_10_29_a","schema":1,"type":"wave","person_key":"person_s-river","logical_date":"2026-10-29","recorded_at":"`+synthContactRecordedAt+`","source":"contact"}`)))

	all, err := a.ReadContactEvents()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "wave", all[0].Type)
	assert.Empty(t, contact.Fold(all), "the fold skips what does not validate")

	next := appendContactT(t, a, synthContact("person_s-river", "2026-10-29"))
	assert.Equal(t, "contact_2026_10_29_b", next.ID)
}

// TestReadContactEventByID: an id reads back from the day it encodes; an
// unparseable id, a missing day and an absent id are a clean not-found.
func TestReadContactEventByID(t *testing.T) {
	a := newContactStore(t)
	appendContactT(t, a, synthContact("person_s-river", "2026-10-28"))
	want := appendContactT(t, a, contact.NewSnooze("person_a-kim", "2026-10-28", synthContactRecordedAt))

	got, ok, err := a.ReadContactEventByID(want.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, want, got)

	for _, id := range []string{"contact_2026_10_28_z", "contact_2026_10_27_a", "rep_2026_10_28_001", "contact_2026_02_30_a", "", "../../etc"} {
		_, ok, err := a.ReadContactEventByID(id)
		require.NoError(t, err, "%q", id)
		assert.False(t, ok, "%q is not found", id)
	}
}

// TestContactAppend_LeavesPriorLinesByteIdentical: every append — a contact, a
// cadence change, a snooze, a void naming an earlier event — adds exactly one
// line and leaves every earlier byte of the shard untouched.
func TestContactAppend_LeavesPriorLinesByteIdentical(t *testing.T) {
	a := newContactStore(t)
	const day = "2026-10-29"
	w, err := contact.ParseWindow("4w")
	require.NoError(t, err)

	target := appendContactT(t, a, synthContact("person_s-river", day))
	events := []contact.Event{
		contact.NewCadence("person_s-river", day, synthContactRecordedAt, contact.ActionSet, w),
		contact.NewCadence("person_s-river", day, synthContactRecordedAt, contact.ActionPause, contact.Window{}),
		contact.NewSnooze("person_a-kim", day, synthContactRecordedAt),
		contact.NewVoid(target.ID, "person_s-river", day, synthContactRecordedAt),
	}
	for _, e := range events {
		before := a.contactDayBytes(t, day)
		stored := appendContactT(t, a, e)
		after := a.contactDayBytes(t, day)
		require.True(t, bytes.HasPrefix(after, before), "%s: earlier lines are byte-identical", stored.ID)
		line, merr := stored.MarshalLine()
		require.NoError(t, merr)
		assert.Equal(t, string(line)+"\n", string(after[len(before):]), "%s: exactly one new line", stored.ID)
	}

	all, err := a.ReadContactEvents()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"contact_2026_10_29_a", "contact_2026_10_29_b", "contact_2026_10_29_c",
		"contact_2026_10_29_d", "contact_2026_10_29_e",
	}, contactEventIDs(all))
	voided, ok := all[4].Voids()
	require.True(t, ok)
	assert.Equal(t, target.ID, voided)
}
