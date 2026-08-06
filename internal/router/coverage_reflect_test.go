package router

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// errBoom is a fixed responder failure the reflect error-path tests inject.
var errBoom = errors.New("boom")

// inWindow / outOfWindow anchor a seeded insight inside or outside the seven-day
// recall window relative to reflectWeekNow().
func inWindow() time.Time    { return reflectWeekNow().Add(-1 * 24 * time.Hour) }
func outOfWindow() time.Time { return reflectWeekNow().Add(-20 * 24 * time.Hour) }

// insightMDPath is the on-disk path of a seeded insight record.
func insightMDPath(home, id string) string { return filepath.Join(home, "insights", id+".md") }

// TestReflectWeek_WindowReadErrorSurfaces proves the weekly pass surfaces a
// corrupt insight the window read cannot fold (reflect.go:144).
func TestReflectWeek_WindowReadErrorSurfaces(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, inWindow(), "an insight to corrupt")
	require.NoError(t, os.WriteFile(insightMDPath(home, id), []byte("{bad"), 0o600))

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: reflectWeekNow(), Provider: &provider.Fake{}, Responder: &scriptRecall{},
	})
	require.Error(t, err)
}

// TestReflectWeek_WriteRecordFails proves the weekly pass surfaces a reflection
// record write failure (reflect.go:162, 335).
func TestReflectWeek_WriteRecordFails(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, inWindow(), "I go quiet in groups.")
	reflectionsDir := filepath.Join(home, "reflections")
	require.NoError(t, os.Chmod(reflectionsDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(reflectionsDir, 0o700) })

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now:       reflectWeekNow(),
		Provider:  &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}},
		Responder: &scriptRecall{def: RecallResponse{}}, // unanswered: no insight write
	})
	require.Error(t, err)
}

// TestApplyRecall_UpdateStatusWriteFails proves a status transition that cannot be
// persisted surfaces the failure (reflect.go:267).
func TestApplyRecall_UpdateStatusWriteFails(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, inWindow(), "I go quiet in groups.")
	require.NoError(t, os.Chmod(insightMDPath(home, id), 0o400)) // readable, write fails
	t.Cleanup(func() { _ = os.Chmod(insightMDPath(home, id), 0o600) })

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now:       reflectWeekNow(),
		Provider:  &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}},
		Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.Error(t, err)
}

// TestApplyRecall_UpdateRuleWriteFails proves a rule transition that cannot be
// persisted surfaces the failure (reflect.go:274).
func TestApplyRecall_UpdateRuleWriteFails(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	id := seedRuledInsight(t, a, inWindow(), "I over-prepare before hard talks.", "pause before I answer")
	require.NoError(t, os.Chmod(insightMDPath(home, id), 0o400))
	t.Cleanup(func() { _ = os.Chmod(insightMDPath(home, id), 0o600) })

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now:      reflectWeekNow(),
		Provider: &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}},
		// Only the rule answers, so the status update is skipped and the rule update
		// is the one that fails.
		Responder: &scriptRecall{def: RecallResponse{Rule: storage.RuleKept}},
	})
	require.Error(t, err)
}

// TestReflectEmptyWeek_HasUnreflectedListFails proves the empty-week fallback
// surfaces an unreadable processed tree while deciding the pointer line
// (reflect.go:182, 459).
func TestReflectEmptyWeek_HasUnreflectedListFails(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	seedInsight(t, a, outOfWindow(), "an older insight") // empty seven-day window
	processedDir := filepath.Join(home, "processed")
	require.NoError(t, os.Chmod(processedDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(processedDir, 0o700) })

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: reflectWeekNow(), Provider: &provider.Fake{}, Responder: &scriptRecall{},
	})
	require.Error(t, err)
}

// TestReflectEmptyWeek_HasUnreflectedLatestReflectionFails proves the fallback
// surfaces an unreadable reflections tree (reflect.go:466).
func TestReflectEmptyWeek_HasUnreflectedLatestReflectionFails(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	seedInsight(t, a, outOfWindow(), "an older insight")
	seedProc(t, a, "raw_2026_05_01_10_00", outOfWindow(), nil, "theme") // processed non-empty
	reflectionsDir := filepath.Join(home, "reflections")
	require.NoError(t, os.Chmod(reflectionsDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(reflectionsDir, 0o700) })

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: reflectWeekNow(), Provider: &provider.Fake{}, Responder: &scriptRecall{},
	})
	require.Error(t, err)
}

// TestReflectEmptyWeek_HasUnreflectedReadProcessedFails proves the fallback
// surfaces a corrupt processed artifact once a prior reflection exists so the
// per-artifact read loop runs (reflect.go:474).
func TestReflectEmptyWeek_HasUnreflectedReadProcessedFails(t *testing.T) {
	r, a, home := newBootedRouter(t)
	seedInsight(t, a, outOfWindow(), "an older insight")
	seedProc(t, a, "raw_2026_05_01_10_00", outOfWindow(), nil, "theme")
	// A prior reflection makes LatestReflectionCreatedAt return a timestamp, so
	// hasUnreflectedEntries walks the processed artifacts and hits the corrupt one.
	_, err := a.WriteReflection(storage.Reflection{
		ID:           "2026-W10",
		ISOWeek:      "2026-W10",
		WindowStart:  outOfWindow(),
		WindowEnd:    outOfWindow(),
		CreatedAt:    outOfWindow(),
		AgentVersion: "reflection-2026.05.0",
		Summary:      "seeded",
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "processed", "raw_2026_05_01_10_00.json"), []byte("{bad"), 0o600))

	_, err = r.Reflect(context.Background(), ReflectRequest{
		Now: reflectWeekNow(), Provider: &provider.Fake{}, Responder: &scriptRecall{},
	})
	require.Error(t, err)
}

// TestReflectEmptyWeek_ApplyRecallResponderError proves the fallback surfaces a
// responder failure over the two most-recent insights (reflect.go:198).
func TestReflectEmptyWeek_ApplyRecallResponderError(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedInsight(t, a, outOfWindow(), "an older insight")

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: reflectWeekNow(), Provider: &provider.Fake{},
		Responder: &scriptRecall{err: errBoom},
	})
	require.Error(t, err)
}

// TestReflectEmptyWeek_WriteRecordFails proves the fallback surfaces a reflection
// record write failure (reflect.go:202).
func TestReflectEmptyWeek_WriteRecordFails(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	seedInsight(t, a, outOfWindow(), "an older insight")
	reflectionsDir := filepath.Join(home, "reflections")
	require.NoError(t, os.Chmod(reflectionsDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(reflectionsDir, 0o700) })

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Now: reflectWeekNow(), Provider: &provider.Fake{}, Responder: &scriptRecall{def: RecallResponse{}},
	})
	require.Error(t, err)
}

// TestReflectGate_WindowReadErrorSurfaces proves the gate pass surfaces a corrupt
// insight the window read cannot fold (reflect.go:214).
func TestReflectGate_WindowReadErrorSurfaces(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, inWindow(), "an insight to corrupt")
	require.NoError(t, os.WriteFile(insightMDPath(home, id), []byte("{bad"), 0o600))

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: reflectWeekNow(), Provider: &provider.Fake{}, Responder: &scriptRecall{},
	})
	require.Error(t, err)
}

// TestReflectGate_ApplyRecallResponderError proves the gate pass surfaces a
// responder failure (reflect.go:228).
func TestReflectGate_ApplyRecallResponderError(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	id := seedInsight(t, a, inWindow(), "I go quiet in groups.")

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: reflectWeekNow(),
		Provider:  &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}},
		Responder: &scriptRecall{err: errBoom},
	})
	require.Error(t, err)
}

// TestReflectGate_GatePanelDominanceListFails proves the gate panel surfaces an
// unreadable processed tree while computing the dominance line
// (reflect.go:232, 362, 394).
func TestReflectGate_GatePanelDominanceListFails(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, inWindow(), "I go quiet in groups.")
	processedDir := filepath.Join(home, "processed")
	require.NoError(t, os.Chmod(processedDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(processedDir, 0o700) })

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: reflectWeekNow(),
		Provider:  &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}},
		Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.Error(t, err)
}

// TestReflectGate_WriteRecordFails proves the gate pass surfaces a reflection
// record write failure (reflect.go:236).
func TestReflectGate_WriteRecordFails(t *testing.T) {
	skipIfRoot(t)
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, inWindow(), "I go quiet in groups.")
	reflectionsDir := filepath.Join(home, "reflections")
	require.NoError(t, os.Chmod(reflectionsDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(reflectionsDir, 0o700) })

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: reflectWeekNow(),
		Provider:  &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}},
		Responder: &scriptRecall{def: RecallResponse{}}, // unanswered: reach the record write
	})
	require.Error(t, err)
}

// TestReflectGate_DominanceOffLimitsReadFails proves the dominance line surfaces a
// corrupt off-limits registry (reflect.go:402).
func TestReflectGate_DominanceOffLimitsReadFails(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, inWindow(), "I go quiet in groups.")
	seedProc(t, a, "raw_2026_05_01_10_00", inWindow(), nil, "theme") // processed non-empty
	require.NoError(t, writeFileHelper(home, "off_limits.json", "{bad"))

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: reflectWeekNow(),
		Provider:  &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}},
		Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.Error(t, err)
}

// TestReflectGate_DominanceReadProcessedFails proves the dominance line surfaces a
// corrupt processed artifact in its per-entry read (reflect.go:411).
func TestReflectGate_DominanceReadProcessedFails(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id := seedInsight(t, a, inWindow(), "I go quiet in groups.")
	seedProc(t, a, "raw_2026_05_01_10_00", inWindow(), nil, "theme")
	require.NoError(t, os.WriteFile(filepath.Join(home, "processed", "raw_2026_05_01_10_00.json"), []byte("{bad"), 0o600))

	_, err := r.Reflect(context.Background(), ReflectRequest{
		Scope: ReflectGate, Now: reflectWeekNow(),
		Provider:  &provider.Fake{Script: []provider.Exchange{recallReplyFor(id)}},
		Responder: &scriptRecall{def: RecallResponse{Status: storage.RecallConfirmed}},
	})
	require.Error(t, err)
}
