package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Synthetic keys beyond the shared pair: person_s-moss is a tombstone merged
// into person_s-river, and person_j-lee is a neutral third person.
const (
	mergedSam = "person_s-moss"
	jordan    = "person_j-lee"
)

// redirects canonicalizes the one synthetic tombstone onto its target.
func redirects(k string) string {
	if k == mergedSam {
		return samRiver
	}
	return k
}

// stamped gives an event its at and the id for at's local day, so tests can
// pin fold order.
func stamped(e Event, at string, slot int) Event {
	e.At = at
	e.ID = ID(localDate(at), slot)
	return e
}

// TestIdentityFold_StatusLatestWins: the latest status event for the exact key
// wins whatever order events arrive in; a same-second tie is broken by slot
// (`_z` before `_aa`); no event reads as active; status is never read through a
// redirect; the date is the host-local day of `at`; and an invalid event is
// skipped.
func TestIdentityFold_StatusLatestWins(t *testing.T) {
	empty := Fold(nil, nil)
	assert.Equal(t, StatusActive, empty.Status(oldSam), "no event means active")
	assert.False(t, empty.IsDormant(oldSam))
	_, ok := empty.StatusEvent(oldSam)
	assert.False(t, ok)
	assert.Empty(t, empty.StatusKeys())
	assert.Empty(t, empty.Claims())
	assert.Empty(t, empty.PendingHolds())
	assert.Empty(t, empty.Duplicates())
	assert.Empty(t, empty.TouchedRawIDs())

	const sameSecond = "2026-11-17T09:00:00-05:00"
	events := []Event{
		// Passed newest-first: order comes from (at, id), not input order.
		stamped(NewStatus("", SourceCLI, oldSam, StatusDormant, "an old contact"), "2026-11-17T23:30:00-05:00", 40),
		stamped(NewStatus("", SourceCLI, oldSam, StatusActive, ""), sameSecond, 26),            // _aa
		stamped(NewStatus("", SourceCLI, oldSam, StatusDormant, "first time"), sameSecond, 25), // _z
		// The same instant written in another offset still orders by instant.
		stamped(NewStatus("", SourceCLI, jordan, StatusDormant, ""), "2026-11-17T15:00:00+01:00", 1),
		stamped(NewStatus("", SourceCLI, jordan, StatusActive, ""), "2026-11-17T08:30:00-05:00", 2),
		// A tombstone's own status is never forwarded to its target.
		stamped(NewStatus("", SourceCLI, mergedSam, StatusDormant, ""), sameSecond, 3),
		// Invalid: skipped by the fold.
		stamped(NewStatus("", SourceCLI, samRiver, "asleep", ""), "2026-11-18T09:00:00-05:00", 0),
	}
	st := Fold(events, redirects)

	assert.Equal(t, StatusDormant, st.Status(oldSam), "the latest event wins")
	fact, ok := st.StatusEvent(oldSam)
	require.True(t, ok)
	assert.Equal(t, StatusFact{
		Status:  StatusDormant,
		Date:    "2026-11-17", // the host-local day, though the instant is the 18th in UTC
		Reason:  "an old contact",
		EventID: ID("2026-11-17", 40),
	}, fact)

	tie := Fold(events[1:3], nil)
	assert.Equal(t, StatusActive, tie.Status(oldSam), "_aa folds after _z in the same second")

	// 15:00+01:00 is 09:00-05:00, so it is later than 08:30-05:00.
	assert.True(t, st.IsDormant(jordan), "instants compare across offsets")

	assert.Equal(t, StatusDormant, st.Status(mergedSam))
	assert.Equal(t, StatusActive, st.Status(samRiver), "status is keyed on the exact key, never redirected; the invalid event is skipped")
	assert.Equal(t, []string{jordan, mergedSam, oldSam}, st.StatusKeys())
}

// TestIdentityFold_ClaimsReleaseAndDuplicate: a form has at most one claimant,
// compared by normalized form; a claim on a form someone else claims is an
// inert duplicate that never takes effect, even after release; re-claiming and
// releasing someone else's claim change nothing; claims canonicalize through
// redirects; and a dormant claimant's claim is inert until restore.
func TestIdentityFold_ClaimsReleaseAndDuplicate(t *testing.T) {
	at := func(m int) string { return "2026-11-17T10:" + twoDigit(m) + ":00-05:00" }
	events := []Event{
		stamped(NewClaim("", SourceCLI, "Sam", samRiver, false, nil), at(1), 0),
		stamped(NewClaim("", SourceCLI, "sam.", samRiver, false, nil), at(2), 1), // same person: no-op
		stamped(NewClaim("", SourceCLI, "SAM", oldSam, false, nil), at(3), 2),    // duplicate
		stamped(NewRelease("", SourceCLI, "Sam", oldSam), at(4), 3),              // not the claimant: no-op
		stamped(NewClaim("", SourceCLI, "Jo", mergedSam, false, nil), at(5), 4),  // canonicalizes
		stamped(NewClaim("", SourceCLI, "Jordan Lee", jordan, false, nil), at(6), 5),
	}
	st := Fold(events, redirects)

	c, ok := st.Claimant("s.a.m")
	require.True(t, ok)
	assert.Equal(t, Claim{Form: "Sam", PersonKey: samRiver, EventID: ID("2026-11-17", 0)}, c)
	dups := st.Duplicates()
	require.Len(t, dups, 1)
	assert.Equal(t, oldSam, dups[0].PersonKey)
	assert.Equal(t, ID("2026-11-17", 2), dups[0].ID)

	jo, ok := st.Claimant("Jo")
	require.True(t, ok)
	assert.Equal(t, samRiver, jo.PersonKey, "a claim by a merged-away key belongs to the canonical record")
	assert.Equal(t, []Claim{
		{Form: "Jo", PersonKey: samRiver, EventID: ID("2026-11-17", 4)},
		{Form: "Sam", PersonKey: samRiver, EventID: ID("2026-11-17", 0)},
	}, st.ClaimsBy(samRiver))
	assert.Empty(t, st.ClaimsBy(oldSam))
	assert.Len(t, st.Claims(), 3)

	// The claimant releases; the inert duplicate never takes effect.
	released := Fold(append(events,
		stamped(NewRelease("", SourceCLI, "SAM", samRiver), at(7), 6),
	), redirects)
	_, ok = released.Claimant("Sam")
	assert.False(t, ok, "the duplicate never takes effect, even after the standing claim is released")

	// A release through a merged-away key reaches the canonical claimant, and a
	// fresh claim afterwards takes effect.
	reclaimed := Fold(append(events,
		stamped(NewRelease("", SourceCLI, "Jo", mergedSam), at(7), 6),
		stamped(NewClaim("", SourceCLI, "Jo", jordan, false, nil), at(8), 7),
	), redirects)
	jo, ok = reclaimed.Claimant("jo")
	require.True(t, ok)
	assert.Equal(t, jordan, jo.PersonKey)

	// A dormant claimant's claim stays recorded but is inert; restore revives it.
	dormant := Fold(append(events,
		stamped(NewStatus("", SourceCLI, samRiver, StatusDormant, ""), at(9), 8),
	), redirects)
	_, ok = dormant.Claimant("Sam")
	assert.True(t, ok, "the claim stays recorded")
	_, ok = dormant.ActiveClaimant("Sam")
	assert.False(t, ok, "a dormant claimant's claim is inert")
	restored := Fold(append(events,
		stamped(NewStatus("", SourceCLI, samRiver, StatusDormant, ""), at(9), 8),
		stamped(NewStatus("", SourceCLI, samRiver, StatusActive, ""), at(10), 9),
	), redirects)
	active, ok := restored.ActiveClaimant("Sam")
	require.True(t, ok, "restore revives the claim")
	assert.Equal(t, samRiver, active.PersonKey)
	_, ok = restored.ActiveClaimant("Riley")
	assert.False(t, ok)
}

// TestIdentityFold_MoveRefChainAndUndo: a raw entry's move-refs come back in
// fold order, canonicalized, so a chain applies step by step and a reverse
// move-ref undoes one; other raws are untouched.
func TestIdentityFold_MoveRefChainAndUndo(t *testing.T) {
	const raw2 = "raw_2026_11_18_21_40"
	events := []Event{
		stamped(NewMoveRef("", SourceCLI, synthRaw, samRiver, oldSam), "2026-11-19T08:31:00-05:00", 1), // undo
		stamped(NewMoveRef("", SourceCLI, synthRaw, oldSam, mergedSam), "2026-11-19T08:30:00-05:00", 0),
		stamped(NewMoveRef("", SourceCLI, synthRaw, oldSam, jordan), "2026-11-19T08:32:00-05:00", 2),
		stamped(NewMoveRef("", SourceCLI, raw2, jordan, oldSam), "2026-11-19T08:33:00-05:00", 3),
		stamped(NewPin("", SourceCLI, "raw_2026_11_17_07_00", "Sam", samRiver, CauseClaim), "2026-11-19T08:34:00-05:00", 4),
	}
	st := Fold(events, redirects)

	moves := st.MovesFor(synthRaw)
	assert.Equal(t, []Move{
		{From: oldSam, To: samRiver, EventID: ID("2026-11-19", 0)}, // mergedSam canonicalizes
		{From: samRiver, To: oldSam, EventID: ID("2026-11-19", 1)}, // the reverse move-ref
		{From: oldSam, To: jordan, EventID: ID("2026-11-19", 2)},
	}, moves)

	// Applying the chain in order: the mention ends under jordan.
	who := oldSam
	for _, m := range moves {
		if who == m.From {
			who = m.To
		}
	}
	assert.Equal(t, jordan, who)

	assert.Equal(t, []Move{{From: jordan, To: oldSam, EventID: ID("2026-11-19", 3)}}, st.MovesFor(raw2))
	assert.Empty(t, st.MovesFor("raw_2026_01_01_00_00"))
	assert.Equal(t, []string{"raw_2026_11_17_07_00", synthRaw, raw2}, st.TouchedRawIDs())

	// The returned slice is a copy.
	moves[0].To = "person_x-tamper"
	assert.Equal(t, samRiver, st.MovesFor(synthRaw)[0].To)
}

// TestIdentityFold_HoldsPendingUntilPinned: a hold stays pending until a pin
// settles its (raw_id, normalized form); the first pin wins and a later one is
// ignored; a repeated hold is one pending mention; candidates canonicalize; and
// pending holds sort by (raw_id, form).
func TestIdentityFold_HoldsPendingUntilPinned(t *testing.T) {
	const (
		rawA = "raw_2026_11_18_09_00"
		rawB = "raw_2026_11_18_12_30"
	)
	at := func(m int) string { return "2026-11-18T11:" + twoDigit(m) + ":00-05:00" }
	holds := []Event{
		stamped(NewHold("", SourceStructure, rawB, "Sam", HoldExact, []string{oldSam, samRiver}), at(1), 0),
		stamped(NewHold("", SourceStructure, rawA, "Sam", HoldFirstName, []string{jordan, mergedSam}), at(2), 1),
		stamped(NewHold("", SourceStructure, rawA, "Jo", HoldExact, []string{jordan, mergedSam, samRiver}), at(3), 2),
		stamped(NewHold("", SourceStructure, rawA, "sam", HoldExact, []string{oldSam}), at(4), 3), // repeat
	}
	st := Fold(holds, redirects)
	assert.Equal(t, []Hold{
		{RawID: rawA, Form: "Jo", Reason: HoldExact, Candidates: []string{jordan, samRiver}, EventID: ID("2026-11-18", 2)},
		{RawID: rawA, Form: "Sam", Reason: HoldFirstName, Candidates: []string{jordan, samRiver}, EventID: ID("2026-11-18", 1)},
		{RawID: rawB, Form: "Sam", Reason: HoldExact, Candidates: []string{oldSam, samRiver}, EventID: ID("2026-11-18", 0)},
	}, st.PendingHolds(), "sorted by raw, then form; the repeat hold is one mention; candidates canonicalized")

	pinned := Fold(append(holds,
		stamped(NewPin("", SourceCLI, rawA, "SAM.", mergedSam, CauseClaim), at(5), 4),
		stamped(NewPin("", SourceCLI, rawA, "Sam", oldSam, CauseDormant), at(6), 5), // later: ignored
	), redirects)
	assert.Equal(t, []string{"Jo"}, holdForms(pinned.PendingHolds(), rawA), "the pin settles its (raw, form) only")
	assert.Equal(t, []string{"Sam"}, holdForms(pinned.PendingHolds(), rawB), "another raw's hold of the same form stays pending")

	pin, ok := pinned.Pin(rawA, "sam")
	require.True(t, ok)
	assert.Equal(t, Pin{RawID: rawA, Form: "SAM.", PersonKey: samRiver, Cause: CauseClaim, EventID: ID("2026-11-18", 4)}, pin,
		"the first pin wins and canonicalizes")
	_, ok = pinned.Pin(rawB, "Sam")
	assert.False(t, ok)
	assert.Equal(t, []Pin{pin}, pinned.PinsFor(rawA))
	assert.Empty(t, pinned.PinsFor(rawB))

	// A move-ref without --from settles a hold through its pin.
	moved := Fold(append(holds,
		stamped(NewPin("", SourceCLI, rawB, "Sam", oldSam, CauseMoveRef), at(7), 6),
	), redirects)
	assert.Empty(t, holdForms(moved.PendingHolds(), rawB))

	// PendingHolds returns copies.
	pending := st.PendingHolds()
	pending[0].Candidates[0] = "person_x-tamper"
	assert.Equal(t, jordan, st.PendingHolds()[0].Candidates[0])
}

// holdForms lists the forms of the pending holds on one raw entry.
func holdForms(holds []Hold, rawID string) []string {
	var out []string
	for _, h := range holds {
		if h.RawID == rawID {
			out = append(out, h.Form)
		}
	}
	return out
}

// twoDigit renders a minute as two digits.
func twoDigit(n int) string {
	return string([]byte{byte('0' + n/10), byte('0' + n%10)})
}
