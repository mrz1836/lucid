package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// seedWeekRaw scaffolds the isolated Ledger (idempotently) and writes one raw
// entry recorded at `at`, returning its id so a deep-dive candidate can cite it.
func seedWeekRaw(t *testing.T, home string, at time.Time, body string) string {
	t.Helper()
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	res, err := a.WriteRaw(storage.RawEntry{
		RecordedAt: at, OccurredAt: at, OccurredAtPrecision: storage.PrecisionExact,
		Source: "cli", Command: "/log", Body: body,
	})
	require.NoError(t, err)
	return res.RawID
}

// weekDeepReply builds a full deep-dive completion; a non-empty shapeTag adds a
// candidate citing citeID. The narrative is deliberately blocklist-clean so
// Safety passes every line without a rewrite (one model call total).
func weekDeepReply(shapeTag, citeID string) provider.Exchange {
	body := `{"summary":"A steadier week overall.",` +
		`"wins":["logged entries"],"misses":["one skipped closeout"],` +
		`"body_pain":["a pain note mid-week"],"habit_change":["earlier evenings"],` +
		`"next_week":["one small experiment"]`
	if shapeTag != "" {
		body += `,"candidate":{"proposal_text":"One possible pattern: preparation as a way to feel safe.",` +
			`"shape_tag":"` + shapeTag + `","supporting_entry_ids":["` + citeID + `"]}`
	}
	body += "}"
	return provider.Exchange{Content: body}
}

// runReflectWeek executes `lucid reflect week [args...]` with empty stdin,
// capturing stdout and the command error.
func runReflectWeek(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"reflect", "week"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

// seedProcessedArtifact scaffolds the isolated Ledger (idempotently) and writes
// one valid processed artifact so the weekly apply path has a recent artifact to
// anchor the insight's provenance to.
func seedProcessedArtifact(t *testing.T, home, id string, at time.Time) {
	t.Helper()
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.WriteProcessed(storage.ProcessedArtifact{
		ID: id, EntryID: id, ProducedAt: at, AgentVersion: "structuring-2026.05.0",
		Emotions: []storage.ProcessedItem{{Name: "calm", Rationale: "user said 'calm'"}},
		Themes:   []storage.ProcessedItem{{Name: "prep", Rationale: "grounded in the entry"}},
	}))
}

// runReflectWeekApply executes `lucid reflect week apply [args...]`, piping the
// given stdin payload and capturing stdout and the command error.
func runReflectWeekApply(t *testing.T, stdin string, args ...string) (stdout string, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"reflect", "week", "apply"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

// applyPayload marshals a well-formed apply envelope citing citeID.
func applyPayload(t *testing.T, citeID, framework, kind, text string) string {
	t.Helper()
	env := reflectWeekApplyEnvelope{
		Candidate: reflectWeekPatternView{
			ProposalText:       "One possible pattern: preparation as a way to feel safe.",
			ShapeTag:           "prep-as-safety",
			SupportingEntryIDs: []string{citeID},
		},
		Framework: framework,
		Response:  reflectWeekApplyResponse{Kind: kind, Text: text},
	}
	b, err := json.Marshal(env)
	require.NoError(t, err)
	return string(b)
}

// TestReflectWeekApply_DispatchResolves proves `reflect week apply` resolves to
// the write leaf under the read-only week surface (AC-8 apply path).
func TestReflectWeekApply_DispatchResolves(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	apply, _, err := root.Find([]string{"reflect", "week", "apply"})
	require.NoError(t, err)
	assert.Equal(t, "apply", apply.Name())
}

// TestReflectWeekApply_AcceptPersistsInsight proves piping an accepted candidate
// persists a tracked insight stamped with provenance.framework through the CLI
// apply leaf (AC-6, AC-8; the Rock 1 DoD end-to-end via the command surface).
func TestReflectWeekApply_AcceptPersistsInsight(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	citeID := seedWeekRaw(t, home, reflectNow().Add(-3*24*time.Hour), "over-prepared for the review call")
	seedProcessedArtifact(t, home, "p_2026_05_08_a", reflectNow().Add(-24*time.Hour))
	withServeProvider(t, &provider.Fake{})

	out, err := runReflectWeekApply(t, applyPayload(t, citeID, "stoicism v1", "accepted", "Yes, that fits."), "--json")
	require.NoError(t, err)

	var view reflectWeekApplyView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.True(t, view.Wrote)
	require.NotEmpty(t, view.InsightID)

	// The insight landed with its lens label and raw-entry citation.
	assert.Equal(t, 1, countHomeFiles(t, home, "insights"))
	ins, err := storage.New(home).ReadInsight(view.InsightID)
	require.NoError(t, err)
	require.NotNil(t, ins.Provenance.Framework)
	assert.Equal(t, "stoicism v1", *ins.Provenance.Framework)
	assert.Equal(t, []string{citeID}, ins.Provenance.RawEntryIDs)
}

// TestReflectWeekApply_UnknownKindErrors proves an unrecognized response kind is
// rejected rather than silently degrading to unanswered.
func TestReflectWeekApply_UnknownKindErrors(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	seedProcessedArtifact(t, home, "p_2026_05_08_a", reflectNow().Add(-24*time.Hour))
	withServeProvider(t, &provider.Fake{})

	_, err := runReflectWeekApply(t, applyPayload(t, "raw_x", "", "maybe", ""), "--json")
	require.Error(t, err)
}

// TestReflectWeekApply_EmptyStdinErrors proves apply is an explicit write: an
// empty payload is an error, never a silent no-op.
func TestReflectWeekApply_EmptyStdinErrors(t *testing.T) {
	isolatedHome(t)
	withServeProvider(t, &provider.Fake{})
	_, err := runReflectWeekApply(t, "")
	require.Error(t, err)
}

// TestReflectWeek_DispatchCoexistsWithReflect proves `reflect week` dispatches to
// the read-only weekly surface while `reflect` and `reflect gate` still dispatch
// to the recall parent (A1 — a non-subcommand positional runs the parent). This
// is the AC-1 registration/dispatch guard.
func TestReflectWeek_DispatchCoexistsWithReflect(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})

	week, _, err := root.Find([]string{"reflect", "week"})
	require.NoError(t, err)
	assert.Equal(t, "week", week.Name(), "`reflect week` resolves to the week subcommand")

	parent, _, err := root.Find([]string{"reflect"})
	require.NoError(t, err)
	assert.Equal(t, "reflect", parent.Name(), "`reflect` resolves to the recall parent")

	gate, gateArgs, err := root.Find([]string{"reflect", "gate"})
	require.NoError(t, err)
	assert.Equal(t, "reflect", gate.Name(), "`reflect gate` runs the parent, not a subcommand")
	assert.Equal(t, []string{"gate"}, gateArgs, "`gate` is passed to the parent as a positional")
}

// TestReflectWeek_JSONRich proves a full week renders the --json view with every
// section and a cited pattern, and writes no reflection or new insight file
// (AC-2, AC-3).
func TestReflectWeek_JSONRich(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	citeID := seedWeekRaw(t, home, reflectNow().Add(-3*24*time.Hour), "over-prepared for the review call")
	seedWeekRaw(t, home, reflectNow().Add(-3*24*time.Hour).Add(time.Minute), "went quiet in standup")

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{weekDeepReply("prep-as-safety", citeID)}})

	out, err := runReflectWeek(t, "--json")
	require.NoError(t, err)

	var view reflectWeekView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Contains(t, view.ISOWeek, "-W")
	assert.NotEmpty(t, view.Summary)
	assert.NotEmpty(t, view.Wins)
	assert.NotEmpty(t, view.Misses)
	assert.NotEmpty(t, view.BodyPain)
	assert.NotEmpty(t, view.HabitChange)
	assert.NotEmpty(t, view.NextWeek)
	require.NotNil(t, view.Pattern)
	assert.Equal(t, "prep-as-safety", view.Pattern.ShapeTag)
	assert.Equal(t, []string{citeID}, view.Pattern.SupportingEntryIDs)

	// Read-only: the weekly surface writes no reflection record and no insight.
	assert.Equal(t, 0, countHomeFiles(t, home, "reflections"), "reflect week writes no reflection record")
	assert.Equal(t, 0, countHomeFiles(t, home, "insights"), "reflect week writes no insight")
}

// TestReflectWeek_ThinTextNoMarkdownTable proves a thin week renders
// Discord-friendly prose — bulleted sections, the ISO-week header — with no
// markdown table pipe (AC-3 Discord output rule; AC-11 thin).
func TestReflectWeek_ThinTextNoMarkdownTable(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	seedWeekRaw(t, home, reflectNow().Add(-2*24*time.Hour), "a single quiet entry")

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{weekDeepReply("", "")}})

	out, err := runReflectWeek(t)
	require.NoError(t, err)

	assert.Contains(t, out, "Week ")
	assert.Contains(t, out, "•", "sections render as bullets")
	assert.NotContains(t, out, "|", "no markdown table in Discord output")
}

// TestReflectWeek_MissingEmptyNoModelCall proves an empty store prints the calm
// fallback and makes no model call (AC-11 missing-context).
func TestReflectWeek_MissingEmptyNoModelCall(t *testing.T) {
	isolatedHome(t)
	withClock(t, reflectNow())

	fake := &provider.Fake{}
	withServeProvider(t, fake)

	out, err := runReflectWeek(t)
	require.NoError(t, err)

	assert.Contains(t, out, reflectWeekEmpty)
	assert.Equal(t, 0, fake.Calls(), "no model call over an empty store")
}

// TestReflectWeek_ProviderBuildErrorSurfaces proves a provider that cannot be
// built fails the verb rather than reporting a false deep-dive.
func TestReflectWeek_ProviderBuildErrorSurfaces(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	seedWeekRaw(t, home, reflectNow().Add(-2*24*time.Hour), "an entry")
	withFailingProvider(t)

	_, err := runReflectWeek(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unavailable")
}

// TestReflectWeek_RejectsArgs proves the read-only surface takes no positional
// arguments.
func TestReflectWeek_RejectsArgs(t *testing.T) {
	isolatedHome(t)
	withServeProvider(t, &provider.Fake{})
	_, err := runReflectWeek(t, "extra")
	require.Error(t, err)
}
