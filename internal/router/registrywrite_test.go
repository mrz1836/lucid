package router

import (
	"testing"

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
	assert.Equal(t, "2014", created.Fields["start"], "a free-form approximate start is kept verbatim")
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

// TestResolveRegistryDate covers the date-resolution branches directly: an
// empty arg yields no field, @yesterday and a bare date normalize to a civil
// date at approximate precision, and an unparseable value is kept verbatim.
func TestResolveRegistryDate(t *testing.T) {
	now := fixedNow()

	val, prec := resolveRegistryDate("", now)
	assert.Empty(t, val)
	assert.Empty(t, prec)

	val, prec = resolveRegistryDate("@yesterday", now)
	assert.Equal(t, "2026-07-04", val)
	assert.Equal(t, observations.PrecisionApproximate, prec)

	// The bare (no-@) yesterday form is accepted too.
	val, _ = resolveRegistryDate("yesterday", now)
	assert.Equal(t, "2026-07-04", val)

	val, prec = resolveRegistryDate("2015-06-01", now)
	assert.Equal(t, "2015-06-01", val)
	assert.Equal(t, observations.PrecisionApproximate, prec)

	val, prec = resolveRegistryDate("spring 2015", now)
	assert.Equal(t, "spring 2015", val, "an unrecognized value is kept verbatim")
	assert.Equal(t, observations.PrecisionApproximate, prec)
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
