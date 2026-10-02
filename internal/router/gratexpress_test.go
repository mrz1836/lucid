package router

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// gratexpress_test.go covers outward expression (gratitude.md §8): `thank`,
// `add --person`, the linked people and last-expressed dates `list` shows, the
// gentle reminders, and the off-limits rules — a person marked off-limits may be
// linked and thanked, but is never named in a reminder, and an entry linked to
// one never reaches the tier-3 judge (gratitude.md §7.5). Every person, phrase,
// and key here is synthetic.

// julyDay is 21:45 EDT on the given day of July 2026 — a logical day of its own
// (well before the 04:00 rollover), extending the day1..day3 fixtures.
func julyDay(d int) time.Time { return time.Date(2026, time.July, d, 21, 45, 0, 0, edt) }

// seedGratitudePerson writes a synthetic live person record whose aka[] carries
// the display name plus any extra forms, and returns its key.
func seedGratitudePerson(t *testing.T, r *Router, key, display string, aka ...string) string {
	t.Helper()
	writePersonFile(t, r.store.Home(), key, display, append([]string{display}, aka...), personSeedTime())
	return key
}

// markOffLimits marks the given person keys off-limits, as `lucid person
// off-limits` does.
func markOffLimits(t *testing.T, r *Router, keys ...string) {
	t.Helper()
	require.NoError(t, r.store.WriteOffLimitsPersonKeys(keys))
}

// listEntry returns the list row for id, failing the test when it is absent.
func listEntry(t *testing.T, res GratitudeListResult, id string) GratitudeListEntry {
	t.Helper()
	for _, e := range res.View.Entries {
		if e.ID == id {
			return e
		}
	}
	require.Failf(t, "entry not listed", "%q is not in the list", id)
	return GratitudeListEntry{}
}

// TestGratitudeThank_LinksAndRecordsExpressed: `thank` resolves the person like
// every person write verb (a name or a key), links the canonical key onto the
// entry once, and appends a tally-neutral `expressed` event with its own receipt
// at the logical day — backdatable with the strict `--day` tier (gratitude.md
// §8). The count, first, and last never move.
func TestGratitudeThank_LinksAndRecordsExpressed(t *testing.T) {
	r := bootedGratitude(t)
	sam := seedGratitudePerson(t, r, "person_a-river", "Sam Rivera", "Sam")
	coffee := addGratitude(t, r, "coffee with Sam on the porch", day1())
	addGratitude(t, r, "coffee with Sam on the porch", day2())

	res, err := r.ThankGratitude(ThankGratitudeRequest{ID: coffee.Key, Person: "Sam Rivera", Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, sam, res.PersonKey)
	assert.Equal(t, "Sam Rivera", res.PersonName)
	assert.Equal(t, coffee.Key, res.Key)
	assert.Equal(t, "coffee with Sam on the porch", res.Thing)
	assert.Equal(t, "2026-07-04", res.Date)
	assert.Equal(t, 2, res.Count, "expressing gratitude never moves the tally")
	assert.Equal(t, "grat_2026_07_04_001", res.Receipt, "the first receipt for its logical date")
	assert.Equal(t,
		"Noted that you told Sam Rivera about \"coffee with Sam on the porch\" (2026-07-04) as `grat_2026_07_04_001` — the tally stays ×2.",
		res.Ack)

	entry := gratitudeEntry(t, r, coffee.Key)
	assert.Equal(t, []string{sam}, entry.People)
	last := entry.History[len(entry.History)-1]
	assert.Equal(t, observations.GratitudeEventExpressed, last.Type)
	assert.Equal(t, sam, last.Person)
	assert.Equal(t, observations.GratitudeTally{Count: 2, First: "2026-07-02", Last: "2026-07-03"}, entry.Tally())

	again, err := r.ThankGratitude(ThankGratitudeRequest{ID: coffee.Key, Person: sam, DayArg: "@yesterday", Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, "2026-07-03", again.Date, "--day backdates when you told them")
	assert.NotEqual(t, res.Receipt, again.Receipt, "every thank-you returns its own receipt")
	assert.Equal(t, []string{sam}, gratitudeEntry(t, r, coffee.Key).People, "the link is added once")
	assert.Equal(t, 2, again.Count)
}

// TestGratitudeThank_ValidatesBeforeWriting: every refusal is checked before
// anything is written (gratitude.md §8) — a blank or unknown id, a merge
// tombstone, a missing person, a person no one matches, a name several people
// share (the candidate keys named), and a rejected `--day` each leave the
// gratitude tree byte-for-byte unchanged.
func TestGratitudeThank_ValidatesBeforeWriting(t *testing.T) {
	r := bootedGratitude(t)
	seedGratitudePerson(t, r, "person_a-river", "Sam Rivera")
	seedGratitudePerson(t, r, "person_b-stone", "Alex Stone", "Alex")
	seedGratitudePerson(t, r, "person_c-field", "Alex Field", "Alex")
	roof := addGratitude(t, r, "a roof over my head", day1())
	house := addGratitude(t, r, "the house on the hill", day1())
	_, err := r.MergeGratitude(GratitudeMergeRequest{Source: house.Key, Target: roof.Key, Now: day2()})
	require.NoError(t, err)

	cases := []struct {
		name, id, person, day string
		want                  string
		is                    error
	}{
		{name: "blank id", id: " ", person: "Sam Rivera", want: "needs an entry id"},
		{name: "unknown id", id: "gratitude_z-nowhere", person: "Sam Rivera", want: "no live gratitude entry"},
		{name: "tombstone", id: house.Key, person: "Sam Rivera", want: "was merged into"},
		{name: "no person", id: roof.Key, person: "  ", want: "needs --person"},
		{name: "unknown person", id: roof.Key, person: "Robin Nobody", is: ErrPersonUnknownSubject},
		{name: "ambiguous person", id: roof.Key, person: "Alex", want: "person_b-stone / person_c-field", is: ErrPersonAmbiguousSubject},
		{name: "future day", id: roof.Key, person: "Sam Rivera", day: "@2026-07-09", want: "has not happened yet"},
		{name: "unreadable day", id: roof.Key, person: "Sam Rivera", day: "@someday", want: "could not read the day"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := gratitudeFiles(t, r)
			_, terr := r.ThankGratitude(ThankGratitudeRequest{ID: tc.id, Person: tc.person, DayArg: tc.day, Now: day3()})
			require.Error(t, terr)
			if tc.want != "" {
				assert.Contains(t, terr.Error(), tc.want)
			}
			if tc.is != nil {
				require.ErrorIs(t, terr, tc.is)
			}
			assert.Contains(t, terr.Error(), "nothing was saved")
			assert.Equal(t, before, gratitudeFiles(t, r), "nothing was written")
		})
	}
}

// TestGratitudeAddPerson_LinkOnly: `add --person` links the person onto the
// entry the occurrence lands on — however it landed — and stamps the person on
// the occurrence, but records no expressed event and counts +1 like any add
// (gratitude.md §3, §8). An unknown person is refused before anything is
// written, so no entry is started.
func TestGratitudeAddPerson_LinkOnly(t *testing.T) {
	r := bootedGratitude(t)
	sam := seedGratitudePerson(t, r, "person_a-river", "Sam Rivera", "Sam")
	alex := seedGratitudePerson(t, r, "person_b-stone", "Alex Stone")

	res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "coffee with Sam on the porch", Person: "Sam", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, sam, res.PersonKey)
	assert.Equal(t, "Sam Rivera", res.PersonName)
	assert.True(t, res.Created)
	assert.Equal(t, 1, res.Count)
	assert.Equal(t, fmt.Sprintf("Started tally for %q (×1) as `%s`. Linked to Sam Rivera.", "coffee with Sam on the porch", res.Receipt), res.Ack)

	entry := gratitudeEntry(t, r, res.Key)
	assert.Equal(t, []string{sam}, entry.People)
	assert.Equal(t, sam, entry.History[0].Person, "the occurrence names the person it linked")
	assert.Empty(t, entry.LastExpressed(), "linking is not expressing")

	plain := addGratitude(t, r, "coffee with Sam on the porch", day2())
	assert.Empty(t, plain.PersonKey)
	assert.Equal(t, fmt.Sprintf("Tallied %q (×2) as `%s`.", "coffee with Sam on the porch", plain.Receipt), plain.Ack,
		"an add without --person keeps its ack")

	into, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "porch coffee", Into: res.Key, Person: alex, Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, 3, into.Count)
	assert.Equal(t, []string{sam, alex}, gratitudeEntry(t, r, res.Key).People, "links grow, in link order")

	before := gratitudeFiles(t, r)
	_, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "a brand new thing", Person: "Robin Nobody", Now: day3()})
	require.ErrorIs(t, err, ErrPersonUnknownSubject)
	assert.Equal(t, before, gratitudeFiles(t, r), "an unknown person writes nothing — no entry is started")
}

// TestGratitudeAddPerson_OffLimitsWithholdsJudge: an `add --person` naming an
// off-limits person skips tier 3 for that add (gratitude.md §7.5): the judge is
// never called, the tier-2 decision stands, and the add reports tier3
// "off_limits". The same add naming a person who is not off-limits reaches the
// judge.
func TestGratitudeAddPerson_OffLimitsWithholdsJudge(t *testing.T) {
	r := bootedTier3(t)
	addGratitude(t, r, "the two wheels that carry me to work", day1())
	dana := seedGratitudePerson(t, r, "person_d-vale", "Dana Vale")
	sam := seedGratitudePerson(t, r, "person_a-river", "Sam Rivera")
	markOffLimits(t, r, dana)

	withheld := &provider.Fake{Script: []provider.Exchange{{Content: `{"matches": [{"n": 1, "score": 1}]}`}}}
	res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my bike", Person: dana, Provider: withheld, Now: day2()})
	require.NoError(t, err)
	assert.Zero(t, withheld.Calls(), "no wording reaches a model on an add linking an off-limits person")
	assert.Equal(t, GratitudeTier3OffLimits, res.Tier3)
	assert.True(t, res.Created, "the tier-2 Low decision stands")
	assert.Equal(t, dana, res.PersonKey, "linking an off-limits person is allowed")

	asked := &provider.Fake{Script: []provider.Exchange{{Content: `{"matches": []}`}}}
	res, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "morning light", Person: sam, Provider: asked, Now: day2()})
	require.NoError(t, err)
	assert.Equal(t, 1, asked.Calls(), "a person who is not off-limits leaves tier 3 as it was")
	assert.Equal(t, GratitudeTier3Used, res.Tier3)
}

// TestGratitudeJudgeWithholdsOffLimitsEntries: an entry linked to an off-limits
// person is withheld from the tier-3 judge, fail closed (gratitude.md §7.5, §8) —
// at add time and in a reconcile dry run. The check follows person redirects, so
// a link stored under a key later merged into an off-limits person is withheld
// too. Tiers 1–2 still match a withheld entry deterministically.
func TestGratitudeJudgeWithholdsOffLimitsEntries(t *testing.T) {
	r := bootedTier3(t)
	dana := seedGratitudePerson(t, r, "person_d-vale", "Dana Vale")
	sam := seedGratitudePerson(t, r, "person_a-river", "Sam Rivera")
	finn := seedGratitudePerson(t, r, "person_f-moss", "Finn Moss")

	porch, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "coffee with Dana on the porch", Person: dana, Now: day1()})
	require.NoError(t, err)
	gate, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the garden gate we built", Person: finn, Now: day1()})
	require.NoError(t, err)
	walk, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "an evening walk by the lake", Person: sam, Now: day1()})
	require.NoError(t, err)
	water := addGratitude(t, r, "clean drinking water from the tap", day2())
	markOffLimits(t, r, dana)
	// Finn is later merged into Dana: the gate's stored link now resolves to an
	// off-limits person, and must be withheld as well.
	writeTombstone(t, r.store.Home(), finn, "Finn Moss", dana, personSeedTime())

	fake := &provider.Fake{Script: []provider.Exchange{{Content: `{"matches": []}`}}}
	_, err = r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my bike", Provider: fake, Now: day3()})
	require.NoError(t, err)
	require.Len(t, fake.Requests, 1)
	var sent []string
	for _, it := range judgeInput(t, fake.Requests[0]).Items {
		sent = append(sent, it.Wordings...)
	}
	assert.ElementsMatch(t, []string{walk.Thing, water.Thing}, sent, "only entries free of off-limits links are judged")
	for _, withheld := range []string{porch.Thing, gate.Thing, "Dana", "Finn"} {
		assert.NotContains(t, fake.Requests[0].Messages[0].Content, withheld)
	}

	reconcile := &provider.Fake{Script: []provider.Exchange{{Content: `{"pairs": []}`}}}
	_, err = r.ReconcileGratitude(t.Context(), ReconcileGratitudeRequest{Provider: reconcile, Now: day3()})
	require.NoError(t, err)
	require.Len(t, reconcile.Requests, 1)
	var in gratitudeReconcileInput
	require.NoError(t, json.Unmarshal([]byte(reconcile.Requests[0].Messages[0].Content), &in))
	sent = sent[:0]
	for _, it := range in.Items {
		sent = append(sent, it.Wordings...)
	}
	assert.NotContains(t, sent, porch.Thing, "reconcile withholds the off-limits entry too")
	assert.NotContains(t, sent, gate.Thing)
	assert.Contains(t, sent, walk.Thing)

	// Tier 2 still reaches a withheld entry deterministically: a plural of its
	// wording is a clear by-wording match, with no model call.
	unused := &provider.Fake{}
	bump, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "coffees with Dana on the porch", Provider: unused, Now: day3()})
	require.NoError(t, err)
	assert.Equal(t, porch.Key, bump.Key)
	assert.Equal(t, 2, bump.MatchTier)
	assert.Zero(t, unused.Calls())
}

// TestGratitudeJudgeAllWithheld: when every live entry is linked to an
// off-limits person there is nothing the judge may see — no call is made and the
// add reports tier3 "off_limits" on the tier-2 decision (gratitude.md §7.5).
func TestGratitudeJudgeAllWithheld(t *testing.T) {
	r := bootedTier3(t)
	dana := seedGratitudePerson(t, r, "person_d-vale", "Dana Vale")
	_, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "coffee with Dana on the porch", Person: dana, Now: day1()})
	require.NoError(t, err)
	markOffLimits(t, r, dana)

	fake := &provider.Fake{}
	res, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "my bike", Provider: fake, Now: day2()})
	require.NoError(t, err)
	assert.Zero(t, fake.Calls())
	assert.Equal(t, GratitudeTier3OffLimits, res.Tier3)
	assert.True(t, res.Created)
}

// TestGratitudeListPeople: `list` shows each linked person — resolved forward
// through person redirects, with their display name, the off-limits flag, and
// when you last told them about that entry ("" when never) — and an unlinked
// entry carries an empty people array, never null (gratitude.md §6, §8). Two
// stored keys that now resolve to one person collapse into a single row with the
// later date, and a merge carries its source's links and dates onto the target.
func TestGratitudeListPeople(t *testing.T) {
	r := bootedGratitude(t)
	sam := seedGratitudePerson(t, r, "person_a-river", "Sam Rivera")
	finn := seedGratitudePerson(t, r, "person_f-moss", "Finn Moss")
	dana := seedGratitudePerson(t, r, "person_d-vale", "Dana Vale")
	markOffLimits(t, r, dana)

	gate, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "the garden gate we built", Person: finn, Now: day1()})
	require.NoError(t, err)
	_, err = r.ThankGratitude(ThankGratitudeRequest{ID: gate.Key, Person: finn, Now: day2()})
	require.NoError(t, err)
	// Finn is later merged into Sam; thanking Sam now links Sam's canonical key.
	writeTombstone(t, r.store.Home(), finn, "Finn Moss", sam, personSeedTime())
	_, err = r.ThankGratitude(ThankGratitudeRequest{ID: gate.Key, Person: sam, Now: day1()})
	require.NoError(t, err)

	porch, err := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "coffee with Dana on the porch", Person: dana, Now: day1()})
	require.NoError(t, err)
	water := addGratitude(t, r, "clean drinking water from the tap", day2())

	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, []GratitudeListPerson{{PersonKey: sam, DisplayName: "Sam Rivera", LastExpressed: "2026-07-03"}},
		listEntry(t, list, gate.Key).People, "one row per canonical person, the later date kept")
	assert.Equal(t, []GratitudeListPerson{{PersonKey: dana, DisplayName: "Dana Vale", OffLimits: true}},
		listEntry(t, list, porch.Key).People, "an off-limits link still shows on its entry, flagged")
	unlinked := listEntry(t, list, water.Key).People
	assert.NotNil(t, unlinked)
	assert.Empty(t, unlinked)

	lines := strings.Join(list.Lines, "\n")
	assert.Contains(t, lines, "the garden gate we built  (last 2026-07-02)  · with Sam Rivera (told 2026-07-03)")
	assert.Contains(t, lines, "coffee with Dana on the porch  (last 2026-07-02)  · with Dana Vale (off-limits)")
	assert.Contains(t, lines, "clean drinking water from the tap  (last 2026-07-03)\n", "an unlinked row renders as before")

	// A merge carries the source's links and dates onto the target.
	_, err = r.MergeGratitude(GratitudeMergeRequest{Source: gate.Key, Target: water.Key, Now: day3()})
	require.NoError(t, err)
	list, err = r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, []GratitudeListPerson{{PersonKey: sam, DisplayName: "Sam Rivera", LastExpressed: "2026-07-03"}},
		listEntry(t, list, water.Key).People)
}

// TestGratitudeReminderRules: the gentle reminders (gratitude.md §8) offer a
// linked, not-off-limits person for an entry when you have never recorded
// telling them, or when the entry has been tallied again since you last did;
// they never name an off-limits person; and they stop at three lines, most
// recently tallied entries first, with nothing saying how many others there are.
// An unlinked tally has none: an empty array, and no reminder lines at all.
func TestGratitudeReminderRules(t *testing.T) {
	r := bootedGratitude(t)
	empty, err := r.GratitudeList()
	require.NoError(t, err)
	assert.NotNil(t, empty.View.Reminders)
	assert.Empty(t, empty.View.Reminders)

	addGratitude(t, r, "clean drinking water from the tap", julyDay(1))
	unlinked, err := r.GratitudeList()
	require.NoError(t, err)
	assert.NotNil(t, unlinked.View.Reminders)
	assert.Empty(t, unlinked.View.Reminders)
	assert.NotContains(t, strings.Join(unlinked.Lines, "\n"), "You might tell")

	people := []struct{ key, name, thing string }{
		{"person_a-river", "Sam Rivera", "coffee with Sam on the porch"},
		{"person_b-stone", "Alex Stone", "the long call with Alex"},
		{"person_c-field", "Robin Field", "a letter from Robin"},
		{"person_e-lark", "Jo Lark", "a ride home from Jo"},
		{"person_g-reed", "Kit Reed", "Kit fixing the bike"},
		{"person_d-vale", "Dana Vale", "coffee with Dana"},
	}
	keys := make(map[string]string, len(people))
	for i, p := range people {
		seedGratitudePerson(t, r, p.key, p.name)
		res, aerr := r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: p.thing, Person: p.key, Now: julyDay(2 + i)})
		require.NoError(t, aerr)
		keys[p.key] = res.Key
	}
	markOffLimits(t, r, "person_d-vale") // the most recently tallied entry — never offered

	// Told Kit after the last tally: not offered. Told Jo, then tallied again: offered.
	_, err = r.ThankGratitude(ThankGratitudeRequest{ID: keys["person_g-reed"], Person: "person_g-reed", Now: julyDay(8)})
	require.NoError(t, err)
	_, err = r.ThankGratitude(ThankGratitudeRequest{ID: keys["person_e-lark"], Person: "person_e-lark", Now: julyDay(6)})
	require.NoError(t, err)
	addGratitude(t, r, "a ride home from Jo", julyDay(9))

	list, err := r.GratitudeList()
	require.NoError(t, err)
	assert.Equal(t, []GratitudeReminder{
		{PersonKey: "person_e-lark", DisplayName: "Jo Lark", EntryID: keys["person_e-lark"], Thing: "a ride home from Jo"},
		{PersonKey: "person_c-field", DisplayName: "Robin Field", EntryID: keys["person_c-field"], Thing: "a letter from Robin"},
		{PersonKey: "person_b-stone", DisplayName: "Alex Stone", EntryID: keys["person_b-stone"], Thing: "the long call with Alex"},
	}, list.View.Reminders, "most recently tallied first, capped at three; off-limits and already-told skipped")

	i := slices.Index(list.Lines, "You might tell:")
	require.Positive(t, i, "the reminders follow the tally")
	assert.Empty(t, list.Lines[i-1], "a blank line sets them apart")
	assert.Equal(t, []string{
		fmt.Sprintf("  Jo Lark — %q (%s)", "a ride home from Jo", keys["person_e-lark"]),
		fmt.Sprintf("  Robin Field — %q (%s)", "a letter from Robin", keys["person_c-field"]),
		fmt.Sprintf("  Alex Stone — %q (%s)", "the long call with Alex", keys["person_b-stone"]),
	}, list.Lines[i+1:], "one quiet line each, and nothing after them")
}
