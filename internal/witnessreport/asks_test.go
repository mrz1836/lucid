package witnessreport

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestDraftAsks_SignalMapping is the table test that pins each concrete signal
// to its concrete ask, the empty-signal generic fallback, and the three-ask
// bound. It builds Report values directly so the mapping is exercised without
// any projection or model in the path.
func TestDraftAsks_SignalMapping(t *testing.T) {
	overBudget := engine.ErrorBudget{Budget: 4, Burn: 6, Remaining: 0, Exceeded: true}

	cases := []struct {
		name string
		in   Report
		want []string
	}{
		{
			name: "no signal yields one honest generic ask",
			in:   Report{},
			want: []string{askGeneric},
		},
		{
			name: "week misses ask for a midweek check-in",
			in:   Report{WeekMisses: 2},
			want: []string{askMidweekCheckIn},
		},
		{
			name: "one miss is below the threshold and stays generic",
			in:   Report{WeekMisses: 1},
			want: []string{askGeneric},
		},
		{
			name: "a spent error budget asks how the chain held",
			in:   Report{ErrorBudget: overBudget},
			want: []string{askChainHeld},
		},
		{
			name: "thin logging asks for a daily nudge",
			in:   Report{LowSignal: true},
			want: []string{askNudgeToLog},
		},
		{
			name: "every signal drafts all three asks in signal order",
			in:   Report{WeekMisses: 4, ErrorBudget: overBudget, LowSignal: true},
			want: []string{askMidweekCheckIn, askChainHeld, askNudgeToLog},
		},
		{
			name: "misses plus thin logging drafts the matching pair",
			in:   Report{WeekMisses: 3, LowSignal: true},
			want: []string{askMidweekCheckIn, askNudgeToLog},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DraftAsks(tc.in)
			assert.Equal(t, tc.want, got)
			assert.LessOrEqual(t, len(got), maxAsks, "asks are bounded to three")
			assert.NotEmpty(t, got, "asks are never empty — a generic ask always lands")
		})
	}
}
