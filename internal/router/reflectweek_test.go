package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/frameworks"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// reflectWeekRouter builds a booted router over a fresh temp Ledger (base +
// observations + engine trees scaffolded) and returns it with the adapter and
// the home path, so the read-only assertion can walk the whole tree.
func reflectWeekRouter(t *testing.T) (*Router, *storage.Adapter, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".lucid")
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.ScaffoldObservations())
	require.NoError(t, a.ScaffoldEngine())
	r := New(a)
	_, err = r.Boot()
	require.NoError(t, err)
	return r, a, home
}

// countTreeFiles counts every non-directory file under root — the read-only
// probe: the count must not change across a ReflectWeek run.
func countTreeFiles(t *testing.T, root string) int {
	t.Helper()
	var n int
	require.NoError(t, filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	}))
	return n
}

// deepWeekReply builds a full, valid deep-dive completion. A non-empty
// candidateText adds a candidate block citing citeID with shape_tag; the text is
// caller-chosen so a test can drive the clean / diagnostic / overclaim gates.
func deepWeekReply(candidateText, shapeTag, citeID string) provider.Exchange {
	body := `{"summary":"A steadier week overall.",` +
		`"wins":["logged entries"],"misses":["one skipped closeout"],` +
		`"body_pain":["a pain note mid-week"],"habit_change":["earlier evenings"],` +
		`"next_week":["one small experiment"]`
	if candidateText != "" {
		body += `,"candidate":{"proposal_text":"` + candidateText + `",` +
			`"shape_tag":"` + shapeTag + `","supporting_entry_ids":["` + citeID + `"]}`
	}
	body += "}"
	return provider.Exchange{Content: body}
}

// seedWeek lays down a realistic week: raws (giving citable ids), a body signal,
// and an in-window accepted insight. It returns the first raw id for citations.
func seedWeek(t *testing.T, a *storage.Adapter) string {
	t.Helper()
	seedRaws(t, a, time.Date(2026, 7, 1, 9, 0, 0, 0, edt), 2)
	seedObs(t, a, observations.KindPain, "2026-07-02", "2026-07-02T09:00:00-04:00")
	seedAcceptedInsight(t, a, time.Date(2026, 7, 1, 12, 0, 0, 0, edt), "I go quiet in groups.")
	return "raw_2026_07_01_09_00"
}

// TestReflectWeek_RichSurfacesSectionsAndPatternReadOnly proves a full week
// yields the ISO-week label, every narrative section, and a Safety-cleared
// candidate — and writes nothing (AC-2, AC-3, AC-7).
func TestReflectWeek_RichSurfacesSectionsAndPatternReadOnly(t *testing.T) {
	r, a, home := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	// Warm the sanctioned projections so the engine-status cache the read path
	// materializes on first call is already present; the file count then isolates
	// exactly what ReflectWeek writes (nothing).
	_, err := r.BuildWeekBundle(weekOf())
	require.NoError(t, err)

	before := countTreeFiles(t, home)
	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.Equal(t, "2026-W27", res.ISOWeek)
	assert.NotEmpty(t, res.Summary)
	assert.NotEmpty(t, res.Wins)
	assert.NotEmpty(t, res.Misses)
	assert.NotEmpty(t, res.BodyPain)
	assert.NotEmpty(t, res.HabitChange)
	assert.NotEmpty(t, res.NextWeek)
	require.NotNil(t, res.Pattern, "a Safety-cleared candidate is surfaced")
	assert.Equal(t, "prep-as-safety", res.Pattern.ShapeTag)
	assert.Equal(t, []string{citeID}, res.Pattern.SupportingEntryIDs)

	assert.Equal(t, before, countTreeFiles(t, home), "reflect week writes no new files (read-only)")
}

// TestReflectWeek_EmptyStoreNoModelCall proves an empty store yields an
// empty-but-valid result with no model call and no pattern (AC-11).
func TestReflectWeek_EmptyStoreNoModelCall(t *testing.T) {
	r, _, home := reflectWeekRouter(t)

	// Warm the projection cache first (see the rich case), then isolate ReflectWeek.
	_, err := r.BuildWeekBundle(weekOf())
	require.NoError(t, err)

	before := countTreeFiles(t, home)
	fake := &provider.Fake{}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.Equal(t, 0, fake.Calls(), "no model call over an empty store")
	assert.Equal(t, "2026-W27", res.ISOWeek)
	assert.Empty(t, res.Summary)
	assert.Nil(t, res.Pattern)
	assert.Equal(t, before, countTreeFiles(t, home), "empty reflect week writes nothing")
}

// TestReflectWeek_SafetyBlocksDiagnosticCandidate proves a diagnostic candidate
// is blocked by Safety and never surfaced, while the narrative survives (AC-7).
func TestReflectWeek_SafetyBlocksDiagnosticCandidate(t *testing.T) {
	r, a, _ := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("You have an avoidant attachment style.", "avoidant-label", citeID),
	}}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.NotEmpty(t, res.Summary, "the narrative survives a blocked candidate")
	assert.Nil(t, res.Pattern, "a diagnostic candidate is blocked, never surfaced")
}

// TestReflectWeek_PauseSuppressesCandidate proves an active proposal pause
// suppresses the candidate while the narrative still surfaces, and the pause
// state is read without being mutated (still read-only).
func TestReflectWeek_PauseSuppressesCandidate(t *testing.T) {
	r, a, _ := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	until := weekOf().Add(48 * time.Hour)
	require.NoError(t, a.WriteProposalPauseState(storage.ProposalPauseState{ConsecutiveUnanswered: 3, PausedUntil: &until}))

	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake})
	require.NoError(t, err)

	assert.NotEmpty(t, res.Summary, "the narrative surfaces even while paused")
	assert.Nil(t, res.Pattern, "an active pause suppresses the candidate")

	// The pause was read, not cleared: it still stands unchanged.
	st, err := a.ReadProposalPauseState()
	require.NoError(t, err)
	require.NotNil(t, st.PausedUntil)
	assert.True(t, st.PausedUntil.Equal(until), "the pause is read-only, never rewritten")
}

// TestReflectWeek_AppliedLensLabel proves a consented lens frames the run and
// its "<id> v<version>" label reaches the result (AC-6 label path).
func TestReflectWeek_AppliedLensLabel(t *testing.T) {
	r, a, _ := reflectWeekRouter(t)
	citeID := seedWeek(t, a)

	lens := frameworks.Lens{ID: "stoicism", Version: 1, Name: "Stoicism"}
	fake := &provider.Fake{Script: []provider.Exchange{
		deepWeekReply("One possible pattern: preparation as a way to feel safe.", "prep-as-safety", citeID),
	}}

	res, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: weekOf(), Provider: fake, ActiveLens: &lens})
	require.NoError(t, err)

	assert.Equal(t, "stoicism v1", res.AppliedLens)
}
