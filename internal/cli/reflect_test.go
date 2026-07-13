package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// reflectNow is the pinned instant the reflect tests recall at — a Saturday in
// ISO week 19, 2026, so the seeded insights (created two days earlier) sit
// inside the rolling seven-day recall window.
func reflectNow() time.Time { return time.Date(2026, time.May, 9, 20, 10, 14, 0, time.UTC) }

// seedAcceptedInsight scaffolds the isolated Ledger (idempotently) and writes
// one accepted insight created at `at`, returning its id. It mirrors the router
// test's seedInsight so the cli-level reflect/ask tests have real material.
func seedAcceptedInsight(t *testing.T, home string, at time.Time, body string) string {
	t.Helper()
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	res, err := a.WriteInsight(storage.Insight{
		CreatedAt: at,
		Status:    storage.InsightStatusAccepted,
		Provenance: storage.InsightProvenance{
			RawEntryIDs:             []string{"raw_2026_05_05_19_42"},
			ProcessedArtifactID:     "raw_2026_05_05_19_42",
			ReflectionPromptVersion: "reflection-2026.05.0",
			UserResponseKind:        storage.ResponseAccepted,
			UserResponseText:        "Yes, that fits.",
		},
		Body: body,
	})
	require.NoError(t, err)
	return res.InsightID
}

// recallReply builds a valid recall completion surfacing every id with one
// clean, blocklist-safe resonance line (so Safety passes it without a model
// call — the fake needs only this single exchange).
func recallReply(ids ...string) provider.Exchange {
	entries := make([]string, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, fmt.Sprintf(`{"id":%q,"surface_text":%q}`, id, recallSurfaceText))
	}
	return provider.Exchange{Content: fmt.Sprintf(`{"outcome":"recall","ordered_insights":[%s]}`, strings.Join(entries, ","))}
}

// recallSurfaceText is the clean line the fake surfaces for each recalled
// insight; the reflect tests assert it reaches stdout.
const recallSurfaceText = "Does this still fit for you?"

// runReflect executes `lucid reflect [args...]` with the given stdin, capturing
// stdout and the command error (stderr is discarded — no reflect test asserts on
// it).
func runReflect(t *testing.T, stdin string, args ...string) (stdout string, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"reflect"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

// TestReflect_Registered proves `reflect` is on the cobra root (AC-6).
func TestReflect_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "reflect" {
			found = true
			break
		}
	}
	assert.True(t, found, "reflect must be registered on the root command")
}

// TestReflect_WeeklySurfacesRecallNoNewInsight proves the weekly pass surfaces
// each in-window insight, writes exactly one reflection record, and — the
// never-proposes guarantee — creates no new insight file. With no stdin batch,
// every surface is left unanswered so the seeded insights keep their status.
func TestReflect_WeeklySurfacesRecallNoNewInsight(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	idA := seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")
	idB := seedAcceptedInsight(t, home, reflectNow().Add(-3*24*time.Hour), "I over-prepare before hard talks.")

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{recallReply(idA, idB)}})

	out, err := runReflect(t, "")
	require.NoError(t, err)

	assert.Contains(t, out, recallSurfaceText, "the recall line is surfaced")
	assert.Contains(t, out, "Recorded reflection", "the ISO-week reflection record id is reported")

	// Never proposes: the two seeded insights are the only insight files.
	assert.Equal(t, 2, countHomeFiles(t, home, "insights"), "/reflect creates no new insight file")
	// Exactly one reflection record was written this pass.
	assert.Equal(t, 1, countHomeFiles(t, home, "reflections"))

	// Unanswered by default: neither insight advanced past its accepted status.
	a := storage.New(home)
	for _, id := range []string{idA, idB} {
		ins, readErr := a.ReadInsight(id)
		require.NoError(t, readErr)
		assert.Equal(t, storage.InsightStatusAccepted, ins.Status, "an unanswered insight keeps its status")
	}
}

// TestReflect_JSONReportsUnansweredByDefault proves the --json view reports each
// surface as unanswered when no batch is piped, and marks the record written.
func TestReflect_JSONReportsUnansweredByDefault(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	idA := seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{recallReply(idA)}})

	out, err := runReflect(t, "", "--json")
	require.NoError(t, err)

	var view reflectView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "week", view.Scope)
	assert.True(t, view.Wrote)
	require.Len(t, view.Surfaces, 1)
	assert.Equal(t, idA, view.Surfaces[0].InsightID)
	assert.Equal(t, "unanswered", view.Surfaces[0].ResponseKind)
	assert.Contains(t, view.Surfaces[0].Surface, recallSurfaceText)
}

// TestReflect_AppliesStdinBatch proves an optional stdin/JSON answer batch is
// applied in one shot: the named insight gets its confirmed status transition,
// and the --json view reports it — while the verb still writes no new insight.
func TestReflect_AppliesStdinBatch(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	idA := seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{recallReply(idA)}})

	batch := fmt.Sprintf(`{"answers":{%q:{"status":%q}}}`, idA, storage.RecallConfirmed)
	out, err := runReflect(t, batch, "--json")
	require.NoError(t, err)

	var view reflectView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.Len(t, view.Surfaces, 1)
	assert.Equal(t, storage.RecallConfirmed, view.Surfaces[0].ResponseKind, "the batch answer is applied")

	// The status transition landed on the insight, and still no new insight file.
	a := storage.New(home)
	ins, err := a.ReadInsight(idA)
	require.NoError(t, err)
	last := ins.StatusHistory[len(ins.StatusHistory)-1]
	assert.Equal(t, storage.RecallConfirmed, last.Kind, "the confirmed transition is recorded on the insight")
	assert.True(t, last.At.Equal(reflectNow()), "the transition is provenance-stamped at the reflect instant")
	assert.Equal(t, 1, countHomeFiles(t, home, "insights"))
}

// TestReflect_Gate proves `reflect gate` runs the gate pass and appends the
// deterministic panel line.
func TestReflect_Gate(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	idA := seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{recallReply(idA)}})

	out, err := runReflect(t, "", "gate")
	require.NoError(t, err)
	assert.Contains(t, out, "Gate panel", "the gate pass appends the deterministic panel")
	assert.Contains(t, out, "Recorded reflection")
}

// TestReflect_NothingValidatedWritesNoRecord proves an empty store returns the
// fixed E-3 copy, writes no reflection record, and makes no model call.
func TestReflect_NothingValidatedWritesNoRecord(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())

	fake := &provider.Fake{}
	withServeProvider(t, fake)

	out, err := runReflect(t, "")
	require.NoError(t, err)
	assert.Contains(t, out, "Nothing validated yet")
	assert.NotContains(t, out, "Recorded reflection")
	assert.Equal(t, 0, countHomeFiles(t, home, "reflections"))
	assert.Equal(t, 0, fake.Calls(), "no model call over an empty store")
}

// TestReflect_UnknownScopeRejected proves a positional token other than `gate`
// is rejected rather than silently running the wrong cadence.
func TestReflect_UnknownScopeRejected(t *testing.T) {
	isolatedHome(t)
	withServeProvider(t, &provider.Fake{})

	_, err := runReflect(t, "", "quarterly")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown scope")
}

// TestReflect_MalformedBatchErrors proves malformed JSON piped as the answer
// batch surfaces an error rather than being silently dropped.
func TestReflect_MalformedBatchErrors(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")
	withServeProvider(t, &provider.Fake{})

	_, err := runReflect(t, "{not json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode answer batch")
}

// TestReflect_ProviderBuildErrorSurfaces proves a provider that cannot be built
// fails the verb rather than reporting a false recall.
func TestReflect_ProviderBuildErrorSurfaces(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")
	withFailingProvider(t)

	_, err := runReflect(t, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unavailable")
}

// TestReflect_BootError proves an unscaffoldable home fails the verb at boot,
// before any recall runs.
func TestReflect_BootError(t *testing.T) {
	unscaffoldableHome(t)
	withServeProvider(t, &provider.Fake{})
	_, err := runReflect(t, "")
	require.Error(t, err)
}

// TestReflect_ReadRecallBatchFromFile covers the stdin-batch reader directly: a
// regular file is not an interactive terminal, so its JSON batch is decoded.
func TestReflect_ReadRecallBatchFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"answers":{"i_1":{"status":"confirmed","rule":"kept"}}}`), 0o600))
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	assert.False(t, stdinIsInteractive(f), "a regular file is not an interactive terminal")
	answers, err := readRecallBatch(f)
	require.NoError(t, err)
	require.Contains(t, answers, "i_1")
	assert.Equal(t, storage.RecallConfirmed, answers["i_1"].Status)
	assert.Equal(t, storage.RuleKept, answers["i_1"].Rule)
}

// TestReflect_ReadRecallBatchEmptyIsNil proves an empty stdin yields no batch
// (the one-shot verb never blocks and leaves every surface unanswered).
func TestReflect_ReadRecallBatchEmptyIsNil(t *testing.T) {
	answers, err := readRecallBatch(strings.NewReader(""))
	require.NoError(t, err)
	assert.Empty(t, answers)
}
