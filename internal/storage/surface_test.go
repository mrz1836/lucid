package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// surfEnt is a tiny synthetic entry so the shared rotation's pick rules are
// tested independent of the focus/reframe entry types they run over in practice.
type surfEnt struct{ id string }

func surfID(e surfEnt) string { return e.id }

// TestLeastRecentlySurfaced_ColdStartTieBreakAndRecency locks the deterministic
// pick the shared rotation (surface.go) makes for both families: unsurfaced
// entries first, then least-recently-surfaced, ties broken by id ascending. The
// pool arrives already sorted by id (the caller's contract).
func TestLeastRecentlySurfaced_ColdStartTieBreakAndRecency(t *testing.T) {
	pool := []surfEnt{{"a"}, {"b"}, {"c"}}

	// Cold start: no last-surfaced dates → the lowest id wins (stable, id order).
	assert.Equal(t, "a", leastRecentlySurfaced(pool, map[string]string{}, surfID).id)

	// b and c surfaced, a unsurfaced → a wins (the empty string sorts before any
	// real YYYY-MM-DD).
	surfaced := map[string]string{"b": "2026-01-02", "c": "2026-01-01"}
	assert.Equal(t, "a", leastRecentlySurfaced(pool, surfaced, surfID).id)

	// All surfaced: the least-recent date wins (c on the 1st over b on the 2nd).
	all := map[string]string{"a": "2026-01-03", "b": "2026-01-02", "c": "2026-01-01"}
	assert.Equal(t, "c", leastRecentlySurfaced(pool, all, surfID).id)

	// Tie on date → id ascending (the stable sort preserves the input id order).
	tie := map[string]string{"a": "2026-01-01", "b": "2026-01-01", "c": "2026-01-01"}
	assert.Equal(t, "a", leastRecentlySurfaced(pool, tie, surfID).id)
}

// TestFindEntryByID covers the within-day idempotence lookup: a recorded pick is
// found in the current pool, and a pick that has left it is not.
func TestFindEntryByID(t *testing.T) {
	pool := []surfEnt{{"x"}, {"y"}}

	got, ok := findEntryByID(pool, "y", surfID)
	require.True(t, ok)
	assert.Equal(t, "y", got.id)

	_, ok = findEntryByID(pool, "gone", surfID)
	assert.False(t, ok, "a pick that has left the pool is not found")
}
