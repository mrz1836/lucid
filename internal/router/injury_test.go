package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestWriteInjury_CreateThenAmend is the append-only round-trip: a first
// mention mints an active record with one status_history entry and the
// convention Fields; a second write amends it — merging new Fields, transitioning
// status, and appending (never overwriting) a second history entry.
func TestWriteInjury_CreateThenAmend(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	created, err := r.WriteInjury(InjuryWriteRequest{
		Name:     "left knee",
		BodyArea: "left knee",
		Cause:    "landed wrong off a boulder",
		Now:      fixedNow(),
	})
	require.NoError(t, err)
	assert.True(t, created.Created, "first mention is a create")
	assert.Equal(t, observations.RegistryInjury, created.Kind)
	assert.Equal(t, observations.StatusActive, created.Status)
	assert.Equal(t, "left knee", created.Fields["body_area"])
	assert.Equal(t, "landed wrong off a boulder", created.Fields["cause"])
	assert.Contains(t, created.Ack, "Recorded")
	assert.Contains(t, created.Ack, created.Key)

	// On disk: exactly one status_history entry, active.
	rec, found, err := a.ReadRegistry(observations.RegistryInjury, created.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Len(t, rec.StatusHistory, 1, "create writes one history entry")
	assert.Equal(t, observations.StatusActive, rec.StatusHistory[0].Status)

	// Amend the same injury: merge a new field + transition to managed.
	amended, err := r.WriteInjury(InjuryWriteRequest{
		Name:               "left knee",
		Status:             observations.StatusManaged,
		Severity:           "moderate now",
		CurrentLimitations: "no deep squats under load",
		Now:                fixedNow(),
	})
	require.NoError(t, err)
	assert.False(t, amended.Created, "an existing record is an amend")
	assert.Equal(t, created.Key, amended.Key, "same salted key")
	assert.Equal(t, observations.StatusManaged, amended.Status)
	assert.Contains(t, amended.Ack, "Updated")

	// Fields merged (the create's fields survive), history appended not replaced.
	assert.Equal(t, "left knee", amended.Fields["body_area"], "prior field preserved on amend")
	assert.Equal(t, "moderate now", amended.Fields["severity"], "new field merged")
	assert.Equal(t, "no deep squats under load", amended.Fields["current_limitations"])

	rec, found, err = a.ReadRegistry(observations.RegistryInjury, amended.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Len(t, rec.StatusHistory, 2, "amend appends a second history entry")
	assert.Equal(t, observations.StatusActive, rec.StatusHistory[0].Status, "first entry unchanged")
	assert.Equal(t, observations.StatusManaged, rec.StatusHistory[1].Status)
}

// TestWriteInjury_BackdatedOnset proves the onset field is backdate-aware
// through the shared observations @-grammar, with the precision recorded
// alongside — and that a free-form approximate value ("2014-09") is kept
// verbatim (capture never blocks).
func TestWriteInjury_BackdatedOnset(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	yesterday, err := r.WriteInjury(InjuryWriteRequest{Name: "ankle", Onset: "@yesterday", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "2026-07-04", yesterday.Fields["onset"], "@yesterday resolves to the prior civil date")
	assert.Equal(t, observations.PrecisionApproximate, yesterday.Fields["onset_precision"])

	dated, err := r.WriteInjury(InjuryWriteRequest{Name: "wrist", Onset: "2015-06-01", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "2015-06-01", dated.Fields["onset"], "a bare date normalizes to YYYY-MM-DD")
	assert.Equal(t, observations.PrecisionApproximate, dated.Fields["onset_precision"])

	approx, err := r.WriteInjury(InjuryWriteRequest{Name: "shoulder", Onset: "2014-09", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "2014-09", approx.Fields["onset"], "a free-form approximate value is kept verbatim")
	assert.Equal(t, observations.PrecisionApproximate, approx.Fields["onset_precision"])
}

// TestWriteInjury_BareFirstMention proves a name-only injury is a valid create
// with an empty (non-nil) Fields map — the frictionless first-mention path.
func TestWriteInjury_BareFirstMention(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	res, err := r.WriteInjury(InjuryWriteRequest{Name: "old back tweak", Now: fixedNow()})
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.Equal(t, observations.StatusActive, res.Status)
	assert.Empty(t, res.Fields, "a bare first mention carries no convention fields")
}

// TestWriteInjury_RejectsBadStatus rejects an unknown status before any write
// (error-states.md §St-1: nothing saved).
func TestWriteInjury_RejectsBadStatus(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.WriteInjury(InjuryWriteRequest{Name: "knee", Status: "flaring", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")

	recs, err := a.ReadRegistryKind(observations.RegistryInjury)
	require.NoError(t, err)
	assert.Empty(t, recs, "a rejected write leaves the registry empty")
}

// TestWriteInjury_RejectsEmptyName rejects a blank name before any write.
func TestWriteInjury_RejectsEmptyName(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.WriteInjury(InjuryWriteRequest{Name: "   ", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a name")
}
