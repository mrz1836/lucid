package storage

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/identity"
)

// Synthetic identity-log fixtures: person_s-oak is an old "Sam", person_s-river
// is "Sam Rivera" — invented keys, never a real person.
const (
	synthIdentityAt  = "2026-11-17T09:14:07-05:00"
	synthIdentityAt2 = "2026-11-18T08:00:00-05:00"
	synthOldSam      = "person_s-oak"
	synthSamRivera   = "person_s-river"
	synthIdentityRaw = "raw_2026_11_18_09_00"
)

// appendIdentityT appends one identity event and fails the test on error.
func appendIdentityT(t *testing.T, a *Adapter, e identity.Event) identity.Event {
	t.Helper()
	stored, err := a.AppendIdentityEvent(e)
	require.NoError(t, err)
	return stored
}

// identityLogBytes reads the identity log's bytes.
func identityLogBytes(t *testing.T, a *Adapter) []byte {
	t.Helper()
	b, err := os.ReadFile(a.identityPath())
	require.NoError(t, err)
	return b
}

// TestIdentityLog_AppendAssignsSlotIDs: ids follow the local date of `at` and
// the first unused per-day slot (`_a`, `_b`, …); the stored line is the event,
// verbatim, one per append; a damaged line that still names its id keeps that
// slot, so it is never reissued; junk lines do not count; a batch is
// validated whole before anything is written; and the log file is created by
// the first write only.
func TestIdentityLog_AppendAssignsSlotIDs(t *testing.T) {
	a := New(t.TempDir())
	assert.NoDirExists(t, a.identityDir(), "nothing exists before the first write")

	first := appendIdentityT(t, a, identity.NewStatus(synthIdentityAt, identity.SourceCLI, synthOldSam, identity.StatusDormant, "an old contact"))
	assert.Equal(t, "identity_2026_11_17_a", first.ID)
	assert.Equal(t, identity.Schema, first.Schema)
	assert.Equal(t, filepath.Join(a.home, "identity", "identity.jsonl"), a.identityPath())
	second := appendIdentityT(t, a, identity.NewClaim(synthIdentityAt, identity.SourceCLI, "Sam", synthSamRivera, false, nil))
	assert.Equal(t, "identity_2026_11_17_b", second.ID)

	body := identityLogBytes(t, a)
	assert.Equal(t, 2, bytes.Count(body, []byte("\n")), "one line per append")
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	wantFirst, err := first.MarshalLine()
	require.NoError(t, err)
	assert.Equal(t, string(wantFirst), lines[0], "the stored line is the event, verbatim")

	// A new local day starts its own slots; the id date is `at`'s own offset
	// date, not UTC's (23:30-05:00 is the 18th in UTC).
	late := appendIdentityT(t, a, identity.NewRelease("2026-11-17T23:30:00-05:00", identity.SourceCLI, "Sam", synthSamRivera))
	assert.Equal(t, "identity_2026_11_17_c", late.ID)
	nextDay := appendIdentityT(t, a, identity.NewRelease(synthIdentityAt2, identity.SourceCLI, "Sam", synthSamRivera))
	assert.Equal(t, "identity_2026_11_18_a", nextDay.ID)

	// A torn line that still names its id keeps that slot: `_d` is never
	// reissued as a second receipt.
	require.NoError(t, appendLineFsync(a.identityPath(), []byte(`{"schema":"identity.v1","id":"identity_2026_11_17_d","ki`)))
	afterTorn := appendIdentityT(t, a, identity.NewClaim(synthIdentityAt, identity.SourceCLI, "Jo", synthSamRivera, false, nil))
	assert.Equal(t, "identity_2026_11_17_e", afterTorn.ID, "a damaged line's slot is never reused")

	// A line that decodes but fails validation still holds its slot.
	require.NoError(t, appendLineFsync(a.identityPath(),
		[]byte(`{"schema":"identity.v1","id":"identity_2026_11_17_f","kind":"wave","at":"`+synthIdentityAt+`","source":"cli"}`)))
	// Lines with no recoverable id do not count either way: the slot follows
	// the ids, never the line count.
	for range 3 {
		require.NoError(t, appendLineFsync(a.identityPath(), []byte(`not json`)))
	}
	afterJunk := appendIdentityT(t, a, identity.NewClaim(synthIdentityAt, identity.SourceCLI, "Jordan", synthSamRivera, false, nil))
	assert.Equal(t, "identity_2026_11_17_g", afterJunk.ID)

	// Any id the caller set is replaced by the minted one.
	preset := identity.NewStatus(synthIdentityAt2, identity.SourceCLI, synthOldSam, identity.StatusActive, "")
	preset.ID = "identity_2026_11_18_q"
	assert.Equal(t, "identity_2026_11_18_b", appendIdentityT(t, a, preset).ID)

	// A batch gets consecutive slots and lands as one write.
	batch, err := a.AppendIdentityEvents([]identity.Event{
		identity.NewRelease(synthIdentityAt2, identity.SourceCLI, "Sam", synthOldSam),
		identity.NewClaim(synthIdentityAt2, identity.SourceCLI, "Sam", synthSamRivera, true, []string{synthOldSam}),
		identity.NewPin(synthIdentityAt2, identity.SourceCLI, synthIdentityRaw, "Sam", synthSamRivera, identity.CauseClaim),
	})
	require.NoError(t, err)
	require.Len(t, batch, 3)
	assert.Equal(t, []string{"identity_2026_11_18_c", "identity_2026_11_18_d", "identity_2026_11_18_e"},
		[]string{batch[0].ID, batch[1].ID, batch[2].ID})

	// One malformed event refuses the whole batch before anything is written.
	before := identityLogBytes(t, a)
	_, err = a.AppendIdentityEvents([]identity.Event{
		identity.NewStatus(synthIdentityAt2, identity.SourceCLI, synthOldSam, identity.StatusDormant, ""),
		identity.NewStatus(synthIdentityAt2, identity.SourceCLI, synthOldSam, identity.StatusDormant, "one\ntwo"),
	})
	require.ErrorIs(t, err, identity.ErrReasonNotOneLine)
	_, err = a.AppendIdentityEvent(identity.NewClaim("", identity.SourceCLI, "Sam", synthSamRivera, false, nil))
	require.Error(t, err, "an event without its at is refused")
	assert.Contains(t, err.Error(), "RFC3339")
	assert.Equal(t, before, identityLogBytes(t, a), "a refused write leaves the log byte-identical")

	none, err := a.AppendIdentityEvents(nil)
	require.NoError(t, err)
	assert.Nil(t, none)
}

// TestIdentityLog_SlotsPastZ: the 27th event of a day is `_aa`, and the fold
// orders `_z` before `_aa` within the same second.
func TestIdentityLog_SlotsPastZ(t *testing.T) {
	a := New(t.TempDir())
	var last identity.Event
	for i := range 28 {
		status := identity.StatusDormant
		if i%2 == 1 {
			status = identity.StatusActive
		}
		last = appendIdentityT(t, a, identity.NewStatus(synthIdentityAt, identity.SourceCLI, synthOldSam, status, ""))
	}
	assert.Equal(t, "identity_2026_11_17_ab", last.ID)

	st, err := a.IdentityState()
	require.NoError(t, err)
	assert.Equal(t, identity.StatusActive, st.Status(synthOldSam), "the 28th event (`_ab`, active) folds last")
}

// TestIdentityLog_MissingFileIsEmpty: with no log every read is the empty
// stream, nothing is reported, and no read creates the file or its directory.
func TestIdentityLog_MissingFileIsEmpty(t *testing.T) {
	a := New(t.TempDir())

	events, err := a.ReadIdentityEvents()
	require.NoError(t, err)
	assert.Empty(t, events)
	require.NoError(t, a.IdentityLogErr())
	ids, err := a.ListIdentityEventIDs()
	require.NoError(t, err)
	assert.Empty(t, ids)
	require.NoError(t, a.ReadIdentityEventErr("identity_2026_11_17_a"))

	assert.NoDirExists(t, a.identityDir(), "reads never create the log")
}

// TestIdentityLog_ReadIdentityLogTellsAbsentFromPresent: ReadIdentityLog
// reads like ReadIdentityEvents once the log exists, but reports a missing log
// as fs.ErrNotExist — without creating it — so validate can skip the identity
// check on a Ledger that never wrote an identity fact.
func TestIdentityLog_ReadIdentityLogTellsAbsentFromPresent(t *testing.T) {
	a := New(t.TempDir())

	events, err := a.ReadIdentityLog()
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Empty(t, events)
	assert.NoDirExists(t, a.identityDir(), "the read never creates the log")

	stored := appendIdentityT(t, a, identity.NewStatus(synthIdentityAt, identity.SourceCLI, synthOldSam, identity.StatusDormant, ""))
	events, err = a.ReadIdentityLog()
	require.NoError(t, err)
	assert.Equal(t, []identity.Event{stored}, events)
}

// TestIdentityLog_ReadSkipsAndReportsMalformed: reads return only well-formed
// events in append order; a malformed line — torn, id-less, or invalid — is
// skipped and named by IdentityLogErr and the locator listing.
func TestIdentityLog_ReadSkipsAndReportsMalformed(t *testing.T) {
	a := New(t.TempDir())
	good := appendIdentityT(t, a, identity.NewStatus(synthIdentityAt, identity.SourceCLI, synthOldSam, identity.StatusDormant, ""))
	require.NoError(t, a.IdentityLogErr(), "a clean log reports nothing")

	require.NoError(t, appendLineFsync(a.identityPath(), []byte(`{"schema":"identity.v1","id":"identity_2026_11_17_b","ki`)))
	require.NoError(t, appendLineFsync(a.identityPath(),
		[]byte(`{"schema":"identity.v1","id":"","kind":"status","at":"`+synthIdentityAt+`","source":"cli","person_key":"person_s-oak","status":"active"}`)))
	require.NoError(t, appendLineFsync(a.identityPath(),
		[]byte(`{"schema":"identity.v1","id":"identity_2026_11_17_c","kind":"status","at":"`+synthIdentityAt+`","source":"cli","person_key":"person_s-oak","status":"asleep"}`)))
	good2 := appendIdentityT(t, a, identity.NewClaim(synthIdentityAt, identity.SourceCLI, "Sam", synthSamRivera, false, nil))
	assert.Equal(t, "identity_2026_11_17_d", good2.ID)

	events, err := a.ReadIdentityEvents()
	require.NoError(t, err)
	assert.Equal(t, []identity.Event{good, good2}, events)

	logErr := a.IdentityLogErr()
	require.Error(t, logErr)
	assert.Contains(t, logErr.Error(), "3 malformed line(s)")
	assert.Contains(t, logErr.Error(), "line-2:")
	assert.Contains(t, logErr.Error(), "line-3:")
	assert.Contains(t, logErr.Error(), "identity_2026_11_17_c:")

	ids, err := a.ListIdentityEventIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"identity_2026_11_17_a", "line-2", "line-3", "identity_2026_11_17_c", "identity_2026_11_17_d"}, ids)
	require.NoError(t, a.ReadIdentityEventErr("identity_2026_11_17_a"))
	require.Error(t, a.ReadIdentityEventErr("line-2"))
	require.ErrorContains(t, a.ReadIdentityEventErr("line-3"), "no id")
	require.Error(t, a.ReadIdentityEventErr("identity_2026_11_17_c"))
	require.NoError(t, a.ReadIdentityEventErr("line-99"), "an unknown locator is clean")
}

// TestIdentityLog_ReadErrorsSurface: an unreadable log path surfaces as an
// error on every read and on append.
func TestIdentityLog_ReadErrorsSurface(t *testing.T) {
	a := New(t.TempDir())
	// A directory where the log file should be cannot be read as a file.
	require.NoError(t, os.MkdirAll(a.identityPath(), dirPerm))

	_, err := a.ReadIdentityEvents()
	require.Error(t, err)
	_, err = a.IdentityState()
	require.Error(t, err)
	require.Error(t, a.IdentityLogErr())
	_, err = a.ListIdentityEventIDs()
	require.Error(t, err)
	require.Error(t, a.ReadIdentityEventErr("identity_2026_11_17_a"))
	_, err = a.AppendIdentityEvent(identity.NewStatus(synthIdentityAt, identity.SourceCLI, synthOldSam, identity.StatusDormant, ""))
	require.Error(t, err)
}

// TestIdentityState_NoLogReadsAllActive: a Ledger with people but no identity
// log reads as everyone active, with no claims, holds, pins, or move-refs, and
// the read creates nothing.
func TestIdentityState_NoLogReadsAllActive(t *testing.T) {
	a := newPeopleAdapter(t)
	for _, name := range []string{"Sam", "Sam Rivera"} {
		_, err := a.UpdatePerson(PersonMention{DisplayName: name, RawEntryID: "raw_2026_11_10_09_00", At: personAt(10)})
		require.NoError(t, err)
	}
	keys, err := a.ListPeopleKeys()
	require.NoError(t, err)
	require.Len(t, keys, 2)

	st, err := a.IdentityState()
	require.NoError(t, err)
	for _, k := range keys {
		assert.Equal(t, identity.StatusActive, st.Status(k), "%s reads as active", k)
		assert.Empty(t, st.ClaimsBy(k))
	}
	_, claimed := st.Claimant("Sam")
	assert.False(t, claimed)
	assert.Empty(t, st.Claims())
	assert.Empty(t, st.PendingHolds())
	assert.Empty(t, st.TouchedRawIDs())
	assert.Empty(t, st.Duplicates())

	assert.NoDirExists(t, a.identityDir(), "reading the state creates nothing")
}

// TestIdentityState_CanonicalizesThroughRedirects: the stored state folds
// claims, pins, move-refs, and hold candidates through the people tree's
// merge redirects, while status stays on the exact key.
func TestIdentityState_CanonicalizesThroughRedirects(t *testing.T) {
	a := newPeopleAdapter(t)
	writePersonTombstone(t, a, "person_s-moss", "Sammy", synthSamRivera, personAt(3))

	appendIdentityT(t, a, identity.NewClaim(synthIdentityAt, identity.SourceCLI, "Sammy", "person_s-moss", false, nil))
	appendIdentityT(t, a, identity.NewStatus(synthIdentityAt, identity.SourceCLI, "person_s-moss", identity.StatusDormant, ""))
	appendIdentityT(t, a, identity.NewHold(synthIdentityAt2, identity.SourceStructure, synthIdentityRaw, "Sam", identity.HoldExact,
		[]string{synthOldSam, "person_s-moss"}))
	appendIdentityT(t, a, identity.NewMoveRef(synthIdentityAt2, identity.SourceCLI, "raw_2026_11_18_21_40", synthOldSam, "person_s-moss"))
	appendIdentityT(t, a, identity.NewPin(synthIdentityAt2, identity.SourceCLI, "raw_2026_11_18_22_00", "Sam", "person_s-moss", identity.CauseClaim))

	st, err := a.IdentityState()
	require.NoError(t, err)
	c, ok := st.Claimant("sammy")
	require.True(t, ok)
	assert.Equal(t, synthSamRivera, c.PersonKey)
	assert.True(t, st.IsDormant("person_s-moss"))
	assert.False(t, st.IsDormant(synthSamRivera), "status is never read through a redirect")
	holds := st.PendingHolds()
	require.Len(t, holds, 1)
	assert.Equal(t, []string{synthOldSam, synthSamRivera}, holds[0].Candidates)
	assert.Equal(t, []identity.Move{{From: synthOldSam, To: synthSamRivera, EventID: "identity_2026_11_18_b"}},
		st.MovesFor("raw_2026_11_18_21_40"))
	pin, ok := st.Pin("raw_2026_11_18_22_00", "SAM")
	require.True(t, ok)
	assert.Equal(t, synthSamRivera, pin.PersonKey)
}

// TestIdentityState_PeopleReadErrorSurfaces: a log that needs canonicalizing
// surfaces an unreadable people tree as an error.
func TestIdentityState_PeopleReadErrorSurfaces(t *testing.T) {
	a := newPeopleAdapter(t)
	appendIdentityT(t, a, identity.NewStatus(synthIdentityAt, identity.SourceCLI, synthOldSam, identity.StatusDormant, ""))

	require.NoError(t, os.MkdirAll(a.peopleDir(), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(a.peopleDir(), synthOldSam+personExt), []byte("{nope"), filePerm))
	_, err := a.IdentityState()
	require.Error(t, err, "a damaged people record is not silently skipped")

	b := newPeopleAdapter(t)
	appendIdentityT(t, b, identity.NewStatus(synthIdentityAt, identity.SourceCLI, synthOldSam, identity.StatusDormant, ""))
	require.NoError(t, os.WriteFile(b.peopleDir(), []byte("not a dir"), filePerm))
	_, err = b.IdentityState()
	require.Error(t, err, "an unreadable people tree surfaces")
}
