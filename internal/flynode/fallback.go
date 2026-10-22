package flynode

import (
	"context"
	"log/slog"

	flywheel "github.com/mrz1836/go-flywheel"
)

// FallbackMarker is the job-run output a scheduled worker returns when its
// delivered message was the deterministic fallback rather than a model-composed
// one. flywheel stores it on the run's job_runs row, so a fallback send is
// distinguishable by query while the run outcome stays success (nothing
// retries, nothing re-sends). A model-composed send returns no marker.
type FallbackMarker struct {
	ComposeFallback bool   `json:"compose_fallback"`
	Reason          string `json:"reason"`
	Error           string `json:"error"`
}

// FallbackReport describes one delivered scheduled fallback for
// [ReportFallback]. Daemon names the sender (companion-morning, companion-night,
// workout, witness); Reason is the composer's fallback reason and Error the
// provider error string ("" when there is none); Day is the local logical day
// the once-a-day ping dedups on. Ping is the user-channel notice text — "" means
// this fallback is never pinged (a witness safety trip already alerts). Claim
// reserves the day's single ping (nil means always ping); Alert posts the ping.
// Both are injected closures so flynode stays storage-, transport-, and
// model-free.
type FallbackReport struct {
	Daemon string
	Reason string
	Error  string
	Day    string
	Ping   string
	Claim  func(FallbackReport) (bool, error)
	Alert  func(text string)
}

// ReportFallback makes a delivered scheduled fallback loud. A scheduled worker
// calls it only after [Fire] returned delivered — the receipt is already
// persisted — so nothing here can delay, fail, or unwind the send.
//
// It always logs a Warn "compose fell back" line (daemon, reason, error) through
// logger — the attempt's job logger, or slog.Default() when nil — and always
// returns the [FallbackMarker] result. When r.Ping and r.Alert are both set it
// claims the day's single ping first: a claim error is logged and the ping still
// goes out (fail open); a refused claim (another fallback already pinged today)
// is logged at info and not pinged; otherwise the ping is sent.
func ReportFallback(ctx context.Context, logger *slog.Logger, r FallbackReport) flywheel.Result {
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, "compose fell back", "daemon", r.Daemon, "reason", r.Reason, "error", r.Error)

	if r.Ping != "" && r.Alert != nil && claimPing(ctx, logger, r) {
		r.Alert(r.Ping)
	}

	return flywheel.Result{Output: FallbackMarker{ComposeFallback: true, Reason: r.Reason, Error: r.Error}}
}

// claimPing reports whether r's ping should be sent, consulting r.Claim when
// set. It fails open: a claim error is logged and the ping is sent anyway.
func claimPing(ctx context.Context, logger *slog.Logger, r FallbackReport) bool {
	if r.Claim == nil {
		return true
	}
	claimed, err := r.Claim(r)
	if err != nil {
		logger.WarnContext(ctx, "compose fallback ping state failed", "error", err)
		return true
	}
	if !claimed {
		logger.InfoContext(ctx, "compose fallback ping suppressed", "daemon", r.Daemon, "reason", r.Reason)
		return false
	}
	return true
}
