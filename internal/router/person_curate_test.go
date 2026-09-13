package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
	"github.com/mrz1836/lucid/internal/validate"
)

// TestPersonMerge_UnionsAndRejectsSelf covers the merge happy path (union +
// tombstone, a later mention resolves to the canonical) and the §P-6 self-merge
// rejection.
func TestPersonMerge_UnionsAndRejectsSelf(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())
	_ = seedPerson(t, a, "Andy", "raw_2026_05_03_10_00", personSeedTime().Add(48*time.Hour))

	res, err := r.PersonMerge(PersonMergeRequest{Source: "Andy", Target: "Alex"})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.Record.PersonKey)
	assert.Contains(t, res.Ack, "Merged")
	assert.Contains(t, res.Ack, "resolve to one record")

	// A later "Andy" mention now folds into Alex.
	folded, err := a.UpdatePerson(newMention("Andy", "raw_2026_05_09_20_10", personSeedTime().Add(96*time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, keyAlex, folded.PersonKey)

	// Merging a person into themselves (any resolving form) is §P-6.
	_, err = r.PersonMerge(PersonMergeRequest{Source: "Andy", Target: "Alex"})
	require.ErrorIs(t, err, ErrPersonMergeSelf)
	_, err = r.PersonMerge(PersonMergeRequest{Source: keyAlex, Target: keyAlex})
	require.ErrorIs(t, err, ErrPersonMergeSelf)
}

// TestPersonAlias_IdempotentAndConflict covers the alias happy path (a later
// mention of the form resolves here), idempotency, and the §P-7 hijack refusal.
func TestPersonAlias_IdempotentAndConflict(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())
	_ = seedPerson(t, a, "Bob", "raw_2026_05_02_09_00", personSeedTime().Add(24*time.Hour))

	res, err := r.PersonAlias(PersonAliasRequest{Subject: "Alex", Form: "Ali"})
	require.NoError(t, err)
	assert.Contains(t, res.Record.Aka, "Ali")

	folded, err := a.UpdatePerson(newMention("Ali", "raw_2026_05_09_20_10", personSeedTime().Add(96*time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, keyAlex, folded.PersonKey)

	// Idempotent.
	_, err = r.PersonAlias(PersonAliasRequest{Subject: "Alex", Form: "Ali"})
	require.NoError(t, err)

	// Aliasing a form that already belongs to Bob is §P-7.
	_, err = r.PersonAlias(PersonAliasRequest{Subject: "Alex", Form: "Bob"})
	require.ErrorIs(t, err, ErrPersonAliasTaken)

	// A blank form is a §P-9 input rejection.
	_, err = r.PersonAlias(PersonAliasRequest{Subject: "Alex", Form: "   "})
	require.ErrorIs(t, err, ErrPersonRejected)
}

// TestPersonRename_KeyStableAndNameTaken covers the rename happy path (key
// unchanged, old form kept) and the §P-8 name-taken refusal.
func TestPersonRename_KeyStableAndNameTaken(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())
	_ = seedPerson(t, a, "Bob", "raw_2026_05_02_09_00", personSeedTime().Add(24*time.Hour))

	res, err := r.PersonRename(PersonRenameRequest{Subject: "Alex", NewName: "Alexandra"})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.Record.PersonKey, "the key is stable")
	assert.Equal(t, "Alexandra", res.Record.DisplayName)
	assert.Contains(t, res.Record.Aka, "Alex")
	assert.Contains(t, res.Ack, "person_key is unchanged")

	// Rename onto Bob's name is §P-8.
	_, err = r.PersonRename(PersonRenameRequest{Subject: "Alexandra", NewName: "Bob"})
	require.ErrorIs(t, err, ErrPersonNameTaken)

	// A blank new name is a §P-9 input rejection.
	_, err = r.PersonRename(PersonRenameRequest{Subject: "Alexandra", NewName: "  "})
	require.ErrorIs(t, err, ErrPersonRejected)
}

// TestPersonSet_FieldsAndValidation covers set writing the durable fields, the
// §P-9 bad-dob rejection, and the no-fields rejection.
func TestPersonSet_FieldsAndValidation(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_ = seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())

	dob, rel := "1990-04-12", "colleague"
	res, err := r.PersonSet(PersonSetRequest{Subject: "Alex", Dob: &dob, Relationship: &rel})
	require.NoError(t, err)
	require.NotNil(t, res.Record.Dob)
	assert.Equal(t, "1990-04-12", *res.Record.Dob)
	assert.Equal(t, "colleague", *res.Record.Relationship)
	assert.Contains(t, res.Ack, "dob 1990-04-12")

	bad := "04/12/1990"
	_, err = r.PersonSet(PersonSetRequest{Subject: "Alex", Dob: &bad})
	require.ErrorIs(t, err, ErrPersonRejected)

	_, err = r.PersonSet(PersonSetRequest{Subject: "Alex"})
	require.ErrorIs(t, err, ErrPersonRejected)
}

// TestPersonOffLimits_AddAndRestore covers toggling redaction and its
// idempotency in both directions.
func TestPersonOffLimits_AddAndRestore(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_01_09_00", personSeedTime())

	res, err := r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Alex"})
	require.NoError(t, err)
	assert.True(t, res.OffLimits)
	assert.Contains(t, res.Ack, "now off-limits")
	assertOffLimits(t, a, []string{keyAlex})

	// Idempotent add.
	_, err = r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Alex"})
	require.NoError(t, err)
	assertOffLimits(t, a, []string{keyAlex})

	// Restore, then idempotent restore.
	res, err = r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Alex", Restore: true})
	require.NoError(t, err)
	assert.False(t, res.OffLimits)
	assert.Contains(t, res.Ack, "no longer off-limits")
	assertOffLimits(t, a, nil)

	_, err = r.PersonOffLimits(PersonOffLimitsRequest{Subject: "Alex", Restore: true})
	require.NoError(t, err)
	assertOffLimits(t, a, nil)
}

// TestResolvePersonSubject_UnknownAmbiguousExact covers the three subject
// resolution outcomes: no live match (§P-4), several matches (§P-5), and an
// exact key (including a tombstone key that resolves forward).
func TestResolvePersonSubject_UnknownAmbiguousExact(t *testing.T) {
	r, a, home := newBootedRouter(t)

	// §P-4 on an empty store.
	_, err := r.PersonSet(PersonSetRequest{Subject: "Nobody", Note: strptrR("x")})
	require.ErrorIs(t, err, ErrPersonUnknownSubject)

	// §P-5: two records sharing an aka.
	writePersonFile(t, home, "person_s-quiet", "Sam", []string{"S.", "Sam"}, personSeedTime())
	writePersonFile(t, home, "person_s-river", "Sammy", []string{"S.", "Sammy"}, personSeedTime().Add(24*time.Hour))
	_, err = r.PersonSet(PersonSetRequest{Subject: "S.", Note: strptrR("x")})
	require.ErrorIs(t, err, ErrPersonAmbiguousSubject)

	// Exact key resolves even when it shares a name — no ambiguity.
	res, err := r.PersonSet(PersonSetRequest{Subject: "person_s-quiet", Relationship: strptrR("colleague")})
	require.NoError(t, err)
	assert.Equal(t, "person_s-quiet", res.Record.PersonKey)

	// A tombstone key resolves forward to its canonical.
	keyAlex := seedPerson(t, a, "Alex", "raw_2026_05_05_19_42", personSeedTime())
	keyAndy := seedPerson(t, a, "Andy", "raw_2026_05_06_08_10", personSeedTime().Add(24*time.Hour))
	_, err = r.PersonMerge(PersonMergeRequest{Source: keyAndy, Target: keyAlex})
	require.NoError(t, err)
	res, err = r.PersonSet(PersonSetRequest{Subject: keyAndy, Note: strptrR("via tombstone")})
	require.NoError(t, err)
	assert.Equal(t, keyAlex, res.Record.PersonKey, "the tombstone key resolves to the canonical")
}

// newMention builds a PersonMention for the curation tests.
func newMention(display, rawID string, at time.Time) storage.PersonMention {
	return storage.PersonMention{DisplayName: display, RawEntryID: rawID, At: at}
}

// assertOffLimits asserts the off-limits registry holds exactly want (order- and
// nil/empty-insensitive, since a cleared registry reads back as an empty slice).
func assertOffLimits(t *testing.T, a *storage.Adapter, want []string) {
	t.Helper()
	got, err := a.ReadOffLimitsPersonKeys()
	require.NoError(t, err)
	assert.ElementsMatch(t, want, got)
}

// strptrR returns a pointer to s, for building sparse set requests.
func strptrR(s string) *string { return &s }

// TestPersonCreate_NoModelCall proves the create path is model-free: the router
// is booted with no provider wired (newBootedRouter), yet PersonCreate succeeds
// and writes the record — impossible if minting a person needed a model.
func TestPersonCreate_NoModelCall(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	res, err := r.PersonCreate(PersonCreateRequest{Name: "Alex", Now: personSeedTime()})
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.Equal(t, `Recorded "Alex".`, res.Ack)

	rec, found, err := a.ReadPerson(res.Record.PersonKey)
	require.NoError(t, err)
	require.True(t, found, "the record was actually written with no model in play")
	assert.Equal(t, "Alex", rec.DisplayName)
	assert.Equal(t, []string{"Alex"}, rec.Aka)
	assert.Empty(t, rec.EntryRefs, "a never-mentioned person carries no entry_refs")
}

// TestPersonCreate_LaterMentionMatches proves key parity: a later bare mention of
// the same name folds into the deliberately-created record (same key, widened
// window, entry_ref added) rather than forking a duplicate.
func TestPersonCreate_LaterMentionMatches(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	res, err := r.PersonCreate(PersonCreateRequest{Name: "Alex", Now: personSeedTime()})
	require.NoError(t, err)
	require.True(t, res.Created)
	createdKey := res.Record.PersonKey

	folded, err := a.UpdatePerson(newMention("Alex", "raw_2026_05_10_11_00", personSeedTime().Add(96*time.Hour)))
	require.NoError(t, err)
	assert.Equal(t, createdKey, folded.PersonKey, "a later mention folds into the created record, not a fork")

	rec, found, err := a.ReadPerson(createdKey)
	require.NoError(t, err)
	require.True(t, found)
	assert.Contains(t, rec.EntryRefs, "raw_2026_05_10_11_00", "the mention widened the record")
	assert.True(t, personSeedTime().Equal(rec.FirstSeenAt), "first_seen stays the creation instant")
	assert.True(t, rec.LastSeenAt.After(rec.FirstSeenAt), "the mention widened the seen-window")

	keys, err := a.ListPeopleKeys()
	require.NoError(t, err)
	assert.Len(t, keys, 1, "no duplicate record forked")
}

// TestPersonCreate_ReconcileNoDuplicates proves reconcile stays clean after the
// create → later-mention flow: one record folds the mention in, so reconcile has
// nothing to flag.
func TestPersonCreate_ReconcileNoDuplicates(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	res, err := r.PersonCreate(PersonCreateRequest{Name: "Alex", Now: personSeedTime()})
	require.NoError(t, err)
	require.True(t, res.Created)

	_, err = a.UpdatePerson(newMention("Alex", "raw_2026_05_11_09_30", personSeedTime().Add(96*time.Hour)))
	require.NoError(t, err)

	rec, err := r.PersonReconcile()
	require.NoError(t, err)
	assert.Empty(t, rec.Candidates, "one record — reconcile has nothing to flag")
	assert.Equal(t, reconcileNoCandidates, rec.Text)
}

// TestPersonCreate_IdempotentNoOp proves a repeat create is an idempotent no-op
// (Q2=A): Created=false, an "already recorded" ack, no second record, and the
// durable fields are never rewritten even when flags are passed (A1).
func TestPersonCreate_IdempotentNoOp(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	first, err := r.PersonCreate(PersonCreateRequest{Name: "Alex", Now: personSeedTime()})
	require.NoError(t, err)
	require.True(t, first.Created)

	second, err := r.PersonCreate(PersonCreateRequest{Name: "Alex", Now: personSeedTime().Add(24 * time.Hour)})
	require.NoError(t, err)
	assert.False(t, second.Created, "a repeat create is a no-op")
	assert.Equal(t, first.Record.PersonKey, second.Record.PersonKey)
	assert.Contains(t, second.Ack, "already recorded")

	// Even with flags, an existing-key create leaves durable fields for `set` (A1).
	third, err := r.PersonCreate(PersonCreateRequest{Name: "Alex", Relationship: strptrR("colleague"), Now: personSeedTime()})
	require.NoError(t, err)
	assert.False(t, third.Created)
	assert.Nil(t, third.Record.Relationship, "the no-op never enriches — that stays `person set`")

	keys, err := a.ListPeopleKeys()
	require.NoError(t, err)
	assert.Len(t, keys, 1, "no second record forked")
}

// TestPersonCreate_CreatesThenEnriches proves create-then-enrich in one call
// (Q3=A): the durable-field flags land on the fresh record, and name-only create
// stays valid.
func TestPersonCreate_CreatesThenEnriches(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	dob, rel, note := "1990-04-12", "colleague", "met at the conference"
	res, err := r.PersonCreate(PersonCreateRequest{
		Name: "Sam Rivera", Dob: &dob, Relationship: &rel, Note: &note, Now: personSeedTime(),
	})
	require.NoError(t, err)
	require.True(t, res.Created)
	require.NotNil(t, res.Record.Dob)
	assert.Equal(t, "1990-04-12", *res.Record.Dob)
	require.NotNil(t, res.Record.Relationship)
	assert.Equal(t, "colleague", *res.Record.Relationship)
	require.NotNil(t, res.Record.Notes)
	assert.Equal(t, "met at the conference", *res.Record.Notes)
	assert.Contains(t, res.Ack, "dob 1990-04-12")

	rec, found, err := a.ReadPerson(res.Record.PersonKey)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, rec.Relationship)
	assert.Equal(t, "colleague", *rec.Relationship, "the enrichment persisted")

	// Name-only create is valid.
	bare, err := r.PersonCreate(PersonCreateRequest{Name: "Jordan", Now: personSeedTime()})
	require.NoError(t, err)
	assert.True(t, bare.Created)
	assert.Equal(t, `Recorded "Jordan".`, bare.Ack)
	assert.Nil(t, bare.Record.Dob)
}

// TestPersonCreate_BadDobRejected covers the §P-9 input rejections: a non-civil
// dob and a blank name both return ErrPersonRejected and write nothing.
func TestPersonCreate_BadDobRejected(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	bad := "04/12/1990"
	_, err := r.PersonCreate(PersonCreateRequest{Name: "Alex", Dob: &bad, Now: personSeedTime()})
	require.ErrorIs(t, err, ErrPersonRejected)

	_, err = r.PersonCreate(PersonCreateRequest{Name: "   ", Now: personSeedTime()})
	require.ErrorIs(t, err, ErrPersonRejected)

	keys, err := a.ListPeopleKeys()
	require.NoError(t, err)
	assert.Empty(t, keys, "a rejected create writes nothing")
}

// TestPersonCreate_RendersAndValidates proves a never-mentioned created person is
// a well-formed record: /person renders it cleanly (0 entries, no zero-time), and
// the store passes the schema + redirect-graph checks.
func TestPersonCreate_RendersAndValidates(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	res, err := r.PersonCreate(PersonCreateRequest{Name: "Alex", Now: personSeedTime()})
	require.NoError(t, err)
	require.True(t, res.Created)

	view, err := r.Person(PersonRequest{Name: "Alex"})
	require.NoError(t, err)
	require.True(t, view.Matched)
	assert.Contains(t, view.Text, "Mentioned in 0 entries")
	assert.NotContains(t, view.Text, "0001-01-01", "the seen-window is the creation date, never the zero time")

	src := personLedgerSource{a: a}
	schema, err := validate.CheckLedgerSchema(src)
	require.NoError(t, err)
	assert.Empty(t, schema, "a deliberately-created person passes the schema check")
	graph, err := validate.CheckPeopleRedirectGraph(src)
	require.NoError(t, err)
	assert.Empty(t, graph, "no dangling redirect from a created person")
}

// TestPersonCreate_SetUnknownSubjectUnchanged guards against regressing `set`:
// create mints identity, but `person set` on an unknown subject still errors as
// it does today (§P-4).
func TestPersonCreate_SetUnknownSubjectUnchanged(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	_, err := r.PersonSet(PersonSetRequest{Subject: "Nobody At All", Relationship: strptrR("x")})
	require.ErrorIs(t, err, ErrPersonUnknownSubject)
}

// personLedgerSource adapts the storage adapter to validate.LedgerSource so the
// create tests can run the schema + redirect-graph checks over the store the
// router wrote. It mirrors the cli ledgerAdapter, kept local to the test since
// that wrapper is unexported.
type personLedgerSource struct{ a *storage.Adapter }

func (s personLedgerSource) Home() string                        { return s.a.Home() }
func (s personLedgerSource) ListProcessedIDs() ([]string, error) { return s.a.ListProcessedIDs() }
func (s personLedgerSource) ReadProcessedErr(id string) error {
	_, err := s.a.ReadProcessed(id)
	return err
}
func (s personLedgerSource) ListInsightIDs() ([]string, error) { return s.a.ListInsightIDs() }
func (s personLedgerSource) ReadInsightErr(id string) error {
	_, err := s.a.ReadInsight(id)
	return err
}
func (s personLedgerSource) ListReflectionIDs() ([]string, error) { return s.a.ListReflectionIDs() }
func (s personLedgerSource) ReadReflectionErr(id string) error {
	_, err := s.a.ReadReflection(id)
	return err
}
func (s personLedgerSource) ListPeopleKeys() ([]string, error) { return s.a.ListPeopleKeys() }
func (s personLedgerSource) ReadPersonErr(key string) error {
	_, _, err := s.a.ReadPerson(key)
	return err
}
func (s personLedgerSource) ListLinkEventIDs() ([]string, error) { return s.a.ListLinkEventIDs() }
func (s personLedgerSource) ReadLinkEventErr(id string) error    { return s.a.ReadLinkEventErr(id) }
func (s personLedgerSource) ListSecretRefIDs() ([]string, error) { return s.a.ListSecretRefIDs() }
func (s personLedgerSource) ReadSecretRefErr(id string) error    { return s.a.ReadSecretRefErr(id) }
func (s personLedgerSource) LoadConfigErr() error                { _, err := s.a.LoadConfig(); return err }

// PersonRedirect returns the redirect_to of one person record, backing the
// redirect-graph check; an unreadable/absent record resolves to "".
func (s personLedgerSource) PersonRedirect(key string) (string, error) {
	rec, found, err := s.a.ReadPerson(key)
	if err != nil || !found {
		return "", err
	}
	return rec.RedirectTo, nil
}
