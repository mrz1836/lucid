package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
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

// runReflectWeekClose executes `lucid reflect week close [args...]` with empty
// stdin, capturing stdout and the command error.
func runReflectWeekClose(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"reflect", "week", "close"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

// renderWeekOut renders one deep-dive result through the real renderer, in text
// or --json form, and returns exactly what the command would have printed. It
// lets the header and cap branches be asserted byte-for-byte without seeding a
// months-long fixture Ledger for every window shape.
func renderWeekOut(t *testing.T, res router.ReflectWeekResult, asJSON bool) string {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().Bool(jsonFlag, false, "")
	if asJSON {
		require.NoError(t, cmd.Flags().Set(jsonFlag, "true"))
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	require.NoError(t, renderReflectWeek(cmd, res))
	return out.String()
}

// weekDay builds a YYYY-MM-DD civil day at midnight UTC, the anchor form the
// window fields speak.
func weekDay(t *testing.T, day string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", day)
	require.NoError(t, err)
	return d
}

// windowResult builds a minimal non-empty deep-dive result over the given
// window, so a render assertion exercises the header and cap branches rather
// than the empty-week fallback. ISOWeek labels the window's END day, exactly as
// the router sets it.
func windowResult(t *testing.T, start, end string, days int) router.ReflectWeekResult {
	t.Helper()
	endAt := weekDay(t, end)
	return router.ReflectWeekResult{
		ISOWeek:     isoweek.Label(endAt),
		WindowStart: weekDay(t, start),
		WindowEnd:   endAt,
		DaysCovered: days,
		Summary:     "A steadier stretch overall.",
	}
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

// TestReflectWeekFlags_MutuallyExclusive proves any combination of the three
// range overrides is a clean error rather than a silent precedence — the user
// never reads a window they did not ask for without being told (AC-8).
func TestReflectWeekFlags_MutuallyExclusive(t *testing.T) {
	combos := [][]string{
		{"--week", "--days", "3"},
		{"--week", "--since", "2026-05-06"},
		{"--since", "2026-05-06", "--days", "3"},
		{"--week", "--since", "2026-05-06", "--days", "3"},
	}
	for _, args := range combos {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			isolatedHome(t)
			withServeProvider(t, &provider.Fake{})

			_, err := runReflectWeek(t, args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "none of the others can be",
				"cobra reports the mutual exclusion")
			// An impossible flag combination is a misuse, not a failed run, so
			// a wrapping script can tell the two apart.
			assert.Equal(t, ExitUsage, exitCodeForError(err))
		})
	}
}

// TestReflectWeekFlags_SinceRejectsBadDate proves an unparseable --since fails
// with a message naming the shape it wanted, rather than degrading to some other
// window (AC-8).
func TestReflectWeekFlags_SinceRejectsBadDate(t *testing.T) {
	isolatedHome(t)
	withClock(t, reflectNow())
	withServeProvider(t, &provider.Fake{})

	_, err := runReflectWeek(t, "--since", "notadate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "YYYY-MM-DD", "the message names the shape it wanted")
	assert.Contains(t, err.Error(), "notadate", "and the value it rejected")
	// A bad flag value is a usage error, like every other flag-parse failure.
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestReflectWeekFlags_DaysZeroSurfacesRejection proves an explicitly-passed
// out-of-range --days is rejected rather than swallowed back into the default
// catch-up window (AC-8).
func TestReflectWeekFlags_DaysZeroSurfacesRejection(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	seedWeekRaw(t, home, reflectNow().Add(-2*24*time.Hour), "an entry")
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{weekDeepReply("", "")}})

	_, err := runReflectWeek(t, "--days", "0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "days must be >= 1")
}

// TestReflectWeekFlags_EachResolvesItsWindow proves each range flag alone
// resolves the window it names, and — the AC-1 evidence at the command surface —
// that the UNFLAGGED default is the catch-up window (from the earliest logged
// entry) rather than the ISO week `--week` still reproduces.
func TestReflectWeekFlags_EachResolvesItsWindow(t *testing.T) {
	// reflectNow() is Saturday 2026-05-09, in ISO week 2026-W19 (Mon 05-04 →
	// Sun 05-10). The earliest seeded entry lands on 05-06.
	cases := []struct {
		name        string
		args        []string
		wantStart   string
		wantEnd     string
		wantDays    int
		wantISOWeek string
	}{
		// The default no longer bounds itself by the calendar week: it starts at
		// the earliest logged entry, three days back, not the week's Monday.
		{"default is the catch-up window", nil, "2026-05-06", "2026-05-09", 4, "2026-W19"},
		// --week restores the pre-catch-up bounds verbatim, Sunday end included.
		{"week restores the ISO week", []string{"--week"}, "2026-05-04", "2026-05-10", 7, "2026-W19"},
		{"since runs from an explicit day", []string{"--since", "2026-05-07"}, "2026-05-07", "2026-05-09", 3, "2026-W19"},
		{"days covers the trailing span", []string{"--days", "2"}, "2026-05-08", "2026-05-09", 2, "2026-W19"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := isolatedHome(t)
			withClock(t, reflectNow())
			citeID := seedWeekRaw(t, home, reflectNow().Add(-3*24*time.Hour), "over-prepared for the review call")
			seedWeekRaw(t, home, reflectNow().Add(-24*time.Hour), "went quiet in standup")
			withServeProvider(t, &provider.Fake{
				Script: []provider.Exchange{weekDeepReply("prep-as-safety", citeID)},
			})

			out, err := runReflectWeek(t, append(slices.Clone(tc.args), "--json")...)
			require.NoError(t, err)

			var view reflectWeekView
			require.NoError(t, json.Unmarshal([]byte(out), &view))
			assert.Equal(t, tc.wantStart, view.WindowStart)
			assert.Equal(t, tc.wantEnd, view.WindowEnd)
			assert.Equal(t, tc.wantDays, view.DaysCovered)
			assert.Equal(t, tc.wantISOWeek, view.ISOWeek, "iso_week labels the window's end day")
			assert.False(t, view.Capped, "a short window is never capped")
			assert.Zero(t, view.UncoveredDays)
		})
	}
}

// TestReflectWeekClose_StampsCursor proves `close --json` reports what it
// stamped and lands the reflected-through receipt under the isolated home,
// round-tripping through the storage adapter (AC-4).
func TestReflectWeekClose_StampsCursor(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	// A provider that cannot be built proves close never reaches the model
	// boundary: it is pure storage and works with no provider configured.
	withFailingProvider(t)

	out, err := runReflectWeekClose(t, "--json")
	require.NoError(t, err)

	var view reflectWeekCloseView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, reflectNow().Format(time.RFC3339), view.ReflectedThrough)
	assert.Equal(t, "close", view.Source)
	assert.True(t, view.Wrote)

	_, statErr := os.Stat(filepath.Join(home, "engine", "reflection", "receipt.json"))
	require.NoError(t, statErr, "the cursor lands at engine/reflection/receipt.json")

	receipt, ok, err := storage.New(home).ReadReflectionReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, reflectNow().Format(time.RFC3339), receipt.ReflectedThrough)
	assert.Equal(t, "close", receipt.Source)
}

// TestReflectWeekClose_TextForm proves the human form names the day the cursor
// now reflects through (AC-4).
func TestReflectWeekClose_TextForm(t *testing.T) {
	isolatedHome(t)
	withClock(t, reflectNow())
	withFailingProvider(t)

	out, err := runReflectWeekClose(t)
	require.NoError(t, err)
	assert.Equal(t, "Reflected through 2026-05-09.\n", out)
}

// TestReflectWeekClose_RejectsArgs proves close takes no positional arguments.
func TestReflectWeekClose_RejectsArgs(t *testing.T) {
	isolatedHome(t)
	withClock(t, reflectNow())
	withFailingProvider(t)

	_, err := runReflectWeekClose(t, "extra")
	require.Error(t, err)
}

// TestReflectWeek_JSONShapeIsAdditive is the golden-shape guard on the --json
// contract: it asserts the EXACT key set, so a renamed, removed, or unexpected
// key fails here. iso_week in particular is a live downstream contract — a
// harness keys its per-window receipt on it — so it must survive verbatim
// (AC-11).
func TestReflectWeek_JSONShapeIsAdditive(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	citeID := seedWeekRaw(t, home, reflectNow().Add(-3*24*time.Hour), "over-prepared for the review call")
	withServeProvider(t, &provider.Fake{
		Script: []provider.Exchange{weekDeepReply("prep-as-safety", citeID)},
	})

	out, err := runReflectWeek(t, "--json")
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &raw))

	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	// applied_lens is omitempty and this run consents to no lens, so it is
	// legitimately absent; every other key is unconditional.
	assert.Equal(t, []string{
		"body_pain", "capped", "days_covered", "habit_change", "iso_week",
		"misses", "next_week", "pattern", "summary", "uncovered_days",
		"window_end", "window_start", "wins",
	}, keys)
}

// TestReflectWeek_HeaderSingleISOWeekUnchanged proves a window whose ends fall in
// one ISO week keeps the familiar one-week header byte-for-byte — the catch-up
// change costs the common case nothing (AC-12, derived decision 10).
func TestReflectWeek_HeaderSingleISOWeekUnchanged(t *testing.T) {
	out := renderWeekOut(t, windowResult(t, "2026-05-06", "2026-05-09", 4), false)
	assert.Equal(t, "Week 2026-W19\nA steadier stretch overall.\n", out)
}

// TestReflectWeek_HeaderSpansMultipleWeeks proves a catch-up window naming more
// than one ISO week states both ends and the day count, so a multi-week read is
// never presented as a single week (AC-12).
func TestReflectWeek_HeaderSpansMultipleWeeks(t *testing.T) {
	cases := []struct {
		name       string
		start, end string
		days       int
		want       string
	}{
		{
			name:  "same ISO year drops the repeated year",
			start: "2026-07-13", end: "2026-07-26", days: 14,
			want: "Weeks 2026-W29 → W30 (14 days)",
		},
		{
			// Across a year boundary the year is the only thing telling the two
			// labels apart, so it stays.
			name:  "year boundary keeps both labels whole",
			start: "2025-12-22", end: "2026-01-04", days: 14,
			want: "Weeks 2025-W52 → 2026-W01 (14 days)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderWeekOut(t, windowResult(t, tc.start, tc.end, tc.days), false)
			assert.Equal(t, tc.want, strings.SplitN(out, "\n", 2)[0])
		})
	}
}

// TestReflectWeek_CappedStatesShortfall proves a capped window says so out loud
// in BOTH forms: the coverage line sits immediately under the text header, and
// --json carries the same fact in machine form. The cap is only acceptable
// because it is honest (AC-9).
func TestReflectWeek_CappedStatesShortfall(t *testing.T) {
	res := windowResult(t, "2026-06-22", "2026-07-26", 35)
	res.Capped = true
	res.UncoveredDays = 61

	lines := strings.Split(renderWeekOut(t, res, false), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	assert.True(t, strings.HasPrefix(lines[0], "Weeks "), "the header still states the range")
	assert.Equal(t,
		"Covering the last 35 of 96 un-reflected days — pass --since to read the full span.",
		lines[1], "the shortfall is stated immediately under the header")

	var view reflectWeekView
	require.NoError(t, json.Unmarshal([]byte(renderWeekOut(t, res, true)), &view))
	assert.True(t, view.Capped)
	assert.Equal(t, 61, view.UncoveredDays)
	assert.Equal(t, 35, view.DaysCovered)
}

// TestReflectWeek_UncappedWindowStatesNoShortfall proves the coverage line is
// absent — not merely zeroed — on a window the cap never touched.
func TestReflectWeek_UncappedWindowStatesNoShortfall(t *testing.T) {
	out := renderWeekOut(t, windowResult(t, "2026-07-13", "2026-07-26", 14), false)
	assert.NotContains(t, out, "un-reflected days")
}
