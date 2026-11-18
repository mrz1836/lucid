package router

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/contact"
	"github.com/mrz1836/lucid/internal/contactweekly"
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// person_reads_test.go covers the who-it-means-now read surfaces
// (data-model.md §"Resolving a bare name"; usage/commands.md §"Resolving a
// subject"): `lucid person <name>` ranks and labels dormant people, names a
// dormant person who shares the form, and the contact subject, gratitude
// `--person`/`thank` and memory linking resolve a name to who it means now. The
// contact listings leave dormant people out unless `--all`, and link/attach
// acks mark a dormant person. Every person here is synthetic: two people named
// "Sam" ("Sam" and "Sam Rivera"), plus "Alex", "Alex Kim", "Alex Stone" and
// "Jordan Lee".

// viewLines splits a person view's text into its lines.
func viewLines(res PersonResult) []string { return strings.Split(res.Text, "\n") }

// candidateRow is one candidate as the §P-2 prose renders it.
func (f *idFixture) candidateRow(key string, dormant bool) string {
	f.t.Helper()
	rec, found, err := f.a.ReadPerson(key)
	require.NoError(f.t, err)
	require.True(f.t, found)
	label := ""
	if dormant {
		label = ", dormant"
	}
	return fmt.Sprintf("%s (first seen %s%s)", rec.DisplayName, rec.FirstSeenAt.Format(personDateLayout), label)
}

// candidateKeysAndFlags lists a §P-2 result's candidate keys and dormant flags.
func candidateKeysAndFlags(res PersonResult) (keys []string, dormant []bool) {
	for _, c := range res.Candidates {
		keys = append(keys, c.PersonKey)
		dormant = append(dormant, c.Dormant)
	}
	return keys, dormant
}

// TestPersonLookup_DormantRankedLastAndLabeled: in a §P-2 candidate list a
// dormant person ranks last and is labeled — both when two active people hold
// the form exactly beside a dormant holder, and when only a dormant person
// holds it and two active people have it as their first name. A dormant person
// is never the answer while an active one holds the name.
func TestPersonLookup_DormantRankedLastAndLabeled(t *testing.T) {
	t.Run("two active exact holders beside a dormant one", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera", "Sam")
		jordan := f.person("Jordan Lee", "Sam")

		before, err := f.r.Person(PersonRequest{Name: "Sam"})
		require.NoError(t, err)
		require.True(t, before.MultipleMatches)
		_, flags := candidateKeysAndFlags(before)
		assert.Equal(t, []bool{false, false, false}, flags, "no one is dormant yet")

		f.dormant(oldSam)
		res, err := f.r.Person(PersonRequest{Name: "Sam"})
		require.NoError(t, err)
		require.True(t, res.MultipleMatches)
		assert.False(t, res.Matched)
		active := sortedKeys([]string{river, jordan})
		keys, flags := candidateKeysAndFlags(res)
		assert.Equal(t, []string{active[0], active[1], oldSam}, keys, "active first by key, the dormant holder last")
		assert.Equal(t, []bool{false, false, true}, flags)
		assert.Equal(t, "That matches more than one person — which did you mean: "+
			f.candidateRow(active[0], false)+"; "+f.candidateRow(active[1], false)+"; "+
			f.candidateRow(oldSam, true)+"?", res.Text)
		assert.Contains(t, res.Text, "Sam (first seen 2026-11-17, dormant)")
	})

	t.Run("only a dormant holder, two active first-name matches", func(t *testing.T) {
		f := newIDFixture(t)
		oldAlex := f.person("Alex")
		kim := f.person("Alex Kim")
		stone := f.person("Alex Stone")
		f.dormant(oldAlex)

		res, err := f.r.Person(PersonRequest{Name: "Alex"})
		require.NoError(t, err)
		require.True(t, res.MultipleMatches)
		active := sortedKeys([]string{kim, stone})
		keys, flags := candidateKeysAndFlags(res)
		assert.Equal(t, []string{active[0], active[1], oldAlex}, keys)
		assert.Equal(t, []bool{false, false, true}, flags)
		assert.True(t, strings.HasSuffix(res.Text, "; Alex (first seen 2026-11-17, dormant)?"), res.Text)
	})

	t.Run("a dormant person never beats an active holder", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera", "Sam")
		f.dormant(oldSam)

		res, err := f.r.Person(PersonRequest{Name: "Sam"})
		require.NoError(t, err)
		require.True(t, res.Matched)
		assert.Equal(t, river, res.PersonKey)
		assert.False(t, res.Dormant)
		assert.Empty(t, res.Candidates)
	})
}

// TestPersonLookup_ActiveMatchNamesDormantSameForm: a lookup that resolves to
// an active person adds one line right after the first naming each dormant
// person who also holds the queried form — one dormant person, several, and
// the first-name route alike — and a dormant person's own view (reached by
// key) carries the "Dormant since" line instead. A Ledger with no dormant
// people renders neither line.
func TestPersonLookup_ActiveMatchNamesDormantSameForm(t *testing.T) {
	t.Run("one dormant person shares the form", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera", "Sam")

		viewBefore, err := f.r.Person(PersonRequest{Name: river})
		require.NoError(t, err)
		f.dormant(oldSam)

		res, err := f.r.Person(PersonRequest{Name: "Sam"})
		require.NoError(t, err)
		require.True(t, res.Matched)
		assert.Equal(t, river, res.PersonKey)
		assert.False(t, res.Dormant)
		assert.Equal(t, []PersonRef{{PersonKey: oldSam, DisplayName: "Sam"}}, res.DormantAlso)
		lines := viewLines(res)
		require.GreaterOrEqual(t, len(lines), 3)
		assert.Equal(t, "Sam Rivera", lines[0])
		assert.Equal(t, fmt.Sprintf(`"Sam" also names a dormant person: "Sam" (%s).`, oldSam), lines[1])
		assert.True(t, strings.HasPrefix(lines[2], "Mentioned in "), lines[2])

		byKey, err := f.r.Person(PersonRequest{Name: river})
		require.NoError(t, err)
		assert.Equal(t, viewBefore.Text, byKey.Text, "a key lookup names no dormant form-sharer")
		assert.Empty(t, byKey.DormantAlso)

		own, err := f.r.Person(PersonRequest{Name: oldSam})
		require.NoError(t, err)
		require.True(t, own.Matched)
		assert.True(t, own.Dormant, "an exact key still reaches a dormant person")
		assert.Equal(t, []string{"Sam", "Dormant since 2026-11-17."}, viewLines(own)[:2])
	})

	t.Run("several dormant people share the form", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera", "Sam")
		f.dormant(oldSam)
		created, err := f.r.PersonCreate(PersonCreateRequest{Name: "Sam", Now: f.now()})
		require.NoError(t, err)
		secondSam := created.Record.PersonKey
		require.NotEqual(t, oldSam, secondSam, "a dormant-only holder mints the next suffix")
		f.dormant(secondSam)

		res, err := f.r.Person(PersonRequest{Name: "Sam"})
		require.NoError(t, err)
		assert.Equal(t, river, res.PersonKey)
		dormantKeys := sortedKeys([]string{oldSam, secondSam})
		assert.Equal(t, fmt.Sprintf(`"Sam" also names dormant people: "Sam" (%s); "Sam" (%s).`, dormantKeys[0], dormantKeys[1]),
			viewLines(res)[1])
		require.Len(t, res.DormantAlso, 2)
	})

	t.Run("the first-name route names the dormant holder", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera")

		before, err := f.r.Person(PersonRequest{Name: "Sam"})
		require.NoError(t, err)
		assert.Equal(t, oldSam, before.PersonKey, "a shared first name alone never moves a name")
		assert.Empty(t, before.DormantAlso)
		assert.NotContains(t, before.Text, "dormant")

		f.dormant(oldSam)
		res, err := f.r.Person(PersonRequest{Name: "Sam"})
		require.NoError(t, err)
		assert.Equal(t, river, res.PersonKey)
		assert.Equal(t, []string{"Sam Rivera", fmt.Sprintf(`"Sam" also names a dormant person: "Sam" (%s).`, oldSam)},
			viewLines(res)[:2])
	})

	t.Run("an off-limits view keeps the line after its header", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera", "Sam")
		f.dormant(oldSam)
		_, err := f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: river})
		require.NoError(t, err)

		res, err := f.r.Person(PersonRequest{Name: "Sam"})
		require.NoError(t, err)
		require.True(t, res.OffLimits)
		lines := viewLines(res)
		assert.True(t, strings.HasPrefix(lines[0], "Sam Rivera is off-limits to inference"), lines[0])
		assert.Equal(t, fmt.Sprintf(`"Sam" also names a dormant person: "Sam" (%s).`, oldSam), lines[1])
	})
}

// dueContactRouter seeds the contact fixture the due tests share: "Sam" with
// a 4w cadence set Oct 8 (closes Thu Nov 5: due in the week of Nov 2) and a
// birthday that week, and "Sam Rivera" with a 1w cadence set Oct 20 (closed
// Oct 27: still open). It returns the router, the adapter, and both keys.
func dueContactRouter(t *testing.T) (r *Router, a *storage.Adapter, sam, river string) {
	t.Helper()
	r, a, _ = newContactRouter(t)
	samDob := "1991-11-05"
	created, err := r.PersonCreate(PersonCreateRequest{Name: "Sam", Dob: &samDob, Now: ctAt(time.October, 1)})
	require.NoError(t, err)
	sam = created.Record.PersonKey
	river = createPerson(t, r, "Sam Rivera")
	cadence(t, r, "Sam", contact.ActionSet, "4w", ctAt(time.October, 8))
	cadence(t, r, "Sam Rivera", contact.ActionSet, "1w", ctAt(time.October, 20))
	return r, a, sam, river
}

// TestContactDue_ExcludesDormantUnlessAll: a dormant person leaves the due
// groups and the birthdays — in the full view and the wrap — while `--all`
// (ContactDueWith all) puts them back where their cadence and birthday place
// them, each line ending " (dormant)" and each JSON row carrying
// "dormant": true, a key no one else gets. Restoring them returns the plain
// view to what it was.
func TestContactDue_ExcludesDormantUnlessAll(t *testing.T) {
	r, _, sam, river := dueContactRouter(t)
	now := ctAt(time.November, 4)

	plainBefore, err := r.ContactDue(now, false)
	require.NoError(t, err)
	require.Contains(t, dueKeys(plainBefore.DueThisWeek), sam)
	require.Len(t, plainBefore.Birthdays, 1)
	textBefore := contact.RenderDueText(plainBefore)
	jsonBefore, err := json.Marshal(plainBefore)
	require.NoError(t, err)
	allNoDormant, err := r.ContactDueWith(now, false, true)
	require.NoError(t, err)
	assert.Equal(t, plainBefore, allNoDormant, "--all changes nothing while no one is dormant")

	_, err = r.PersonDormant(PersonDormantRequest{Subject: sam, Now: ctAt(time.November, 3)})
	require.NoError(t, err)

	for _, wrap := range []bool{false, true} {
		v, verr := r.ContactDue(now, wrap)
		require.NoError(t, verr)
		for _, group := range [][]contact.DueRow{v.DueThisWeek, v.StillOpen, v.ComingUp} {
			assert.NotContains(t, dueKeys(group), sam, "wrap=%v", wrap)
		}
		assert.Empty(t, v.Birthdays, "wrap=%v", wrap)
		assert.Equal(t, []string{river}, dueKeys(v.StillOpen), "wrap=%v", wrap)
		assert.Equal(t, 1, v.ActiveCadences, "a dormant cadence is not counted (wrap=%v)", wrap)
		raw, merr := json.Marshal(v)
		require.NoError(t, merr)
		text := contact.RenderDueText(v)
		if wrap {
			text = contact.RenderWrapText(v)
		}
		for _, surface := range []string{string(raw), text} {
			assert.NotContains(t, surface, sam, "wrap=%v", wrap)
			assert.NotContains(t, surface, "dormant", "wrap=%v", wrap)
			assert.Contains(t, surface, "Sam Rivera", "wrap=%v", wrap)
		}
	}

	all, err := r.ContactDueWith(now, false, true)
	require.NoError(t, err)
	require.Len(t, all.DueThisWeek, 1)
	assert.Equal(t, sam, all.DueThisWeek[0].PersonKey)
	assert.True(t, all.DueThisWeek[0].Dormant)
	require.Len(t, all.StillOpen, 1)
	assert.False(t, all.StillOpen[0].Dormant)
	require.Len(t, all.Birthdays, 1)
	assert.Equal(t, contact.BirthdayRow{PersonKey: sam, DisplayName: "Sam", Date: "2026-11-05", Dormant: true}, all.Birthdays[0])
	assert.Equal(t, 2, all.ActiveCadences)

	text := contact.RenderDueText(all)
	assert.Contains(t, text, "- Sam (every 4 weeks), not logged yet (dormant)")
	assert.Contains(t, text, "- Sam — Thu Nov 5 (dormant)")
	assert.Contains(t, text, "- Sam Rivera (every week), not logged yet\n", "an active row is unlabeled")
	raw, err := json.Marshal(all)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(raw), `"dormant":true`), "the dormant row and birthday only")
	assert.NotContains(t, string(raw), `"dormant":false`)

	wrapAll, err := r.ContactDueWith(now, true, true)
	require.NoError(t, err)
	assert.Equal(t, []string{sam}, dueKeys(wrapAll.DueThisWeek))
	assert.Contains(t, contact.RenderWrapText(wrapAll), "- Sam (every 4 weeks), not logged yet (dormant)")

	_, err = r.PersonDormant(PersonDormantRequest{Subject: sam, Restore: true, Now: ctAt(time.November, 3)})
	require.NoError(t, err)
	restored, err := r.ContactDue(now, false)
	require.NoError(t, err)
	jsonRestored, err := json.Marshal(restored)
	require.NoError(t, err)
	assert.Equal(t, textBefore, contact.RenderDueText(restored), "restore returns the view exactly as it was")
	assert.JSONEq(t, string(jsonBefore), string(jsonRestored))
}

// TestContactWeekly_ExcludesDormant: the Monday post, driven through the real
// weekly node over the real router, never names a dormant person — not in its
// lists and not among the birthdays — though the same person was in the post
// before they went dormant. The node has no --all.
func TestContactWeekly_ExcludesDormant(t *testing.T) {
	_, a := lucidtest.Ledger(t, lucidtest.NestedHome(), lucidtest.WithEngine())
	r := New(a)
	_, err := r.Boot()
	require.NoError(t, err)
	samDob := "1991-11-05"
	created, err := r.PersonCreate(PersonCreateRequest{Name: "Sam", Dob: &samDob, Now: ctAt(time.October, 1)})
	require.NoError(t, err)
	sam := created.Record.PersonKey
	createPerson(t, r, "Sam Rivera")
	cadence(t, r, "Sam", contact.ActionSet, "4w", ctAt(time.October, 8))
	cadence(t, r, "Sam Rivera", contact.ActionSet, "1w", ctAt(time.October, 20))

	mark := time.Date(2026, time.November, 2, 9, 0, 0, 0, time.FixedZone("EST", -5*3600)) // Monday of 2026-W45
	beforeView, err := r.ContactDue(mark, false)
	require.NoError(t, err)
	beforePost, ok := contact.RenderMondayPost(beforeView)
	require.True(t, ok)
	require.Contains(t, beforePost, "- Sam (every 4 weeks), not logged yet")
	require.Contains(t, beforePost, "Sam (Thu Nov 5)", "their birthday was in the post")

	_, err = r.PersonDormant(PersonDormantRequest{Subject: sam, Now: ctAt(time.November, 1)})
	require.NoError(t, err)

	del := &lucidtest.FakeDeliverer{}
	runner := contactweekly.NewRunner(contactweekly.Options{
		Store: a, Config: config.DefaultContactWeekly(), Due: r, Notifier: del,
	})
	out, err := runner.Fire(context.Background(), mark)
	require.NoError(t, err)
	require.True(t, out.Delivered, "Sam Rivera is still open, so the week posts")
	require.Len(t, del.Sends, 1)
	post := del.Sends[0].Text
	assert.Contains(t, post, "- Sam Rivera (every week), not logged yet")
	assert.NotContains(t, post, "- Sam (every")
	assert.NotContains(t, post, "Birthdays", "the only birthday this week was the dormant person's")
	assert.NotContains(t, post, "dormant")
}

// TestLinkAck_DormantLabel: link and attach stay key-only, and their acks mark
// a dormant person by following their person:<key> with " (dormant)" — in a
// link, an already-linked no-op, an unlink, and an attach --to. Every other
// token, and every ack before anyone is dormant, renders exactly as before.
func TestLinkAck_DormantLabel(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")
	river := f.person("Sam Rivera")
	mediaID := seedLinkMedia(t, f.a)

	first, err := f.r.Link(LinkRequest{
		Media: mediaID, Subjects: []string{"person:" + oldSam, "day:2026-07-05"}, Op: storage.LinkOpLink, Now: fixedNow(),
	})
	require.NoError(t, err)
	assert.Equal(t, mediaID+": Linked to person:"+oldSam+", day:2026-07-05.", first.Ack)
	assert.False(t, first.Applied[0].Dormant)

	f.dormant(oldSam)
	again, err := f.r.Link(LinkRequest{
		Media: mediaID, Subjects: []string{"person:" + oldSam, "person:" + river}, Op: storage.LinkOpLink, Now: fixedNow(),
	})
	require.NoError(t, err)
	assert.Equal(t, mediaID+": Linked to person:"+river+"; already linked to person:"+oldSam+" (dormant).", again.Ack)
	assert.True(t, again.Applied[0].Dormant)
	assert.False(t, again.Applied[1].Dormant)

	unl, err := f.r.Link(LinkRequest{Media: mediaID, Subjects: []string{"person:" + oldSam}, Op: storage.LinkOpUnlink, Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, mediaID+": Unlinked from person:"+oldSam+" (dormant).", unl.Ack)

	path := writeTempFile(t, "trail.jpg", []byte("\xff\xd8\xff synthetic trail bytes"))
	att, err := f.r.Attach(AttachRequest{Path: path, Now: fixedNow(), Subjects: []string{"person:" + oldSam, "person:" + river}})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(att.Ack, " Linked to person:"+oldSam+" (dormant), person:"+river+"."), att.Ack)
	require.Len(t, att.Linked, 2)
	assert.True(t, att.Linked[0].Dormant)

	_, err = f.r.PersonDormant(PersonDormantRequest{Subject: oldSam, Restore: true, Now: f.now()})
	require.NoError(t, err)
	relink, err := f.r.Link(LinkRequest{Media: mediaID, Subjects: []string{"person:" + oldSam}, Op: storage.LinkOpLink, Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, mediaID+": Linked to person:"+oldSam+".", relink.Ack, "a restored person is unlabeled again")
}

// TestContactSubject_WhoItMeansNow: a contact verb's subject resolves to who
// the name means now, not to a record — a bare "Sam" whose only holder went
// dormant reaches the one active Sam by first name, a claim routes it, and an
// exact key still reaches a dormant person. No match keeps the person create
// pointer (Ct-1) and two active holders keep the candidate copy (Ct-2).
func TestContactSubject_WhoItMeansNow(t *testing.T) {
	t.Run("dormant, first name, claim, key", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera")

		res, err := f.r.ContactLog(ContactLogRequest{Subject: "Sam", Now: f.now()})
		require.NoError(t, err)
		assert.Equal(t, oldSam, res.PersonKey, "a shared first name alone never moves the subject")

		f.dormant(oldSam)
		res, err = f.r.ContactLog(ContactLogRequest{Subject: "Sam", Now: f.now()})
		require.NoError(t, err)
		assert.Equal(t, river, res.PersonKey, "the one active Sam by first name")
		assert.Equal(t, "Sam Rivera", res.DisplayName)

		cad, err := f.r.ContactCadence(ContactCadenceRequest{Subject: "Sam", Action: contact.ActionSet, Window: "4w", Now: f.now()})
		require.NoError(t, err)
		assert.Equal(t, river, cad.PersonKey)

		res, err = f.r.ContactLog(ContactLogRequest{Subject: oldSam, Now: f.now()})
		require.NoError(t, err)
		assert.Equal(t, oldSam, res.PersonKey, "an exact key still reaches a dormant person")

		_, err = f.r.PersonDormant(PersonDormantRequest{Subject: oldSam, Restore: true, Now: f.now()})
		require.NoError(t, err)
		f.claim("Sam Rivera", "Sam", true)
		res, err = f.r.ContactLog(ContactLogRequest{Subject: "Sam", Now: f.now()})
		require.NoError(t, err)
		assert.Equal(t, river, res.PersonKey, "the form's active claimant wins over an active exact holder")
	})

	t.Run("refusals keep their copy", func(t *testing.T) {
		f := newIDFixture(t)
		oldSam := f.person("Sam")
		river := f.person("Sam Rivera", "Sam")

		_, err := f.r.ContactLog(ContactLogRequest{Subject: "Sam", Now: f.now()})
		requireContactRefusal(t, err, ErrPersonAmbiguousSubject, fmt.Sprintf(
			`"Sam" matches more than one person — disambiguate with a person_key (%s); nothing was saved.`,
			strings.Join(sortedKeys([]string{oldSam, river}), " / "),
		))

		_, err = f.r.ContactLog(ContactLogRequest{Subject: "Nobody", Now: f.now()})
		requireContactRefusal(t, err, ErrPersonUnknownSubject,
			`no one matches "Nobody" — add them first with lucid person create <name>; nothing was saved.`)

		f.dormant(oldSam)
		res, err := f.r.ContactLog(ContactLogRequest{Subject: "Sam", Now: f.now()})
		require.NoError(t, err)
		assert.Equal(t, river, res.PersonKey, "with the other holder dormant the name settles")
	})
}

// TestGratitudePerson_WhoItMeansNow: gratitude `add --person` and `thank`
// resolve their subject to who the name means now — a bare "Sam" whose only
// holder is dormant links the one active Sam by first name, and an exact key
// still reaches a dormant person — while two active holders are refused naming
// the candidates and nothing is written.
func TestGratitudePerson_WhoItMeansNow(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.person("Sam")
	river := f.person("Sam Rivera")
	f.dormant(oldSam)

	added, err := f.r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "coffee with Sam on the porch", Person: "Sam", Now: f.now()})
	require.NoError(t, err)
	assert.Equal(t, river, added.PersonKey)
	assert.Equal(t, "Sam Rivera", added.PersonName)

	thanked, err := f.r.ThankGratitude(ThankGratitudeRequest{ID: added.Key, Person: "Sam", Now: f.now()})
	require.NoError(t, err)
	assert.Equal(t, river, thanked.PersonKey)

	byKey, err := f.r.ThankGratitude(ThankGratitudeRequest{ID: added.Key, Person: oldSam, Now: f.now()})
	require.NoError(t, err)
	assert.Equal(t, oldSam, byKey.PersonKey, "an exact key still reaches a dormant person")

	g := newIDFixture(t)
	sam := g.person("Sam")
	alsoSam := g.person("Sam Rivera", "Sam")
	before := snapshotTree(t, g.home)
	_, err = g.r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "a long walk", Person: "Sam", Now: g.now()})
	require.ErrorIs(t, err, ErrPersonAmbiguousSubject)
	assert.Equal(t, fmt.Sprintf(`"Sam" matches more than one person — disambiguate with a person_key (%s); nothing was saved.`,
		strings.Join(sortedKeys([]string{sam, alsoSam}), " / ")), err.Error())
	after := snapshotTree(t, g.home)
	delete(after, "observations/config.json") // the idempotent observations scaffold, run before any resolution
	assert.Equal(t, before, after, "a refused --person starts no entry and writes no identity event")
}

// enableMemory turns the memory kind on for a fixture's Ledger, as
// `lucid day` would.
func enableMemory(t *testing.T, a *storage.Adapter) {
	t.Helper()
	require.NoError(t, a.ScaffoldObservations())
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = append(cfg.KindsEnabled, observations.KindMemory)
	require.NoError(t, a.SaveObservationsConfig(cfg))
}

// TestMemoryLink_WhoItMeansNow: a story's --people links each name that
// resolves to exactly one person now — a bare "Sam" whose only holder is
// dormant links the one active Sam, an exact key links even a dormant person —
// and leaves a name that is ambiguous or matches no one as testimony only.
func TestMemoryLink_WhoItMeansNow(t *testing.T) {
	f := newIDFixture(t)
	enableMemory(t, f.a)
	oldSam := f.person("Sam")
	river := f.person("Sam Rivera")
	alex := f.person("Alex")
	f.person("Jordan Lee", "Alex")

	res, err := f.r.WriteMemory(MemoryWriteRequest{Text: "the porch summer", People: []string{"Sam"}, Now: f.now()})
	require.NoError(t, err)
	assert.Equal(t, []string{oldSam}, res.Refs["person"], "before anyone is dormant a bare Sam is the old Sam")

	f.dormant(oldSam)
	res, err = f.r.WriteMemory(MemoryWriteRequest{
		Text: "the lake trip", People: []string{"Sam", oldSam, "Alex", "Nobody", "  "}, Now: f.now(),
	})
	require.NoError(t, err)
	assert.Equal(t, sortedKeys([]string{river, oldSam}), res.Refs["person"],
		"Sam means Sam Rivera now; the key links the dormant Sam; the ambiguous Alex and the unknown name link no one")
	assert.NotContains(t, res.Refs["person"], alex)
}
