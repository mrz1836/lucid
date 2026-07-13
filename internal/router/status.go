package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// StatusResult is the read-only `/status` L0 surface: the derived Status plus
// the human-readable lines rendered from it. The CLI prints the lines for a
// person and the Status as JSON for a script.
type StatusResult struct {
	Status engine.Status
	Lines  []string
}

// Status executes `/status` (engine-module.md §Commands): the read-only L0
// ambient surface. It writes nothing in the happy path — it reads the derived
// engine/status.json rebuilt after the last close-out. If that file is
// corrupt or missing it is regenerated silently from days/ (engine-module.md
// §Error states: "status.json corrupt/missing → Rebuild silently"), the one
// recovery path where a read triggers a write.
func (r *Router) Status(now time.Time) (StatusResult, error) {
	now = whenOr(now)
	if err := r.prepareEngine(); err != nil {
		return StatusResult{}, err
	}

	st, err := r.store.ReadEngineStatus()
	if err != nil {
		// Corrupt or missing: regenerate from the day records, silently.
		st, err = r.store.RebuildEngineStatus(now.Location())
		if err != nil {
			return StatusResult{}, err
		}
	}
	return StatusResult{Status: st, Lines: statusLines(st)}, nil
}

// statusLines renders the honest-number-paired L0 surface (engine-module.md
// §3: adherence never travels without the floor-day ratio and the days-
// accounted co-number). It surfaces the streak, both rolling windows, the
// error-budget burn, days-to-gate, storm state, and — only when they hold —
// the stake-owed and witness-lapsed notes. No score theater, one line each.
func statusLines(st engine.Status) []string {
	lines := []string{
		fmt.Sprintf("Streak: %d (longest %d).", st.CurrentStreak, st.LongestStreak),
		"7-day " + windowLine(st.Adherence7d),
		"30-day " + windowLine(st.Adherence30d),
		budgetLine(st.ErrorBudget),
	}
	if st.ConsecutiveMisses > 0 {
		lines = append(lines, fmt.Sprintf("Consecutive misses: %d.", st.ConsecutiveMisses))
	}
	if st.StormState == engine.StormStandingState {
		through := ""
		if st.StormThrough != nil {
			through = " through " + *st.StormThrough
		}
		lines = append(lines, fmt.Sprintf("Storm standing%s — the stake is stayed; contact continues.", through))
	}
	if st.DaysToNextGate != nil {
		lines = append(lines, fmt.Sprintf("Days to next gate: %d.", *st.DaysToNextGate))
	}
	if st.StakeOwed {
		lines = append(lines, "Stake owed — a breach outlived its execution window.")
	}
	if st.WitnessLapsed {
		lines = append(lines, "Witness lapsed — L2 disarmed.")
	}
	return lines
}

// windowLine renders one adherence window with its honest co-numbers. When no
// day in the window is decided yet, it says so rather than printing a hollow
// 0%.
func windowLine(w engine.Window) string {
	if w.Decided == 0 {
		return fmt.Sprintf("adherence: no decided days yet (%d accounted).", w.DaysAccounted)
	}
	return fmt.Sprintf("adherence: %s (%d/%d decided, %d accounted; floor-days %d, %s).",
		percent(w.Adherence), w.Completed, w.Decided, w.DaysAccounted, w.FloorDays, percent(w.FloorDayRatio)+" floor")
}

// budgetLine renders the isolated-miss error budget and its burn.
func budgetLine(b engine.ErrorBudget) string {
	line := fmt.Sprintf("Error budget: %d/%d isolated misses spent (%d left).", b.Burn, b.Budget, b.Remaining)
	if b.Exceeded {
		line += " Over budget — gates hold."
	}
	return line
}

// percent renders a 0..1 rate as a whole-percent string, rounding to nearest.
func percent(rate float64) string {
	return strings.TrimSpace(fmt.Sprintf("%d%%", int(rate*100+0.5)))
}
