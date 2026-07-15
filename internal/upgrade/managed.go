package upgrade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// minutesPerDay is the wrap modulus for the drain-window arithmetic.
const minutesPerDay = 24 * 60

// DrainWindow is the evening→close-out interval a managed upgrade must never
// run inside (ADR-0007, P10): never between the evening bell and the morning
// close-out — an upgrade that costs a night of the practice is a failed
// upgrade regardless of what shipped. Both marks are minutes since local
// midnight. When BellMin > CloseoutMin — the ordinary night chain, bell 19:00
// with close-out due by the 04:00 rollover — the window wraps midnight.
type DrainWindow struct {
	// BellMin is the evening bell mark (minutes since local midnight).
	BellMin int
	// CloseoutMin is the close-out deadline mark: the logical-day rollover,
	// after which the night can no longer be closed out.
	CloseoutMin int
}

// NewDrainWindow builds a window from "HH:MM" bell and close-out (rollover)
// marks — the two clocks a chain profile already carries.
func NewDrainWindow(bell, closeout string) (DrainWindow, error) {
	b, err := parseHM(bell)
	if err != nil {
		return DrainWindow{}, fmt.Errorf("lucid/upgrade: drain window bell: %w", err)
	}
	c, err := parseHM(closeout)
	if err != nil {
		return DrainWindow{}, fmt.Errorf("lucid/upgrade: drain window close-out: %w", err)
	}
	return DrainWindow{BellMin: b, CloseoutMin: c}, nil
}

// Contains reports whether minute-of-day m lies in the drain window, inclusive
// of both the bell and close-out marks, handling the midnight-wrapping case
// (BellMin > CloseoutMin). m is normalized into [0, minutesPerDay).
func (w DrainWindow) Contains(m int) bool {
	m = ((m % minutesPerDay) + minutesPerDay) % minutesPerDay
	if w.BellMin <= w.CloseoutMin {
		return m >= w.BellMin && m <= w.CloseoutMin
	}
	return m >= w.BellMin || m <= w.CloseoutMin
}

// MinuteOfDay returns t's minutes since local midnight — the value
// [DrainWindow.Contains] tests.
func MinuteOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }

// parseHM parses an "HH:MM" 24-hour clock mark into minutes since midnight.
func parseHM(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("%w: %q", errInvalidClockMark, s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("lucid/upgrade: parse hour %q: %w", s, err)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("lucid/upgrade: parse minute %q: %w", s, err)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("%w: %q", errInvalidClockMark, s)
	}
	return h*60 + m, nil
}

// errInvalidClockMark is returned for a malformed "HH:MM" drain-window mark.
var errInvalidClockMark = errors.New("lucid/upgrade: invalid clock mark")

// ManagedOutcome names the terminal state of a managed upgrade attempt.
type ManagedOutcome string

// The managed-upgrade outcomes.
const (
	// OutcomeDeferred: now is inside the drain window; nothing was attempted.
	OutcomeDeferred ManagedOutcome = "deferred_drain_window"
	// OutcomeUpgraded: the upgrade step ran and the health check (if any)
	// passed — the ordinary success.
	OutcomeUpgraded ManagedOutcome = "upgraded"
	// OutcomeUpgradeFailed: the upgrade step itself errored; the health check
	// did not run.
	OutcomeUpgradeFailed ManagedOutcome = "upgrade_failed"
	// OutcomeHealthCheckFailed: the upgrade ran but the post-upgrade tripwire
	// self-check failed — treat the upgrade as failed (P10) and roll back.
	OutcomeHealthCheckFailed ManagedOutcome = "health_check_failed"
)

// ErrHealthCheckFailed wraps a post-upgrade tripwire self-check failure so a
// supervisor can distinguish "shipped but the scheduler can't fire" from a
// download/install error and trigger a rollback.
var ErrHealthCheckFailed = errors.New("lucid/upgrade: post-upgrade health check failed")

// ErrManagedConfig is returned when [RunManaged] is called without the upgrade
// step wired.
var ErrManagedConfig = errors.New("lucid/upgrade: managed config incomplete")

// ManagedConfig wires the managed-upgrade orchestration (ADR-0007 §"On a
// supervised host"). The two side-effecting steps are injected so the flow is
// testable with no network and no host: Upgrade performs the install (a
// closure over [Install]); HealthCheck is the post-upgrade tripwire self-check
// (a closure over the scheduler's dry-run).
type ManagedConfig struct {
	// Now is the wall-clock instant the drain window is tested against; its
	// location determines minute-of-day.
	Now time.Time
	// Window is the bell→close-out interval upgrades are refused inside.
	Window DrainWindow
	// Upgrade performs the install step. Required.
	Upgrade func(context.Context) error
	// HealthCheck runs after a successful upgrade — the tripwire self-check
	// that proves the scheduler can still fire next morning. Optional; a nil
	// check is treated as "no health gate" (the caller accepted that).
	HealthCheck func() error
	// Stdout receives the one-line outcome narration. Nil → messages are
	// dropped.
	Stdout io.Writer
}

// RunManaged executes the managed-upgrade flow: refuse inside the drain
// window; otherwise upgrade, then run the post-upgrade health check. It
// returns the terminal [ManagedOutcome] and, for a real failure, the
// underlying error. A drain-window deferral is not an error — it is the
// correct, expected skip (exit 0), so the supervisor simply retries after the
// window.
func RunManaged(ctx context.Context, cfg ManagedConfig) (ManagedOutcome, error) {
	if cfg.Upgrade == nil {
		return "", fmt.Errorf("%w: Upgrade step is nil", ErrManagedConfig)
	}

	if cfg.Window.Contains(MinuteOfDay(cfg.Now)) {
		managedf(cfg.Stdout, "lucid: upgrade: deferred — inside the drain window (bell..close-out); will retry after it")
		return OutcomeDeferred, nil
	}

	if err := cfg.Upgrade(ctx); err != nil {
		return OutcomeUpgradeFailed, err
	}

	if cfg.HealthCheck != nil {
		if err := cfg.HealthCheck(); err != nil {
			managedf(cfg.Stdout, "lucid: upgrade: health check FAILED — the scheduler may not fire; roll back")
			return OutcomeHealthCheckFailed, fmt.Errorf("%w: %w", ErrHealthCheckFailed, err)
		}
	}

	managedf(cfg.Stdout, "lucid: upgrade: managed upgrade complete — tripwire self-check passed")
	return OutcomeUpgraded, nil
}

// managedf writes one narration line when a writer is present.
func managedf(w io.Writer, msg string) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintln(w, msg)
}
