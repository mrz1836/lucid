package validate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/identity"
)

// Synthetic identity fixtures: person_s-oak is an old "Sam", person_s-river is
// "Sam Rivera", person_z-ghost has no record — invented keys, never a real
// person.
const (
	identityOldSam = "person_s-oak"
	identityRiver  = "person_s-river"
	identityGhost  = "person_z-ghost"
)

// identityEvents stamps each event with the next id and a recorded instant one
// minute after the last, so the fold applies them in the order given.
func identityEvents(events ...identity.Event) []identity.Event {
	base := time.Date(2026, time.November, 17, 9, 0, 0, 0, time.FixedZone("EST", -5*3600))
	out := make([]identity.Event, len(events))
	for i, e := range events {
		e.At = base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		e.ID = identity.ID("2026-11-17", i)
		out[i] = e
	}
	return out
}

// identityLedger is a fake Ledger holding an old Sam and Sam Rivera — merged
// into the old Sam when merged is set — with the given identity log.
func identityLedger(merged bool, events []identity.Event) *fakeLedger {
	f := &fakeLedger{
		people:          []string{identityOldSam, identityRiver},
		identity:        events,
		identityPresent: true,
	}
	if merged {
		f.redirects = map[string]string{identityRiver: identityOldSam}
	}
	return f
}

// checkIdentity runs the check, requiring it to apply.
func checkIdentity(t *testing.T, src LedgerSource) []Finding {
	t.Helper()
	found, ran, err := CheckPeopleIdentityLog(src)
	require.NoError(t, err)
	require.True(t, ran, "the check runs when the identity log exists")
	return found
}

// TestCheckPeopleIdentity_DanglingClaim flags an effective claim naming a
// person_key with no record; a claim on a recorded person is clean, and a
// released dangling claim is no longer effective.
func TestCheckPeopleIdentity_DanglingClaim(t *testing.T) {
	events := identityEvents(
		identity.NewClaim("", identity.SourceCLI, "Sam", identityOldSam, false, nil),
		identity.NewClaim("", identity.SourceCLI, "Rivera", identityGhost, false, nil),
	)
	found := checkIdentity(t, identityLedger(false, events))
	require.Len(t, found, 1)
	assert.Equal(t, Finding{
		Check:    CheckPeopleIdentity,
		Severity: SeverityError,
		Path:     "identity/identity.jsonl#" + events[1].ID,
		Rule:     ruleIdentityClaimDangling,
		Message:  `the claim on "Rivera" names person "person_z-ghost", which has no record`,
	}, found[0])

	released := identityEvents(
		identity.NewClaim("", identity.SourceCLI, "Rivera", identityGhost, false, nil),
		identity.NewRelease("", identity.SourceCLI, "Rivera", identityGhost),
	)
	assert.Empty(t, checkIdentity(t, identityLedger(false, released)))
}

// TestCheckPeopleIdentity_ClaimOnTombstone flags an effective claim still
// recorded on a redirect tombstone; the re-point a merge writes (release the
// source's claim, claim the form for the target) is clean.
func TestCheckPeopleIdentity_ClaimOnTombstone(t *testing.T) {
	stale := identityEvents(identity.NewClaim("", identity.SourceCLI, "Rivera", identityRiver, false, nil))
	found := checkIdentity(t, identityLedger(true, stale))
	require.Len(t, found, 1)
	assert.Equal(t, ruleIdentityClaimTombstone, found[0].Rule)
	assert.Equal(t, "identity/identity.jsonl#"+stale[0].ID, found[0].Path)
	assert.Contains(t, found[0].Message, `a redirect tombstone (merged into "person_s-oak")`)

	repointed := identityEvents(
		identity.NewClaim("", identity.SourceCLI, "Rivera", identityRiver, false, nil),
		identity.NewRelease("", identity.SourceMerge, "Rivera", identityRiver),
		identity.NewClaim("", identity.SourceMerge, "Rivera", identityOldSam, false, nil),
	)
	assert.Empty(t, checkIdentity(t, identityLedger(true, repointed)))
}

// TestCheckPeopleIdentity_DuplicateActiveClaims flags a claim written while a
// different person claimed the same normalized form — "sam" and "Sam" are one
// form. Re-claiming one's own form is not a duplicate.
func TestCheckPeopleIdentity_DuplicateActiveClaims(t *testing.T) {
	events := identityEvents(
		identity.NewClaim("", identity.SourceCLI, "Sam", identityOldSam, false, nil),
		identity.NewClaim("", identity.SourceCLI, "Sam", identityOldSam, false, nil),
		identity.NewClaim("", identity.SourceCLI, "sam", identityRiver, false, nil),
	)
	found := checkIdentity(t, identityLedger(false, events))
	require.Len(t, found, 1)
	assert.Equal(t, Finding{
		Check:    CheckPeopleIdentity,
		Severity: SeverityError,
		Path:     "identity/identity.jsonl#" + events[2].ID,
		Rule:     ruleIdentityClaimDuplicate,
		Message:  `the claim on "sam" for person "person_s-river" was written while another person claimed the form, so it is inert`,
	}, found[0])
}

// TestCheckPeopleIdentity_StatusOnTombstone flags a tombstone whose folded
// status is dormant; the clearing status a merge writes, and a dormant
// canonical person, are clean.
func TestCheckPeopleIdentity_StatusOnTombstone(t *testing.T) {
	events := identityEvents(
		identity.NewStatus("", identity.SourceCLI, identityRiver, identity.StatusDormant, ""),
		identity.NewStatus("", identity.SourceCLI, identityOldSam, identity.StatusDormant, ""),
	)
	found := checkIdentity(t, identityLedger(true, events))
	require.Len(t, found, 1)
	assert.Equal(t, Finding{
		Check:    CheckPeopleIdentity,
		Severity: SeverityError,
		Path:     "identity/identity.jsonl#" + events[0].ID,
		Rule:     ruleIdentityStatusTombstone,
		Message:  `person "person_s-river" is a redirect tombstone (merged into "person_s-oak") but its status is dormant`,
	}, found[0])

	cleared := identityEvents(
		identity.NewStatus("", identity.SourceCLI, identityRiver, identity.StatusDormant, ""),
		identity.NewStatus("", identity.SourceMerge, identityRiver, identity.StatusActive, ""),
	)
	assert.Empty(t, checkIdentity(t, identityLedger(true, cleared)))
}

// TestCheckPeopleIdentity_RunsOnlyWithTheLog: a Ledger without the identity log
// runs no identity check — no finding and no ran entry, so it validates exactly
// as before — while a present log (even an empty one) adds people-identity to
// the ran list after people-redirects, and its findings fail the sweep.
func TestCheckPeopleIdentity_RunsOnlyWithTheLog(t *testing.T) {
	found, ran, err := CheckPeopleIdentityLog(&fakeLedger{people: []string{identityOldSam}})
	require.NoError(t, err)
	assert.False(t, ran)
	assert.Empty(t, found)

	rep, err := Run(Options{Ledger: &fakeLedger{people: []string{identityOldSam}}})
	require.NoError(t, err)
	assert.Equal(t, []string{CheckSchema, CheckPeopleRedirects, CheckAnchors}, rep.Ran)

	rep, err = Run(Options{Ledger: identityLedger(false, nil)})
	require.NoError(t, err)
	assert.Equal(t, []string{CheckSchema, CheckPeopleRedirects, CheckPeopleIdentity, CheckAnchors}, rep.Ran)
	assert.True(t, rep.OK())

	dangling := identityEvents(identity.NewClaim("", identity.SourceCLI, "Rivera", identityGhost, false, nil))
	rep, err = Run(Options{Ledger: identityLedger(false, dangling)})
	require.NoError(t, err)
	assert.False(t, rep.OK())
	assert.Equal(t, 1, rep.Errors())
}

// TestCheckPeopleIdentity_ErrorsAndUnreadable: a log or people-listing read
// failure is an error, not a finding; an unreadable person record is the
// schema check's finding, read here as canonical; and a malformed identity
// line is a schema finding under identity/.
func TestCheckPeopleIdentity_ErrorsAndUnreadable(t *testing.T) {
	_, _, err := CheckPeopleIdentityLog(&fakeLedger{identityErr: errSentinel})
	require.ErrorIs(t, err, errSentinel)
	_, _, err = CheckPeopleIdentityLog(&fakeLedger{identityPresent: true, peopleListErr: errSentinel})
	require.ErrorIs(t, err, errSentinel)
	_, err = Run(Options{Ledger: &fakeLedger{identityErr: errSentinel}})
	require.ErrorIs(t, err, errSentinel)

	unreadable := identityLedger(true, identityEvents(
		identity.NewClaim("", identity.SourceCLI, "Rivera", identityRiver, false, nil),
	))
	unreadable.badPerson = map[string]bool{identityRiver: true}
	assert.Empty(t, checkIdentity(t, unreadable), "an unreadable record is not double-reported")

	schema, err := CheckLedgerSchema(&fakeLedger{
		identityIDs: []string{"identity_2026_11_17_a", "line-2"},
		badIdentity: map[string]bool{"line-2": true},
	})
	require.NoError(t, err)
	require.Len(t, schema, 1)
	assert.Equal(t, "identity/line-2", schema[0].Path)
	assert.Equal(t, "identity", schema[0].Rule)
}
