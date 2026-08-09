package storage

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// --- Task 18: the resolver registry and RegisterSubjectKind ---

// TestRegisterSubjectKind_ThrowawayKindIsLinkable is the structural proof of the
// open taxonomy (SC-2 / AC-3): a new subject kind costs exactly one resolver and
// zero changes to the link ledger (links.go).
//
// ⚠️ Do not delete or "simplify" this test. It looks trivial — it registers a
// fake kind and links against it — but it is the entire promise the design rests
// on. It is what makes a future kind such as a pet, and every kind after it,
// free. If this ever needs a links.go edit to pass, the taxonomy has stopped
// being open, which is the one thing this feature must never lose.
func TestRegisterSubjectKind_ThrowawayKindIsLinkable(t *testing.T) {
	a := New(t.TempDir())

	const throwawayKind = "throwaway_kind"
	require.NoError(t, a.RegisterSubjectKind(throwawayKind, func(_ *Adapter, key string) error {
		if key == "known" {
			return nil
		}
		return fmt.Errorf("no throwaway subject %q", key)
	}))

	// The registered kind resolves and refuses exactly as its resolver dictates,
	// and it never touched the ledger writer to get there.
	require.NoError(t, a.ResolveSubject(throwawayKind, "known"))
	require.Error(t, a.ResolveSubject(throwawayKind, "missing"))

	// It is linkable like any built-in kind: an event against it appends and
	// folds back — proof the ledger never learned the kind, only stored it.
	_, err := a.AppendLinkEvent(LinkEvent{
		MediaID: "2026-07-05-x.jpg",
		Subject: LinkSubject{Kind: throwawayKind, Key: "known"},
		Op:      LinkOpLink,
		At:      fixedTime().Format(time.RFC3339),
		Source:  "cli",
	})
	require.NoError(t, err)
	views, err := a.LinksForSubject(throwawayKind, "known")
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, "2026-07-05-x.jpg", views[0].MediaID)
}

// TestRegisterSubjectKind_RefusesBadKindNilAndDuplicate: kind names must match
// ^[a-z][a-z0-9_]*$, a resolver must not be nil, and an already-registered kind
// is refused rather than silently replaced (which would let a caller hijack a
// built-in like person).
func TestRegisterSubjectKind_RefusesBadKindNilAndDuplicate(t *testing.T) {
	a := New(t.TempDir())
	ok := func(_ *Adapter, _ string) error { return nil }

	for _, bad := range []string{"", "Person", "1kind", "kind-dash", "kind space", "_leading", "ki:nd"} {
		require.Error(t, a.RegisterSubjectKind(bad, ok), "kind %q must be rejected", bad)
	}
	require.Error(t, a.RegisterSubjectKind("also_valid", nil), "a nil resolver must be rejected")

	require.NoError(t, a.RegisterSubjectKind("valid_kind_2", ok))
	require.Error(t, a.RegisterSubjectKind("valid_kind_2", ok), "a second registration must be refused, never a silent replace")

	// A built-in default kind cannot be silently replaced either.
	require.Error(t, a.RegisterSubjectKind("person", ok))
}

// TestSubjectKinds_ListsTheThirteenLaunchKindsSorted pins the launch set: the
// thirteen kinds resolve out of the box, sorted, so a kind can never be silently
// dropped from or added to the default taxonomy without this test noticing.
func TestSubjectKinds_ListsTheThirteenLaunchKindsSorted(t *testing.T) {
	a := New(t.TempDir())
	want := []string{
		"anchor", "day", "era", "injury", "memory", "observation",
		"person", "pet", "place", "raw", "self", "storm", "thread",
	}
	assert.Equal(t, want, a.SubjectKinds())
}

// --- Task 19: the thirteen resolvers ---

// seededSubject is one kind with a key that resolves and a well-formed key that
// does not, driven from the same seeded Ledger.
type seededSubject struct {
	kind       string
	validKey   string
	invalidKey string
}

// seedAllThirteenSubjects seeds one subject of every launch kind into a
// scaffolded Ledger and returns the resolve/refuse table over them. Every seeded
// value is synthetic — no real person, injury, place, or content.
func seedAllThirteenSubjects(t *testing.T, a *Adapter) ([]seededSubject, observations.Event) {
	t.Helper()
	at := fixedTime().Format(time.RFC3339)

	// person
	pr, err := a.UpdatePerson(PersonMention{DisplayName: "Test Alpha", RawEntryID: "raw_2026_07_05_09_00", At: fixedTime()})
	require.NoError(t, err)

	// injury / thread / era / place / pet — seeded under explicit synthetic keys.
	registrySeed := map[string]string{
		observations.RegistryInjury: "injury_test-a",
		observations.RegistryThread: "thread_test-a",
		observations.RegistryEra:    "era_test-a",
		observations.RegistryPlace:  "place_test-a",
		observations.RegistryPet:    "pet_test-a",
	}
	for kind, key := range registrySeed {
		_, uerr := a.UpdateRegistry(kind, key, observations.RegistryPatch{DisplayName: "Test " + kind, At: at})
		require.NoError(t, uerr)
	}

	// raw
	rr, err := a.WriteRaw(RawEntry{
		RecordedAt: fixedTime(), OccurredAt: fixedTime(),
		OccurredAtPrecision: PrecisionExact, Source: "cli", Command: "log", Body: "x",
	})
	require.NoError(t, err)

	// observation (a real memory) — serves both the observation and memory kinds.
	mem := seedObservation(t, a, observations.KindMemory)

	// anchor / self / storm — minted-id records under synthetic ids.
	require.NoError(t, a.AppendAnchor(engine.Anchor{ID: "anchor_2026_07_05_a", Label: "gate-a", Date: "2026-07-05", RecordedAt: at}))
	require.NoError(t, a.AppendSelfFact(engine.SelfFact{ID: "self_2026_07_05_a", Key: "trait-a", Value: "v", RecordedAt: at}))
	require.NoError(t, a.WriteStormEpisodes(engine.StormEpisode{ID: "storm_2026_07_05_a", OpenerAt: at}))

	table := []seededSubject{
		{"person", pr.PersonKey, "person_nobody"},
		{"injury", "injury_test-a", "injury_none"},
		{"thread", "thread_test-a", "thread_none"},
		{"era", "era_test-a", "era_none"},
		{"place", "place_test-a", "place_none"},
		{"pet", "pet_test-a", "pet_none"},
		{"raw", rr.RawID, "raw_2000_01_01_00_00"},
		{"day", "2020-01-01", "2999-01-01"}, // shape+ceiling: a past day resolves, a future one does not
		{"observation", mem.ID, "obs_2000_01_01_001"},
		{"anchor", "anchor_2026_07_05_a", "anchor_2000_01_01_z"},
		{"self", "self_2026_07_05_a", "self_2000_01_01_z"},
		{"storm", "storm_2026_07_05_a", "storm_2000_01_01_z"},
		{"memory", mem.ID, "obs_2000_01_01_001"},
	}
	return table, mem
}

// seedObservation appends a synthetic observation of the given kind and returns
// it with its assigned id.
func seedObservation(t *testing.T, a *Adapter, kind observations.Kind) observations.Event {
	t.Helper()
	ev, err := a.AppendObservation(observations.Event{
		Kind:                kind,
		RecordedAt:          fixedTime().Format(time.RFC3339),
		OccurredAt:          fixedTime().Format(time.RFC3339),
		OccurredAtPrecision: observations.PrecisionExact,
		LogicalDate:         "2026-07-05",
		Source:              observations.SourceMicrolog,
	})
	require.NoError(t, err)
	return ev
}

// TestSubjectResolvers_AllThirteenKindsResolve (AC-4): every launch kind resolves
// a seeded subject and refuses a well-formed key that names nothing — driven from
// one seeded Ledger so a kind can never be silently dropped from the launch set.
func TestSubjectResolvers_AllThirteenKindsResolve(t *testing.T) {
	a := newEngineAdapter(t)
	table, _ := seedAllThirteenSubjects(t, a)

	require.Len(t, table, 13, "every launch kind must be exercised")
	for _, s := range table {
		t.Run(s.kind, func(t *testing.T) {
			require.NoError(t, a.ResolveSubject(s.kind, s.validKey), "seeded %s must resolve", s.kind)
			require.Error(t, a.ResolveSubject(s.kind, s.invalidKey), "unseeded %s must be refused", s.kind)
		})
	}
}

// TestSubjectResolvers_MemoryRefusesNonMemory (AC-5): memory is an alias over a
// KindMemory observation — it resolves a real memory but refuses an observation
// of any other kind, naming the kind it actually is.
func TestSubjectResolvers_MemoryRefusesNonMemory(t *testing.T) {
	a := newEngineAdapter(t)
	mem := seedObservation(t, a, observations.KindMemory)
	other := seedObservation(t, a, observations.KindMeasurement)

	require.NoError(t, a.ResolveSubject("memory", mem.ID))

	err := a.ResolveSubject("memory", other.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a memory", "the refusal must name why the observation is not a memory")

	// The same non-memory observation still resolves as a plain observation.
	require.NoError(t, a.ResolveSubject("observation", other.ID))
}

// TestSubjectResolvers_DayShapeAndFutureCeiling: a logical day resolves on shape
// and the future ceiling alone — a past (even quiet, unrecorded) day resolves, a
// malformed date and a future date are refused by name.
func TestSubjectResolvers_DayShapeAndFutureCeiling(t *testing.T) {
	a := New(t.TempDir())

	require.NoError(t, a.ResolveSubject("day", "2020-01-01"), "a past day resolves even with nothing recorded on it")

	malformed := a.ResolveSubject("day", "2026-13-40")
	require.Error(t, malformed)
	assert.Contains(t, malformed.Error(), "not a valid")

	future := a.ResolveSubject("day", "2999-01-01")
	require.Error(t, future)
	assert.Contains(t, future.Error(), "future")
}

// --- Task 20: refusal copy, remedy naming ---

// TestResolveSubject_RefusesUnknownKind (AC-13): an unknown kind is refused by
// name, and the refusal lists every accepted kind so a typo is correctable from
// the message alone.
func TestResolveSubject_RefusesUnknownKind(t *testing.T) {
	a := New(t.TempDir())
	err := a.ResolveSubject("peron", "whatever")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "peron", "the refusal names the bad kind")
	assert.Contains(t, err.Error(), "person", "the refusal lists the accepted kinds")
	assert.Contains(t, err.Error(), "storm")
}

// TestResolveSubject_RefusesLegacyAnchorKeyWithRemedy (AC-21, D12): a
// legacy:<label> anchor key is refused outright — a read-time synthetic identity
// is never a durable link key — and the refusal names the backfill remedy.
func TestResolveSubject_RefusesLegacyAnchorKeyWithRemedy(t *testing.T) {
	a := newEngineAdapter(t)
	err := a.ResolveSubject("anchor", engine.LegacyAnchorIDPrefix+"gate-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backfill-ids", "the refusal must name the remedy command")
}

// TestResolveSubject_UnbackfilledIDsNameRemedy (AC-21, D19): a well-formed
// anchor or storm id that is not in the log yet is refused with the remedy named,
// because the likeliest reason is that the record predates ids.
func TestResolveSubject_UnbackfilledIDsNameRemedy(t *testing.T) {
	a := newEngineAdapter(t)

	anchorErr := a.ResolveSubject("anchor", "anchor_2000_01_01_a")
	require.Error(t, anchorErr)
	assert.Contains(t, anchorErr.Error(), "backfill-ids")

	stormErr := a.ResolveSubject("storm", "storm_2000_01_01_a")
	require.Error(t, stormErr)
	assert.Contains(t, stormErr.Error(), "backfill-episodes")
}

// TestResolveSubject_RefusalWritesNothing (AC-21): resolution runs before any
// AppendLinkEvent and is a pure read, so a refused subject leaves the link ledger
// byte-identical — a bad --to can never leave a half-written ledger.
func TestResolveSubject_RefusalWritesNothing(t *testing.T) {
	a := New(t.TempDir())
	_, err := a.AppendLinkEvent(syntheticLinkEvent("2026-07-05-x.jpg", "person", "person_a"))
	require.NoError(t, err)

	path := linksLogPath(a.Home())
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	require.Error(t, a.ResolveSubject("no_such_kind", "whatever"))
	require.Error(t, a.ResolveSubject("person", "person_nobody"))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused resolve must not touch the link ledger")
}

// TestResolveSubject_UnresolvedKeyNamesTheProblem: an unresolved key of a known
// kind is refused with the kind and the key named — never a bare error.
func TestResolveSubject_UnresolvedKeyNamesTheProblem(t *testing.T) {
	a := newEngineAdapter(t)
	err := a.ResolveSubject("injury", "injury_missing")
	require.Error(t, err)
	msg := err.Error()
	assert.True(t, strings.Contains(msg, "injury") && strings.Contains(msg, "injury_missing"),
		"the refusal names the kind and the key: %q", msg)
}
