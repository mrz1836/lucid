package router

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/identity"
	"github.com/mrz1836/lucid/internal/storage"
)

// person_resolve_test.go guards the router's single name-resolution seam
// (person_resolve.go; data-model.md §"Resolving a bare name"; ADR-0016): one
// table of cases runs through every surface that takes a person, and each
// surface must give its documented outcome for that case — capture, the
// who-it-means-now reads, record targeting, link/attach, and reconcile. A
// source scan keeps every other router file off the low-level matchers. Every
// person here is synthetic: two people named "Sam" ("Sam" and "Sam Rivera"),
// plus "Alex", "Alex Kim", "Alex Stone", "Andy", "Jordan Lee", and "Riley
// Moss".

// consistencyOutcome is the kind of answer one surface gives for a case.
type consistencyOutcome string

const (
	// outKey: the name resolves to one person (want.who).
	outKey consistencyOutcome = "key"
	// outHeld: capture holds the mention (§P-11) for want.reason, naming the
	// people in want.candidates.
	outHeld consistencyOutcome = "held"
	// outMint: capture mints a fresh record — under the next free collision
	// suffix of want.who when it is set, else at the form's own derived key.
	outMint consistencyOutcome = "mint"
	// outAmbiguous: the name could mean any of want.candidates — §P-2 on
	// lookup, a §P-5 refusal on a subject surface, no link from memory.
	outAmbiguous consistencyOutcome = "ambiguous"
	// outNone: no one holds the name — §P-1 on lookup, a §P-4 refusal on a
	// subject surface, no link from memory.
	outNone consistencyOutcome = "none"
)

// surfaceWant is one surface's expected answer for a case. who and
// candidates name people by the labels the case's setup returns.
type surfaceWant struct {
	kind       consistencyOutcome
	who        string
	dormant    bool
	reason     string
	candidates []string
}

// reconcileWant is what `person reconcile` lists for a case's form after one
// capture of it: a held mention, a first-name overlap, and/or a dormant
// look-alike of the labeled dormant person.
type reconcileWant struct {
	held         bool
	overlap      bool
	dormantShare string
}

// consistencyCase is one row: a Ledger (setup returns its people by label),
// the bare form under test, and every surface's documented outcome. now is
// the who-it-means-now answer shared by lookup, the contact subject,
// gratitude --person/thank, memory linking, and link/attach; record is the
// curation verbs' record-targeting answer (a key only where it agrees with
// now).
type consistencyCase struct {
	name      string
	form      string
	setup     func(f *idFixture) map[string]string
	capture   surfaceWant
	now       surfaceWant
	record    surfaceWant
	reconcile reconcileWant
}

// consistencyCases is the table the resolver-consistency guard runs: the
// AC-U1 cases, plus a claim on a form no one holds and a name no one holds.
func consistencyCases() []consistencyCase {
	samAndRiver := func(f *idFixture) map[string]string {
		return map[string]string{"sam": f.person("Sam"), "river": f.person("Sam Rivera")}
	}
	return []consistencyCase{
		{
			name: "a single active holder",
			form: "Sam",
			setup: func(f *idFixture) map[string]string {
				return map[string]string{"sam": f.person("Sam"), "jordan": f.person("Jordan Lee")}
			},
			capture: surfaceWant{kind: outKey, who: "sam"},
			now:     surfaceWant{kind: outKey, who: "sam"},
			record:  surfaceWant{kind: outKey, who: "sam"},
		},
		{
			name: "dormant-only holder, one active first-name match",
			form: "Sam",
			setup: func(f *idFixture) map[string]string {
				w := samAndRiver(f)
				f.dormant(w["sam"])
				return w
			},
			capture: surfaceWant{kind: outKey, who: "river"},
			now:     surfaceWant{kind: outKey, who: "river"},
			record:  surfaceWant{kind: outAmbiguous, candidates: []string{"river", "sam"}},
		},
		{
			name: "dormant-only holder, two active first-name matches",
			form: "Alex",
			setup: func(f *idFixture) map[string]string {
				w := map[string]string{"alex": f.person("Alex"), "kim": f.person("Alex Kim"), "stone": f.person("Alex Stone")}
				f.dormant(w["alex"])
				return w
			},
			capture:   surfaceWant{kind: outHeld, reason: identity.HoldFirstName, candidates: []string{"kim", "stone", "alex"}},
			now:       surfaceWant{kind: outAmbiguous, candidates: []string{"kim", "stone", "alex"}},
			record:    surfaceWant{kind: outAmbiguous, candidates: []string{"kim", "stone", "alex"}},
			reconcile: reconcileWant{held: true},
		},
		{
			name: "dormant-only holder, no active first-name match",
			form: "Sam",
			setup: func(f *idFixture) map[string]string {
				w := map[string]string{"sam": f.person("Sam"), "jordan": f.person("Jordan Lee")}
				f.dormant(w["sam"])
				return w
			},
			capture:   surfaceWant{kind: outMint, who: "sam"},
			now:       surfaceWant{kind: outKey, who: "sam", dormant: true},
			record:    surfaceWant{kind: outKey, who: "sam"},
			reconcile: reconcileWant{dormantShare: "sam"},
		},
		{
			name: "dormant-only holder of a multi-word form",
			form: "Sam Rivera",
			setup: func(f *idFixture) map[string]string {
				w := samAndRiver(f)
				f.dormant(w["river"])
				return w
			},
			capture:   surfaceWant{kind: outMint, who: "river"},
			now:       surfaceWant{kind: outKey, who: "river", dormant: true},
			record:    surfaceWant{kind: outKey, who: "river"},
			reconcile: reconcileWant{dormantShare: "river"},
		},
		{
			name: "a claim over an active exact holder",
			form: "Sam",
			setup: func(f *idFixture) map[string]string {
				w := samAndRiver(f)
				f.claim(w["river"], "Sam", true)
				return w
			},
			capture: surfaceWant{kind: outKey, who: "river"},
			now:     surfaceWant{kind: outKey, who: "river"},
			record:  surfaceWant{kind: outAmbiguous, candidates: []string{"river", "sam"}},
		},
		{
			name: "a claim on a form no one holds",
			form: "Rivera",
			setup: func(f *idFixture) map[string]string {
				w := samAndRiver(f)
				f.claim(w["river"], "Rivera", false)
				return w
			},
			capture: surfaceWant{kind: outKey, who: "river"},
			now:     surfaceWant{kind: outKey, who: "river"},
			record:  surfaceWant{kind: outKey, who: "river"},
		},
		{
			name: "a dormant claimant",
			form: "Sam",
			setup: func(f *idFixture) map[string]string {
				w := samAndRiver(f)
				f.claim(w["river"], "Sam", true)
				f.dormant(w["river"])
				return w
			},
			capture: surfaceWant{kind: outKey, who: "sam"},
			now:     surfaceWant{kind: outKey, who: "sam"},
			record:  surfaceWant{kind: outKey, who: "sam"},
		},
		{
			name: "two active exact holders (the aka collision)",
			form: "Sam",
			setup: func(f *idFixture) map[string]string {
				return map[string]string{"sam": f.person("Sam"), "river": f.person("Sam Rivera", "Sam")}
			},
			capture:   surfaceWant{kind: outHeld, reason: identity.HoldExact, candidates: []string{"sam", "river"}},
			now:       surfaceWant{kind: outAmbiguous, candidates: []string{"sam", "river"}},
			record:    surfaceWant{kind: outAmbiguous, candidates: []string{"sam", "river"}},
			reconcile: reconcileWant{held: true},
		},
		{
			name:      "a shared first name only",
			form:      "Sam",
			setup:     samAndRiver,
			capture:   surfaceWant{kind: outKey, who: "sam"},
			now:       surfaceWant{kind: outKey, who: "sam"},
			record:    surfaceWant{kind: outKey, who: "sam"},
			reconcile: reconcileWant{overlap: true},
		},
		{
			// The R1/R5 exception: discovery through aka can hold a mention but
			// never assign one, so capture holds while lookup still resolves to
			// the sole active holder.
			name: "a sole active aka-only holder behind a dormant slug holder",
			form: "Sam",
			setup: func(f *idFixture) map[string]string {
				w := map[string]string{"sam": f.person("Sam"), "river": f.person("Sam Rivera", "Sam")}
				f.dormant(w["sam"])
				return w
			},
			capture:   surfaceWant{kind: outHeld, reason: identity.HoldExact, candidates: []string{"river", "sam"}},
			now:       surfaceWant{kind: outKey, who: "river"},
			record:    surfaceWant{kind: outAmbiguous, candidates: []string{"river", "sam"}},
			reconcile: reconcileWant{held: true, dormantShare: "sam"},
		},
		{
			name: "no holder at all",
			form: "Riley Moss",
			setup: func(f *idFixture) map[string]string {
				return map[string]string{"jordan": f.person("Jordan Lee")}
			},
			capture: surfaceWant{kind: outMint},
			now:     surfaceWant{kind: outNone},
			record:  surfaceWant{kind: outNone},
		},
	}
}

// consistencySurfaces is every surface the guard runs a case through, each on
// a fresh Ledger built by the case's setup.
func consistencySurfaces() []struct {
	name  string
	check func(t *testing.T, c consistencyCase, f *idFixture, w map[string]string)
} {
	return []struct {
		name  string
		check func(t *testing.T, c consistencyCase, f *idFixture, w map[string]string)
	}{
		{"capture", checkCaptureSurface},
		{"lookup", checkLookupSurface},
		{"contact subject", checkContactSurface},
		{"gratitude person", checkGratitudeSurface},
		{"memory linking", checkMemorySurface},
		{"record targeting", checkRecordSurface},
		{"link and attach", checkLinkSurface},
		{"reconcile", checkReconcileSurface},
	}
}

// TestResolverConsistency_AllSurfaces: one table of cases — a single active
// holder; a dormant-only holder with one, two, and no active first-name
// matches; a dormant-only multi-word form; a claim (over an active holder and
// on a form no one holds); a dormant claimant; two active exact holders; a
// shared first name only; the aka-only active holder behind a dormant slug
// holder; and a name no one holds — runs through every surface, and each
// surface gives its documented outcome. The who-it-means-now surfaces (lookup,
// contact, gratitude, memory, link/attach) all give the lookup answer; capture
// differs only where the rules say it must (a hold, a mint, a first-name
// route recorded as a pin); record targeting returns a key only where it
// agrees, and refuses (§P-5) rather than guess.
func TestResolverConsistency_AllSurfaces(t *testing.T) {
	for _, c := range consistencyCases() {
		t.Run(c.name, func(t *testing.T) {
			for _, s := range consistencySurfaces() {
				t.Run(s.name, func(t *testing.T) {
					f := newIDFixture(t)
					w := c.setup(f)
					s.check(t, c, f, w)
				})
			}
		})
	}
}

// labelKeys maps labels to keys, in the order given.
func labelKeys(w map[string]string, labels []string) []string {
	keys := make([]string, 0, len(labels))
	for _, l := range labels {
		keys = append(keys, w[l])
	}
	return keys
}

// orderedCandidates is the candidate order every surface shows: active people
// first, then dormant ones, each group by key.
func orderedCandidates(f *idFixture, w map[string]string, labels []string) []string {
	f.t.Helper()
	st := f.state()
	var active, dormant []string
	for _, k := range labelKeys(w, labels) {
		if st.IsDormant(k) {
			dormant = append(dormant, k)
			continue
		}
		active = append(active, k)
	}
	return append(sortedKeys(active), sortedKeys(dormant)...)
}

// checkCaptureSurface structures one raw entry mentioning the form through the
// real Structure pass and checks where the mention landed: a person (whose
// aka[] never gains a form they did not already hold), a hold, or a mint.
func checkCaptureSurface(t *testing.T, c consistencyCase, f *idFixture, w map[string]string) {
	var before storage.PersonRecord
	if c.capture.kind == outKey {
		rec, found, err := f.a.ReadPerson(w[c.capture.who])
		require.NoError(t, err)
		require.True(t, found)
		before = rec
	}
	raw := f.structure(m(c.form))
	art, err := f.a.ReadProcessed(raw)
	require.NoError(t, err)

	switch c.capture.kind {
	case outKey:
		require.Len(t, art.People, 1)
		assert.Empty(t, art.HeldPeople)
		assert.Equal(t, w[c.capture.who], art.People[0].PersonKey)
		after, _, rerr := f.a.ReadPerson(w[c.capture.who])
		require.NoError(t, rerr)
		if !holdsFormExactly(before, c.form) {
			assert.Equal(t, before.Aka, after.Aka, "a routed mention never adds the form to the target's aka[]")
		}
	case outHeld:
		assert.Empty(t, art.People, "a held mention is assigned to no one")
		require.Len(t, art.HeldPeople, 1)
		assert.Equal(t, c.form, art.HeldPeople[0].DisplayName)
		assert.Equal(t, c.capture.reason, art.HeldPeople[0].Reason)
		assert.Equal(t, sortedKeys(labelKeys(w, c.capture.candidates)), art.HeldPeople[0].Candidates)
	case outMint:
		require.Len(t, art.People, 1)
		assert.Empty(t, art.HeldPeople)
		key := art.People[0].PersonKey
		assert.NotContains(t, slices.Collect(maps.Values(w)), key, "a mint is a fresh record")
		if c.capture.who != "" {
			assert.Equal(t, w[c.capture.who]+"-2", key, "the next free collision suffix")
		}
		rec, found, rerr := f.a.ReadPerson(key)
		require.NoError(t, rerr)
		require.True(t, found)
		assert.Equal(t, c.form, rec.DisplayName)
		assert.False(t, f.state().IsDormant(key), "a fresh record is active")
	case outAmbiguous, outNone:
		t.Fatalf("capture never answers %s", c.capture.kind)
	}
}

// checkLookupSurface runs `lucid person <form>`: the person (dormant labeled),
// §P-2 listing the candidates active first, or §P-1.
func checkLookupSurface(t *testing.T, c consistencyCase, f *idFixture, w map[string]string) {
	res, err := f.r.Person(PersonRequest{Name: c.form})
	require.NoError(t, err)
	switch c.now.kind {
	case outKey:
		require.True(t, res.Matched, res.Text)
		assert.Equal(t, w[c.now.who], res.PersonKey)
		assert.Equal(t, c.now.dormant, res.Dormant)
		assert.Equal(t, c.now.dormant, strings.Contains(res.Text, "\nDormant since "), res.Text)
	case outAmbiguous:
		require.True(t, res.MultipleMatches, res.Text)
		keys, _ := candidateKeysAndFlags(res)
		assert.Equal(t, orderedCandidates(f, w, c.now.candidates), keys)
	case outNone:
		assert.False(t, res.Matched)
		assert.False(t, res.MultipleMatches)
		assert.Equal(t, personNoMatch, res.Text)
	case outHeld, outMint:
		t.Fatalf("lookup never answers %s", c.now.kind)
	}
}

// requireSubjectRefusal checks a who-it-means-now subject surface refused the
// way the case says: §P-5 when ambiguous, §P-4 when no one holds the name.
func requireSubjectRefusal(t *testing.T, want surfaceWant, err error) {
	t.Helper()
	switch want.kind {
	case outAmbiguous:
		require.ErrorIs(t, err, ErrPersonAmbiguousSubject)
	case outNone:
		require.ErrorIs(t, err, ErrPersonUnknownSubject)
	case outKey, outHeld, outMint:
		t.Fatalf("%s is not a refusal", want.kind)
	}
}

// checkContactSurface logs a contact with the form as its subject.
func checkContactSurface(t *testing.T, c consistencyCase, f *idFixture, w map[string]string) {
	res, err := f.r.ContactLog(ContactLogRequest{Subject: c.form, Now: f.now()})
	if c.now.kind == outKey {
		require.NoError(t, err)
		assert.Equal(t, w[c.now.who], res.PersonKey)
		return
	}
	requireSubjectRefusal(t, c.now, err)
}

// checkGratitudeSurface adds a gratitude with --person and thanks an existing
// one, both with the form as the person.
func checkGratitudeSurface(t *testing.T, c consistencyCase, f *idFixture, w map[string]string) {
	plain, err := f.r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "a quiet morning", Now: f.now()})
	require.NoError(t, err)
	added, addErr := f.r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "a long walk", Person: c.form, Now: f.now()})
	thanked, thankErr := f.r.ThankGratitude(ThankGratitudeRequest{ID: plain.Key, Person: c.form, Now: f.now()})
	if c.now.kind == outKey {
		require.NoError(t, addErr)
		require.NoError(t, thankErr)
		assert.Equal(t, w[c.now.who], added.PersonKey)
		assert.Equal(t, w[c.now.who], thanked.PersonKey)
		return
	}
	requireSubjectRefusal(t, c.now, addErr)
	requireSubjectRefusal(t, c.now, thankErr)
}

// checkMemorySurface writes a story naming the form in --people: it links
// exactly the one person the name means now, and nothing otherwise.
func checkMemorySurface(t *testing.T, c consistencyCase, f *idFixture, w map[string]string) {
	enableMemory(t, f.a)
	res, err := f.r.WriteMemory(MemoryWriteRequest{Text: "the lake trip", People: []string{c.form}, Now: f.now()})
	require.NoError(t, err)
	if c.now.kind == outKey {
		assert.Equal(t, []string{w[c.now.who]}, res.Refs["person"])
		return
	}
	assert.Empty(t, res.Refs["person"], "an ambiguous or unknown name links no one")
}

// checkRecordSurface resolves the form as a curation verb's subject — the
// seam, then a real write (`person dormant`) — and gets the record only where
// it agrees with who the name means now; otherwise a refusal that writes
// nothing.
func checkRecordSurface(t *testing.T, c consistencyCase, f *idFixture, w map[string]string) {
	rec, err := f.r.resolvePersonRecord(c.form, false)
	before := f.identityBytes()
	res, verbErr := f.r.PersonDormant(PersonDormantRequest{Subject: c.form, Now: f.now()})
	if c.record.kind == outKey {
		require.NoError(t, err)
		require.NoError(t, verbErr)
		assert.Equal(t, w[c.record.who], rec.PersonKey)
		assert.Equal(t, w[c.record.who], res.Record.PersonKey)
		return
	}
	requireSubjectRefusal(t, c.record, err)
	requireSubjectRefusal(t, c.record, verbErr)
	assert.Equal(t, before, f.identityBytes(), "a refused curation write writes nothing")
	if c.record.kind == outAmbiguous {
		var refused *PersonRejectedError
		require.ErrorAs(t, err, &refused)
		for _, k := range labelKeys(w, c.record.candidates) {
			assert.Contains(t, refused.Reason, k, "§P-5 lists every candidate")
		}
	}
}

// checkLinkSurface links media to the person(s) the name means now, by key:
// link/attach stay key-only (the bare form is refused), and an ack marks a
// dormant person.
func checkLinkSurface(t *testing.T, c consistencyCase, f *idFixture, w map[string]string) {
	mediaID := seedLinkMedia(t, f.a)
	_, err := f.r.Link(LinkRequest{Media: mediaID, Subjects: []string{"person:" + c.form}, Op: storage.LinkOpLink, Now: fixedNow()})
	require.Error(t, err, "link never resolves a name")

	var labels []string
	switch c.now.kind {
	case outKey:
		labels = []string{c.now.who}
	case outAmbiguous:
		labels = c.now.candidates
	case outNone, outHeld, outMint:
		return
	}
	st := f.state()
	for _, k := range labelKeys(w, labels) {
		res, lerr := f.r.Link(LinkRequest{Media: mediaID, Subjects: []string{"person:" + k}, Op: storage.LinkOpLink, Now: fixedNow()})
		require.NoError(t, lerr)
		assert.Equal(t, st.IsDormant(k), strings.Contains(res.Ack, "person:"+k+" (dormant)"), res.Ack)
	}
}

// checkReconcileSurface captures the form once, then reads `person reconcile`:
// a pending held mention for that raw entry, a first-name overlap on the
// form, and/or a dormant look-alike — exactly the ones the case lists.
func checkReconcileSurface(t *testing.T, c consistencyCase, f *idFixture, w map[string]string) {
	raw := f.structure(m(c.form))
	res, err := f.r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)

	held := slices.ContainsFunc(res.Held, func(h ReconcileHeld) bool { return h.RawID == raw && h.Form == c.form })
	assert.Equal(t, c.reconcile.held, held, res.Text)
	assert.Equal(t, c.reconcile.held, strings.Contains(res.Text, "- "+raw+": "), res.Text)

	overlaps := slices.Clone(res.Overlaps)
	shares := slices.Clone(res.DormantShares)
	for _, cand := range res.Candidates {
		if cand.Overlap != nil {
			overlaps = append(overlaps, *cand.Overlap)
		}
		if cand.DormantShare != nil {
			shares = append(shares, *cand.DormantShare)
		}
	}
	overlap := slices.ContainsFunc(overlaps, func(o ReconcileOverlap) bool { return o.Form == c.form })
	assert.Equal(t, c.reconcile.overlap, overlap, res.Text)
	assert.Equal(t, c.reconcile.overlap, strings.Contains(res.Text, `also has the first name "`+c.form+`"`), res.Text)

	dormantKey := w[c.reconcile.dormantShare]
	share := slices.ContainsFunc(shares, func(d ReconcileDormantShare) bool { return d.DormantKey == dormantKey })
	assert.Equal(t, c.reconcile.dormantShare != "", share, res.Text)
	if c.reconcile.dormantShare != "" {
		assert.Contains(t, res.Text, "lucid person dormant "+dormantKey+" --restore")
	}
}

// keySurfaceAnswers resolves one exact person_key on every surface that takes
// a person subject and returns the person each one reached, by surface.
// Capture has no key form — structuring emits display names, never keys — and
// reconcile takes no subject, so neither is listed.
func keySurfaceAnswers(t *testing.T, f *idFixture, key string) map[string]string {
	t.Helper()
	out := map[string]string{}

	res, err := f.a.ResolvePersonName(key, storage.ResolveOptions{Mode: storage.ResolveLookup})
	require.NoError(t, err)
	assert.Equal(t, storage.StepKey, res.Step)
	out["storage lookup"] = res.PersonKey

	view, err := f.r.Person(PersonRequest{Name: key})
	require.NoError(t, err)
	require.True(t, view.Matched, view.Text)
	out["lookup"] = view.PersonKey

	ct, err := f.r.ContactLog(ContactLogRequest{Subject: key, Now: f.now()})
	require.NoError(t, err)
	out["contact subject"] = ct.PersonKey

	at := f.now()
	added, err := f.r.AddGratitude(t.Context(), AddGratitudeRequest{Thing: "a long walk on " + at.Format(time.RFC3339), Person: key, Now: at})
	require.NoError(t, err)
	out["gratitude --person"] = added.PersonKey
	thanked, err := f.r.ThankGratitude(ThankGratitudeRequest{ID: added.Key, Person: key, Now: f.now()})
	require.NoError(t, err)
	out["gratitude thank"] = thanked.PersonKey

	at = f.now()
	mem, err := f.r.WriteMemory(MemoryWriteRequest{Text: "the lake trip, told on " + at.Format(time.RFC3339), People: []string{key}, Now: at})
	require.NoError(t, err)
	if linked, ok := mem.Refs["person"].([]string); ok && len(linked) == 1 {
		out["memory linking"] = linked[0]
	}

	rec, err := f.r.resolvePersonRecord(key, false)
	require.NoError(t, err)
	out["record targeting"] = rec.PersonKey

	mediaID := seedLinkMedia(t, f.a)
	_, err = f.r.Link(LinkRequest{Media: mediaID, Subjects: []string{"person:" + key}, Op: storage.LinkOpLink, Now: fixedNow()})
	require.NoError(t, err, "link accepts the key")
	return out
}

// assertKeyEverywhere checks every surface reached want for the subject key.
func assertKeyEverywhere(t *testing.T, f *idFixture, key, want string) {
	t.Helper()
	got := keySurfaceAnswers(t, f, key)
	require.Len(t, got, 7, "every subject surface answered")
	for surface, k := range got {
		assert.Equal(t, want, k, "%s for %s", surface, key)
	}
}

// TestResolverConsistency_ExactKeyWinsEverywhere: an exact person_key reaches
// its person on every subject surface — while their bare name is held as
// ambiguous, after another person claims that name, and while they are
// dormant — and a tombstone key resolves forward to its canonical record.
func TestResolverConsistency_ExactKeyWinsEverywhere(t *testing.T) {
	f := newIDFixture(t)
	enableMemory(t, f.a)
	oldSam, river, held := heldSamPair(f)
	alex := f.person("Alex")
	andy := f.person("Andy")
	_, err := f.r.PersonMerge(PersonMergeRequest{Source: andy, Target: alex, Now: f.now()})
	require.NoError(t, err)

	// The bare name is held: lookup lists both, but each key wins.
	require.Equal(t, storage.NameAmbiguous, f.lookup("Sam").Outcome)
	require.Len(t, f.state().PendingHolds(), 1)
	assertKeyEverywhere(t, f, oldSam, oldSam)
	assertKeyEverywhere(t, f, river, river)

	// A forced claim makes a bare "Sam" mean Sam Rivera; the old key still wins.
	claim := f.claim(river, "Sam", true)
	require.Len(t, claim.Pinned, 1)
	assert.Equal(t, held, claim.Pinned[0].RawID)
	require.Equal(t, river, f.lookup("Sam").PersonKey)
	assertKeyEverywhere(t, f, oldSam, oldSam)

	// A dormant person is still reached by key.
	f.dormant(oldSam)
	assertKeyEverywhere(t, f, oldSam, oldSam)
	rec, err := f.r.resolvePersonRecord(oldSam, true)
	require.NoError(t, err)
	assert.Equal(t, oldSam, rec.PersonKey, "--restore reaches the dormant person by key")

	// A tombstone key resolves forward to its canonical record.
	assertKeyEverywhere(t, f, andy, alex)
}

// seamRestricted are the low-level name matchers and the shared resolver:
// only the seam file may name them.
func seamRestricted() []string {
	return []string{"matchPeople", "recordMatchesName", "exactKeySubject", "ResolvePersonName"}
}

// declaredPeopleReaders are the router functions outside the seam that may
// scan the people tree (ListPeopleKeys), each for a reason that is not
// resolving a name.
func declaredPeopleReaders() map[string]string {
	return map[string]string{
		"contact.go:contactIndex":           "the contact due listing walks every person's cadence; no name is resolved",
		"person.go:personRedirectIndex":     "maps every key to its canonical key and display name for dominance; no name is resolved",
		"person_reconcile.go:reconcileScan": "the reconcile detector reads every live person; its one exact-form test goes through holdsFormExactly",
	}
}

// identUse is one non-comment identifier in a router file, with the function
// declaration it sits in ("" at file scope).
type identUse struct {
	file, fn, name string
	pos            token.Position
}

// routerIdentUses parses every non-test router source file (comments are not
// parsed, so a comment naming a matcher is not a use) and returns each
// identifier occurrence, with its enclosing function.
func routerIdentUses(t *testing.T) []identUse {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var uses []identUse
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		require.NoError(t, perr)
		for _, decl := range file.Decls {
			fn := ""
			if fd, ok := decl.(*ast.FuncDecl); ok {
				fn = fd.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					uses = append(uses, identUse{file: name, fn: fn, name: id.Name, pos: fset.Position(id.Pos())})
				}
				return true
			})
		}
	}
	require.NotEmpty(t, uses)
	return uses
}

// TestPersonNameResolution_SingleSeam: a go/parser scan of every non-test
// router file. The low-level matchers and the shared resolver are named only
// in person_resolve.go; the exact-form test helper only there and in the
// reconcile detector; the people-tree scan only there and in the declared
// readers, none of which resolves a name. Every other surface reaches people
// names through resolvePersonNow or resolvePersonRecord, and recall — which
// has no person dimension — names none of them.
func TestPersonNameResolution_SingleSeam(t *testing.T) {
	const seam = "person_resolve.go"
	uses := routerIdentUses(t)
	restricted := seamRestricted()
	readers := declaredPeopleReaders()
	seenReaders := map[string]bool{}
	callers := map[string]map[string]bool{"resolvePersonNow": {}, "resolvePersonRecord": {}}

	for _, u := range uses {
		where := u.pos.String()
		switch {
		case slices.Contains(restricted, u.name):
			assert.Equal(t, seam, u.file, "%s names %s outside the seam", where, u.name)
		case u.name == "holdsFormExactly":
			assert.Contains(t, []string{seam, "person_reconcile.go"}, u.file, "%s names holdsFormExactly", where)
		case u.name == "ListPeopleKeys" && u.file != seam:
			reader := u.file + ":" + u.fn
			_, ok := readers[reader]
			assert.True(t, ok, "%s scans the people tree in %s, which is not a declared reader", where, reader)
			seenReaders[reader] = true
		case callers[u.name] != nil && u.file != seam:
			callers[u.name][u.file] = true
		}
		if u.file == "recall.go" {
			assert.NotContains(t, append(restricted, "holdsFormExactly", "ListPeopleKeys", "resolvePersonNow", "resolvePersonRecord"),
				u.name, "recall has no person dimension (%s)", where)
		}
	}

	for reader := range readers {
		assert.True(t, seenReaders[reader], "declared reader %s no longer scans the people tree; drop it from the allowlist", reader)
	}
	for _, file := range []string{"person.go", "contact.go", "gratexpress.go", "memory.go"} {
		assert.True(t, callers["resolvePersonNow"][file], "%s resolves who a name means now through the seam", file)
	}
	for _, file := range []string{"person_curate.go", "person_identity.go"} {
		assert.True(t, callers["resolvePersonRecord"][file], "%s targets a record through the seam", file)
	}
}
