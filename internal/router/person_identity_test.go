package router

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/identity"
	"github.com/mrz1836/lucid/internal/storage"
	"github.com/mrz1836/lucid/internal/validate"
)

// The identity-verb fixtures use synthetic people only: two people named "Sam"
// ("Sam" and "Sam Rivera"), plus "Alex", "Alex Kim", "Alex Stone", "Andy", and
// "Jordan Lee".

// idFixture is a booted router over a temp Ledger, seeded through the real
// People routine so every key is derived, never hand-picked, with a clock that
// ticks forward so identity events fold in the order a test writes them.
type idFixture struct {
	t    *testing.T
	r    *Router
	a    *storage.Adapter
	home string
	tick int
	raw  int
}

// newIDFixture returns an empty booted Ledger.
func newIDFixture(t *testing.T) *idFixture {
	t.Helper()
	r, a, home := newBootedRouter(t)
	return &idFixture{t: t, r: r, a: a, home: home}
}

// now returns the next fixture instant, one minute after the last.
func (f *idFixture) now() time.Time {
	f.tick++
	return time.Date(2026, time.November, 17, 9, 0, 0, 0, time.FixedZone("EST", -5*3600)).Add(time.Duration(f.tick) * time.Minute)
}

// nextRaw returns a fresh synthetic raw id.
func (f *idFixture) nextRaw() string {
	f.raw++
	return fmt.Sprintf("raw_2026_11_%02d_09_00", f.raw)
}

// capture records one mention through the People routine in a fresh raw
// entry, returning the raw id and the result.
func (f *idFixture) capture(display string, aka ...string) (string, storage.PersonResult) {
	f.t.Helper()
	raw := f.nextRaw()
	at := f.now()
	res, err := f.a.UpdatePerson(storage.PersonMention{
		DisplayName: display, Aka: aka, RawEntryID: raw, At: at, RecordedAt: at,
	})
	require.NoError(f.t, err)
	return raw, res
}

// person seeds one person through a mention and returns their key.
func (f *idFixture) person(display string, aka ...string) string {
	f.t.Helper()
	_, res := f.capture(display, aka...)
	require.False(f.t, res.Held, "seeding %q must not hold", display)
	return res.PersonKey
}

// hold captures a mention that must be held, returning its raw id.
func (f *idFixture) hold(display string) string {
	f.t.Helper()
	raw, res := f.capture(display)
	require.True(f.t, res.Held, "a bare %q must be held", display)
	return raw
}

// dormant marks a subject dormant through the verb.
func (f *idFixture) dormant(subject string) PersonIdentityResult {
	f.t.Helper()
	res, err := f.r.PersonDormant(PersonDormantRequest{Subject: subject, Now: f.now()})
	require.NoError(f.t, err)
	return res
}

// claim claims form for subject through the verb.
func (f *idFixture) claim(subject, form string, force bool) PersonIdentityResult {
	f.t.Helper()
	res, err := f.r.PersonClaim(PersonClaimRequest{Subject: subject, Form: form, Force: force, Now: f.now()})
	require.NoError(f.t, err)
	return res
}

// state folds the identity log.
func (f *idFixture) state() identity.State {
	f.t.Helper()
	st, err := f.a.IdentityState()
	require.NoError(f.t, err)
	return st
}

// events reads the identity log.
func (f *idFixture) events() []identity.Event {
	f.t.Helper()
	evs, err := f.a.ReadIdentityEvents()
	require.NoError(f.t, err)
	return evs
}

// lookup resolves a name in lookup mode.
func (f *idFixture) lookup(form string) storage.NameResolution {
	f.t.Helper()
	res, err := f.a.ResolvePersonName(form, storage.ResolveOptions{Mode: storage.ResolveLookup})
	require.NoError(f.t, err)
	return res
}

// identityBytes returns the identity log's bytes ("" when it does not exist).
func (f *idFixture) identityBytes() string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.home, "identity", "identity.jsonl"))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(f.t, err)
	return string(b)
}

// recordTrees snapshots people/, contacts/, and processed/ so a write can be
// proven to have left them byte-identical.
func (f *idFixture) recordTrees() map[string]map[string]string {
	f.t.Helper()
	out := map[string]map[string]string{}
	for _, sub := range []string{"people", "contacts", "processed"} {
		dir := filepath.Join(f.home, sub)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			out[sub] = nil
			continue
		}
		out[sub] = snapshotTree(f.t, dir)
	}
	return out
}

// lastEvents returns the last n identity events.
func (f *idFixture) lastEvents(n int) []identity.Event {
	f.t.Helper()
	evs := f.events()
	require.GreaterOrEqual(f.t, len(evs), n)
	return evs[len(evs)-n:]
}

// heldSamPair seeds an old "Sam" and "Sam Rivera" whose aka holds "Sam", then
// captures a bare "Sam" — two active exact holders, so it is held (§P-11).
func heldSamPair(f *idFixture) (oldSam, river, raw string) {
	oldSam = f.person("Sam")
	river = f.person("Sam Rivera", "Sam")
	return oldSam, river, f.hold("Sam")
}

// TestPersonDormant_PreservesRecordAndRestore: dormant and restore each append
// one status event and leave every people/, contacts/, and processed/ byte as
// it was; while dormant a bare name stops folding into the person, and restore
// returns them to normal resolution. Repeating either is a no-op with no
// receipt.
func TestPersonDormant_PreservesRecordAndRestore(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")
	_, again := f.capture("Sam")
	require.Equal(t, oldSam, again.PersonKey)
	river := f.person("Sam Rivera")
	note := "met through the running club"
	_, err := f.r.PersonSet(PersonSetRequest{Subject: oldSam, Note: &note})
	require.NoError(t, err)
	_, err = f.r.ContactLog(ContactLogRequest{Subject: oldSam, Now: f.now()})
	require.NoError(t, err)
	seedArtifactMentioning(t, f.a, "raw_2026_11_01_09_00", "Sam", oldSam, f.now())
	before := f.recordTrees()

	res := f.dormant(oldSam)
	assert.Equal(t, identity.StatusDormant, res.Status)
	assert.Equal(t, "identity_2026_11_17_a", res.Receipt)
	assert.Equal(t, `"Sam" is now dormant — the record and its history are kept (identity_2026_11_17_a).`, res.Ack)
	assert.Equal(t, before, f.recordTrees(), "dormant writes only the identity log")
	assert.True(t, f.state().IsDormant(oldSam))

	// While dormant, a bare "Sam" means the one active Sam.
	now := f.lookup("Sam")
	assert.Equal(t, river, now.PersonKey)
	assert.Equal(t, storage.StepFirstName, now.Step)

	res, err = f.r.PersonDormant(PersonDormantRequest{Subject: oldSam, Now: f.now()})
	require.NoError(t, err)
	assert.Empty(t, res.Receipt, "a no-op has no receipt")
	assert.Equal(t, `"Sam" is already dormant — nothing to change.`, res.Ack)

	res, err = f.r.PersonDormant(PersonDormantRequest{Subject: "Sam", Restore: true, Now: f.now()})
	require.NoError(t, err)
	assert.Equal(t, oldSam, res.Record.PersonKey)
	assert.Equal(t, identity.StatusActive, res.Status)
	assert.Equal(t, `"Sam" is active again (identity_2026_11_17_b).`, res.Ack)
	assert.Equal(t, before, f.recordTrees(), "restore writes only the identity log")
	assert.Len(t, f.events(), 2)

	// Restore returns normal resolution: lookup and capture both land on Sam.
	now = f.lookup("Sam")
	assert.Equal(t, oldSam, now.PersonKey)
	assert.False(t, now.Dormant)
	_, captured := f.capture("Sam")
	assert.Equal(t, oldSam, captured.PersonKey)

	res, err = f.r.PersonDormant(PersonDormantRequest{Subject: oldSam, Restore: true, Now: f.now()})
	require.NoError(t, err)
	assert.Empty(t, res.Receipt)
	assert.Equal(t, `"Sam" is already active — nothing to change.`, res.Ack)
	assert.Len(t, f.events(), 2, "a no-op appends nothing")
}

// TestPersonDormant_ReasonShownOnView: the one-line --reason-file text is
// stored on the status event and shown on the person view, right after its
// first line ("Dormant since YYYY-MM-DD — <reason>", or "Dormant since
// YYYY-MM-DD." with none) — the off-limits raw view included — and restore
// drops the line; a reason of more than one line, or one given with --restore,
// is refused before any write.
func TestPersonDormant_ReasonShownOnView(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")

	_, err := f.r.PersonDormant(PersonDormantRequest{Subject: oldSam, Reason: "one line\nand another", Now: f.now()})
	require.ErrorIs(t, err, ErrPersonRejected)
	assert.Equal(t, "a dormant reason is one line; nothing was saved.", err.Error())
	_, err = f.r.PersonDormant(PersonDormantRequest{Subject: oldSam, Restore: true, Reason: "why", Now: f.now()})
	require.ErrorIs(t, err, ErrPersonRejected)
	assert.Equal(t, "--reason-file only applies when marking someone dormant, not with --restore; nothing was saved.", err.Error())
	assert.Empty(t, f.identityBytes(), "a refusal writes nothing")

	res, err := f.r.PersonDormant(PersonDormantRequest{Subject: oldSam, Reason: "an old contact from one passing mention\r", Now: f.now()})
	require.NoError(t, err)
	require.NotEmpty(t, res.Receipt)
	fact, ok := f.state().StatusEvent(oldSam)
	require.True(t, ok)
	assert.Equal(t, "an old contact from one passing mention", fact.Reason)
	assert.Equal(t, "2026-11-17", fact.Date)
	assert.Equal(t, res.Receipt, fact.EventID)

	view, err := f.r.Person(PersonRequest{Name: oldSam})
	require.NoError(t, err)
	assert.True(t, view.Dormant)
	lines := strings.Split(view.Text, "\n")
	require.GreaterOrEqual(t, len(lines), 3)
	assert.Equal(t, []string{"Sam", "Dormant since 2026-11-17 — an old contact from one passing mention"}, lines[:2])
	assert.True(t, strings.HasPrefix(lines[2], "Mentioned in "), lines[2])
	byName, err := f.r.Person(PersonRequest{Name: "Sam"})
	require.NoError(t, err)
	assert.Equal(t, view.Text, byName.Text, "the lone dormant holder still answers its own name, labeled")

	_, err = f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: oldSam})
	require.NoError(t, err)
	raw, err := f.r.Person(PersonRequest{Name: oldSam})
	require.NoError(t, err)
	require.True(t, raw.OffLimits)
	rawLines := strings.Split(raw.Text, "\n")
	assert.True(t, strings.HasPrefix(rawLines[0], "Sam is off-limits to inference"), rawLines[0])
	assert.Equal(t, "Dormant since 2026-11-17 — an old contact from one passing mention", rawLines[1])
	_, err = f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: oldSam, Restore: true})
	require.NoError(t, err)

	_, err = f.r.PersonDormant(PersonDormantRequest{Subject: oldSam, Restore: true, Now: f.now()})
	require.NoError(t, err)
	restored, err := f.r.Person(PersonRequest{Name: oldSam})
	require.NoError(t, err)
	assert.False(t, restored.Dormant)
	assert.NotContains(t, restored.Text, "Dormant since")

	f.dormant(oldSam)
	plain, err := f.r.Person(PersonRequest{Name: oldSam})
	require.NoError(t, err)
	assert.Equal(t, "Dormant since 2026-11-17.", strings.Split(plain.Text, "\n")[1], "no reason: the dated line alone")
}

// TestPersonDormant_OrthogonalToOffLimits: dormant and off-limits are
// independent flags — a person can be both, and toggling one never moves the
// other.
func TestPersonDormant_OrthogonalToOffLimits(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")

	_, err := f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: oldSam})
	require.NoError(t, err)
	f.dormant(oldSam)
	assertOffLimits(t, f.a, []string{oldSam})
	assert.True(t, f.state().IsDormant(oldSam))

	_, err = f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: oldSam, Restore: true})
	require.NoError(t, err)
	assertOffLimits(t, f.a, nil)
	assert.True(t, f.state().IsDormant(oldSam), "clearing off-limits leaves dormant")

	_, err = f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: oldSam})
	require.NoError(t, err)
	_, err = f.r.PersonDormant(PersonDormantRequest{Subject: oldSam, Restore: true, Now: f.now()})
	require.NoError(t, err)
	assert.False(t, f.state().IsDormant(oldSam))
	assertOffLimits(t, f.a, []string{oldSam})
}

// TestPersonDormantRestore_ReachesDormantAfterNameMovedOn: once a bare "Sam"
// means someone else, `person dormant Sam` is refused as a disagreement
// (§P-5), but `person dormant Sam --restore` considers only dormant records and
// reaches the old Sam.
func TestPersonDormantRestore_ReachesDormantAfterNameMovedOn(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")
	river := f.person("Sam Rivera")
	f.dormant("Sam")
	f.claim("Sam Rivera", "Sam", false)

	_, err := f.r.PersonDormant(PersonDormantRequest{Subject: "Sam", Now: f.now()})
	require.ErrorIs(t, err, ErrPersonAmbiguousSubject)
	assert.Equal(t, fmt.Sprintf(
		`"Sam" matches more than one person — disambiguate with a person_key (%s / %s (dormant)); nothing was saved.`,
		river, oldSam,
	), err.Error())

	res, err := f.r.PersonDormant(PersonDormantRequest{Subject: "Sam", Restore: true, Now: f.now()})
	require.NoError(t, err)
	assert.Equal(t, oldSam, res.Record.PersonKey)
	assert.NotEmpty(t, res.Receipt)
	assert.False(t, f.state().IsDormant(oldSam))
	// The claim still decides who a bare "Sam" means.
	assert.Equal(t, river, f.lookup("Sam").PersonKey)
}

// TestPersonClaim_RoutesFutureBareMentions: a claim sends every later bare
// mention of the form to the claimant — a form whose old holder is dormant and
// a form no one holds — without touching aka[] or minting anyone.
func TestPersonClaim_RoutesFutureBareMentions(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")
	river := f.person("Sam Rivera")
	f.dormant(oldSam)

	res := f.claim("Sam Rivera", "Sam", false)
	assert.Equal(t, river, res.Record.PersonKey)
	assert.Equal(t, "Sam", res.Form)
	assert.Equal(t, fmt.Sprintf(`A bare "Sam" now means "Sam Rivera" — earlier mentions stay where they are (%s).`, res.Receipt), res.Ack)
	f.claim(river, "Rivera", false)

	people, err := f.a.ListPeopleKeys()
	require.NoError(t, err)
	for _, form := range []string{"Sam", "Rivera"} {
		_, got := f.capture(form)
		assert.Equal(t, river, got.PersonKey, "a bare %q goes to the claimant", form)
		assert.Equal(t, river, f.lookup(form).PersonKey)
	}
	after, err := f.a.ListPeopleKeys()
	require.NoError(t, err)
	assert.Equal(t, people, after, "a claimed form mints no one")
	rec, _, err := f.a.ReadPerson(river)
	require.NoError(t, err)
	assert.Equal(t, "Sam Rivera", rec.DisplayName)
	assert.NotContains(t, rec.Aka, "Sam", "a claim never touches aka[]")
	assert.NotContains(t, rec.Aka, "Rivera")
	assert.Len(t, rec.EntryRefs, 3)
}

// TestPersonClaim_ConflictActiveHolderRefusedP10: a different active holder
// of the form — its slug holder, an exact aka holder, or its claimant — refuses
// the claim with §P-10, naming them and the remedy, and nothing is written.
func TestPersonClaim_ConflictActiveHolderRefusedP10(t *testing.T) {
	cases := []struct {
		name string
		form string
		seed func(f *idFixture) []string // returns the holders the copy names
	}{
		{name: "slug holder", form: "Sam", seed: func(f *idFixture) []string {
			return []string{f.person("Sam")}
		}},
		{name: "exact aka holder", form: "Sam", seed: func(f *idFixture) []string {
			f.dormant(f.person("Sam"))
			return []string{f.person("Jordan Lee", "Sam")}
		}},
		{name: "active claimant", form: "Rivera", seed: func(f *idFixture) []string {
			jordan := f.person("Jordan Lee")
			f.claim(jordan, "Rivera", false)
			return []string{jordan}
		}},
		{name: "several holders", form: "Sam", seed: func(f *idFixture) []string {
			return []string{f.person("Sam"), f.person("Jordan Lee", "Sam")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newIDFixture(t)
			holders := tc.seed(f)
			f.person("Sam Rivera")
			logBefore := f.identityBytes()
			trees := f.recordTrees()

			_, err := f.r.PersonClaim(PersonClaimRequest{Subject: "Sam Rivera", Form: tc.form, Now: f.now()})
			require.ErrorIs(t, err, ErrPersonClaimConflict)
			var want string
			if len(holders) == 1 {
				rec, _, rerr := f.a.ReadPerson(holders[0])
				require.NoError(t, rerr)
				want = fmt.Sprintf(`the form %q is held by %q (%s), who is active — mark them dormant with lucid person dormant %s, or re-run with --force; nothing was saved.`,
					tc.form, rec.DisplayName, holders[0], holders[0])
			} else {
				refs, rerr := f.r.personRefs(sortedKeys(holders))
				require.NoError(t, rerr)
				want = fmt.Sprintf(`the form %q is held by other active people — %s; mark each dormant with lucid person dormant <key>, or re-run with --force; nothing was saved.`,
					tc.form, joinPersonRefs(refs))
			}
			assert.Equal(t, want, err.Error())
			assert.Equal(t, logBefore, f.identityBytes(), "a refusal appends nothing")
			assert.Equal(t, trees, f.recordTrees())
		})
	}
}

// sortedKeys returns a sorted copy of keys.
func sortedKeys(keys []string) []string {
	out := slices.Clone(keys)
	slices.Sort(out)
	return out
}

// TestPersonClaim_ForceOverrideReceiptNamesDisplaced: --force writes the
// claim over active holders, releases a displaced claimant first, and the
// receipt names everyone it overrode — who stay active and reachable by key.
func TestPersonClaim_ForceOverrideReceiptNamesDisplaced(t *testing.T) {
	f := newIDFixture(t)
	jordan := f.person("Jordan Lee")
	river := f.person("Sam Rivera")
	f.claim(jordan, "Rivera", false)

	res := f.claim("Sam Rivera", "Rivera", true)
	require.Len(t, res.Displaced, 1)
	assert.Equal(t, PersonRef{PersonKey: jordan, DisplayName: "Jordan Lee"}, res.Displaced[0])
	assert.Equal(t, fmt.Sprintf("A bare \"Rivera\" now means \"Sam Rivera\" — earlier mentions stay where they are (%s).\n"+
		"Overrode \"Jordan Lee\" (%s); still active and reached by person_key.", res.Receipt, jordan), res.Ack)
	evs := f.lastEvents(2)
	assert.Equal(t, identity.KindRelease, evs[0].Kind)
	assert.Equal(t, jordan, evs[0].PersonKey)
	assert.Equal(t, identity.KindClaim, evs[1].Kind)
	assert.True(t, evs[1].Force)
	assert.Equal(t, []string{jordan}, evs[1].Displaced)
	assert.Equal(t, res.Receipt, evs[1].ID, "the receipt is the claim")
	c, ok := f.state().ActiveClaimant("Rivera")
	require.True(t, ok)
	assert.Equal(t, river, c.PersonKey)
	assert.Empty(t, f.state().Duplicates())

	// The displaced person stays active and reachable by key.
	assert.False(t, f.state().IsDormant(jordan))
	byKey := f.lookup(jordan)
	assert.Equal(t, jordan, byKey.PersonKey)

	// Several active holders are all named.
	oldSam := f.person("Sam")
	res = f.claim("Sam Rivera", "Sam", true)
	refs, err := f.r.personRefs([]string{oldSam})
	require.NoError(t, err)
	assert.Contains(t, res.Ack, "Overrode "+joinPersonRefs(refs)+"; still active and reached by person_key.")
	assert.Equal(t, river, f.lookup("Sam").PersonKey)
}

// TestPersonClaim_DormantOrFirstNameNeverBlocks: a dormant holder, someone who
// only shares the first name, and a dormant claimant never block a claim; a
// dormant claimant's inert claim is released first and the receipt says so.
func TestPersonClaim_DormantOrFirstNameNeverBlocks(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")
	river := f.person("Sam Rivera")
	jordan := f.person("Jordan Lee")
	f.dormant(oldSam)

	// A dormant slug holder and a first-name-only "Sam Rivera" never block.
	res := f.claim(jordan, "Sam", false)
	assert.Empty(t, res.Displaced)
	assert.False(t, f.lastEvents(1)[0].Force)

	// A dormant claimant never blocks; its claim is released first.
	f.dormant(jordan)
	res = f.claim("Sam Rivera", "Sam", false)
	assert.Equal(t, fmt.Sprintf("A bare \"Sam\" now means \"Sam Rivera\" — earlier mentions stay where they are (%s).\n"+
		"Released the dormant claimant \"Jordan Lee\" (%s).", res.Receipt, jordan), res.Ack)
	evs := f.lastEvents(2)
	assert.Equal(t, identity.KindRelease, evs[0].Kind)
	assert.Equal(t, jordan, evs[0].PersonKey)
	c, ok := f.state().Claimant("Sam")
	require.True(t, ok)
	assert.Equal(t, river, c.PersonKey)
	assert.Empty(t, f.state().Duplicates())

	// Claiming a form one already claims is a no-op.
	res = f.claim(river, "Sam", false)
	assert.Empty(t, res.Receipt)
	assert.Equal(t, `"Sam Rivera" already claims "Sam" — nothing to change.`, res.Ack)

	// A blank form is §P-9.
	_, err := f.r.PersonClaim(PersonClaimRequest{Subject: river, Form: " ?! ", Now: f.now()})
	require.ErrorIs(t, err, ErrPersonRejected)
	assert.Equal(t, "a name form is required; nothing was saved.", err.Error())
}

// TestPersonClaim_NeverRewritesPastMentions: a claim changes no people/ or
// processed/ byte, and a mention already recorded keeps its person on a
// re-run; only mentions resolved after the claim go to the claimant.
func TestPersonClaim_NeverRewritesPastMentions(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")
	rawPast := "raw_2026_10_01_09_00"
	at := f.now()
	_, err := f.a.UpdatePerson(storage.PersonMention{DisplayName: "Sam", RawEntryID: rawPast, At: at, RecordedAt: at})
	require.NoError(t, err)
	seedArtifactMentioning(t, f.a, rawPast, "Sam", oldSam, at)
	river := f.person("Sam Rivera")
	trees := f.recordTrees()

	f.claim("Sam Rivera", "Sam", true)
	assert.Equal(t, trees, f.recordTrees(), "a claim writes only the identity log")

	// The past mention stays with Sam on a re-run of its raw entry.
	again, err := f.a.UpdatePerson(storage.PersonMention{DisplayName: "Sam", RawEntryID: rawPast, At: at, RecordedAt: f.now()})
	require.NoError(t, err)
	assert.Equal(t, oldSam, again.PersonKey)
	rec, _, err := f.a.ReadPerson(oldSam)
	require.NoError(t, err)
	assert.Contains(t, rec.EntryRefs, rawPast)

	// A new mention goes to the claimant.
	_, fresh := f.capture("Sam")
	assert.Equal(t, river, fresh.PersonKey)
}

// TestPersonRelease_UndoesClaim: a release ends the claim for mentions
// resolved after it; releasing a form one does not claim is a no-op.
func TestPersonRelease_UndoesClaim(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")
	river := f.person("Sam Rivera")
	f.claim("Sam Rivera", "Sam", true)
	_, routed := f.capture("Sam")
	require.Equal(t, river, routed.PersonKey)

	res, err := f.r.PersonRelease(PersonReleaseRequest{Subject: "Sam Rivera", Form: "Sam", Now: f.now()})
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf(`Released "Sam" from "Sam Rivera" — a bare "Sam" no longer means them by claim (%s).`, res.Receipt), res.Ack)
	assert.Equal(t, "Sam", res.Form)
	_, ok := f.state().Claimant("Sam")
	assert.False(t, ok)
	_, back := f.capture("Sam")
	assert.Equal(t, oldSam, back.PersonKey, "after the release a bare Sam folds as before")
	assert.Equal(t, oldSam, f.lookup("Sam").PersonKey)

	res, err = f.r.PersonRelease(PersonReleaseRequest{Subject: river, Form: "Sam", Now: f.now()})
	require.NoError(t, err)
	assert.Empty(t, res.Receipt)
	assert.Equal(t, `"Sam Rivera" does not claim "Sam" — nothing to release.`, res.Ack)

	_, err = f.r.PersonRelease(PersonReleaseRequest{Subject: river, Form: "  ", Now: f.now()})
	require.ErrorIs(t, err, ErrPersonRejected)
}

// TestPersonCuration_PinsHeldMentions: a claim, release, dormant, or restore
// write re-resolves the held mentions of each form it affects under capture
// rules and pins each one that now resolves to exactly one person, with the
// verb as the cause; the receipt lists every pinned raw entry and person and
// counts the ones still held.
func TestPersonCuration_PinsHeldMentions(t *testing.T) {
	cases := []struct {
		name  string
		cause string
		// run seeds the held mention, runs the verb, and returns the raw id,
		// the person it should pin to, and the write's result.
		run func(f *idFixture) (raw, want string, res PersonIdentityResult)
	}{
		{name: "claim", cause: identity.CauseClaim, run: func(f *idFixture) (string, string, PersonIdentityResult) {
			_, river, raw := heldSamPair(f)
			return raw, river, f.claim("Sam Rivera", "Sam", true)
		}},
		{name: "dormant", cause: identity.CauseDormant, run: func(f *idFixture) (string, string, PersonIdentityResult) {
			oldSam, _, raw := heldSamPair(f)
			return raw, oldSam, f.dormant("Sam Rivera")
		}},
		{name: "restore", cause: identity.CauseRestore, run: func(f *idFixture) (string, string, PersonIdentityResult) {
			alex := f.person("Alex")
			f.person("Alex Kim")
			f.person("Alex Stone")
			f.dormant("Alex")
			raw := f.hold("Alex")
			res, err := f.r.PersonDormant(PersonDormantRequest{Subject: "Alex", Restore: true, Now: f.now()})
			require.NoError(f.t, err)
			return raw, alex, res
		}},
		{name: "release", cause: identity.CauseRelease, run: func(f *idFixture) (string, string, PersonIdentityResult) {
			oldSam, river, raw := heldSamPair(f)
			jordan := f.person("Jordan Lee")
			f.dormant(jordan)
			// A dormant claimant's claim is inert, so it settles nothing.
			inert := f.claim(jordan, "Sam", true)
			require.Empty(f.t, inert.Pinned)
			require.Equal(f.t, 1, inert.StillHeld)
			require.Contains(f.t, inert.Ack, "1 mention is still held — see lucid person reconcile.")
			// Merging the pair makes the name one person's, but a merge
			// pins nothing; the release re-resolves it.
			_, err := f.r.PersonMerge(PersonMergeRequest{Source: river, Target: oldSam, Now: f.now()})
			require.NoError(f.t, err)
			res, err := f.r.PersonRelease(PersonReleaseRequest{Subject: "Jordan Lee", Form: "Sam", Now: f.now()})
			require.NoError(f.t, err)
			return raw, oldSam, res
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newIDFixture(t)
			raw, want, res := tc.run(f)
			require.NotEmpty(t, res.Receipt)
			require.Len(t, res.Pinned, 1)
			assert.Equal(t, raw, res.Pinned[0].RawID)
			assert.Equal(t, want, res.Pinned[0].PersonKey)
			assert.Equal(t, 0, res.StillHeld)

			pin := f.lastEvents(1)[0]
			assert.Equal(t, identity.KindPin, pin.Kind)
			assert.Equal(t, tc.cause, pin.Cause)
			assert.Equal(t, identity.SourceCLI, pin.Source)
			assert.Equal(t, raw, pin.RawID)
			assert.Equal(t, want, pin.PersonKey)
			assert.Empty(t, f.state().PendingHolds(), "the pin settles the hold")

			refs, err := f.r.personRefs([]string{want})
			require.NoError(t, err)
			assert.Contains(t, res.Ack, "\nPinned 1 held mention:\n- "+raw+" "+fmt.Sprintf("%q", res.Pinned[0].Form)+" → "+joinPersonRefs(refs))
			assert.NotContains(t, res.Ack, "still held")
		})
	}
}

// TestPersonCuration_WouldMintStaysHeld: a curation write never mints, so a
// held mention whose capture would now need a new record stays held — and so
// does the aka-only capture hold, even though a lookup resolves it. The
// receipt counts what is still held.
func TestPersonCuration_WouldMintStaysHeld(t *testing.T) {
	t.Run("would mint", func(t *testing.T) {
		f := newIDFixture(t)
		f.person("Alex")
		f.person("Alex Kim")
		stone := f.person("Alex Stone")
		f.dormant("Alex")
		raw := f.hold("Alex")
		_, err := f.r.PersonRename(PersonRenameRequest{Subject: "Alex Kim", NewName: "Kim"})
		require.NoError(t, err)
		_, err = f.r.PersonRename(PersonRenameRequest{Subject: stone, NewName: "Stone"})
		require.NoError(t, err)
		people := f.recordTrees()["people"]

		// Stone is a candidate of the held "Alex"; with no active first-name
		// match left, its capture would mint — so it stays held.
		res := f.dormant(stone)
		assert.Empty(t, res.Pinned)
		assert.Equal(t, 1, res.StillHeld)
		assert.Contains(t, res.Ack, "\n1 mention is still held — see lucid person reconcile.")
		assert.Equal(t, people, f.recordTrees()["people"], "a curation write never mints")
		holds := f.state().PendingHolds()
		require.Len(t, holds, 1)
		assert.Equal(t, raw, holds[0].RawID)
	})
	t.Run("aka-only hold", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam, river, raw := heldSamPair(f)
		res := f.dormant(oldSam)
		assert.Empty(t, res.Pinned)
		assert.Equal(t, 1, res.StillHeld)
		holds := f.state().PendingHolds()
		require.Len(t, holds, 1)
		assert.Equal(t, raw, holds[0].RawID)
		// A lookup still resolves to the sole active aka holder.
		assert.Equal(t, river, f.lookup("Sam").PersonKey)
	})
}

// TestPersonMerge_RepointsClaimsAndClearsSourceStatus: a merge keeps the
// identity log valid — the dormant source is made active again, a source claim
// moves to the target, and a claim on a form another active person holds
// exactly is released — and a merge with no identity facts acks as before.
func TestPersonMerge_RepointsClaimsAndClearsSourceStatus(t *testing.T) {
	f := newIDFixture(t)
	alex := f.person("Alex")
	andy := f.person("Andy")
	jordan := f.person("Jordan Lee")
	f.claim(andy, "Al", false)
	f.claim(andy, "Jordan Lee", true)
	f.dormant(andy)
	f.dormant(alex)

	res, err := f.r.PersonMerge(PersonMergeRequest{Source: andy, Target: alex, Now: f.now()})
	require.NoError(t, err)
	assert.Equal(t, "Merged \"Andy\" into \"Alex\" — 2 entries now resolve to one record.\n"+
		"Claims moved: \"Al\".\n"+
		"Claims released: \"Jordan Lee\" (another active person holds it).", res.Ack)

	evs := f.lastEvents(4)
	for _, e := range evs {
		assert.Equal(t, identity.SourceMerge, e.Source)
	}
	assert.Equal(t, identity.KindStatus, evs[0].Kind)
	assert.Equal(t, andy, evs[0].PersonKey)
	assert.Equal(t, identity.StatusActive, evs[0].Status)
	assert.Equal(t, []string{identity.KindRelease, identity.KindClaim, identity.KindRelease},
		[]string{evs[1].Kind, evs[2].Kind, evs[3].Kind})
	assert.Equal(t, alex, evs[2].PersonKey)

	st := f.state()
	assert.False(t, st.IsDormant(andy), "no dormant status sits on the tombstone")
	assert.True(t, st.IsDormant(alex), "the target keeps its status")
	c, ok := st.Claimant("Al")
	require.True(t, ok)
	assert.Equal(t, alex, c.PersonKey)
	_, ok = st.Claimant("Jordan Lee")
	assert.False(t, ok)
	assert.Empty(t, st.Duplicates())
	// Folded without redirects, no effective claim names the tombstone.
	raw := identity.Fold(f.events(), nil)
	for _, claim := range raw.Claims() {
		assert.NotEqual(t, andy, claim.PersonKey)
	}
	assert.False(t, raw.IsDormant(andy))
	assert.False(t, st.IsDormant(jordan), "the active exact holder the released claim yields to")

	// lucid validate's people-identity check runs (the log exists) and finds
	// nothing after the verbs plus merge: no claim and no dormant status sits on
	// the tombstone, and no claim is a duplicate.
	found, ran, err := validate.CheckPeopleIdentityLog(personLedgerSource{a: f.a})
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Empty(t, found, "a legitimate merge leaves the identity log valid")
	rep, err := validate.Run(validate.Options{Ledger: personLedgerSource{a: f.a}})
	require.NoError(t, err)
	assert.Contains(t, rep.Ran, validate.CheckPeopleIdentity)
	assert.True(t, rep.OK(), "validate is clean: %v", rep.Findings)

	// A merge with no identity facts acks exactly as before.
	g := newIDFixture(t)
	g.person("Alex")
	g.person("Andy")
	plain, err := g.r.PersonMerge(PersonMergeRequest{Source: "Andy", Target: "Alex", Now: g.now()})
	require.NoError(t, err)
	assert.Equal(t, "Merged \"Andy\" into \"Alex\" — 2 entries now resolve to one record.", plain.Ack)
	assert.Empty(t, g.identityBytes(), "no identity log is created")
}

// TestPersonCuration_RecordTargetingDisagreeReturnsCandidates: a curation verb
// targets a record — key-first, then the exact display_name/aka match across
// active and dormant records — and refuses with §P-5 when that match disagrees
// with who the name means now, or when several records match. The candidates
// list active people first and mark dormant ones.
func TestPersonCuration_RecordTargetingDisagreeReturnsCandidates(t *testing.T) {
	note := "a note"
	t.Run("a claim moves the name", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera")
		f.claim("Sam Rivera", "Sam", true)
		_, err := f.r.PersonSet(PersonSetRequest{Subject: "Sam", Note: &note})
		require.ErrorIs(t, err, ErrPersonAmbiguousSubject)
		assert.Equal(t, fmt.Sprintf(`"Sam" matches more than one person — disambiguate with a person_key (%s); nothing was saved.`,
			candidateKeys(mustMatches(t, f, oldSam, river))), err.Error())
		// A key still targets the record.
		res, err := f.r.PersonSet(PersonSetRequest{Subject: oldSam, Note: &note})
		require.NoError(t, err)
		assert.Equal(t, oldSam, res.Record.PersonKey)
	})
	t.Run("a dormant holder whose name routes by first name", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera")
		f.dormant(oldSam)
		_, err := f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Sam"})
		require.ErrorIs(t, err, ErrPersonAmbiguousSubject)
		assert.Equal(t, fmt.Sprintf(`"Sam" matches more than one person — disambiguate with a person_key (%s / %s (dormant)); nothing was saved.`,
			river, oldSam), err.Error())
		assertOffLimits(t, f.a, nil)
	})
	t.Run("two exact matches", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		jordan := f.person("Jordan Lee", "Sam")
		_, err := f.r.PersonAlias(PersonAliasRequest{Subject: "Sam", Form: "Sammy"})
		require.ErrorIs(t, err, ErrPersonAmbiguousSubject)
		assert.Equal(t, fmt.Sprintf(`"Sam" matches more than one person — disambiguate with a person_key (%s); nothing was saved.`,
			candidateKeys(mustMatches(t, f, oldSam, jordan))), err.Error())
	})
	t.Run("agreement and a claimed form", func(t *testing.T) {
		f := newIDFixture(t)
		river := f.person("Sam Rivera")
		f.claim(river, "Rivera", false)
		res, err := f.r.PersonSet(PersonSetRequest{Subject: "Rivera", Note: &note})
		require.NoError(t, err)
		assert.Equal(t, river, res.Record.PersonKey, "a claimed form no one holds names its claimant")
		res, err = f.r.PersonSet(PersonSetRequest{Subject: "Sam Rivera", Note: &note})
		require.NoError(t, err)
		assert.Equal(t, river, res.Record.PersonKey)
		_, err = f.r.PersonSet(PersonSetRequest{Subject: "Nobody", Note: &note})
		require.ErrorIs(t, err, ErrPersonUnknownSubject)
	})
}

// mustMatches builds the candidate list for keys, ordered as the verbs order
// it.
func mustMatches(t *testing.T, f *idFixture, keys ...string) []PersonMatch {
	t.Helper()
	m, err := f.r.personMatches(f.state(), keys)
	require.NoError(t, err)
	return m
}

// TestResolvePersonNow_LookupOutcomes: the who-it-means-now seam resolves a
// name through lookup mode — two active exact holders are ambiguous and come
// back as candidates with §P-5, a dormant exact holder is named alongside the
// active answer, an exact key resolves even to a dormant person, §P-4 is no
// one, §P-9 a blank subject — and it never writes.
func TestResolvePersonNow_LookupOutcomes(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")
	jordan := f.person("Jordan Lee", "Sam")
	f.person("Sam Rivera")

	subj, err := f.r.resolvePersonNow("Sam")
	require.ErrorIs(t, err, ErrPersonAmbiguousSubject)
	assert.Equal(t, fmt.Sprintf(`"Sam" matches more than one person — disambiguate with a person_key (%s); nothing was saved.`,
		candidateKeys(mustMatches(t, f, oldSam, jordan))), err.Error())
	require.Len(t, subj.Candidates, 2)
	assert.False(t, subj.Candidates[0].Dormant)

	f.dormant(oldSam)
	logBefore := f.identityBytes()
	subj, err = f.r.resolvePersonNow("Sam")
	require.NoError(t, err)
	assert.Equal(t, jordan, subj.Record.PersonKey, "the one active exact holder")
	assert.False(t, subj.Dormant)
	require.Len(t, subj.DormantAlso, 1)
	assert.Equal(t, oldSam, subj.DormantAlso[0].Record.PersonKey)
	assert.True(t, subj.DormantAlso[0].Dormant)

	subj, err = f.r.resolvePersonNow(oldSam)
	require.NoError(t, err)
	assert.Equal(t, oldSam, subj.Record.PersonKey)
	assert.True(t, subj.Dormant)

	_, err = f.r.resolvePersonNow("Nobody")
	require.ErrorIs(t, err, ErrPersonUnknownSubject)
	_, err = f.r.resolvePersonNow("   ")
	require.ErrorIs(t, err, ErrPersonRejected)
	assert.Equal(t, logBefore, f.identityBytes(), "a lookup never writes")
}
