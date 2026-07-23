package witnessreport

// maxAsks bounds the "how friends can help this week" section: at most three
// concrete asks so the section stays scannable and never becomes a wall.
const maxAsks = 3

// The concrete asks, each grounded in one real signal. They are phrased as
// something a friend can actually do this week, not vague status.
const (
	askMidweekCheckIn = "Check in on me midweek — I missed a couple of days this week."
	askChainHeld      = "Ask me how the chain held this week."
	askNudgeToLog     = "Nudge me to log daily — my logging got thin this week."
	// askGeneric is the single honest ask used when the week carries no signal
	// and the operator supplied no curated override. It invents no specifics —
	// it just opens the door for a friend to engage.
	askGeneric = "Ask me one thing I'm proud of from this week."
)

// DraftAsks maps the week's real signals to concrete friend-asks, bounded to
// three. Each ask is grounded in a signal already present on the report
// (misses, a spent error budget, thin logging), so nothing is invented. When
// there is genuinely no signal — a clean week with nothing to flag — it yields
// exactly one honest generic ask rather than fabricating a specific one. The
// curated-asks override that lets the operator's own asks win is layered on in
// the compose phase; this is the always-populated deterministic floor.
func DraftAsks(r Report) []string {
	var asks []string

	if r.WeekMisses >= weekMissWatchOut {
		asks = append(asks, askMidweekCheckIn)
	}
	if r.ErrorBudget.Exceeded {
		asks = append(asks, askChainHeld)
	}
	if r.LowSignal {
		asks = append(asks, askNudgeToLog)
	}

	if len(asks) == 0 {
		return []string{askGeneric}
	}
	if len(asks) > maxAsks {
		asks = asks[:maxAsks]
	}
	return asks
}
