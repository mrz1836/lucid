package composekit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFallbackPing_Copy pins the user-channel notice a scheduled fallback send
// pings: an outage names the CLI-reported detail when there is one, an unusable
// reply says so, both close with the once-a-day note, a safety trip (or any
// unknown reason) yields no ping, and the copy never leaks an error chain.
func TestFallbackPing_Copy(t *testing.T) {
	const tail = " Later fallbacks today are logged, not pinged."
	cases := []struct {
		name, kind, reason, detail, want string
	}{
		{
			name: "outage with detail", kind: "morning companion",
			reason: FallbackProviderOutage, detail: "Not logged in · Please run /login",
			want: `Lucid morning companion went out as the generic fallback — the model was unreachable ("Not logged in · Please run /login").` + tail,
		},
		{
			name: "outage without detail", kind: "workout card",
			reason: FallbackProviderOutage, detail: "  ",
			want: "Lucid workout card went out as the generic fallback — the model was unreachable." + tail,
		},
		{
			name: "unusable reply", kind: "night companion",
			reason: FallbackUnusableReply,
			want:   "Lucid night companion went out as the generic fallback — the model's reply was unusable." + tail,
		},
		{name: "safety trip is not pinged", kind: "weekly witness report", reason: FallbackSafetyTripped, want: ""},
		{name: "unknown reason is not pinged", kind: "workout card", reason: "something_else", want: ""},
		{name: "empty reason is not pinged", kind: "workout card", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FallbackPing(tc.kind, tc.reason, tc.detail)
			assert.Equal(t, tc.want, got)
			if got == "" {
				return
			}
			assert.Contains(t, got, tc.kind, "the ping names the message kind")
			assert.NotContains(t, got, "claudecli:", "the ping never carries the Go error chain")
			assert.NotContains(t, got, "provider:", "the ping never carries the Go error chain")
		})
	}
}

// TestFallbackPing_ReasonValues pins the marker vocabulary the job-run output and
// the log line carry, so a rename is a deliberate, visible change.
func TestFallbackPing_ReasonValues(t *testing.T) {
	assert.Equal(t, "provider_outage", FallbackProviderOutage)
	assert.Equal(t, "unusable_reply", FallbackUnusableReply)
	assert.Equal(t, "safety_tripped", FallbackSafetyTripped)
}
