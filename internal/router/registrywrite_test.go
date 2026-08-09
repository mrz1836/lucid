package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestWriteEra_RangeRoundTrip proves an era carries a backdate-aware start/end
// range with precision recorded alongside, through the same append-only merge
// path as injury.
func TestWriteEra_RangeRoundTrip(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	created, err := r.WriteEra(EraWriteRequest{
		Name:  "the Lisbon years",
		Start: "2014",
		End:   "2018-12-31",
		Now:   fixedNow(),
	})
	require.NoError(t, err)
	assert.True(t, created.Created)
	assert.Equal(t, observations.RegistryEra, created.Kind)
	assert.Equal(t, observations.StatusActive, created.Status)
	assert.Equal(t, "2014", created.Fields["start"], "a partial start is stored as typed, never snapped to 2014-01-01")
	assert.Equal(t, observations.PrecisionApproximate, created.Fields["start_precision"])
	assert.Equal(t, "2018-12-31", created.Fields["end"], "a bare date end normalizes to YYYY-MM-DD")
	assert.Equal(t, observations.PrecisionApproximate, created.Fields["end_precision"])

	// An open-ended amend leaves start intact and appends history.
	amended, err := r.WriteEra(EraWriteRequest{Name: "the Lisbon years", Note: "chapter still open", Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, amended.Created)
	assert.Equal(t, "2014", amended.Fields["start"], "prior range preserved on amend")
	assert.Equal(t, "chapter still open", amended.Fields["note"])

	rec, found, err := a.ReadRegistry(observations.RegistryEra, amended.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Len(t, rec.StatusHistory, 2, "amend appends, never overwrites")
}

// TestWriteThread_IntentAndDomains proves a thread round-trips its intent and
// domains and carries NO progress/percent/streak field — the obliquity guard.
func TestWriteThread_IntentAndDomains(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	res, err := r.WriteThread(ThreadWriteRequest{
		Name:    "learning to rest",
		Intent:  "let recovery be a practice, not a failure",
		Domains: []string{"body", "  ", "mind"},
		Now:     fixedNow(),
	})
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.Equal(t, observations.RegistryThread, res.Kind)
	assert.Equal(t, "let recovery be a practice, not a failure", res.Fields["intent"])
	assert.Equal(t, []string{"body", "mind"}, res.Fields["domains"], "blank domains dropped")

	// No progress-shaped key exists on the stored record (the obliquity guard).
	rec, found, err := a.ReadRegistry(observations.RegistryThread, res.Key)
	require.NoError(t, err)
	require.True(t, found)
	for key := range rec.Fields {
		assert.NotContains(t, key, "progress", "thread carries no progress field")
		assert.NotContains(t, key, "percent", "thread carries no percent field")
		assert.NotContains(t, key, "streak", "thread carries no streak field")
	}
}

// TestStripObliquityFields guards the structural obliquity strip directly: any
// progress-shaped key is removed, allowed keys survive.
func TestStripObliquityFields(t *testing.T) {
	fields := map[string]any{
		"intent":       "keep going",
		"domains":      []string{"body"},
		"progress":     "40%",
		"percent_done": 40,
		"streak_days":  9,
	}
	stripObliquityFields(fields)
	assert.Equal(t, map[string]any{"intent": "keep going", "domains": []string{"body"}}, fields)
}

// TestResolveRegistryDate covers the registry date branches directly: an empty
// arg yields no field, a relative word and a full date normalize, a partial
// date is written back as typed, and free text is now rejected.
func TestResolveRegistryDate(t *testing.T) {
	now := fixedNow()

	t.Run("an empty arg yields no field", func(t *testing.T) {
		val, prec, err := resolveRegistryDate("", now)
		require.NoError(t, err)
		assert.Empty(t, val)
		assert.Empty(t, prec)
	})

	t.Run("relative words and full dates normalize", func(t *testing.T) {
		for _, tc := range []struct{ name, in, want string }{
			{"the @ form", "@yesterday", "2026-07-04"},
			{"the bare form", "yesterday", "2026-07-04"},
			{"a full date", "2015-06-01", "2015-06-01"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				val, prec, err := resolveRegistryDate(tc.in, now)
				require.NoError(t, err)
				assert.Equal(t, tc.want, val)
				assert.Equal(t, observations.PrecisionApproximate, prec)
			})
		}
	})

	// Behavior change: `yesterday` now steps back from the logical base date, so
	// before the 04:00 rollover it names the day before the one a bare capture
	// files under — where it used to read the plain calendar day. A daytime now
	// cannot tell the two apart, which is why this case injects 02:00.
	t.Run("yesterday is rollover-aware", func(t *testing.T) {
		preRollover := time.Date(2026, time.July, 5, 2, 0, 0, 0, time.UTC)
		val, _, err := resolveRegistryDate("@yesterday", preRollover)
		require.NoError(t, err)
		assert.Equal(t, "2026-07-03", val, "02:00 precedes the rollover, so yesterday is two calendar days back")
	})

	// The registry's one deliberate divergence from a capture: it keeps the
	// degree of imprecision instead of snapping to the period's first day.
	t.Run("a partial date is stored as typed", func(t *testing.T) {
		for _, in := range []string{"2014", "2014-09"} {
			t.Run(in, func(t *testing.T) {
				val, prec, err := resolveRegistryDate(in, now)
				require.NoError(t, err)
				assert.Equal(t, in, val, "written back as typed, never snapped to the period's first day")
				assert.Equal(t, observations.PrecisionApproximate, prec)
			})
		}
	})

	t.Run("a supplied time is parsed but not stored", func(t *testing.T) {
		val, prec, err := resolveRegistryDate("2014-09-01 19:30", now)
		require.NoError(t, err)
		assert.Equal(t, "2014-09-01", val, "registry fields are date-granular, so the time truncates away")
		assert.Equal(t, observations.PrecisionApproximate, prec)
	})

	// The one capability the unified grammar removed (life-archive.md §4): free
	// text used to be stored exactly as typed, and is now a clean refusal.
	t.Run("free text is rejected", func(t *testing.T) {
		val, prec, err := resolveRegistryDate("spring 2015", now)
		require.Error(t, err)
		assert.Empty(t, val, "nothing is resolved, so nothing can be stored")
		assert.Empty(t, prec)
		assert.Contains(t, err.Error(), acceptedRegistryDateForms, "the refusal names the accepted forms")
		assert.Contains(t, err.Error(), "--note", "the refusal points prose at --note")
		assert.Contains(t, err.Error(), "nothing was saved")
	})

	// The ceiling reads the resolved instant while the typed string is what gets
	// stored, so the two ends of the current year land on opposite sides of it.
	t.Run("a future date is rejected via its resolved day", func(t *testing.T) {
		_, _, err := resolveRegistryDate("2027", now)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "2027-01-01", "a partial future date is refused via its snapped day")
		assert.Contains(t, err.Error(), "has not happened yet")
		assert.Contains(t, err.Error(), "nothing was saved")

		val, _, err := resolveRegistryDate("2026", now)
		require.NoError(t, err, "the current year snaps to a past instant, so it is accepted")
		assert.Equal(t, "2026", val, "and is still stored as typed")
	})
}

// TestValidRegistryStatus covers the kind-aware status vocabularies and pins
// injury/thread behavior while pets use their own lifecycle terms.
func TestValidRegistryStatus(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		valid   []string
		invalid []string
	}{
		{
			kind:    observations.RegistryInjury,
			valid:   []string{"", observations.StatusActive, observations.StatusManaged, observations.StatusResolved},
			invalid: []string{observations.StatusRehomed, observations.StatusPassed, "flaring"},
		},
		{
			kind:    observations.RegistryThread,
			valid:   []string{"", observations.StatusActive, observations.StatusManaged, observations.StatusResolved},
			invalid: []string{observations.StatusRehomed, observations.StatusPassed, "paused"},
		},
		{
			kind:    observations.RegistryPet,
			valid:   []string{"", observations.StatusActive, observations.StatusRehomed, observations.StatusPassed},
			invalid: []string{observations.StatusManaged, observations.StatusResolved, "ACTIVE"},
		},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			for _, status := range tc.valid {
				assert.True(t, validRegistryStatus(tc.kind, status), "%q should be valid", status)
			}
			for _, status := range tc.invalid {
				assert.False(t, validRegistryStatus(tc.kind, status), "%q should be rejected", status)
			}
		})
	}
}

// TestWritePet_CreateThenAmend proves pet Fields round-trip through the shared
// registry core and an amend appends status history rather than rewriting it.
func TestWritePet_CreateThenAmend(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	created, err := r.WritePet(PetWriteRequest{
		Name:    "Fixture Pet",
		Species: " dog ",
		Note:    "likes the window",
		Now:     fixedNow(),
	})
	require.NoError(t, err)
	assert.True(t, created.Created)
	assert.Equal(t, observations.RegistryPet, created.Kind)
	assert.Equal(t, observations.StatusActive, created.Status)
	assert.Equal(t, "dog", created.Fields["species"])
	assert.Equal(t, "likes the window", created.Fields["note"])
	assert.Len(t, created.Fields, 2, "pet stores only the documented Fields")

	amended, err := r.WritePet(PetWriteRequest{
		Name:   "Fixture Pet",
		Status: observations.StatusRehomed,
		Note:   "settled into a new home",
		Now:    fixedNow().Add(time.Hour),
	})
	require.NoError(t, err)
	assert.False(t, amended.Created)
	assert.Equal(t, created.Key, amended.Key)
	assert.Equal(t, observations.StatusRehomed, amended.Status)
	assert.Equal(t, "dog", amended.Fields["species"], "prior Fields survive an amend")

	rec, found, err := a.ReadRegistry(observations.RegistryPet, amended.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, rec.StatusHistory, 2, "amend appends a new history entry")
	assert.Equal(t, observations.StatusActive, rec.StatusHistory[0].Status)
	assert.Equal(t, observations.StatusRehomed, rec.StatusHistory[1].Status)
}

// TestWritePet_RejectsInvalidInput proves validation happens before persistence.
func TestWritePet_RejectsInvalidInput(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.WritePet(PetWriteRequest{Name: " ", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a name")

	_, err = r.WritePet(PetWriteRequest{Name: "Fixture Pet", Status: observations.StatusManaged, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "want active, rehomed, or passed")
	assert.Contains(t, err.Error(), "nothing was saved")

	recs, err := a.ReadRegistryKind(observations.RegistryPet)
	require.NoError(t, err)
	assert.Empty(t, recs)
}

// TestWriteEra_RejectsEmptyName rejects a blank era name before any write.
func TestWriteEra_RejectsEmptyName(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	_, err := r.WriteEra(EraWriteRequest{Name: "  ", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a name")
}

// TestWriteEra_RejectsUnreadableDate proves the strict tier reaches the write
// verb: free text on either bound is refused before any field is built, so the
// registry is left empty rather than carrying a half-applied range.
func TestWriteEra_RejectsUnreadableDate(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	for _, tc := range []struct {
		name string
		req  EraWriteRequest
	}{
		{"free-text start", EraWriteRequest{Name: "the coast years", Start: "spring 2015", Now: fixedNow()}},
		{"free-text end", EraWriteRequest{Name: "the coast years", Start: "2010", End: "sometime later", Now: fixedNow()}},
		{"future start", EraWriteRequest{Name: "the coast years", Start: "2027", Now: fixedNow()}},
		{"future end", EraWriteRequest{Name: "the coast years", Start: "2010", End: "2030-01-01", Now: fixedNow()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.WriteEra(tc.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "nothing was saved")
		})
	}

	recs, err := a.ReadRegistryKind(observations.RegistryEra)
	require.NoError(t, err)
	assert.Empty(t, recs, "a rejected date leaves no record behind")
}

// TestWriteInjury_RejectsUnreadableOnset proves the same refusal on the injury
// verb, including the future ceiling these fields never used to carry.
func TestWriteInjury_RejectsUnreadableOnset(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.WriteInjury(InjuryWriteRequest{Name: "left knee", Onset: "spring 2015", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--note", "the refusal points prose at --note")

	_, err = r.WriteInjury(InjuryWriteRequest{Name: "left knee", Onset: "2027-03-01", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not happened yet")

	recs, err := a.ReadRegistryKind(observations.RegistryInjury)
	require.NoError(t, err)
	assert.Empty(t, recs, "a rejected onset leaves no record behind")
}

// TestWritePet_LifeSpanRoundTrip proves a pet carries a backdate-aware start/end
// life-span with precision recorded alongside — the same strict grammar and
// as-typed storage as an era — through the shared append-only merge, and that a
// later amend preserves the range while appending history.
func TestWritePet_LifeSpanRoundTrip(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	created, err := r.WritePet(PetWriteRequest{
		Name:    "Old Sable",
		Species: "dog",
		Status:  observations.StatusPassed,
		Start:   "2005",
		End:     "2012-06",
		Now:     fixedNow(),
	})
	require.NoError(t, err)
	assert.True(t, created.Created)
	assert.Equal(t, observations.RegistryPet, created.Kind)
	assert.Equal(t, observations.StatusPassed, created.Status)
	assert.Equal(t, "2005", created.Fields["start"], "a year-only start is stored as typed, never snapped to 2005-01-01")
	assert.Equal(t, observations.PrecisionApproximate, created.Fields["start_precision"])
	assert.Equal(t, "2012-06", created.Fields["end"], "a YYYY-MM end is stored as typed")
	assert.Equal(t, observations.PrecisionApproximate, created.Fields["end_precision"])
	assert.Equal(t, "dog", created.Fields["species"])

	// An amend that only adds a note leaves the life-span and status intact.
	amended, err := r.WritePet(PetWriteRequest{Name: "Old Sable", Note: "best good boy", Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, amended.Created)
	assert.Equal(t, observations.StatusPassed, amended.Status, "an amend with no status keeps the status in force")
	assert.Equal(t, "2005", amended.Fields["start"], "prior range preserved on amend")
	assert.Equal(t, "2012-06", amended.Fields["end"])
	assert.Equal(t, "best good boy", amended.Fields["note"])

	rec, found, err := a.ReadRegistry(observations.RegistryPet, amended.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Len(t, rec.StatusHistory, 2, "amend appends, never overwrites")
}

// TestWritePet_RejectsUnreadableOrFutureDate proves the strict tier reaches the
// pet verb on both bounds: free text and a future day are refused before any
// field is built, so the registry is left empty rather than half-written.
func TestWritePet_RejectsUnreadableOrFutureDate(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	for _, tc := range []struct {
		name string
		req  PetWriteRequest
	}{
		{"free-text start", PetWriteRequest{Name: "Ghost Pup", Start: "last summer", Now: fixedNow()}},
		{"free-text end", PetWriteRequest{Name: "Ghost Pup", Start: "2005", End: "sometime later", Now: fixedNow()}},
		{"future start", PetWriteRequest{Name: "Ghost Pup", Start: "2030", Now: fixedNow()}},
		{"future end", PetWriteRequest{Name: "Ghost Pup", Start: "2005", End: "2030-01-01", Now: fixedNow()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.WritePet(tc.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "nothing was saved")
		})
	}

	recs, err := a.ReadRegistryKind(observations.RegistryPet)
	require.NoError(t, err)
	assert.Empty(t, recs, "a rejected date leaves no pet behind")
}

// TestWriteThread_RejectsBadStatus rejects an unknown thread status with
// nothing written.
func TestWriteThread_RejectsBadStatus(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	_, err := r.WriteThread(ThreadWriteRequest{Name: "rest", Status: "paused", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")

	recs, err := a.ReadRegistryKind(observations.RegistryThread)
	require.NoError(t, err)
	assert.Empty(t, recs)
}
