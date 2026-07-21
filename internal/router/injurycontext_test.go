package router

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestInjuryContext_ThroughWriteVerbs threads the real seam: injuries created
// and amended through `lucid injury` (WriteInjury) surface in the router
// projection a workout planner consumes — active + managed only, resolved
// excluded, in byte-stable key order, with the convention Fields mapped onto
// the struct.
func TestInjuryContext_ThroughWriteVerbs(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.WriteInjury(InjuryWriteRequest{
		Name: "left knee", Status: observations.StatusManaged,
		BodyArea: "left knee", CurrentLimitations: "no deep squats under load",
		Timeline: "since 2014", Severity: "mild now", Now: fixedNow(),
	})
	require.NoError(t, err)
	_, err = r.WriteInjury(InjuryWriteRequest{
		Name: "right shoulder", Status: observations.StatusActive,
		BodyArea: "right shoulder", Now: fixedNow(),
	})
	require.NoError(t, err)
	// A resolved injury must NOT surface in the projection.
	_, err = r.WriteInjury(InjuryWriteRequest{
		Name: "old ankle roll", Status: observations.StatusResolved,
		BodyArea: "left ankle", Now: fixedNow(),
	})
	require.NoError(t, err)

	ctx, err := r.InjuryContext()
	require.NoError(t, err)

	require.Len(t, ctx, 2, "resolved injuries are excluded from the projection")
	// Byte-stable: sorted by key.
	keys := make([]string, len(ctx))
	names := make([]string, len(ctx))
	for i, c := range ctx {
		keys[i] = c.Key
		names[i] = c.DisplayName
	}
	assert.True(t, slices.IsSorted(keys), "projection is byte-stable in key order")
	assert.Contains(t, names, "left knee")
	assert.Contains(t, names, "right shoulder")
	assert.NotContains(t, names, "old ankle roll")

	// The knee's convention Fields survived the round-trip onto the struct.
	var knee bool
	for _, c := range ctx {
		if c.DisplayName == "left knee" {
			knee = true
			assert.Equal(t, observations.StatusManaged, c.Status)
			assert.Equal(t, "no deep squats under load", c.CurrentLimitations)
			assert.Equal(t, "since 2014", c.Timeline)
			assert.Equal(t, "mild now", c.Severity)
		}
	}
	assert.True(t, knee, "the managed knee surfaced")
}

// TestInjuryContext_EmptyStore proves the seam degrades to an honest empty
// result over a fresh Ledger — no injuries, no error, no panic.
func TestInjuryContext_EmptyStore(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	ctx, err := r.InjuryContext()
	require.NoError(t, err)
	assert.Empty(t, ctx, "an empty registry projects to an empty slice, not an error")
}

// TestInjuryContext_NoDiagnosticLanguage guards the sanctuary boundary at the
// router seam: the projection carries the user's registry facts verbatim and
// adds no diagnostic or treatment language of its own (observations.md §9).
func TestInjuryContext_NoDiagnosticLanguage(t *testing.T) {
	r, _, _ := newBootedRouter(t)

	_, err := r.WriteInjury(InjuryWriteRequest{
		Name: "left knee", Status: observations.StatusManaged,
		BodyArea: "left knee", CurrentLimitations: "no deep squats", Severity: "moderate",
		Now: fixedNow(),
	})
	require.NoError(t, err)

	ctx, err := r.InjuryContext()
	require.NoError(t, err)
	require.Len(t, ctx, 1)

	rendered := strings.ToLower(strings.Join([]string{
		ctx[0].DisplayName, ctx[0].Status, ctx[0].BodyArea,
		ctx[0].CurrentLimitations, ctx[0].Timeline, ctx[0].Severity,
	}, " "))
	for _, banned := range []string{"diagnos", "prescrib", "you should", "treatment plan", "recommend", "consult a"} {
		assert.NotContains(t, rendered, banned,
			"the seam renders registry facts only, never synthesized clinical advice")
	}
}
