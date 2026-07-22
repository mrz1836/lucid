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

// TestBuildReport_AgingAnchorWatchOut proves the aging-anchor branch: an anchor
// past the mark surfaces a neutral, honest check-in watch-out. It injects the
// projection directly, so no fixture couples to a specific calendar.
func TestBuildReport_AgingAnchorWatchOut(t *testing.T) {
	m := engine.Metrics{
		Adherence: engine.Window{Length: 30},
		Anchors: []engine.AnchorDaysSince{
			{Label: "fresh", Date: "2026-07-10", DaysSince: 3},
			{Label: "stale-check-in", Date: "2026-05-01", DaysSince: 60},
		},
	}
	r, err := BuildReport(reportNow(), fakeNumbers{result: router.MetricsResult{Metrics: m}}, fakeRecords{})
	require.NoError(t, err)

	assert.Contains(t, r.WatchOuts, "60 days since stale-check-in — worth a check-in.")
	assert.NotContains(t, strings.Join(r.WatchOuts, "\n"), "fresh", "a recent anchor never trips the mark")
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
