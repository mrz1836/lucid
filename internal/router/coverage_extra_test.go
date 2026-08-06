package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// anchorCalls is the shared set of anchor verbs exercised by the scaffold/read
// failure tables, each over a request that passes its pure input validation so
// the failure lands on the store, not the guard.
func anchorCalls() []struct {
	name string
	call func(r *Router) error
} {
	return []struct {
		name string
		call func(r *Router) error
	}{
		{"add", func(r *Router) error {
			_, err := r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-01-01", Now: fixedNow()})
			return err
		}},
		{"sunset", func(r *Router) error {
			_, err := r.AnchorSunset(AnchorSunsetRequest{Label: "sobriety", Now: fixedNow()})
			return err
		}},
		{"rename", func(r *Router) error {
			_, err := r.AnchorRename(AnchorRenameRequest{Label: "sobriety", NewLabel: "sober", Now: fixedNow()})
			return err
		}},
	}
}

// TestAnchor_ScaffoldFailsSurfaces covers the prepareEngine guard shared by the
// three anchor verbs under a read-only home.
func TestAnchor_ScaffoldFailsSurfaces(t *testing.T) {
	skipIfRoot(t)
	for _, tc := range anchorCalls() {
		t.Run(tc.name, func(t *testing.T) {
			r, _, home := newBootedRouter(t)
			require.NoError(t, os.Chmod(home, 0o500))
			t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
			assert.Error(t, tc.call(r))
		})
	}
}

// TestAnchor_ReadAnchorsCorruptSurfaces covers the ReadAnchors guard shared by
// the three anchor verbs against a malformed anchors.json.
func TestAnchor_ReadAnchorsCorruptSurfaces(t *testing.T) {
	for _, tc := range anchorCalls() {
		t.Run(tc.name, func(t *testing.T) {
			r, home := scaffoldedEngine(t)
			require.NoError(t, writeFileHelper(home, "engine/anchors.json", "{bad"))
			assert.Error(t, tc.call(r))
		})
	}
}

// TestAnchorAdd_AppendFailureSurfaced covers the add path's own append failure —
// the sunset and rename append failures are already locked; this closes the add.
func TestAnchorAdd_AppendFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	_, err := r.AnchorAdd(AnchorAddRequest{Label: "gate-30", Date: "2026-01-01", Now: fixedNow()})
	require.NoError(t, err)

	anchorsPath := filepath.Join(home, "engine", "anchors.json")
	require.NoError(t, os.Chmod(anchorsPath, 0o400)) // readable, so the failure is the write
	t.Cleanup(func() { _ = os.Chmod(anchorsPath, 0o600) })

	_, err = r.AnchorAdd(AnchorAddRequest{Label: "sobriety", Date: "2026-02-01", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestWriteMemory_AppendFailureSurfaces proves a story capture surfaces an event
// append failure with nothing saved.
func TestWriteMemory_AppendFailureSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, _, home := bootedMemoryRouter(t)
	obsDir := filepath.Join(home, "observations")
	require.NoError(t, os.Chmod(obsDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(obsDir, 0o700) })
	_, err := r.WriteMemory(MemoryWriteRequest{Text: "a memory", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestWriteMemory_ResolveRefsFailuresSurface proves resolving a story's place and
// people links fails the write closed rather than half-writing a story.
func TestWriteMemory_ResolveRefsFailuresSurface(t *testing.T) {
	skipIfRoot(t)

	t.Run("placeUpdateFails", func(t *testing.T) {
		r, _, home := bootedMemoryRouter(t)
		placesDir := filepath.Join(home, "registries", "places")
		require.NoError(t, os.Chmod(placesDir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(placesDir, 0o700) })
		_, err := r.WriteMemory(MemoryWriteRequest{Text: "the coast", Place: "Ocean City", Now: fixedNow()})
		require.Error(t, err)
	})

	t.Run("peopleReadFails", func(t *testing.T) {
		r, _, home := bootedMemoryRouter(t)
		peopleDir := filepath.Join(home, "people")
		require.NoError(t, os.Chmod(peopleDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(peopleDir, 0o700) })
		_, err := r.WriteMemory(MemoryWriteRequest{Text: "the road trip", People: []string{"Dana"}, Now: fixedNow()})
		require.Error(t, err)
	})
}

// TestWriteMemory_BlankPersonNameSkipped proves a whitespace-only person name is
// skipped (linking no key) rather than resolved or rejected — the empty-name
// continue in resolvePeopleKeys.
func TestWriteMemory_BlankPersonNameSkipped(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)
	res, err := r.WriteMemory(MemoryWriteRequest{Text: "a quiet memory", People: []string{"   "}, Now: fixedNow()})
	require.NoError(t, err)
	assert.NotContains(t, res.Refs, "person", "a blank name links no person key")
	_ = a
}

// TestRegistryWrite_EmptyThreadNameRejected covers the required-name guard on the
// thread verb (its sibling injury/era guards are already locked).
func TestRegistryWrite_EmptyThreadNameRejected(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	_, err := r.WriteThread(ThreadWriteRequest{Name: "  ", Intent: "fun", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestRegistryWrite_ResolveKeyConfigCorruptSurfaces proves the shared writer
// surfaces a corrupt observations config while resolving the salted key.
func TestRegistryWrite_ResolveKeyConfigCorruptSurfaces(t *testing.T) {
	r, _, home := bootedMemoryRouter(t)
	require.NoError(t, writeFileHelper(home, "observations/config.json", "{bad"))
	_, err := r.WriteInjury(InjuryWriteRequest{Name: "left knee", Now: fixedNow()})
	require.Error(t, err)
}

// TestReflectWeek_WindowResolveFails proves an invalid window request surfaces
// before any bundle read.
func TestReflectWeek_WindowResolveFails(t *testing.T) {
	r, _, _ := reflectWeekRouter(t)
	_, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{
		Now:      fixedNow(),
		Provider: &provider.Fake{},
		Window:   ReflectWindowOptions{Mode: ReflectWindowDays, Days: 0},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve window")
}

// TestReflectWeek_PauseStateCorruptSurfaces proves the read-only weekly surface
// surfaces a corrupt pause-state file rather than proceeding.
func TestReflectWeek_PauseStateCorruptSurfaces(t *testing.T) {
	r, _, home := reflectWeekRouter(t)
	require.NoError(t, writeFileHelper(home, "proposal_pause.json", "{bad"))
	_, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: fixedNow(), Provider: &provider.Fake{}})
	require.Error(t, err)
}

// TestReflectWeek_DenylistReadFails proves the shape-denylist read failure fails
// the deep-dive closed rather than re-surfacing a dismissed shape.
func TestReflectWeek_DenylistReadFails(t *testing.T) {
	skipIfRoot(t)
	r, _, home := reflectWeekRouter(t)
	processedDir := filepath.Join(home, "processed")
	require.NoError(t, os.Chmod(processedDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(processedDir, 0o700) })
	_, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: fixedNow(), Provider: &provider.Fake{}})
	require.Error(t, err)
}

// TestCloseReflectWeek_CursorWriteFails proves an unwritable engine dir fails the
// close rather than silently dropping the reflected-through cursor.
func TestCloseReflectWeek_CursorWriteFails(t *testing.T) {
	skipIfRoot(t)
	r, _, home := reflectWeekRouter(t)
	engineDir := filepath.Join(home, "engine")
	require.NoError(t, os.Chmod(engineDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(engineDir, 0o700) })
	_, err := r.CloseReflectWeek(fixedNow())
	require.Error(t, err)
}
