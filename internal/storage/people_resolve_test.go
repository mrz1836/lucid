package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/data"
	"github.com/mrz1836/lucid/internal/identity"
)

// The resolver fixtures use synthetic people only: two people named "Sam"
// ("Sam" and "Sam Rivera"), plus "Alex", "Alex Kim", "Alex Stone", "Andy", and
// "Jordan Lee".

// resolveFixture is a temp Ledger the resolver tests seed through the real
// People routine and the identity log, so every key is derived, never
// hand-picked.
type resolveFixture struct {
	t    *testing.T
	a    *Adapter
	keys map[string]string // display name → person_key
	raw  int
	tick int
}

// newResolveFixture returns an empty Ledger.
func newResolveFixture(t *testing.T) *resolveFixture {
	t.Helper()
	return &resolveFixture{t: t, a: New(t.TempDir()), keys: map[string]string{}}
}

// nextRaw returns a fresh synthetic raw id.
func (f *resolveFixture) nextRaw() string {
	f.raw++
	return fmt.Sprintf("raw_2026_11_%02d_09_00", f.raw)
}

// at returns the next synthetic identity-event instant, so events fold in the
// order the test writes them.
func (f *resolveFixture) at() string {
	f.tick++
	return time.Date(2026, time.November, 17, 9, f.tick, 0, 0, time.FixedZone("EST", -5*3600)).Format(time.RFC3339)
}

// mention records one mention through the People routine and remembers the
// key it resolved to under the display name.
func (f *resolveFixture) mention(display string, aka ...string) string {
	f.t.Helper()
	res, err := f.a.UpdatePerson(PersonMention{
		DisplayName: display, Aka: aka, RawEntryID: f.nextRaw(), At: personAt(1 + f.raw%27),
	})
	require.NoError(f.t, err)
	require.False(f.t, res.Held, "seeding %q must not hold", display)
	f.keys[display] = res.PersonKey
	return res.PersonKey
}

// create deliberately mints a person, returning the record and whether it was
// created.
func (f *resolveFixture) create(display string) (PersonRecord, bool) {
	f.t.Helper()
	rec, created, err := f.a.CreatePerson(display, personAt(20), PersonPatch{})
	require.NoError(f.t, err)
	return rec, created
}

// key returns the remembered key for a display name.
func (f *resolveFixture) key(display string) string {
	f.t.Helper()
	k, ok := f.keys[display]
	require.True(f.t, ok, "no seeded person %q", display)
	return k
}

// setStatus appends a status event for the person.
func (f *resolveFixture) setStatus(key, status string) {
	f.t.Helper()
	appendIdentityT(f.t, f.a, identity.NewStatus(f.at(), identity.SourceCLI, key, status, ""))
}

// dormant marks the named people dormant.
func (f *resolveFixture) dormant(names ...string) {
	f.t.Helper()
	for _, n := range names {
		f.setStatus(f.key(n), identity.StatusDormant)
	}
}

// claim appends a claim of form for key.
func (f *resolveFixture) claim(form, key string) {
	f.t.Helper()
	appendIdentityT(f.t, f.a, identity.NewClaim(f.at(), identity.SourceCLI, form, key, false, nil))
}

// lookup resolves form in lookup mode.
func (f *resolveFixture) lookup(form string) NameResolution {
	f.t.Helper()
	res, err := f.a.ResolvePersonName(form, ResolveOptions{Mode: ResolveLookup})
	require.NoError(f.t, err)
	return res
}

// capture resolves form in capture mode for a fresh raw entry.
func (f *resolveFixture) capture(form string) NameResolution {
	f.t.Helper()
	res, err := f.a.ResolvePersonName(form, ResolveOptions{Mode: ResolveCapture, RawID: "raw_2026_12_01_09_00"})
	require.NoError(f.t, err)
	return res
}

// keysOf maps display names to their keys.
func (f *resolveFixture) keysOf(names ...string) []string {
	f.t.Helper()
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, f.key(n))
	}
	return out
}

// peopleBytes snapshots every people/ file.
func (f *resolveFixture) peopleBytes() map[string]string {
	f.t.Helper()
	return snapshotPeople(f.t, f.a)
}

// identityEvents reads the identity log.
func (f *resolveFixture) identityEvents() []identity.Event {
	f.t.Helper()
	events, err := f.a.ReadIdentityEvents()
	require.NoError(f.t, err)
	return events
}

// eventsOfKind filters the identity log by kind.
func (f *resolveFixture) eventsOfKind(kind string) []identity.Event {
	f.t.Helper()
	var out []identity.Event
	for _, e := range f.identityEvents() {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// secondSlot is key's first collision-suffix slot, -2.
func secondSlot(key string) string { return key + "-2" }

// dormantHolderExpect is one mode's expected answer in the dormant-holder
// table.
type dormantHolderExpect struct {
	outcome NameOutcome
	step    ResolveStep
	to      string   // display name it resolves to
	cands   []string // display names of the candidates (any order)
	mint    bool     // mints under the dormant holder's next free suffix
	reason  string
}

// TestResolvePersonName_DormantHolderTable covers every outcome when a new
// mention's only exact holder is dormant (A2, R1 step 5, R6), in both modes,
// then runs the mention through the People routine: a single-word form with
// one active first-name match routes there (a first_name pin, aka[] untouched);
// two matches hold it (first_name); none mints under the next free suffix; a
// multi-word form always mints. The dormant record's bytes never change.
func TestResolvePersonName_DormantHolderTable(t *testing.T) {
	cases := []struct {
		name    string
		seed    []string
		dormant string
		form    string
		capture dormantHolderExpect
		lookup  dormantHolderExpect
	}{
		{
			name: "single word, one active first-name match routes", seed: []string{"Sam", "Sam Rivera"},
			dormant: "Sam", form: "Sam",
			capture: dormantHolderExpect{outcome: NameResolved, step: StepFirstName, to: "Sam Rivera"},
			lookup:  dormantHolderExpect{outcome: NameResolved, step: StepFirstName, to: "Sam Rivera"},
		},
		{
			name: "single word, two active first-name matches hold", seed: []string{"Alex", "Alex Kim", "Alex Stone"},
			dormant: "Alex", form: "Alex",
			capture: dormantHolderExpect{outcome: NameAmbiguous, step: StepFirstName, reason: identity.HoldFirstName, cands: []string{"Alex Kim", "Alex Stone", "Alex"}},
			lookup:  dormantHolderExpect{outcome: NameAmbiguous, step: StepFirstName, reason: identity.HoldFirstName, cands: []string{"Alex Kim", "Alex Stone", "Alex"}},
		},
		{
			name: "single word, no active first-name match mints the next suffix", seed: []string{"Sam", "Jordan Lee"},
			dormant: "Sam", form: "Sam",
			capture: dormantHolderExpect{outcome: NameMint, step: StepMintNext, mint: true},
			lookup:  dormantHolderExpect{outcome: NameResolved, step: StepDormant, to: "Sam"},
		},
		{
			name: "multi-word form always mints the next suffix", seed: []string{"Sam Rivera", "Sam"},
			dormant: "Sam Rivera", form: "Sam Rivera",
			capture: dormantHolderExpect{outcome: NameMint, step: StepMintNext, mint: true},
			lookup:  dormantHolderExpect{outcome: NameResolved, step: StepDormant, to: "Sam Rivera"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newResolveFixture(t)
			for _, n := range tc.seed {
				f.mention(n)
			}
			f.dormant(tc.dormant)
			dormantKey := f.key(tc.dormant)
			dormantBefore := readFileBytes(t, f.a, dormantKey)

			checkDormantHolder(f, "capture", f.capture(tc.form), tc.capture, dormantKey)
			look := f.lookup(tc.form)
			checkDormantHolder(f, "lookup", look, tc.lookup, dormantKey)
			if tc.lookup.step == StepDormant {
				assert.True(t, look.Dormant, "a lookup of a dormant-only name resolves labeled dormant")
			}
			if tc.lookup.step == StepFirstName && tc.lookup.outcome == NameResolved {
				assert.Equal(t, []string{dormantKey}, look.DormantAlso, "the view names the dormant holder")
			}

			captureDormantHolder(f, tc.form, tc.capture, dormantKey)
			assert.Equal(t, dormantBefore, readFileBytes(t, f.a, dormantKey), "the dormant record's bytes never change")
		})
	}
}

// checkDormantHolder asserts one mode's resolution against the table.
func checkDormantHolder(f *resolveFixture, mode string, got NameResolution, want dormantHolderExpect, dormantKey string) {
	t := f.t
	t.Helper()
	assert.Equal(t, want.outcome, got.Outcome, mode)
	assert.Equal(t, want.step, got.Step, mode)
	if want.to != "" {
		assert.Equal(t, f.key(want.to), got.PersonKey, mode)
	}
	if want.cands != nil {
		assert.Equal(t, listingOrder(f.keysOf(want.cands...), dormantKey), got.Candidates, mode)
		assert.Equal(t, want.reason, got.HoldReason, mode)
	}
	if want.mint {
		assert.Equal(t, secondSlot(dormantKey), got.MintKey, mode)
	}
}

// captureDormantHolder runs the mention through the People routine and checks
// it carried the capture outcome out: a first-name route counts the mention and
// pins it without touching the target's identity; a hold writes nothing; a mint
// creates the next-suffix record.
func captureDormantHolder(f *resolveFixture, form string, want dormantHolderExpect, dormantKey string) {
	t := f.t
	t.Helper()
	var target string
	var targetBefore PersonRecord
	if want.to != "" {
		target = f.key(want.to)
		targetBefore, _, _ = f.a.ReadPerson(target)
	}
	raw := f.nextRaw()
	res, err := f.a.UpdatePerson(PersonMention{DisplayName: form, RawEntryID: raw, At: personAt(25), RecordedAt: personAt(25)})
	require.NoError(t, err)
	switch want.outcome {
	case NameResolved:
		assert.Equal(t, target, res.PersonKey)
		after, _, rerr := f.a.ReadPerson(target)
		require.NoError(t, rerr)
		assert.Equal(t, targetBefore.Aka, after.Aka, "a first-name route never touches the target's aka[]")
		assert.Equal(t, targetBefore.DisplayName, after.DisplayName)
		assert.Contains(t, after.EntryRefs, raw)
		st, serr := f.a.IdentityState()
		require.NoError(t, serr)
		pin, ok := st.Pin(raw, form)
		require.True(t, ok, "the route records a pin")
		assert.Equal(t, target, pin.PersonKey)
		assert.Equal(t, identity.CauseFirstName, pin.Cause)
	case NameAmbiguous:
		assert.True(t, res.Held)
		assert.Equal(t, want.reason, res.HoldReason)
	case NameMint:
		assert.Equal(t, secondSlot(dormantKey), res.PersonKey)
		minted, found, rerr := f.a.ReadPerson(res.PersonKey)
		require.NoError(t, rerr)
		require.True(t, found)
		assert.Equal(t, form, minted.DisplayName)
		assert.True(t, res.FirstMention)
	case NameNoMatch:
		t.Fatalf("capture never answers no-match")
	}
}

// listingOrder is the candidate order every list shows: the active keys sorted,
// then the dormant one.
func listingOrder(keys []string, dormantKey string) []string {
	var active []string
	for _, k := range keys {
		if k != dormantKey {
			active = append(active, k)
		}
	}
	slices.Sort(active)
	return append(active, dormantKey)
}

// TestResolvePersonName_DormantNeverBeatsActive: once a dormant "Sam" and a
// newer, active "Sam" share the name, both modes land on the active one, and a
// lookup names the dormant one on its own line.
func TestResolvePersonName_DormantNeverBeatsActive(t *testing.T) {
	f := newResolveFixture(t)
	old := f.mention("Sam")
	f.dormant("Sam")
	fresh, created := f.create("Sam")
	require.True(t, created)
	require.Equal(t, secondSlot(old), fresh.PersonKey)

	got := f.capture("Sam")
	assert.Equal(t, NameResolved, got.Outcome)
	assert.Equal(t, StepSlug, got.Step)
	assert.Equal(t, fresh.PersonKey, got.PersonKey)
	assert.True(t, got.SlugHolder)

	look := f.lookup("Sam")
	assert.Equal(t, NameResolved, look.Outcome)
	assert.Equal(t, StepExact, look.Step)
	assert.Equal(t, fresh.PersonKey, look.PersonKey)
	assert.False(t, look.Dormant)
	assert.Equal(t, []string{old}, look.DormantAlso)

	// The People routine folds the mention into the active "Sam".
	oldBefore := readFileBytes(t, f.a, old)
	res, err := f.a.UpdatePerson(PersonMention{DisplayName: "Sam", RawEntryID: f.nextRaw(), At: personAt(26)})
	require.NoError(t, err)
	assert.Equal(t, fresh.PersonKey, res.PersonKey)
	assert.Equal(t, oldBefore, readFileBytes(t, f.a, old))
}

// TestResolvePersonName_ClaimRoutesBareForm: a claim makes a bare form resolve
// to its claimant in both modes — over an active exact holder, and for a form
// no one holds — and the People routine counts the mention without adding the
// form to the claimant's aka[].
func TestResolvePersonName_ClaimRoutesBareForm(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	rivera := f.mention("Sam Rivera")
	f.claim("Sam", rivera)
	f.claim("Rivera", rivera)

	for _, form := range []string{"Sam", "sam", "Rivera"} {
		got := f.capture(form)
		assert.Equal(t, NameResolved, got.Outcome, form)
		assert.Equal(t, StepClaim, got.Step, form)
		assert.Equal(t, rivera, got.PersonKey, form)
		assert.False(t, got.SlugHolder, form)

		look := f.lookup(form)
		assert.Equal(t, NameResolved, look.Outcome, form)
		assert.Equal(t, StepClaim, look.Step, form)
		assert.Equal(t, rivera, look.PersonKey, form)
		assert.Empty(t, look.DormantAlso, form)
	}

	samBefore := readFileBytes(t, f.a, sam)
	riveraBefore, _, err := f.a.ReadPerson(rivera)
	require.NoError(t, err)
	raw := f.nextRaw()
	res, err := f.a.UpdatePerson(PersonMention{DisplayName: "Sam", Aka: []string{"Sammy"}, RawEntryID: raw, At: personAt(26)})
	require.NoError(t, err)
	assert.Equal(t, rivera, res.PersonKey)
	after, _, err := f.a.ReadPerson(rivera)
	require.NoError(t, err)
	assert.Equal(t, riveraBefore.Aka, after.Aka, "a claim route never adds the form (or an explicit aka) to aka[]")
	assert.Equal(t, riveraBefore.DisplayName, after.DisplayName)
	assert.Contains(t, after.EntryRefs, raw)
	assert.Equal(t, samBefore, readFileBytes(t, f.a, sam), "the slug holder is untouched")
	assert.Empty(t, f.eventsOfKind(identity.KindPin), "a claim route records no pin")
}

// TestResolvePersonName_DormantClaimantInert: a dormant claimant's claim is
// skipped by both modes and takes effect again on restore (D5).
func TestResolvePersonName_DormantClaimantInert(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	rivera := f.mention("Sam Rivera")
	f.claim("Sam", rivera)
	f.dormant("Sam Rivera")

	got := f.capture("Sam")
	assert.Equal(t, StepSlug, got.Step)
	assert.Equal(t, sam, got.PersonKey)
	look := f.lookup("Sam")
	assert.Equal(t, StepExact, look.Step)
	assert.Equal(t, sam, look.PersonKey)

	// A claim of a form no one else holds is inert too: the name falls back to
	// its dormant exact holder.
	look = f.lookup("Sam Rivera")
	assert.Equal(t, StepDormant, look.Step)
	assert.True(t, look.Dormant)

	f.setStatus(rivera, identity.StatusActive)
	got = f.capture("Sam")
	assert.Equal(t, StepClaim, got.Step)
	assert.Equal(t, rivera, got.PersonKey)
	look = f.lookup("Sam")
	assert.Equal(t, StepClaim, look.Step)
	assert.Equal(t, rivera, look.PersonKey)
}

// TestResolvePersonName_TwoActiveExactHoldersAmbiguous: with no claim, two
// active exact holders make the form ambiguous in both modes — a form two
// records share through aka[] (the C3 collision), and a same-name pair left
// when a dormant holder is restored after a second record was minted (R4).
func TestResolvePersonName_TwoActiveExactHoldersAmbiguous(t *testing.T) {
	t.Run("aka collision", func(t *testing.T) {
		f := newResolveFixture(t)
		alex := f.mention("Alex", "A.")
		andy := f.mention("Andy", "A.")
		want := []string{alex, andy}
		if andy < alex {
			want = []string{andy, alex}
		}
		for _, got := range []NameResolution{f.capture("A."), f.lookup("A.")} {
			assert.Equal(t, NameAmbiguous, got.Outcome)
			assert.Equal(t, StepExact, got.Step)
			assert.Equal(t, identity.HoldExact, got.HoldReason)
			assert.Equal(t, want, got.Candidates)
		}
	})
	t.Run("restored same-name holder", func(t *testing.T) {
		f := newResolveFixture(t)
		old := f.mention("Sam")
		f.dormant("Sam")
		fresh, created := f.create("Sam")
		require.True(t, created)
		f.setStatus(old, identity.StatusActive)
		for _, got := range []NameResolution{f.capture("Sam"), f.lookup("Sam")} {
			assert.Equal(t, NameAmbiguous, got.Outcome)
			assert.Equal(t, StepExact, got.Step)
			assert.Equal(t, []string{old, fresh.PersonKey}, got.Candidates)
		}
	})
}

// TestResolvePersonName_SharedFirstNameNotAmbiguous: "Sam" and "Sam Rivera"
// both active, no claim — a bare "Sam" still goes to "Sam" in both modes (A1).
func TestResolvePersonName_SharedFirstNameNotAmbiguous(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	f.mention("Sam Rivera")

	got := f.capture("Sam")
	assert.Equal(t, NameResolved, got.Outcome)
	assert.Equal(t, StepSlug, got.Step)
	assert.Equal(t, sam, got.PersonKey)
	assert.True(t, got.SlugHolder)

	look := f.lookup("Sam")
	assert.Equal(t, NameResolved, look.Outcome)
	assert.Equal(t, StepExact, look.Step)
	assert.Equal(t, sam, look.PersonKey)
	assert.Empty(t, look.DormantAlso)

	res, err := f.a.UpdatePerson(PersonMention{DisplayName: "Sam", RawEntryID: f.nextRaw(), At: personAt(26)})
	require.NoError(t, err)
	assert.Equal(t, sam, res.PersonKey)
	assert.False(t, res.Held)
}

// TestResolvePersonName_AkaDiscoveryHoldsNeverAssigns covers all three PD5
// branches of capture step 4: the discovery read of exact display_name/aka
// holders can hold a mention but never assign one. Lookup keeps resolving
// through aka[] as /person always has.
func TestResolvePersonName_AkaDiscoveryHoldsNeverAssigns(t *testing.T) {
	t.Run("a slug-path holder plus a discovery-only holder holds", func(t *testing.T) {
		f := newResolveFixture(t)
		sam := f.mention("Sam")
		rivera := f.mention("Sam Rivera", "Sam") // the aka's slug is Sam's, so no tombstone
		got := f.capture("Sam")
		assert.Equal(t, NameAmbiguous, got.Outcome)
		assert.Equal(t, identity.HoldExact, got.HoldReason)
		assert.ElementsMatch(t, []string{sam, rivera}, got.Candidates)
		assert.Equal(t, NameAmbiguous, f.lookup("Sam").Outcome)
	})
	t.Run("a free slug path with a single discovery-only holder mints as before", func(t *testing.T) {
		f := newResolveFixture(t)
		sam := f.mention("Sam")
		_, err := f.a.RenamePerson(sam, "Sam Rivera") // "Sam" stays in aka[], its slot's own name moves on
		require.NoError(t, err)
		got := f.capture("Sam")
		assert.Equal(t, NameMint, got.Outcome)
		assert.Equal(t, StepMint, got.Step)
		assert.Equal(t, secondSlot(sam), got.MintKey)
		look := f.lookup("Sam")
		assert.Equal(t, NameResolved, look.Outcome)
		assert.Equal(t, sam, look.PersonKey)
		res, err := f.a.UpdatePerson(PersonMention{DisplayName: "Sam", RawEntryID: f.nextRaw(), At: personAt(26)})
		require.NoError(t, err)
		assert.Equal(t, secondSlot(sam), res.PersonKey, "the existing mint branch runs unchanged")
	})
	t.Run("only dormant slug-path holders plus a single discovery-only holder holds", func(t *testing.T) {
		f := newResolveFixture(t)
		sam := f.mention("Sam")
		rivera := f.mention("Sam Rivera", "Sam")
		f.dormant("Sam")
		got := f.capture("Sam")
		assert.Equal(t, NameAmbiguous, got.Outcome)
		assert.Equal(t, StepExact, got.Step)
		assert.Equal(t, identity.HoldExact, got.HoldReason)
		assert.Equal(t, []string{rivera, sam}, got.Candidates, "active first, then dormant")

		look := f.lookup("Sam")
		assert.Equal(t, NameResolved, look.Outcome, "lookup still resolves to the sole active aka holder")
		assert.Equal(t, rivera, look.PersonKey)
		assert.Equal(t, []string{sam}, look.DormantAlso)

		before := f.peopleBytes()
		res, err := f.a.UpdatePerson(PersonMention{DisplayName: "Sam", RawEntryID: f.nextRaw(), At: personAt(26)})
		require.NoError(t, err)
		assert.True(t, res.Held, "the aka-only holder is never assigned at capture")
		assert.Equal(t, before, f.peopleBytes(), "no one is counted for a held mention")
	})
}

// TestResolvePersonName_ExactKeyWins: in lookup an exact person_key beats a
// claim on its name and a held form; a tombstone key resolves forward; a
// dormant key still resolves, labeled dormant.
func TestResolvePersonName_ExactKeyWins(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	rivera := f.mention("Sam Rivera")
	f.claim("Sam", rivera)
	alex := f.mention("Alex", "A.")
	andy := f.mention("Andy", "A.")

	got := f.lookup(sam)
	assert.Equal(t, NameResolved, got.Outcome)
	assert.Equal(t, StepKey, got.Step)
	assert.Equal(t, sam, got.PersonKey, "a key beats the claim on its name")

	require.Equal(t, NameAmbiguous, f.lookup("A.").Outcome)
	got = f.lookup(andy)
	assert.Equal(t, StepKey, got.Step)
	assert.Equal(t, andy, got.PersonKey, "a key beats a held form")

	tomb, err := DerivePersonKey("A.", data.Wordlist())
	require.NoError(t, err)
	rec, found, err := f.a.ReadPerson(tomb)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, rec.IsTombstone())
	got = f.lookup(tomb)
	assert.Equal(t, StepKey, got.Step)
	assert.Equal(t, alex, got.PersonKey, "a tombstone key resolves forward")

	f.dormant("Sam")
	got = f.lookup(sam)
	assert.Equal(t, StepKey, got.Step)
	assert.Equal(t, sam, got.PersonKey)
	assert.True(t, got.Dormant)
}

// TestResolvePersonName_RestoreReturnsToNormal: restoring a dormant holder
// returns its name to ordinary resolution in both modes.
func TestResolvePersonName_RestoreReturnsToNormal(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	rivera := f.mention("Sam Rivera")

	f.dormant("Sam")
	assert.Equal(t, rivera, f.capture("Sam").PersonKey)
	assert.Equal(t, rivera, f.lookup("Sam").PersonKey)

	f.setStatus(sam, identity.StatusActive)
	got := f.capture("Sam")
	assert.Equal(t, StepSlug, got.Step)
	assert.Equal(t, sam, got.PersonKey)
	look := f.lookup("Sam")
	assert.Equal(t, StepExact, look.Step)
	assert.Equal(t, sam, look.PersonKey)
	assert.False(t, look.Dormant)
	assert.Empty(t, look.DormantAlso)
}

// TestResolvePersonName_LookupEdges covers the lookup branches the named tables
// do not: no match, two dormant exact holders, a claimant beside a dormant
// exact holder, a claim on a person that no longer exists, and a blank form.
func TestResolvePersonName_LookupEdges(t *testing.T) {
	f := newResolveFixture(t)
	assert.Equal(t, NameNoMatch, f.lookup("Nobody").Outcome, "an empty Ledger matches no one")

	alex := f.mention("Alex", "A.")
	andy := f.mention("Andy", "A.")
	f.dormant("Alex", "Andy")
	got := f.lookup("A.")
	assert.Equal(t, NameAmbiguous, got.Outcome, "two dormant exact holders and no one else")
	assert.Equal(t, StepExact, got.Step)
	assert.ElementsMatch(t, []string{alex, andy}, got.Candidates)

	sam := f.mention("Sam")
	rivera := f.mention("Sam Rivera")
	f.dormant("Sam")
	f.claim("Sam", rivera)
	got = f.lookup("Sam")
	assert.Equal(t, StepClaim, got.Step)
	assert.Equal(t, rivera, got.PersonKey)
	assert.Equal(t, []string{sam}, got.DormantAlso)

	f.claim("Ghost", "person_z-none")
	assert.Equal(t, NameNoMatch, f.lookup("Ghost").Outcome, "a claim on a missing person is skipped")
	assert.Equal(t, NameNoMatch, f.lookup("   ").Outcome)
	assert.Equal(t, NameNoMatch, f.lookup("Nobody").Outcome)
}

// TestResolvePersonName_CaptureStickiness covers capture steps 1–2 at the
// resolver seam: a pin outranks everything, a prior key is kept (and marked a
// slug-holder fold only when it is the ordinary fold's target), a prior hold
// stays held, and a prior key or pin that no longer names a live person falls
// through to ordinary resolution.
func TestResolvePersonName_CaptureStickiness(t *testing.T) {
	f := newResolveFixture(t)
	sam := f.mention("Sam")
	rivera := f.mention("Sam Rivera")
	raw := "raw_2026_12_02_09_00"
	resolve := func(opts ResolveOptions) NameResolution {
		t.Helper()
		opts.Mode = ResolveCapture
		opts.RawID = raw
		res, err := f.a.ResolvePersonName("Sam", opts)
		require.NoError(t, err)
		return res
	}

	got := resolve(ResolveOptions{PriorKey: sam})
	assert.Equal(t, StepPrior, got.Step)
	assert.True(t, got.SlugHolder, "a prior entry on the ordinary fold's target folds as before")

	got = resolve(ResolveOptions{PriorKey: rivera})
	assert.Equal(t, StepPrior, got.Step)
	assert.Equal(t, rivera, got.PersonKey)
	assert.False(t, got.SlugHolder)

	got = resolve(ResolveOptions{PriorHeld: &HeldPerson{DisplayName: "Sam", Reason: identity.HoldExact, Candidates: []string{rivera, sam}}})
	assert.Equal(t, NameAmbiguous, got.Outcome)
	assert.Equal(t, StepPrior, got.Step)
	assert.Equal(t, identity.HoldExact, got.HoldReason)

	got = resolve(ResolveOptions{PriorKey: "person_z-none"})
	assert.Equal(t, StepSlug, got.Step, "a prior key with no live record falls through")

	appendIdentityT(t, f.a, identity.NewPin(f.at(), identity.SourceCLI, raw, "sam", rivera, identity.CauseClaim))
	got = resolve(ResolveOptions{PriorKey: sam})
	assert.Equal(t, StepPin, got.Step, "a pin outranks the prior entry")
	assert.Equal(t, rivera, got.PersonKey)

	_, err := f.a.ResolvePersonName(".!?", ResolveOptions{Mode: ResolveCapture, RawID: raw})
	require.Error(t, err, "a form with no letter or digit cannot derive a slug")
}

// TestResolvePersonName_ReadErrorsSurface: an unreadable people record or
// identity log surfaces from the resolver rather than resolving on partial
// data.
func TestResolvePersonName_ReadErrorsSurface(t *testing.T) {
	f := newResolveFixture(t)
	f.mention("Sam")
	require.NoError(t, os.WriteFile(filepath.Join(f.a.peopleDir(), "person_bad.json"), []byte("{corrupt"), filePerm))
	_, err := f.a.ResolvePersonName("Sam", ResolveOptions{})
	require.Error(t, err)
	require.NoError(t, os.Remove(filepath.Join(f.a.peopleDir(), "person_bad.json")))

	require.NoError(t, os.MkdirAll(f.a.identityPath(), dirPerm)) // a directory where the log should be
	_, err = f.a.ResolvePersonName("Sam", ResolveOptions{})
	require.Error(t, err)
}

// TestResolvePersonName_ClaimThroughRedirect: a claim written for a key that
// was later merged away belongs to the canonical record (the fold
// canonicalizes claims through redirects), in both modes.
func TestResolvePersonName_ClaimThroughRedirect(t *testing.T) {
	f := newResolveFixture(t)
	f.mention("Sam")
	rivera := f.mention("Sam Rivera")
	kim := f.mention("Alex Kim")
	f.claim("Sam", kim)
	_, err := f.a.MergePersons(kim, rivera)
	require.NoError(t, err)

	for _, got := range []NameResolution{f.capture("Sam"), f.lookup("Sam")} {
		assert.Equal(t, StepClaim, got.Step)
		assert.Equal(t, rivera, got.PersonKey, "the claim follows the merge redirect")
	}
}
