package engine

import (
	"testing"
	"time"
)

// These are baseline benchmarks for the deterministic Engine hot paths that run
// on every `lucid day` / `lucid closeout` / morning tripwire — not an
// optimization target, but a floor so a future change that regresses the
// no-model daily surface is visible. They reuse the package's test fixtures.

// BenchmarkParseCompact measures the compact close-out parse against the chain
// (the `/closeout` fast path).
func BenchmarkParseCompact(b *testing.B) {
	chain := DefaultChain()
	b.ReportAllocs()
	for b.Loop() {
		if _, _, _, _, err := ParseCompact(chain, "dfx 3/wrist Long day but the chain ran."); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBuildDayRecord measures folding one close-out into its day record.
func BenchmarkBuildDayRecord(b *testing.B) {
	chain := DefaultChain()
	in := baseInput()
	b.ReportAllocs()
	for b.Loop() {
		_ = BuildDayRecord(chain, in)
	}
}

// BenchmarkComputeStreaks measures the streak fold over a two-month window of
// completed days — the shape a long-running Ledger's day view reduces.
func BenchmarkComputeStreaks(b *testing.B) {
	start := refDay("2026-05-01")
	recs := make([]DayRecord, 0, 60)
	for i := range 60 {
		recs = append(recs, completedDay(DateString(start.AddDate(0, 0, i)), ModeGreen))
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = ComputeStreaks(recs, time.UTC)
	}
}

// BenchmarkEvaluateTripwire measures the pure morning dead-man decision on a
// one-miss (L1) day — the escalation path.
func BenchmarkEvaluateTripwire(b *testing.B) {
	in := TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       time.UTC,
		Reference: refDay("2026-07-05"), // absent ⇒ a miss
		Chain:     startedChain("2026-07-01"),
		Records:   recMap(completedDay("2026-07-04", ModeGreen)),
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = EvaluateTripwire(in)
	}
}

// BenchmarkEvaluateTripwireUnderStorm measures the same decision with a standing
// storm in history — the branch that reads and folds the storm ledger.
func BenchmarkEvaluateTripwireUnderStorm(b *testing.B) {
	in := TripwireInput{
		Now:       ts(2026, 7, 6, 9, 0),
		Loc:       time.UTC,
		Reference: refDay("2026-07-05"),
		Chain:     startedChain("2026-07-01"),
		Storm:     StormHistory{History: []StormEvent{{At: "2026-07-01T07:00:00Z", Event: StormDeclared, Label: "clause-1"}}},
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = EvaluateTripwire(in)
	}
}
