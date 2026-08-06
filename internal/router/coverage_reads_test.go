package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/provider"
)

// scaffoldedEngine returns a booted router whose engine tree is materialized, so
// a test can corrupt or chmod a specific engine record file.
func scaffoldedEngine(t *testing.T) (*Router, string) {
	t.Helper()
	r, a, home := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	return r, home
}

// TestSelf_ReadSelfFactsCorruptSurfaces proves every self-fact verb (write and
// read) surfaces a corrupt self.json rather than folding over garbage.
func TestSelf_ReadSelfFactsCorruptSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"set", func(r *Router) error {
			_, err := r.SelfSet(SelfSetRequest{Key: "body.handedness", Value: "left", Now: fixedNow()})
			return err
		}},
		{"retire", func(r *Router) error {
			_, err := r.SelfRetire(SelfRetireRequest{Key: "body.handedness", Now: fixedNow()})
			return err
		}},
		{"move", func(r *Router) error {
			_, err := r.SelfMove(SelfMoveRequest{Key: "body.handedness", NewKey: "body.height", Now: fixedNow()})
			return err
		}},
		{"profile", func(r *Router) error {
			_, err := r.SelfProfile(SelfProfileRequest{})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, home := scaffoldedEngine(t)
			require.NoError(t, writeFileHelper(home, "engine/self.json", "{bad"))
			assert.Error(t, tc.call(r))
		})
	}
}

// TestProfileState_CorruptSurfaces proves the engine verbs that resolve the
// governing profile surface a corrupt profile.json rather than silently
// defaulting the clocks.
func TestProfileState_CorruptSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"closeout", func(r *Router) error {
			_, err := r.Closeout(CloseoutRequest{Now: atUTC(2026, 7, 5, 22, 0), Links: compactLinks(), Journal: "x"})
			return err
		}},
		{"backfill", func(r *Router) error {
			_, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 5, 9, 0), Links: compactLinks(), Journal: "x"})
			return err
		}},
		{"metrics", func(r *Router) error { _, err := r.Metrics(atUTC(2026, 7, 5, 9, 0)); return err }},
		{"storm-day", func(r *Router) error {
			_, err := r.StormCommand(StormRequest{Arg: "unwritten", DayArg: "@yesterday", Now: atUTC(2026, 7, 5, 9, 0)})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, home := scaffoldedEngine(t)
			require.NoError(t, writeFileHelper(home, "engine/profile.json", "{bad"))
			assert.Error(t, tc.call(r))
		})
	}
}

// TestReadEngineDaysCorruptSurfaces proves the practice-quality reads that fold
// every day record surface one corrupt day file rather than a partial fold.
func TestReadEngineDaysCorruptSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"metrics", func(r *Router) error { _, err := r.Metrics(atUTC(2026, 7, 5, 9, 0)); return err }},
		{"backfill", func(r *Router) error {
			_, err := r.Backfill(BackfillRequest{Now: atUTC(2026, 7, 5, 9, 0), Links: compactLinks(), Journal: "x"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, home := scaffoldedEngine(t)
			require.NoError(t, writeFileHelper(home, "engine/days/2026/07/day_2026_07_04.json", "{bad"))
			assert.Error(t, tc.call(r))
		})
	}
}

// TestStorm_ChainCorruptWithDayArg proves a backdated storm surfaces a corrupt
// chain.json when it resolves the strict-tier `--day`.
func TestStorm_ChainCorruptWithDayArg(t *testing.T) {
	r, home := scaffoldedEngine(t)
	require.NoError(t, writeFileHelper(home, "engine/chain.json", "{bad"))
	_, err := r.StormCommand(StormRequest{Arg: "unwritten", DayArg: "@yesterday", Now: atUTC(2026, 7, 5, 9, 0)})
	require.Error(t, err)
}

// TestStorm_LastEventUnparseableAtIsIgnored proves lastStormEventAt treats an
// unreadable last-event timestamp as "no last event" rather than stranding the
// command — a subsequent command still runs.
func TestStorm_LastEventUnparseableAtIsIgnored(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	require.NoError(t, a.ScaffoldEngine())
	require.NoError(t, a.AppendStormEvents(
		engine.StormEvent{At: "not-a-timestamp", Event: engine.StormDeclared, Label: "unwritten"},
	))
	// The out-of-order guard reads lastStormEventAt; an unparseable `at` makes it
	// report no last event, so the command proceeds (here: end with none standing).
	res, err := r.Storm("end", atUTC(2026, 7, 20, 8, 0))
	require.NoError(t, err)
	assert.True(t, res.Rejected)
}

// TestMetrics_AnchorsCorruptSurfaces proves a corrupt anchors.json fails the
// metrics rollup rather than silently dropping the days-since lines.
func TestMetrics_AnchorsCorruptSurfaces(t *testing.T) {
	r, home := scaffoldedEngine(t)
	require.NoError(t, writeFileHelper(home, "engine/anchors.json", "{bad"))
	_, err := r.Metrics(atUTC(2026, 7, 5, 9, 0))
	require.Error(t, err)
}

// TestProfile_StateCorruptSurfaces proves a `/profile` switch surfaces a corrupt
// profile.json after the name is validated against the chain.
func TestProfile_StateCorruptSurfaces(t *testing.T) {
	r, home := scaffoldedEngine(t)
	require.NoError(t, writeFileHelper(home, "engine/profile.json", "{bad"))
	_, err := r.Profile("nights", atUTC(2026, 7, 5, 9, 0))
	require.Error(t, err)
}

// TestMode_ReadEngineDayCorruptSurfaces proves a same-day `/mode` re-declaration
// surfaces a corrupt day record rather than overwriting it.
func TestMode_ReadEngineDayCorruptSurfaces(t *testing.T) {
	r, a, home := newBootedRouter(t)
	_, err := r.Mode(engine.ModeYellow, atUTC(2026, 7, 5, 14, 2))
	require.NoError(t, err)
	require.NoError(t, writeFileHelper(home, "engine/days/2026/07/day_2026_07_05.json", "{bad"))
	_, err = r.Mode(engine.ModeYellow, atUTC(2026, 7, 5, 14, 2))
	require.Error(t, err)
	_ = a
}

// TestPerson_ReadFailuresSurface walks each store read behind /person and proves
// a failure at any of them fails the join closed rather than rendering a partial
// record.
func TestPerson_ReadFailuresSurface(t *testing.T) {
	skipIfRoot(t)
	at := personSeedTime()

	t.Run("listPeopleKeys", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedPerson(t, a, "M.", "raw_2026_05_05_19_42", at)
		peopleDir := filepath.Join(home, "people")
		require.NoError(t, os.Chmod(peopleDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(peopleDir, 0o700) })
		_, err := r.Person(PersonRequest{Name: "M."})
		require.Error(t, err)
	})

	t.Run("readPerson", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		key := seedPerson(t, a, "M.", "raw_2026_05_05_19_42", at)
		require.NoError(t, os.WriteFile(filepath.Join(home, "people", key+".json"), []byte("{bad"), 0o600))
		_, err := r.Person(PersonRequest{Name: "M."})
		require.Error(t, err)
	})

	t.Run("offLimitsCorrupt", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedPerson(t, a, "M.", "raw_2026_05_05_19_42", at)
		require.NoError(t, writeFileHelper(home, "off_limits.json", "{bad"))
		_, err := r.Person(PersonRequest{Name: "M."})
		require.Error(t, err)
	})

	t.Run("insightsListFails", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedPerson(t, a, "M.", "raw_2026_05_05_19_42", at)
		insightsDir := filepath.Join(home, "insights")
		require.NoError(t, os.Chmod(insightsDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(insightsDir, 0o700) })
		_, err := r.Person(PersonRequest{Name: "M."})
		require.Error(t, err)
	})

	t.Run("insightReadFails", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedPerson(t, a, "M.", "raw_2026_05_05_19_42", at)
		id := seedInsight(t, a, at, "an insight to make unreadable")
		insightPath := filepath.Join(home, "insights", id+".md")
		require.NoError(t, os.Chmod(insightPath, 0o000))
		t.Cleanup(func() { _ = os.Chmod(insightPath, 0o600) })
		_, err := r.Person(PersonRequest{Name: "M."})
		require.Error(t, err)
	})

	t.Run("processedListFails", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedPerson(t, a, "M.", "raw_2026_05_05_19_42", at)
		seedArtifactMentioning(t, a, "raw_2026_05_05_19_42", "M.", "", at)
		processedDir := filepath.Join(home, "processed")
		require.NoError(t, os.Chmod(processedDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(processedDir, 0o700) })
		_, err := r.Person(PersonRequest{Name: "M."})
		require.Error(t, err)
	})

	t.Run("processedReadCorrupt", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedPerson(t, a, "M.", "raw_2026_05_05_19_42", at)
		seedArtifactMentioning(t, a, "raw_2026_05_05_19_42", "M.", "", at)
		require.NoError(t, os.WriteFile(filepath.Join(home, "processed", "raw_2026_05_05_19_42.json"), []byte("{bad"), 0o600))
		_, err := r.Person(PersonRequest{Name: "M."})
		require.Error(t, err)
	})
}

// TestAsk_SliceReadFailuresSurface proves each grounding-slice read fails /ask
// closed rather than answering over a partial slice.
func TestAsk_SliceReadFailuresSurface(t *testing.T) {
	t.Run("insights", func(t *testing.T) {
		skipIfRoot(t)
		r, _, home := newBootedRouter(t)
		insightsDir := filepath.Join(home, "insights")
		require.NoError(t, os.Chmod(insightsDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(insightsDir, 0o700) })
		_, err := r.Ask(context.Background(), AskRequest{Question: "why?", Provider: &provider.Fake{}})
		require.Error(t, err)
	})

	t.Run("reflections", func(t *testing.T) {
		skipIfRoot(t)
		r, _, home := newBootedRouter(t)
		reflectionsDir := filepath.Join(home, "reflections")
		require.NoError(t, os.Chmod(reflectionsDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(reflectionsDir, 0o700) })
		_, err := r.Ask(context.Background(), AskRequest{Question: "why?", Provider: &provider.Fake{}})
		require.Error(t, err)
	})

	t.Run("selfFacts", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		require.NoError(t, a.ScaffoldEngine())
		require.NoError(t, writeFileHelper(home, "engine/self.json", "{bad"))
		_, err := r.Ask(context.Background(), AskRequest{Question: "why?", Provider: &provider.Fake{}})
		require.Error(t, err)
	})
}

// TestExcavate_RegistryReadFailuresSurface proves the excavation bundle fails
// closed on a registry read error rather than selecting over a partial store.
func TestExcavate_RegistryReadFailuresSurface(t *testing.T) {
	skipIfRoot(t)
	for _, dir := range []string{"injuries", "eras"} {
		t.Run(dir, func(t *testing.T) {
			r, _, home := bootedMemoryRouter(t)
			target := filepath.Join(home, "registries", dir)
			require.NoError(t, os.Chmod(target, 0o000))
			t.Cleanup(func() { _ = os.Chmod(target, 0o700) })
			_, err := r.Excavate(fixedNow())
			require.Error(t, err)
		})
	}
}

// TestCuriosity_ReadFailuresSurface proves the deterministic curiosity picker
// surfaces a location read failure and an unreadable ask-state file rather than
// choosing over an unknown store.
func TestCuriosity_ReadFailuresSurface(t *testing.T) {
	skipIfRoot(t)

	t.Run("locationRead", func(t *testing.T) {
		r, _, home := bootedMemoryRouter(t)
		res, err := r.Capture(CaptureRequest{Tokens: []string{"pain", "6", "knee"}, Now: fixedNow()})
		require.NoError(t, err)
		require.False(t, res.Rejected)
		obsFile := filepath.Join(home, "observations", "2026", "07", "obs_2026_07_05.jsonl")
		require.NoError(t, os.Chmod(obsFile, 0o000))
		t.Cleanup(func() { _ = os.Chmod(obsFile, 0o600) })
		_, err = r.Curiosity(fixedNow(), false)
		require.Error(t, err)
	})

	t.Run("stateRead", func(t *testing.T) {
		r, _, home := bootedMemoryRouter(t)
		_, err := r.Curiosity(fixedNow(), false) // first run writes the ephemeral state file
		require.NoError(t, err)
		statePath := filepath.Join(home, "projections", ".curiosity.json")
		require.NoError(t, os.Chmod(statePath, 0o000))
		t.Cleanup(func() { _ = os.Chmod(statePath, 0o600) })
		_, err = r.Curiosity(fixedNow(), false)
		require.Error(t, err)
	})
}

// TestStats_ObservationsConfigCorruptSurfaces proves the volume rollup surfaces a
// corrupt observations config rather than counting over an unknown kind set.
func TestStats_ObservationsConfigCorruptSurfaces(t *testing.T) {
	r, _, home := bootedMemoryRouter(t)
	require.NoError(t, writeFileHelper(home, "observations/config.json", "{bad"))
	_, err := r.Stats(StatsOptions{}, fixedNow())
	require.Error(t, err)
}

// TestObservationsConfigCorruptSurfaces proves the capture verbs that read the
// observations config before deciding a kind is enabled surface a corrupt config.
func TestObservationsConfigCorruptSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(r *Router) error
	}{
		{"writeMemory", func(r *Router) error {
			_, err := r.WriteMemory(MemoryWriteRequest{Text: "x", Now: fixedNow()})
			return err
		}},
		{"workoutLog", func(r *Router) error {
			_, err := r.WorkoutLog(WorkoutLogRequest{Now: fixedNow()})
			return err
		}},
		{"workoutLogFromText", func(r *Router) error {
			_, err := r.WorkoutLogFromText(context.Background(), WorkoutLogTextRequest{Text: "did pull", Now: fixedNow()},
				&provider.Fake{Script: []provider.Exchange{{Content: "{}"}}})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, home := bootedMemoryRouter(t)
			require.NoError(t, writeFileHelper(home, "observations/config.json", "{bad"))
			assert.Error(t, tc.call(r))
		})
	}
}

// TestDayView_BadDateSurfaces proves an unparseable date argument surfaces the
// storage read error rather than rendering an empty day for a bogus date.
func TestDayView_BadDateSurfaces(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	_, err := r.DayView("not-a-date", fixedNow())
	require.Error(t, err)
}

// TestWeekBundle_MetricsReadFailureSurfaces proves the week bundle surfaces a
// corrupt day record through its Metrics projection rather than reporting hollow
// numbers.
func TestWeekBundle_MetricsReadFailureSurfaces(t *testing.T) {
	r, _, home := reflectWeekRouter(t)
	require.NoError(t, writeFileHelper(home, "engine/days/2026/07/day_2026_07_04.json", "{bad"))
	_, err := r.ReflectWeek(context.Background(), ReflectWeekRequest{Now: atUTC(2026, 7, 5, 9, 0), Provider: &provider.Fake{}})
	require.Error(t, err)
}

// weekApplyReq builds a minimal accepted weekly-apply request.
func weekApplyReq() ApplyWeekProposalRequest {
	return applyReq(cleanCandidate, "prep-as-safety", "",
		ProposalResponse{Kind: RespAccepted, Text: "Yes."}, RuleResponse{}, &provider.Fake{})
}

// TestWeekApply_ProcessedReadFailuresSurface proves the weekly apply path fails
// closed when it cannot resolve the anchoring processed artifact, and when the
// pause state is unreadable.
func TestWeekApply_ProcessedReadFailuresSurface(t *testing.T) {
	at := fixedNow()
	t.Run("listProcessedFails", func(t *testing.T) {
		skipIfRoot(t)
		r, _, home := newBootedRouter(t)
		processedDir := filepath.Join(home, "processed")
		require.NoError(t, os.Chmod(processedDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(processedDir, 0o700) })
		_, err := r.ApplyWeekProposal(context.Background(), weekApplyReq())
		require.Error(t, err)
	})

	t.Run("readProcessedCorrupt", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedProc(t, a, curr, at, nil, "prep")
		require.NoError(t, os.WriteFile(filepath.Join(home, "processed", curr+".json"), []byte("{bad"), 0o600))
		_, err := r.ApplyWeekProposal(context.Background(), weekApplyReq())
		require.Error(t, err)
	})

	t.Run("pauseStateCorrupt", func(t *testing.T) {
		r, a, home := newBootedRouter(t)
		seedProc(t, a, curr, at, nil, "prep")
		require.NoError(t, writeFileHelper(home, "proposal_pause.json", "{bad"))
		_, err := r.ApplyWeekProposal(context.Background(), weekApplyReq())
		require.Error(t, err)
	})
}

// TestReflectionEngaged_Cases covers the pure engagement switch's non-answering
// arms (unanswered and any unknown kind advance no cursor).
func TestReflectionEngaged_Cases(t *testing.T) {
	assert.True(t, reflectionEngaged(RespAccepted))
	assert.False(t, reflectionEngaged(RespUnanswered))
	assert.False(t, reflectionEngaged(ResponseKind("bogus")))
}

// TestReflect_EmptyStoreZeroNow covers the zero-Now default on the empty-store
// short-circuit: the wall clock fills in but the result is still the fixed
// nothing-validated copy, so the assertion stays deterministic.
func TestReflect_EmptyStoreZeroNow(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	res, err := r.Reflect(context.Background(), ReflectRequest{})
	require.NoError(t, err)
	assert.Equal(t, reflectNothingValidated, res.Message)
}

// TestReflect_ListInsightsFailsSurfaces proves /reflect surfaces an unreadable
// insights tree rather than treating it as nothing-validated.
func TestReflect_ListInsightsFailsSurfaces(t *testing.T) {
	skipIfRoot(t)
	r, _, home := newBootedRouter(t)
	insightsDir := filepath.Join(home, "insights")
	require.NoError(t, os.Chmod(insightsDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(insightsDir, 0o700) })
	_, err := r.Reflect(context.Background(), ReflectRequest{Now: fixedNow(), Responder: &scriptRecall{}})
	require.Error(t, err)
}
