package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// noPeopleExtraction is a well-formed extraction reply mentioning nobody, so a
// pass exercises the processed write without also exercising the People
// routine.
const noPeopleExtraction = `{"emotions":[{"name":"calm","rationale":"'quiet'"}],"themes":[],"people":[],"notes":null}`

// passDay returns an instant on the fixed pass day at the given hour/minute, so
// seeded entries land on distinct minute-precision ids within one window.
func passDay(hour, minute int) time.Time {
	return time.Date(2026, time.July, 5, hour, minute, 0, 0, time.UTC)
}

// writeRawCmd seeds a raw entry captured by an arbitrary verb, which the shared
// writeRawAt helper cannot do (it always writes a /log). The pass must reach
// every raw entry whatever captured it, so the tests need the other verbs too.
func writeRawCmd(t *testing.T, a *storage.Adapter, verb, body string, at time.Time) string {
	t.Helper()
	res, err := a.WriteRaw(storage.RawEntry{
		RecordedAt:          at,
		OccurredAt:          at,
		OccurredAtPrecision: storage.PrecisionExact,
		Source:              "cli",
		Command:             verb,
		Body:                body,
	})
	require.NoError(t, err)
	return res.RawID
}

// structureOne runs a single-entry pass with one scripted extraction, so a test
// that only needs an entry already structured can say so in one line.
func structureOne(t *testing.T, r *Router, id string) {
	t.Helper()
	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}
	res, err := r.StructurePass(context.Background(), StructurePassRequest{RawID: id, Now: fixedNow(), Provider: p})
	require.NoError(t, err)
	require.Equal(t, 1, res.Wrote)
}

// rangedReq wires a ranged pass over the whole fixed pass day.
func rangedReq(p provider.Provider) StructurePassRequest {
	return StructurePassRequest{
		Since:    passDay(0, 0),
		Until:    passDay(23, 59),
		Now:      fixedNow(),
		Provider: p,
	}
}

// outcomes projects a result's entries to an id -> outcome map for assertions
// that do not care about order.
func outcomes(res StructurePassResult) map[string]string {
	out := make(map[string]string, len(res.Entries))
	for _, e := range res.Entries {
		out[e.RawID] = e.Outcome
	}
	return out
}

// TestStructurePass_SingleEntryUnprocessed is the base case: an entry this run
// did not capture is structured on demand, writing its artifact.
func TestStructurePass_SingleEntryUnprocessed(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "Dinner with M. I pushed back then dropped it.", passDay(9, 0))
	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}

	res, err := r.StructurePass(context.Background(), StructurePassRequest{RawID: id, Now: fixedNow(), Provider: p})
	require.NoError(t, err)

	assert.False(t, res.Ranged)
	assert.Empty(t, res.Since)
	assert.Empty(t, res.Until)
	assert.Equal(t, 1, res.Attempted)
	assert.Equal(t, 1, res.Wrote)
	assert.Equal(t, 0, res.Skipped)
	assert.Equal(t, 0, res.Degraded)
	assert.Equal(t, 0, res.Failed)
	require.Len(t, res.Entries, 1)
	assert.Equal(t, StructurePassEntry{RawID: id, Outcome: StructureOutcomeWrote}, res.Entries[0])
	assert.Equal(t, 1, p.Calls())

	m := readProcessedMap(t, home, id)
	assert.Equal(t, id, m["entry_id"])
}

// TestStructurePass_SkipsAlreadyProcessed is the default that makes an
// interrupted window run resumable: an entry with an artifact costs no model
// call at all.
func TestStructurePass_SkipsAlreadyProcessed(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "Dinner with M.", passDay(9, 0))
	structureOne(t, r, id)

	p := &provider.Fake{} // no scripted calls — none expected
	res, err := r.StructurePass(context.Background(), StructurePassRequest{RawID: id, Now: fixedNow(), Provider: p})
	require.NoError(t, err)

	assert.Equal(t, 0, res.Attempted)
	assert.Equal(t, 0, res.Wrote)
	assert.Equal(t, 1, res.Skipped)
	require.Len(t, res.Entries, 1)
	assert.Equal(t, StructureOutcomeSkipped, res.Entries[0].Outcome)
	assert.Equal(t, 0, p.Calls(), "a skip must never reach the model")
}

// TestStructurePass_ForceRestructures proves the override: --force re-runs an
// entry that already has an artifact, overwriting it.
func TestStructurePass_ForceRestructures(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, rawPath := writeRawAt(t, a, home, "Dinner with M.", passDay(9, 0))
	structureOne(t, r, id)
	first := readProcessedMap(t, home, id)
	rawBefore, err := os.ReadFile(rawPath)
	require.NoError(t, err)

	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}
	res, err := r.StructurePass(context.Background(), StructurePassRequest{
		RawID: id, Force: true, Now: fixedNow().Add(2 * time.Hour), Provider: p,
	})
	require.NoError(t, err)

	assert.Equal(t, 1, res.Attempted)
	assert.Equal(t, 1, res.Wrote)
	assert.Equal(t, 0, res.Skipped)
	assert.Equal(t, StructureOutcomeWrote, res.Entries[0].Outcome)
	assert.Equal(t, 1, p.Calls())

	// The artifact was rewritten (produced_at moved) and the raw entry was not.
	second := readProcessedMap(t, home, id)
	assert.NotEqual(t, first["produced_at"], second["produced_at"])
	rawAfter, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	assert.Equal(t, rawBefore, rawAfter, "the pass must never modify a raw entry")
}

// TestStructurePass_RangedMixed covers the window: entries outside the bounds
// are never touched, an entry already structured is skipped, and the rest are
// attempted — with the counts reporting exactly that.
func TestStructurePass_RangedMixed(t *testing.T) {
	r, a, home := newBootedRouter(t)
	before, _ := writeRawAt(t, a, home, "The day before.", passDay(9, 0).AddDate(0, 0, -1))
	done, _ := writeRawAt(t, a, home, "Already distilled.", passDay(8, 0))
	first, _ := writeRawAt(t, a, home, "Dinner with M.", passDay(9, 0))
	second, _ := writeRawAt(t, a, home, "A quiet, personless evening.", passDay(21, 0))
	after, _ := writeRawAt(t, a, home, "The day after.", passDay(9, 0).AddDate(0, 0, 1))
	structureOne(t, r, done)

	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction), extractEx(noPeopleExtraction)}}
	res, err := r.StructurePass(context.Background(), rangedReq(p))
	require.NoError(t, err)

	assert.True(t, res.Ranged)
	assert.Equal(t, "2026-07-05", res.Since)
	assert.Equal(t, "2026-07-05", res.Until)
	assert.Equal(t, 2, res.Attempted)
	assert.Equal(t, 2, res.Wrote)
	assert.Equal(t, 1, res.Skipped)
	assert.Equal(t, 0, res.Degraded)
	assert.Equal(t, 0, res.Failed)
	assert.Equal(t, 2, p.Calls())

	got := outcomes(res)
	assert.Equal(t, map[string]string{
		done:   StructureOutcomeSkipped,
		first:  StructureOutcomeWrote,
		second: StructureOutcomeWrote,
	}, got, "only the in-window entries are reported")
	assert.NotContains(t, got, before)
	assert.NotContains(t, got, after)
}

// TestStructurePass_NoVerbFiltering is the agent-contracts.md §"How contracts
// compose" rule: Structuring runs over every raw entry in the window, /log and
// /closeout journal lines included. Which captures are worth distilling is a
// reader's judgment, not the binary's.
func TestStructurePass_NoVerbFiltering(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	logged := writeRawCmd(t, a, "/log", "A logged line.", passDay(9, 0))
	checkin := writeRawCmd(t, a, "/checkin", "A check-in capture.", passDay(10, 0))
	closeout := writeRawCmd(t, a, "/closeout", "A closeout journal line.", passDay(21, 0))

	p := &provider.Fake{Script: []provider.Exchange{
		extractEx(noPeopleExtraction), extractEx(noPeopleExtraction), extractEx(noPeopleExtraction),
	}}
	res, err := r.StructurePass(context.Background(), rangedReq(p))
	require.NoError(t, err)

	assert.Equal(t, 3, res.Attempted)
	assert.Equal(t, 3, res.Wrote)
	assert.Equal(t, map[string]string{
		logged:   StructureOutcomeWrote,
		checkin:  StructureOutcomeWrote,
		closeout: StructureOutcomeWrote,
	}, outcomes(res))
}

// TestStructurePass_AgentDegradeStillWrites is the ordinary degrade shape: the
// extraction fails and its stricter retry fails too, so the pass degrades and
// still writes an empty artifact. The entry counts in written AND degraded,
// while its label reports the more informative of the two.
func TestStructurePass_AgentDegradeStillWrites(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "some real content", passDay(9, 0))
	p := &provider.Fake{Script: []provider.Exchange{extractEx("garbage"), extractEx("still garbage")}}

	res, err := r.StructurePass(context.Background(), StructurePassRequest{RawID: id, Now: fixedNow(), Provider: p})
	require.NoError(t, err)

	assert.Equal(t, 1, res.Attempted)
	assert.Equal(t, 1, res.Wrote)
	assert.Equal(t, 1, res.Degraded)
	assert.Equal(t, 0, res.Failed)
	assert.Equal(t, StructureOutcomeDegraded, res.Entries[0].Outcome)
	assert.Equal(t, 2, p.Calls(), "a malformed reply is retried once, strictly")

	m := readProcessedMap(t, home, id)
	assert.Equal(t, storage.NotesStructuringFailed, m["notes"])
}

// TestStructurePass_StorageDegradeWritesNothing is error-states.md §St-3: the
// artifact cannot be written, so the entry degrades without writing. The raw
// entry stays captured and the pass is replayable.
func TestStructurePass_StorageDegradeWritesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, a, home := newBootedRouter(t)
	id, rawPath := writeRawAt(t, a, home, "a quiet, personless day", passDay(9, 0))
	rawBefore, err := os.ReadFile(rawPath)
	require.NoError(t, err)

	procDir := filepath.Join(home, "processed")
	require.NoError(t, os.Chmod(procDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(procDir, 0o700) })

	p := &provider.Fake{Script: []provider.Exchange{extractEx(noPeopleExtraction)}}
	res, err := r.StructurePass(context.Background(), StructurePassRequest{RawID: id, Now: fixedNow(), Provider: p})
	require.NoError(t, err)

	assert.Equal(t, 1, res.Attempted)
	assert.Equal(t, 0, res.Wrote)
	assert.Equal(t, 1, res.Degraded)
	assert.Equal(t, 0, res.Failed)
	assert.Equal(t, StructureOutcomeDegraded, res.Entries[0].Outcome)

	rawAfter, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	assert.Equal(t, rawBefore, rawAfter, "a degrade leaves the raw entry captured and untouched")
}

// TestStructurePass_RangedFailureContinues proves a window run finishes: an
// entry whose document cannot be read is counted as failed and the remaining
// entries still run, so an interrupted corpus does not block the rest.
func TestStructurePass_RangedFailureContinues(t *testing.T) {
	r, a, home := newBootedRouter(t)
	good, _ := writeRawAt(t, a, home, "Dinner with M.", passDay(9, 0))

	// A well-formed id whose document has no frontmatter fence: the enumerator
	// reads names, so it is listed, and the read fails when the pass opens it.
	broken := "raw_2026_07_05_21_00"
	brokenPath := filepath.Join(home, "raw", "2026", "07", broken+".md")
	require.NoError(t, os.WriteFile(brokenPath, []byte("no frontmatter here\n"), 0o600))

	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}
	res, err := r.StructurePass(context.Background(), rangedReq(p))
	require.NoError(t, err, "a window run records a per-entry failure rather than aborting")

	assert.Equal(t, 2, res.Attempted)
	assert.Equal(t, 1, res.Wrote)
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, 0, res.Degraded)
	assert.Equal(t, map[string]string{
		good:   StructureOutcomeWrote,
		broken: StructureOutcomeFailed,
	}, outcomes(res))
}

// TestStructurePass_SingleEntryMissingSurfacesError is the other half: with no
// remaining window to protect, a read fault surfaces verbatim alongside the
// counted failure — it is infrastructure, not a degrade.
func TestStructurePass_SingleEntryMissingSurfacesError(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	p := &provider.Fake{}

	res, err := r.StructurePass(context.Background(), StructurePassRequest{
		RawID: "raw_2026_07_05_00_00", Now: fixedNow(), Provider: p,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read raw")
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, 1, res.Attempted)
	assert.Equal(t, 0, res.Wrote)
	require.Len(t, res.Entries, 1)
	assert.Equal(t, StructureOutcomeFailed, res.Entries[0].Outcome)
}

// TestStructurePass_ContextCancellation proves an interrupted sweep still
// reports what landed: cancellation between entries returns the partial result
// with the context error, and nothing further reaches the model.
func TestStructurePass_ContextCancellation(t *testing.T) {
	r, a, home := newBootedRouter(t)
	writeRawAt(t, a, home, "First entry.", passDay(9, 0))
	writeRawAt(t, a, home, "Second entry.", passDay(10, 0))
	writeRawAt(t, a, home, "Third entry.", passDay(11, 0))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p := &provider.Fake{Script: []provider.Exchange{extractEx(noPeopleExtraction)}}

	req := rangedReq(p)
	req.Progress = func(done, _ int, _, _ string) {
		if done == 1 {
			cancel()
		}
	}
	res, err := r.StructurePass(ctx, req)

	require.ErrorIs(t, err, context.Canceled, "the context error surfaces verbatim")
	assert.Equal(t, 1, res.Attempted, "the partial result reports what landed")
	assert.Equal(t, 1, res.Wrote)
	assert.Len(t, res.Entries, 1)
	assert.Equal(t, 1, p.Calls(), "no entry after the cancellation reaches the model")
	assert.Equal(t, 1, countFiles(t, home, "processed"))
}

// TestStructurePass_EmptyWindow is the calm nothing-to-do path: a window with
// no raw entries spends nothing and is not an error.
func TestStructurePass_EmptyWindow(t *testing.T) {
	r, a, home := newBootedRouter(t)
	writeRawAt(t, a, home, "Outside the window.", passDay(9, 0).AddDate(0, 0, -3))
	p := &provider.Fake{}

	res, err := r.StructurePass(context.Background(), rangedReq(p))
	require.NoError(t, err)

	assert.True(t, res.Ranged)
	assert.Equal(t, 0, res.Attempted)
	assert.Equal(t, 0, res.Wrote)
	assert.Equal(t, 0, res.Skipped)
	assert.Equal(t, 0, res.Degraded)
	assert.Equal(t, 0, res.Failed)
	assert.Empty(t, res.Entries)
	assert.NotNil(t, res.Entries, "entries is an empty list, never null")
	assert.Equal(t, 0, p.Calls())
}

// TestStructurePass_ProgressReportsEveryEntry proves the liveness signal a long
// window run depends on: one callback per entry — skips included — with a
// monotonic index and the total held steady.
func TestStructurePass_ProgressReportsEveryEntry(t *testing.T) {
	r, a, home := newBootedRouter(t)
	done, _ := writeRawAt(t, a, home, "Already distilled.", passDay(8, 0))
	fresh, _ := writeRawAt(t, a, home, "A quiet, personless evening.", passDay(9, 0))
	structureOne(t, r, done)

	type step struct {
		done    int
		total   int
		rawID   string
		outcome string
	}
	var seen []step

	p := &provider.Fake{Script: []provider.Exchange{extractEx(noPeopleExtraction)}}
	req := rangedReq(p)
	req.Progress = func(d, total int, rawID, outcome string) {
		seen = append(seen, step{d, total, rawID, outcome})
	}
	res, err := r.StructurePass(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, res.Entries, 2)

	assert.Equal(t, []step{
		{1, 2, done, StructureOutcomeSkipped},
		{2, 2, fresh, StructureOutcomeWrote},
	}, seen, "progress fires once per entry, skips included")
}

// TestStructurePass_ZeroNowUsesWallClock covers the default-clock branch, which
// matches Structure's own.
func TestStructurePass_ZeroNowUsesWallClock(t *testing.T) {
	r, a, home := newBootedRouter(t)
	id, _ := writeRawAt(t, a, home, "Dinner with M.", passDay(9, 0))
	p := &provider.Fake{Script: []provider.Exchange{extractEx(structExtraction)}}

	res, err := r.StructurePass(context.Background(), StructurePassRequest{RawID: id, Provider: p}) // no Now
	require.NoError(t, err)
	assert.Equal(t, 1, res.Wrote)
}

// TestStructurePass_UnreadableProcessedDirSurfaces confirms the skip check
// refuses out loud: if the artifacts cannot be listed, the pass errors rather
// than treating every entry as unstructured and re-spending the whole window.
func TestStructurePass_UnreadableProcessedDirSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, a, home := newBootedRouter(t)
	writeRawAt(t, a, home, "Dinner with M.", passDay(9, 0))

	procDir := filepath.Join(home, "processed")
	require.NoError(t, os.Chmod(procDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(procDir, 0o700) })

	p := &provider.Fake{}
	res, err := r.StructurePass(context.Background(), rangedReq(p))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list processed artifacts")
	assert.Equal(t, 0, res.Attempted)
	assert.Equal(t, 0, p.Calls(), "nothing is spent when the skip check cannot be made")
}

// TestStructurePass_MalformedWindowBoundSurfaces confirms the enumerator's
// bound validation reaches the caller rather than reading as an empty window.
func TestStructurePass_MalformedWindowBoundSurfaces(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	p := &provider.Fake{}

	// Since after Until is the enumerator's refusal, not a quiet no-op.
	res, err := r.StructurePass(context.Background(), StructurePassRequest{
		Since: passDay(9, 0).AddDate(0, 0, 1), Until: passDay(9, 0), Now: fixedNow(), Provider: p,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list raw entries")
	assert.Equal(t, 0, res.Attempted)
	assert.Equal(t, 0, p.Calls())
}
