package composekit

import (
	"fmt"
	"strings"
)

// Fallback reasons. Each composer (companion, workout, witness report) records
// why it sent its deterministic fallback instead of a model-composed message, so
// a scheduled worker can log it, mark the job run, and decide whether to ping.
// They are the values the `compose_fallback` job-run marker carries.
const (
	// FallbackProviderOutage: the model backend was unreachable (provider.IsOutage).
	FallbackProviderOutage = "provider_outage"
	// FallbackUnusableReply: the model answered, but with nothing usable.
	FallbackUnusableReply = "unusable_reply"
	// FallbackSafetyTripped: the witness-safe scan rejected the composed report.
	FallbackSafetyTripped = "safety_tripped"
)

// pingTail closes every fallback ping, so the reader knows a quiet channel for
// the rest of the day is the once-a-day limit, not a recovery.
const pingTail = " Later fallbacks today are logged, not pinged."

// FallbackPing returns the plain-prose user-channel notice for a scheduled
// fallback send of the named message kind ("morning companion", "night
// companion", "workout card", "weekly witness report"). An outage names the
// CLI-reported detail when there is one; an unusable reply says so. It returns
// "" for [FallbackSafetyTripped] (that path already alerts) and for any other
// reason, meaning "no ping". The copy never carries an error chain — only the
// kind and the bare reason the CLI itself reported.
func FallbackPing(kind, reason, detail string) string {
	lead := fmt.Sprintf("Lucid %s went out as the generic fallback — ", kind)
	switch reason {
	case FallbackProviderOutage:
		msg := lead + "the model was unreachable"
		if d := strings.TrimSpace(detail); d != "" {
			msg += ` ("` + d + `")`
		}
		return msg + "." + pingTail
	case FallbackUnusableReply:
		return lead + "the model's reply was unusable." + pingTail
	default:
		return ""
	}
}
