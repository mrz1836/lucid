package witnessreport

import (
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/router"
)

// reportNow is the fixed Monday-morning instant the smoke tests generate a
// report at. Its date bounds "this week" (the 07-07..07-13 window) and labels
// the ISO week; a fixed instant keeps every assertion byte-deterministic.
func reportNow() time.Time { return time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC) }

// fakeNumbers injects a canned metrics projection — the honest-numbers seam a
// test controls in place of the live router.
type fakeNumbers struct {
	result router.MetricsResult
	err    error
}

func (f fakeNumbers) Metrics(time.Time) (router.MetricsResult, error) { return f.result, f.err }

// fakeRecords injects canned day records — the 7-day window's only input.
type fakeRecords struct {
	days []engine.DayRecord
	err  error
}

func (f fakeRecords) ReadEngineDays() ([]engine.DayRecord, error) { return f.days, f.err }

// loadFixture reads a synthetic day-record fixture from the fixtures dir. The
// fixtures carry no real personal data — they are hand-shaped weeks (strong,
// quiet, miss-heavy). They live under fixtures/ rather than the Go-conventional
// testdata/ because this repo gitignores testdata (fuzz corpus), and the smoke
// test's inputs must be committed.
func loadFixture(t *testing.T, name string) []engine.DayRecord {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("fixtures", name))
	require.NoError(t, err)
	var recs []engine.DayRecord
	require.NoError(t, json.Unmarshal(b, &recs))
	return recs
}

// buildFromFixture runs the real engine projection over a fixture and folds it
// through BuildReport exactly as the CLI/scheduler will — the numbers are
// honest (produced by engine.BuildMetrics), only the readers are fakes.
func buildFromFixture(t *testing.T, name string) (Report, engine.Metrics) {
	t.Helper()
	recs := loadFixture(t, name)
	clocks, err := engine.DefaultChain().ClocksFor(engine.DefaultProfile)
	require.NoError(t, err)
	m := engine.BuildMetrics(engine.MetricsInput{
		Records: recs,
		Chain:   engine.DefaultChain(),
		Now:     reportNow(),
		Clocks:  clocks,
		Loc:     reportNow().Location(),
	})
	r, err := BuildReport(reportNow(), fakeNumbers{result: router.MetricsResult{Metrics: m}}, fakeRecords{days: recs})
	require.NoError(t, err)
	return r, m
}

// TestBuildReport_StrongWeek: a fully committed week shows honest full numbers,
// no watch-outs at all (AC-5 omits the section cleanly), and exactly one honest
// generic ask (AC-4 with no signal).
func TestBuildReport_StrongWeek(t *testing.T) {
	r, _ := buildFromFixture(t, "strong-week.json")

	assert.Equal(t, isoweek.Label(reportNow()), r.ISOWeek)
	assert.Equal(t, 7, r.Streak)
	assert.Equal(t, 7, r.LongestStreak)
	assert.Equal(t, 7, r.Week.DaysAccounted)
	assert.Equal(t, 7, r.Week.Decided)
	assert.Equal(t, 7, r.Week.Completed)
	assert.Equal(t, 0, r.WeekMisses)
	assert.False(t, r.LowSignal)
	assert.InDelta(t, 1.0, r.Adherence.Adherence, 1e-9)
	assert.False(t, r.ErrorBudget.Exceeded)

	assert.Empty(t, r.WatchOuts, "a strong week surfaces no watch-outs")
	assert.Equal(t, []string{askGeneric}, r.Asks)
}

// TestBuildReport_QuietWeek: a thin week is never suppressed — it posts, and the
// thin logging is itself the watch-out (AC-13), driving the daily-nudge ask.
func TestBuildReport_QuietWeek(t *testing.T) {
	r, _ := buildFromFixture(t, "quiet-week.json")

	assert.Equal(t, 2, r.Week.DaysAccounted)
	assert.Equal(t, 0, r.WeekMisses)
	assert.True(t, r.LowSignal, "two logged days is a quiet week")

	require.Len(t, r.WatchOuts, 1, "the only watch-out is the thin logging itself")
	assert.Equal(t, "Logged only 2 of 7 days this week — accountability risk.", r.WatchOuts[0])
	assert.Equal(t, []string{askNudgeToLog}, r.Asks)
}

// TestBuildReport_MissHeavyWeek: a miss-heavy week surfaces every real signal —
// this-week misses, a 30-day adherence dip, and a spent error budget — in a
// stable order, with the two matching asks.
func TestBuildReport_MissHeavyWeek(t *testing.T) {
	r, _ := buildFromFixture(t, "miss-heavy-week.json")

	assert.Equal(t, 1, r.Streak, "the last completed day stands alone after a miss")
	assert.Equal(t, 7, r.Week.DaysAccounted)
	assert.Equal(t, 3, r.Week.Completed)
	assert.Equal(t, 4, r.WeekMisses)
	assert.False(t, r.LowSignal)
	assert.True(t, r.ErrorBudget.Exceeded)
	assert.Equal(t, 6, r.ErrorBudget.Burn)

	assert.Equal(t, []string{
		"Missed 4 of 7 decided days this week.",
		"30-day adherence at 33% — below the 80% line.",
		"Error budget spent — 6 isolated misses against a budget of 4.",
	}, r.WatchOuts)
	assert.Equal(t, []string{askMidweekCheckIn, askChainHeld}, r.Asks)
}

// TestBuildReport_CopiesHonestNumbers is the AC-2 guard: every streak,
// adherence, error-budget, and anchor number on the report is the projection's
// number verbatim — BuildReport copies, it never recomputes.
func TestBuildReport_CopiesHonestNumbers(t *testing.T) {
	for _, name := range []string{"strong-week.json", "quiet-week.json", "miss-heavy-week.json"} {
		t.Run(name, func(t *testing.T) {
			r, m := buildFromFixture(t, name)
			assert.Equal(t, m.CurrentStreak, r.Streak)
			assert.Equal(t, m.LongestStreak, r.LongestStreak)
			assert.Equal(t, m.Adherence, r.Adherence)
			assert.Equal(t, m.ErrorBudget, r.ErrorBudget)
			assert.Equal(t, m.Anchors, r.Anchors)
		})
	}
}

// TestBuildReport_TwoWeeksDiffer is the AC-8 seed: two different weeks produce
// materially different reports — not stale boilerplate.
func TestBuildReport_TwoWeeksDiffer(t *testing.T) {
	strong, _ := buildFromFixture(t, "strong-week.json")
	heavy, _ := buildFromFixture(t, "miss-heavy-week.json")

	assert.NotEqual(t, strong.Streak, heavy.Streak)
	assert.False(t, reflect.DeepEqual(strong.WatchOuts, heavy.WatchOuts), "watch-outs differ")
	assert.False(t, reflect.DeepEqual(strong.Asks, heavy.Asks), "asks differ")
	assert.False(t, reflect.DeepEqual(strong, heavy), "the reports differ materially")
}

// TestBuildReport_SunsetAnchorRaisesNoWatchOut proves the report inherits the
// retirement filter rather than implementing one: a limit over its allowance
// raises its watch-out while it counts, and stops the moment it is sunset —
// with no anchor-state logic anywhere in this package. The projection is built
// by the real engine fold, because a retirement is only expressible upstream:
// by the time it reaches the report the retired anchor is simply absent.
func TestBuildReport_SunsetAnchorRaisesNoWatchOut(t *testing.T) {
	const wantWatchOut = "late-night-snacks: over allowance — 3 of 2 in the last 30 days."
	limit := engine.Anchor{
		ID: "anchor_2026_06_01_a", Label: "late-night-snacks", Date: "2026-06-01",
		RecordedAt: "2026-06-01T12:00:00Z",
	}
	event := func(id, typ, day string) engine.AnchorEvent {
		return engine.AnchorEvent{
			ID: id, AnchorID: limit.ID, Type: typ, Day: day, RecordedAt: day + "T12:00:00Z",
		}
	}
	kind := event("anchorev_2026_06_01_a", engine.AnchorEventKind, "2026-06-01")
	kind.Kind, kind.Allowance, kind.WindowDays = engine.AnchorKindLimit, 2, 30
	events := engine.AnchorEventLog{Version: engine.AnchorEventsVersion, Events: []engine.AnchorEvent{kind}}
	for _, day := range []string{"2026-07-01", "2026-07-05", "2026-07-08"} {
		use := event("anchorev_"+strings.ReplaceAll(day, "-", "_")+"_a", engine.AnchorEventUse, day)
		use.Intent = engine.AnchorIntentPlanned
		events.Events = append(events.Events, use)
	}

	build := func(t *testing.T, history []engine.Anchor) Report {
		t.Helper()
		clocks, err := engine.DefaultChain().ClocksFor(engine.DefaultProfile)
		require.NoError(t, err)
		log := engine.AnchorLog{Version: engine.AnchorVersion, History: history}
		m := engine.BuildMetrics(engine.MetricsInput{
			Anchors:   engine.LatestAnchors(log),
			AnchorLog: log,
			Events:    events,
			Chain:     engine.DefaultChain(),
			Now:       reportNow(),
			Clocks:    clocks,
			Loc:       reportNow().Location(),
		})
		r, err := BuildReport(reportNow(), fakeNumbers{result: router.MetricsResult{Metrics: m}}, fakeRecords{})
		require.NoError(t, err)
		return r
	}

	active := build(t, []engine.Anchor{limit})
	require.Len(t, active.Anchors, 1)
	require.NotNil(t, active.Anchors[0].Limit)
	assert.True(t, active.Anchors[0].Limit.OverAllowance, "three use-days against an allowance of two")
	assert.Contains(t, active.WatchOuts, wantWatchOut, "while it counts, an over-allowance limit is flagged")

	retired := limit
	retired.RecordedAt = "2026-07-10T09:00:00Z"
	retired.State = engine.AnchorStateSunset
	sunset := build(t, []engine.Anchor{limit, retired})
	assert.Empty(t, sunset.Anchors, "a retired anchor never reaches the report")
	assert.NotContains(t, sunset.WatchOuts, wantWatchOut, "and so raises no watch-out")
}

// TestBuildReport_ReaderErrorsPropagate guards both read seams: a failing
// numbers or records reader surfaces a wrapped error rather than a half-built
// report.
func TestBuildReport_ReaderErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")

	_, err := BuildReport(reportNow(), fakeNumbers{err: boom}, fakeRecords{})
	require.ErrorIs(t, err, boom)

	m := engine.Metrics{Adherence: engine.Window{Length: 30}}
	_, err = BuildReport(reportNow(), fakeNumbers{result: router.MetricsResult{Metrics: m}}, fakeRecords{err: boom})
	require.ErrorIs(t, err, boom)
}

// TestDeterministicCore_NoProviderImport is the structural firewall assertion
// for Phase 2: the deterministic-core files (report.go, asks.go) import no
// provider/agent/model package and no observations/journal reader, so a private
// detail or a model can never reach the honest-number scaffold. The compose
// pass added later lives in its own files; these two stay pure.
// TestPct locks the whole-percent rounding at half-up (to nearest), the same
// rule the router's status/metrics/person surfaces and the companion's status
// panel apply. The two-thirds and above-midpoint rows are the ones the former
// truncation rendered a point low (66 rather than 67), the drift this corrects.
func TestPct(t *testing.T) {
	cases := []struct {
		name  string
		ratio float64
		want  int
	}{
		{"exact whole percent", 0.80, 80},
		{"two thirds rounds up (was 66 under truncation)", 2.0 / 3.0, 67},
		{"above midpoint rounds up (was 66)", 0.666, 67},
		{"below midpoint rounds down", 0.664, 66},
		{"zero", 0, 0},
		{"full", 1, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pct(tc.ratio))
		})
	}
}

func TestDeterministicCore_NoProviderImport(t *testing.T) {
	forbidden := []string{
		"internal/provider",
		"internal/agents",
		"internal/observations",
	}
	fset := token.NewFileSet()
	for _, name := range []string{"report.go", "asks.go"} {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range f.Imports {
			for _, bad := range forbidden {
				assert.NotContainsf(t, imp.Path.Value, bad,
					"%s imports %s — the deterministic core must stay model-free and input-restricted", name, imp.Path.Value)
			}
		}
	}
}

// TestReport_JSONOmitsFallbackDiagnostics pins the `lucid witness report --json`
// shape: the fallback diagnostics the scheduled worker reads (reason, provider
// error, CLI-reported detail) never serialize, while fallback and
// safety_tripped serialize exactly as before.
func TestReport_JSONOmitsFallbackDiagnostics(t *testing.T) {
	r := Report{
		ISOWeek:        "2026-W29",
		Fallback:       true,
		SafetyTripped:  true,
		FallbackReason: "safety_tripped",
		FallbackError:  "synthetic provider error",
		FallbackDetail: "synthetic detail",
	}
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	out := string(raw)

	for _, key := range []string{"fallback_reason", "FallbackReason", "fallback_error", "FallbackError", "fallback_detail", "FallbackDetail"} {
		assert.NotContains(t, out, `"`+key+`"`, "diagnostics never reach --json")
	}
	assert.NotContains(t, out, "synthetic provider error")
	assert.NotContains(t, out, "synthetic detail")
	assert.Contains(t, out, `"fallback":true`)
	assert.Contains(t, out, `"safety_tripped":true`)

	// With the diagnostics zeroed the encoding is byte-identical: they add nothing.
	bare := r
	bare.FallbackReason, bare.FallbackError, bare.FallbackDetail = "", "", ""
	rawBare, err := json.Marshal(bare)
	require.NoError(t, err)
	assert.Equal(t, string(rawBare), out)
}
