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

// TestValidRegistryStatus covers the status allowlist.
func TestValidRegistryStatus(t *testing.T) {
	for _, ok := range []string{"", observations.StatusActive, observations.StatusManaged, observations.StatusResolved} {
		assert.True(t, validRegistryStatus(ok), "%q should be valid", ok)
	}
	for _, bad := range []string{"flaring", "done", "ACTIVE", "open"} {
		assert.False(t, validRegistryStatus(bad), "%q should be rejected", bad)
	}
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
