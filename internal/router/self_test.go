package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// selfStorePath returns the self-facts store path under a temp Ledger home. It
// is the one place the Ledger-relative location is spelled out in these tests,
// so an absent-file assertion and a byte-identical assertion cannot drift apart.
func selfStorePath(home string) string {
	return filepath.Join(home, "engine", "self.json")
}

// readSelfStore returns the raw bytes of the self store, and whether it exists
// at all. A rejection must leave both unchanged — on a scaffolded tree the
// bytes stay identical, on a bare one the file stays absent.
func readSelfStore(t *testing.T, home string) (content []byte, exists bool) {
	t.Helper()
	b, err := os.ReadFile(selfStorePath(home))
	if os.IsNotExist(err) {
		return nil, false
	}
	require.NoError(t, err)
	return b, true
}

// TestSelfSet_RecordsAndAcks records a fact and asserts the inventory ack, the
// minted id, and a byte-stable RecordedAt from the pinned clock.
func TestSelfSet_RecordsAndAcks(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	res, err := r.SelfSet(SelfSetRequest{Key: "identity.generation", Value: "elder millennial", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "Fact recorded: identity.generation — elder millennial.", res.Ack)
	assert.Equal(t, "identity.generation", res.Fact.Key)
	assert.Equal(t, "elder millennial", res.Fact.Value)
	assert.Equal(t, "self_2026_07_05_a", res.Fact.ID)
	assert.Equal(t, fixedNow().Format("2006-01-02T15:04:05Z07:00"), res.Fact.RecordedAt)
	assert.Empty(t, res.Fact.State, "a recorded fact is active")

	log, err := a.ReadSelfFacts()
	require.NoError(t, err)
	require.Len(t, log.History, 1)
	assert.Equal(t, "identity.generation", log.History[0].Key)
	assert.Equal(t, engine.SelfVersion, log.Version)
}

// TestSelfSet_KeepsSinceAndNote records the optional origin and annotation
// verbatim — including a partial origin, which this store accepts and stores as
// typed rather than snapping to a civil date.
func TestSelfSet_KeepsSinceAndNote(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	res, err := r.SelfSet(SelfSetRequest{
		Key: "identity.generation", Value: "elder millennial",
		Since: "1985", Note: "from the birth year", Now: fixedNow(),
	})
	require.NoError(t, err)
	assert.Equal(t, "1985", res.Fact.Since, "a partial origin is stored as typed")
	assert.Equal(t, "from the birth year", res.Fact.Note)
}

// TestSelfSet_CorrectionReusesID records the same active key twice: the second
// append targets the first fact rather than minting a second one, so the two
// records share one identity, both persist, and the fold shows the newer value.
func TestSelfSet_CorrectionReusesID(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	first, err := r.SelfSet(SelfSetRequest{Key: "body.handedness", Value: "right", Now: fixedNow()})
	require.NoError(t, err)
	require.NotEmpty(t, first.Fact.ID, "a first use mints an id")

	second, err := r.SelfSet(SelfSetRequest{Key: "body.handedness", Value: "left", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, first.Fact.ID, second.Fact.ID, "a correction targets the existing fact")
	assert.Equal(t, "Fact recorded: body.handedness — left.", second.Ack, "a correction is not a new fact")

	log, err := a.ReadSelfFacts()
	require.NoError(t, err)
	require.Len(t, log.History, 2, "both records persist in the append-only history")

	latest := engine.LatestSelfFacts(log)
	require.Len(t, latest, 1, "one identity, one folded entry")
	assert.Equal(t, "left", latest[0].Value, "the most-recently-appended record wins")
}

// TestSelfSet_AfterRetireMintsNewID reuses a key whose only prior holder was
// retired: the retirement freed the key, so this is a new fact under a new
// identity — and the ack names the earlier retirement so the reuse is not a
// surprise.
func TestSelfSet_AfterRetireMintsNewID(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	first, err := r.SelfSet(SelfSetRequest{Key: "constraint.diet.dairy", Value: "avoid", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfRetire(SelfRetireRequest{Key: "constraint.diet.dairy", Now: fixedNow()})
	require.NoError(t, err)

	again, err := r.SelfSet(SelfSetRequest{Key: "constraint.diet.dairy", Value: "none", Now: fixedNow()})
	require.NoError(t, err)
	assert.NotEqual(t, first.Fact.ID, again.Fact.ID, "a freed key starts a new identity")
	assert.Contains(t, again.Ack, "was retired 2026-07-05", "the ack names the earlier retirement")

	log, err := a.ReadSelfFacts()
	require.NoError(t, err)
	require.Len(t, log.History, 3)
	assert.Len(t, engine.LatestSelfFacts(log), 2, "the retired fact and the new one stay distinct")
	assert.Len(t, engine.ActiveSelfFacts(engine.LatestSelfFacts(log)), 1, "only the new fact is active")
}

// TestSelfSet_RejectsUnknownNamespace refuses a key outside the taxonomy. The
// message must name both the accepted set and misc., so a rejected fact always
// has a landing spot rather than being turned away.
func TestSelfSet_RejectsUnknownNamespace(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.SelfSet(SelfSetRequest{Key: "health.blood_type", Value: "O+", Now: fixedNow()})
	require.ErrorIs(t, err, ErrSelfRejected)
	for _, want := range []string{"identity.", "constraint.", "body.", "pref.", "misc."} {
		assert.Contains(t, err.Error(), want, "the rejection names the accepted namespace")
	}
	assert.NotContains(t, err.Error(), "engine:", "the reason names the fact, not the package")
}

// TestSelfSet_RejectsEmptyValue refuses a fact with nothing to record.
func TestSelfSet_RejectsEmptyValue(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.SelfSet(SelfSetRequest{Key: "body.handedness", Value: "   ", Now: fixedNow()})
	require.ErrorIs(t, err, ErrSelfRejected)
	assert.Contains(t, err.Error(), "value")
}

// TestSelfSet_RejectionWritesNothing walks every rejection path and asserts the
// store is untouched by each — byte-identical on a scaffolded tree, and still
// absent on a tree that was never scaffolded, because validation runs before
// prepareEngine.
func TestSelfSet_RejectionWritesNothing(t *testing.T) {
	r, a, home := newBootedRouter(t)

	bad := []SelfSetRequest{
		{Key: "health.blood_type", Value: "O+"},         // unknown namespace
		{Key: "handedness", Value: "left"},              // no namespace at all
		{Key: "body.", Value: "left"},                   // empty leaf
		{Key: "body.Handedness", Value: "left"},         // uppercase leaf
		{Key: "body.handedness", Value: ""},             // empty value
		{Key: "body.height", Value: longSelfValue(501)}, // over the ceiling
	}

	// A never-scaffolded tree: a rejection must not even create the store.
	for _, req := range bad {
		req.Now = fixedNow()
		_, err := r.SelfSet(req)
		require.ErrorIs(t, err, ErrSelfRejected, req.Key)
		_, exists := readSelfStore(t, home)
		assert.False(t, exists, "a rejected input must not scaffold the store")
	}

	// A scaffolded tree holding a real fact: the bytes must not move.
	_, err := r.SelfSet(SelfSetRequest{Key: "identity.name", Value: "sample", Now: fixedNow()})
	require.NoError(t, err)
	before, exists := readSelfStore(t, home)
	require.True(t, exists)

	for _, req := range bad {
		req.Now = fixedNow()
		_, err = r.SelfSet(req)
		require.ErrorIs(t, err, ErrSelfRejected, req.Key)
		after, _ := readSelfStore(t, home)
		assert.Equal(t, before, after, "a rejected input must not write")
	}

	log, err := a.ReadSelfFacts()
	require.NoError(t, err)
	assert.Len(t, log.History, 1)
}

// longSelfValue builds a value of exactly n runes, for the ceiling check.
func longSelfValue(n int) string { return strings.Repeat("x", n) }

// TestSelfRetire_AppendsStateRecord retires a fact by appending. The record
// mirrors the fact — id, key, value and origin verbatim — and adds the retired
// state; the origin is the fact's own, never today's.
func TestSelfRetire_AppendsStateRecord(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	set, err := r.SelfSet(SelfSetRequest{
		Key: "constraint.diet.dairy", Value: "avoid", Since: "2014-09", Now: fixedNow(),
	})
	require.NoError(t, err)

	res, err := r.SelfRetire(SelfRetireRequest{Key: "constraint.diet.dairy", Reason: "resolved", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, set.Fact.ID, res.Fact.ID, "the retirement carries the fact's identity")
	assert.Equal(t, "avoid", res.Fact.Value, "the value is mirrored")
	assert.Equal(t, "2014-09", res.Fact.Since, "the fact's own origin, never today's")
	assert.Equal(t, "resolved", res.Fact.Note, "the reason lands in the note")
	assert.Equal(t, engine.SelfStateRetired, res.Fact.State)
	assert.Contains(t, res.Ack, "the record is kept")

	log, err := a.ReadSelfFacts()
	require.NoError(t, err)
	require.Len(t, log.History, 2, "nothing is deleted")
	assert.Empty(t, engine.ActiveSelfFacts(engine.LatestSelfFacts(log)), "the fact leaves the profile")
}

// TestSelfRetire_UnknownKeyListsKeys names the recorded keys when a bare key
// matches nothing: there is no list verb, so the remedy travels with the error.
func TestSelfRetire_UnknownKeyListsKeys(t *testing.T) {
	r, _, home := newBootedRouter(t)

	// An empty store says so rather than trailing off after an empty list.
	_, err := r.SelfRetire(SelfRetireRequest{Key: "body.height", Now: fixedNow()})
	require.ErrorIs(t, err, ErrSelfUnknownKey)
	assert.Contains(t, err.Error(), "no facts are recorded yet")

	_, err = r.SelfSet(SelfSetRequest{Key: "identity.name", Value: "sample", Now: fixedNow()})
	require.NoError(t, err)
	before, _ := readSelfStore(t, home)

	_, err = r.SelfRetire(SelfRetireRequest{Key: "body.height", Now: fixedNow()})
	require.ErrorIs(t, err, ErrSelfUnknownKey)
	assert.Contains(t, err.Error(), "identity.name", "the rejection names the keys that do exist")

	after, _ := readSelfStore(t, home)
	assert.Equal(t, before, after, "a rejected retire must not write")
}

// TestSelfRetire_AlreadyRetired refuses a second retirement and names the date
// of the first, pointing at set as the way to start a new fact under that key.
func TestSelfRetire_AlreadyRetired(t *testing.T) {
	r, _, home := newBootedRouter(t)

	_, err := r.SelfSet(SelfSetRequest{Key: "pref.coffee", Value: "black", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfRetire(SelfRetireRequest{Key: "pref.coffee", Now: fixedNow()})
	require.NoError(t, err)
	before, _ := readSelfStore(t, home)

	_, err = r.SelfRetire(SelfRetireRequest{Key: "pref.coffee", Now: fixedNow()})
	require.ErrorIs(t, err, ErrSelfAlreadyRetired)
	assert.Contains(t, err.Error(), "2026-07-05")
	assert.Contains(t, err.Error(), "set it again")

	after, _ := readSelfStore(t, home)
	assert.Equal(t, before, after, "a rejected retire must not write")
}

// TestSelfMove_AppendsUnderSameID re-categorizes a fact. The log grows by
// exactly one record — this store has no id-less era, so a move never needs the
// two-record adoption path anchor rename uses.
func TestSelfMove_AppendsUnderSameID(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	set, err := r.SelfSet(SelfSetRequest{Key: "body.blood_type", Value: "O+", Note: "from a lab draw", Since: "2019", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.SelfMove(SelfMoveRequest{Key: "body.blood_type", NewKey: "misc.blood_type", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, set.Fact.ID, res.Fact.ID, "the fact keeps its identity")
	assert.Equal(t, "misc.blood_type", res.Fact.Key)
	assert.Equal(t, "O+", res.Fact.Value, "the value carries forward")
	assert.Equal(t, "2019", res.Fact.Since, "the origin carries forward")
	assert.Equal(t, "from a lab draw", res.Fact.Note, "the annotation carries forward")
	assert.Equal(t, "Fact moved: body.blood_type → misc.blood_type — O+.", res.Ack)

	log, err := a.ReadSelfFacts()
	require.NoError(t, err)
	assert.Len(t, log.History, 2, "a move is exactly one append")
	assert.Len(t, engine.LatestSelfFacts(log), 1, "one identity, not a fork")
}

// TestSelfMove_HistorySurvivesTheMove is the store's central promise: a fact
// re-categorized after a correction keeps every record it ever had, including
// the ones written under its previous key, while the folded profile shows only
// the new key. Nothing forks and nothing is orphaned.
func TestSelfMove_HistorySurvivesTheMove(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	set, err := r.SelfSet(SelfSetRequest{Key: "body.blood_type", Value: "O-", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfSet(SelfSetRequest{Key: "body.blood_type", Value: "O+", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfMove(SelfMoveRequest{Key: "body.blood_type", NewKey: "misc.blood_type", Now: fixedNow()})
	require.NoError(t, err)

	log, err := a.ReadSelfFacts()
	require.NoError(t, err)

	history := engine.SelfFactHistory(log, set.Fact.ID)
	require.Len(t, history, 3, "every record stays reachable by identity")
	assert.Equal(t, []string{"body.blood_type", "body.blood_type", "misc.blood_type"},
		[]string{history[0].Key, history[1].Key, history[2].Key},
		"the records written under the old key are still this fact's history",
	)
	assert.Equal(t, []string{"O-", "O+", "O+"},
		[]string{history[0].Value, history[1].Value, history[2].Value},
		"the correction is retained, not rewritten",
	)

	latest := engine.LatestSelfFacts(log)
	require.Len(t, latest, 1, "the move forked nothing")
	assert.Equal(t, "misc.blood_type", latest[0].Key, "the profile shows only the new key")
	assert.Equal(t, "O+", latest[0].Value)
}

// TestSelfMove_RejectsTakenKey refuses a key an active fact already holds.
func TestSelfMove_RejectsTakenKey(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.SelfSet(SelfSetRequest{Key: "body.height", Value: "180cm", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfSet(SelfSetRequest{Key: "misc.height", Value: "tall", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.SelfMove(SelfMoveRequest{Key: "body.height", NewKey: "misc.height", Now: fixedNow()})
	require.ErrorIs(t, err, ErrSelfKeyTaken)
	assert.Contains(t, err.Error(), "retire or move it first")
}

// TestSelfMove_RejectsUnknownKey refuses a key no active fact holds.
func TestSelfMove_RejectsUnknownKey(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.SelfSet(SelfSetRequest{Key: "identity.name", Value: "sample", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.SelfMove(SelfMoveRequest{Key: "body.height", NewKey: "misc.height", Now: fixedNow()})
	require.ErrorIs(t, err, ErrSelfUnknownKey)
	assert.Contains(t, err.Error(), "identity.name")
}

// TestSelfMove_RejectsSameKey refuses moving a key to the address it already
// has, before any read.
func TestSelfMove_RejectsSameKey(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.SelfSet(SelfSetRequest{Key: "body.height", Value: "180cm", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.SelfMove(SelfMoveRequest{Key: "body.height", NewKey: "body.height", Now: fixedNow()})
	require.ErrorIs(t, err, ErrSelfKeyTaken)
	assert.Contains(t, err.Error(), "already its key")
}

// TestSelfMove_AllowsKeyFreedByRetirement moves onto a key held only by a
// retired fact. The retirement freed it, which is the same rule set follows.
func TestSelfMove_AllowsKeyFreedByRetirement(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.SelfSet(SelfSetRequest{Key: "misc.height", Value: "tall", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfRetire(SelfRetireRequest{Key: "misc.height", Now: fixedNow()})
	require.NoError(t, err)
	moved, err := r.SelfSet(SelfSetRequest{Key: "body.height", Value: "180cm", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.SelfMove(SelfMoveRequest{Key: "body.height", NewKey: "misc.height", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, moved.Fact.ID, res.Fact.ID)

	log, err := a.ReadSelfFacts()
	require.NoError(t, err)
	active := engine.ActiveSelfFacts(engine.LatestSelfFacts(log))
	require.Len(t, active, 1, "the retired fact stays retired")
	assert.Equal(t, "misc.height", active[0].Key)
	assert.Equal(t, "180cm", active[0].Value)
}

// TestSelfMove_RejectionWritesNothing asserts every move rejection leaves the
// store byte-identical, and that an invalid new key is refused before the tree
// is even scaffolded.
func TestSelfMove_RejectionWritesNothing(t *testing.T) {
	r, _, home := newBootedRouter(t)

	_, err := r.SelfMove(SelfMoveRequest{Key: "body.height", NewKey: "health.height", Now: fixedNow()})
	require.ErrorIs(t, err, ErrSelfRejected)
	_, exists := readSelfStore(t, home)
	assert.False(t, exists, "an invalid new key is refused before any scaffold")

	_, err = r.SelfSet(SelfSetRequest{Key: "body.height", Value: "180cm", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfSet(SelfSetRequest{Key: "misc.height", Value: "tall", Now: fixedNow()})
	require.NoError(t, err)
	before, _ := readSelfStore(t, home)

	bad := []SelfMoveRequest{
		{Key: "body.height", NewKey: "health.height"}, // unknown namespace
		{Key: "body.height", NewKey: "   "},           // blank
		{Key: "body.height", NewKey: "body.height"},   // already its key
		{Key: "body.height", NewKey: "misc.height"},   // taken by an active fact
		{Key: "pref.tea", NewKey: "misc.tea"},         // unknown source key
	}
	for _, req := range bad {
		req.Now = fixedNow()
		_, err := r.SelfMove(req)
		require.Error(t, err, req.NewKey)
		after, _ := readSelfStore(t, home)
		assert.Equal(t, before, after, "a rejected move must not write")
	}
}

// TestSelfProfile_EmptyStoreCreatesNothing is the read-only proof: reading a
// profile on a never-scaffolded tree returns an empty result and leaves
// engine/self.json absent. SelfProfile deliberately skips prepareEngine, which
// is the same tolerance /ask relies on to stay contractually read-only.
func TestSelfProfile_EmptyStoreCreatesNothing(t *testing.T) {
	r, _, home := newBootedRouter(t)

	res, err := r.SelfProfile(SelfProfileRequest{History: true})
	require.NoError(t, err)
	assert.Empty(t, res.Facts)
	assert.Empty(t, res.History)
	assert.Equal(t, SelfMatchAll, res.Matched)

	_, exists := readSelfStore(t, home)
	assert.False(t, exists, "a read must not create the store")
}

// TestSelfProfile_ReadsWholeProfile returns every active fact in namespace
// priority order.
func TestSelfProfile_ReadsWholeProfile(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	seedSelfProfile(t, r)

	res, err := r.SelfProfile(SelfProfileRequest{})
	require.NoError(t, err)
	assert.Equal(t, SelfMatchAll, res.Matched)
	assert.Equal(t,
		[]string{"identity.name", "constraint.diet.dairy", "body.handedness", "body.height", "pref.coffee"},
		selfKeysOf(res.Facts),
		"identity leads, then constraint, then body, then pref",
	)
}

// TestSelfProfile_FiltersByPrefix selects a namespace and its descendants, and
// never a bare string prefix of an unrelated key.
func TestSelfProfile_FiltersByPrefix(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	seedSelfProfile(t, r)

	res, err := r.SelfProfile(SelfProfileRequest{Query: "body"})
	require.NoError(t, err)
	assert.Equal(t, SelfMatchPrefix, res.Matched)
	assert.Equal(t, []string{"body.handedness", "body.height"}, selfKeysOf(res.Facts))

	// A dotted descendant filter reaches into a multi-part leaf.
	res, err = r.SelfProfile(SelfProfileRequest{Query: "constraint.diet"})
	require.NoError(t, err)
	assert.Equal(t, []string{"constraint.diet.dairy"}, selfKeysOf(res.Facts))

	// A bare string prefix of a key is not a match: `bod` selects nothing.
	res, err = r.SelfProfile(SelfProfileRequest{Query: "bod"})
	require.NoError(t, err)
	assert.Empty(t, res.Facts)
}

// TestSelfProfile_ReadsOneKey selects a single fact when the query is an active
// key, rather than that fact plus everything nested beneath it.
func TestSelfProfile_ReadsOneKey(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	seedSelfProfile(t, r)

	res, err := r.SelfProfile(SelfProfileRequest{Query: "body.height"})
	require.NoError(t, err)
	assert.Equal(t, SelfMatchKey, res.Matched)
	require.Len(t, res.Facts, 1)
	assert.Equal(t, "180cm", res.Facts[0].Value)
	assert.Empty(t, res.History, "history is returned only when asked for")
}

// TestSelfProfile_HistorySpansAMove resolves history by identity, so a moved
// fact's records written under its previous key are still returned — in append
// order, and reachable through the fact's *new* key.
func TestSelfProfile_HistorySpansAMove(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.SelfSet(SelfSetRequest{Key: "body.blood_type", Value: "O-", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfSet(SelfSetRequest{Key: "body.blood_type", Value: "O+", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfMove(SelfMoveRequest{Key: "body.blood_type", NewKey: "misc.blood_type", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.SelfProfile(SelfProfileRequest{Query: "misc.blood_type", History: true})
	require.NoError(t, err)
	require.Len(t, res.Facts, 1)
	require.Len(t, res.History, 3, "the old-key records are still this fact's history")
	assert.Equal(t, []string{"body.blood_type", "body.blood_type", "misc.blood_type"}, selfKeysOf(res.History))
}

// TestSelfProfile_RetiredLeavesProfileNotHistory asserts the retirement
// contract on the read surface: the key drops off the rendered profile and the
// records stay in --history.
func TestSelfProfile_RetiredLeavesProfileNotHistory(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.SelfSet(SelfSetRequest{Key: "pref.coffee", Value: "black", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.SelfRetire(SelfRetireRequest{Key: "pref.coffee", Reason: "switched to tea", Now: fixedNow()})
	require.NoError(t, err)

	res, err := r.SelfProfile(SelfProfileRequest{History: true})
	require.NoError(t, err)
	assert.Empty(t, res.Facts, "a retired fact leaves the profile")
	require.Len(t, res.History, 2, "and stays in the history in full")
	assert.True(t, res.History[1].IsRetired())
	assert.Equal(t, "switched to tea", res.History[1].Note)
}

// TestSelfFacts_StayInsideTheSanctuary confirms the boundary this store lives
// behind. engine/self.json is inside the Sanctuary denylist, so no agent slice
// may ever name it by path; grounding reaches an agent as a router-composed
// projection of the *values* instead — the same shape the companion panel uses
// for engine metrics.
func TestSelfFacts_StayInsideTheSanctuary(t *testing.T) {
	assert.False(t, PathAllowedForAgent("engine/self.json"), "the self store is inside the Sanctuary")
	assert.False(t, PathAllowedForAgent("./engine/self.json"), "the check is not defeated by a relative form")

	r, _, _ := newBootedRouter(t)
	seedSelfProfile(t, r)

	res, err := r.SelfProfile(SelfProfileRequest{History: true})
	require.NoError(t, err)
	require.NotEmpty(t, res.Facts)

	for _, f := range append(append([]engine.SelfFact{}, res.Facts...), res.History...) {
		composed := strings.Join([]string{f.ID, f.Key, f.Value, f.Since, f.Note}, " ")
		assert.NotContains(t, composed, "engine/", "a composed fact carries values, never a Ledger path")
	}
}

// seedSelfProfile records one fact per namespace, deliberately out of priority
// order, so a read surface asserting the order proves it sorts rather than
// echoing the append order.
func seedSelfProfile(t *testing.T, r *Router) {
	t.Helper()
	seed := []SelfSetRequest{
		{Key: "pref.coffee", Value: "black"},
		{Key: "body.height", Value: "180cm"},
		{Key: "body.handedness", Value: "left"},
		{Key: "constraint.diet.dairy", Value: "avoid"},
		{Key: "identity.name", Value: "sample"},
	}
	for _, req := range seed {
		req.Now = fixedNow()
		_, err := r.SelfSet(req)
		require.NoError(t, err, req.Key)
	}
}

// selfKeysOf projects the keys of a fact slice, in order.
func selfKeysOf(facts []engine.SelfFact) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		out = append(out, f.Key)
	}
	return out
}
