package storage

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/identity"
)

// identityWriteAt is a fixed instant for the storage identity-write tests.
func identityWriteAt(minute int) time.Time {
	return time.Date(2026, time.November, 17, 10, minute, 0, 0, time.FixedZone("EST", -5*3600))
}

// identityLogExists reports whether the Ledger has an identity log.
func identityLogExists(t *testing.T, a *Adapter) bool {
	t.Helper()
	_, err := os.Stat(a.identityPath())
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	require.NoError(t, err)
	return true
}

// TestSetPersonStatus_RefusesBadInputBeforeWriting: an unknown status, a
// reason on a restore, and a key that is not a live canonical record are all
// refused before anything is written.
func TestSetPersonStatus_RefusesBadInputBeforeWriting(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	tomb := f.mention("Ghost")
	_, err := f.a.MergePersons(tomb, sam)
	require.NoError(t, err)

	_, err = f.a.SetPersonStatus(sam, "asleep", "", identityWriteAt(1))
	require.Error(t, err)
	_, err = f.a.SetPersonStatus(sam, identity.StatusActive, "why", identityWriteAt(1))
	require.Error(t, err)
	_, err = f.a.SetPersonStatus(tomb, identity.StatusDormant, "", identityWriteAt(1))
	require.ErrorIs(t, err, ErrIdentitySubjectNotCanonical)
	_, err = f.a.SetPersonStatus("person_x-none", identity.StatusDormant, "", identityWriteAt(1))
	require.ErrorIs(t, err, ErrIdentitySubjectNotCanonical)
	_, err = f.a.ClaimForm(tomb, "Sam", false, identityWriteAt(1))
	require.ErrorIs(t, err, ErrIdentitySubjectNotCanonical)
	_, err = f.a.ReleaseForm(tomb, "Sam", identityWriteAt(1))
	require.ErrorIs(t, err, ErrIdentitySubjectNotCanonical)
	assert.False(t, identityLogExists(t, f.a), "a refusal writes nothing")

	// Restoring someone already active is a no-op that creates nothing.
	w, err := f.a.SetPersonStatus(sam, identity.StatusActive, "", identityWriteAt(2))
	require.NoError(t, err)
	assert.True(t, w.NoOp())
	assert.Empty(t, w.Receipt)
	assert.False(t, identityLogExists(t, f.a))
}

// TestClaimForm_ConflictBlankAndNoOp: a claim over an active holder is a
// ClaimConflictError naming the holders; a blank form is refused; claiming
// and releasing are no-ops when nothing would change.
func TestClaimForm_ConflictBlankAndNoOp(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	river := f.mention("Sam Rivera")

	_, err := f.a.ClaimForm(river, "Sam", false, identityWriteAt(1))
	var conflict *ClaimConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "Sam", conflict.Form)
	assert.Equal(t, []string{sam}, conflict.Holders)
	assert.Contains(t, conflict.Error(), sam)
	_, err = f.a.ClaimForm(river, "  !  ", false, identityWriteAt(1))
	require.ErrorIs(t, err, identity.ErrBlankForm)
	_, err = f.a.ReleaseForm(river, "", identityWriteAt(1))
	require.ErrorIs(t, err, identity.ErrBlankForm)
	assert.False(t, identityLogExists(t, f.a))

	w, err := f.a.ReleaseForm(river, "Sam", identityWriteAt(2))
	require.NoError(t, err)
	assert.True(t, w.NoOp(), "releasing an unclaimed form is a no-op")

	cw, err := f.a.ClaimForm(river, "Sam", true, identityWriteAt(3))
	require.NoError(t, err)
	assert.Equal(t, []string{sam}, cw.Displaced)
	require.Len(t, cw.Events, 1)
	assert.Equal(t, cw.Receipt, cw.Events[0].ID)
	cw, err = f.a.ClaimForm(river, "sam.", false, identityWriteAt(4))
	require.NoError(t, err)
	assert.True(t, cw.NoOp(), "a form compares by its normalized form")
}

// TestPinPendingHolds_SettlesOnlyWhatResolves: pinning re-resolves the pending
// held mentions of the given forms under capture rules, pins the ones that
// now name exactly one person, leaves the rest held, and appends nothing when
// nothing settles.
func TestPinPendingHolds_SettlesOnlyWhatResolves(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	river := f.mention("Sam Rivera", "Sam")
	raw := f.nextRaw()
	res, err := f.a.UpdatePerson(PersonMention{DisplayName: "Sam", RawEntryID: raw, At: personAt(5)})
	require.NoError(t, err)
	require.True(t, res.Held)

	// Still ambiguous: nothing settles, nothing is appended.
	before, err := f.a.ReadIdentityEvents()
	require.NoError(t, err)
	w, err := f.a.PinPendingHolds([]string{"Sam", ""}, identity.CauseDormant, identityWriteAt(1))
	require.NoError(t, err)
	assert.True(t, w.NoOp())
	assert.Equal(t, 1, w.StillHeld)
	after, err := f.a.ReadIdentityEvents()
	require.NoError(t, err)
	assert.Len(t, after, len(before))

	// With Sam Rivera dormant, a bare "Sam" is the slug holder's.
	f.setStatus(river, identity.StatusDormant)
	w, err = f.a.PinPendingHolds([]string{"Sam"}, identity.CauseDormant, identityWriteAt(2))
	require.NoError(t, err)
	require.Len(t, w.Pinned, 1)
	assert.Equal(t, PinnedMention{RawID: raw, Form: "Sam", PersonKey: sam}, w.Pinned[0])
	assert.Equal(t, 0, w.StillHeld)
	require.Len(t, w.Events, 1)
	assert.Equal(t, identity.KindPin, w.Events[0].Kind)
	assert.Empty(t, w.Receipt, "pins alone carry no receipt")

	st, err := f.a.IdentityState()
	require.NoError(t, err)
	assert.Empty(t, st.PendingHolds())
	p, ok := st.Pin(raw, "Sam")
	require.True(t, ok)
	assert.Equal(t, identity.CauseDormant, p.Cause)
}

// TestRepointIdentityOnMerge_NoLogWritesNothing: with no identity log, a
// merge appends nothing and never creates the log.
func TestRepointIdentityOnMerge_NoLogWritesNothing(t *testing.T) {
	f := newResolveFixture(t)
	alex := f.mention("Alex")
	andy := f.mention("Andy")
	_, err := f.a.MergePersons(andy, alex)
	require.NoError(t, err)
	m, err := f.a.RepointIdentityOnMerge(andy, alex, identityWriteAt(1))
	require.NoError(t, err)
	assert.Empty(t, m.Events)
	assert.Empty(t, m.Moved)
	assert.Empty(t, m.Released)
	assert.False(t, identityLogExists(t, f.a))

	// A log with nothing about the source appends nothing either.
	f.setStatus(alex, identity.StatusDormant)
	before, err := f.a.ReadIdentityEvents()
	require.NoError(t, err)
	m, err = f.a.RepointIdentityOnMerge(andy, alex, identityWriteAt(2))
	require.NoError(t, err)
	assert.Empty(t, m.Events)
	after, err := f.a.ReadIdentityEvents()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

// TestFormatAt_WallClockFallback: a zero instant falls back to the wall clock;
// any other is rendered RFC3339 in its own offset.
func TestFormatAt_WallClockFallback(t *testing.T) {
	assert.Equal(t, "2026-11-17T10:01:00-05:00", formatAt(identityWriteAt(1)))
	_, err := time.Parse(time.RFC3339, formatAt(time.Time{}))
	require.NoError(t, err)
}

// TestSetPersonStatus_PinsHeldMentionsOfThePersonsForms: a dormant write pins
// the pending held mentions of the person's forms — here a hold that lists
// them as a candidate — in the same append as its status event, and returns
// the status event as its receipt.
func TestSetPersonStatus_PinsHeldMentionsOfThePersonsForms(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	river := f.mention("Sam Rivera", "Sam")
	raw := f.nextRaw()
	res, err := f.a.UpdatePerson(PersonMention{DisplayName: "Sam", RawEntryID: raw, At: personAt(5)})
	require.NoError(t, err)
	require.True(t, res.Held)

	w, err := f.a.SetPersonStatus(river, identity.StatusDormant, "an old contact", identityWriteAt(1))
	require.NoError(t, err)
	require.Len(t, w.Events, 2)
	assert.Equal(t, identity.KindStatus, w.Events[0].Kind)
	assert.Equal(t, "an old contact", w.Events[0].Reason)
	assert.Equal(t, w.Events[0].ID, w.Receipt)
	assert.Equal(t, identity.KindPin, w.Events[1].Kind)
	assert.Equal(t, identity.CauseDormant, w.Events[1].Cause)
	assert.Equal(t, []PinnedMention{{RawID: raw, Form: "Sam", PersonKey: sam}}, w.Pinned)

	forms := statusForms(identity.State{}, PersonRecord{PersonKey: river, DisplayName: "Sam Rivera", Aka: []string{"Sam", ""}})
	assert.Equal(t, map[string]bool{"samrivera": true, "sam": true}, forms)
}

// TestRepointIdentityOnMerge_MovesAndReleasesClaims: a merge clears a dormant
// source's status, moves each source claim to the target, and releases a claim
// on a form another active person holds exactly — all with source merge.
func TestRepointIdentityOnMerge_MovesAndReleasesClaims(t *testing.T) {
	f := newResolveFixture(t)
	alex := f.mention("Alex")
	andy := f.mention("Andy")
	f.mention("Jordan Lee")
	f.claim("Al", andy)
	appendIdentityT(t, f.a, identity.NewClaim(f.at(), identity.SourceCLI, "Jordan Lee", andy, true, []string{f.key("Jordan Lee")}))
	f.setStatus(andy, identity.StatusDormant)
	_, err := f.a.MergePersons(andy, alex)
	require.NoError(t, err)

	m, err := f.a.RepointIdentityOnMerge(andy, alex, identityWriteAt(30))
	require.NoError(t, err)
	assert.Equal(t, []string{"Al"}, m.Moved)
	assert.Equal(t, []string{"Jordan Lee"}, m.Released)
	require.Len(t, m.Events, 4)
	for _, e := range m.Events {
		assert.Equal(t, identity.SourceMerge, e.Source)
	}
	st, err := f.a.IdentityState()
	require.NoError(t, err)
	assert.False(t, st.IsDormant(andy))
	c, ok := st.Claimant("Al")
	require.True(t, ok)
	assert.Equal(t, alex, c.PersonKey)
	_, ok = st.Claimant("Jordan Lee")
	assert.False(t, ok)
	assert.Empty(t, st.Duplicates())
}
