package upgrade

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hm builds a UTC instant at the given hour:minute for the drain-window tests.
func hm(h, m int) time.Time { return time.Date(2026, 7, 6, h, m, 0, 0, time.UTC) }

// ── DrainWindow arithmetic ───────────────────────────────────────────────────

// TestDrainWindow_WrapsMidnight covers the ordinary night chain: bell 21:30,
// close-out by the 04:00 rollover — the window spans midnight.
func TestDrainWindow_WrapsMidnight(t *testing.T) {
	w, err := NewDrainWindow("21:30", "04:00")
	require.NoError(t, err)

	// Inside the drain window (never upgrade here).
	for _, tt := range []struct{ h, m int }{{21, 30}, {22, 0}, {23, 59}, {0, 0}, {2, 0}, {4, 0}} {
		assert.Truef(t, w.Contains(MinuteOfDay(hm(tt.h, tt.m))), "%02d:%02d should be inside the drain window", tt.h, tt.m)
	}
	// Outside — safe to upgrade.
	for _, tt := range []struct{ h, m int }{{21, 29}, {4, 1}, {9, 0}, {12, 0}, {17, 0}} {
		assert.Falsef(t, w.Contains(MinuteOfDay(hm(tt.h, tt.m))), "%02d:%02d should be outside the drain window", tt.h, tt.m)
	}
}

// TestDrainWindow_NoWrap covers a same-day window (bell before close-out).
func TestDrainWindow_NoWrap(t *testing.T) {
	w := DrainWindow{BellMin: 10 * 60, CloseoutMin: 15 * 60} // 10:00 → 15:00
	assert.True(t, w.Contains(MinuteOfDay(hm(10, 0))))
	assert.True(t, w.Contains(MinuteOfDay(hm(12, 30))))
	assert.True(t, w.Contains(MinuteOfDay(hm(15, 0))))
	assert.False(t, w.Contains(MinuteOfDay(hm(9, 59))))
	assert.False(t, w.Contains(MinuteOfDay(hm(15, 1))))
	assert.False(t, w.Contains(MinuteOfDay(hm(23, 0))))
}

// TestDrainWindow_ContainsNormalizes proves Contains folds an out-of-range
// minute back into a single day before testing.
func TestDrainWindow_ContainsNormalizes(t *testing.T) {
	w := DrainWindow{BellMin: 21 * 60, CloseoutMin: 4 * 60}
	assert.True(t, w.Contains(22*60+minutesPerDay))  // wraps to 22:00, inside
	assert.False(t, w.Contains(12*60-minutesPerDay)) // wraps to 12:00, outside
}

// TestNewDrainWindow_ParseErrors covers every malformed clock mark.
func TestNewDrainWindow_ParseErrors(t *testing.T) {
	cases := []struct{ bell, closeout string }{
		{"", "04:00"},
		{"21:30", "4"},
		{"25:00", "04:00"},
		{"21:60", "04:00"},
		{"21:30", "aa:bb"},
		{"xx:30", "04:00"},
	}
	for _, c := range cases {
		_, err := NewDrainWindow(c.bell, c.closeout)
		assert.Errorf(t, err, "bell=%q closeout=%q should fail", c.bell, c.closeout)
	}
}

// TestNewDrainWindow_OK parses valid marks into minutes.
func TestNewDrainWindow_OK(t *testing.T) {
	w, err := NewDrainWindow("21:30", "04:00")
	require.NoError(t, err)
	assert.Equal(t, 21*60+30, w.BellMin)
	assert.Equal(t, 4*60, w.CloseoutMin)
}

// ── RunManaged orchestration ─────────────────────────────────────────────────

// wrapWindow is the standard bell→close-out drain window for the flow tests.
func wrapWindow(t *testing.T) DrainWindow {
	t.Helper()
	w, err := NewDrainWindow("21:30", "04:00")
	require.NoError(t, err)
	return w
}

// TestRunManaged_DeferredInsideWindow: an upgrade requested at 22:00 is
// refused — the install step is never called, and the outcome is a clean
// deferral (no error).
func TestRunManaged_DeferredInsideWindow(t *testing.T) {
	upgraded, checked := false, false
	var buf bytes.Buffer
	outcome, err := RunManaged(context.Background(), ManagedConfig{
		Now:         hm(22, 0),
		Window:      wrapWindow(t),
		Upgrade:     func(context.Context) error { upgraded = true; return nil },
		HealthCheck: func() error { checked = true; return nil },
		Stdout:      &buf,
	})
	require.NoError(t, err)
	assert.Equal(t, OutcomeDeferred, outcome)
	assert.False(t, upgraded, "no install inside the drain window")
	assert.False(t, checked, "no health check when deferred")
	assert.Contains(t, buf.String(), "deferred")
}

// TestRunManaged_UpgradesOutsideWindow: at noon the install runs and the
// post-upgrade health check runs after it.
func TestRunManaged_UpgradesOutsideWindow(t *testing.T) {
	var order []string
	var buf bytes.Buffer
	outcome, err := RunManaged(context.Background(), ManagedConfig{
		Now:         hm(12, 0),
		Window:      wrapWindow(t),
		Upgrade:     func(context.Context) error { order = append(order, "upgrade"); return nil },
		HealthCheck: func() error { order = append(order, "health"); return nil },
		Stdout:      &buf,
	})
	require.NoError(t, err)
	assert.Equal(t, OutcomeUpgraded, outcome)
	assert.Equal(t, []string{"upgrade", "health"}, order, "health check runs after the upgrade")
	assert.Contains(t, buf.String(), "self-check passed")
}

// TestRunManaged_UpgradeFailsSkipsHealthCheck: a failed install surfaces the
// error and never runs the health check.
func TestRunManaged_UpgradeFailsSkipsHealthCheck(t *testing.T) {
	boom := errors.New("download failed")
	checked := false
	outcome, err := RunManaged(context.Background(), ManagedConfig{
		Now:         hm(12, 0),
		Window:      wrapWindow(t),
		Upgrade:     func(context.Context) error { return boom },
		HealthCheck: func() error { checked = true; return nil },
	})
	require.ErrorIs(t, err, boom)
	assert.Equal(t, OutcomeUpgradeFailed, outcome)
	assert.False(t, checked, "health check does not run when the upgrade failed")
}

// TestRunManaged_HealthCheckFails: a failed post-upgrade tripwire self-check is
// wrapped as ErrHealthCheckFailed so a supervisor can roll back — an upgrade
// that would cost a night is a failed upgrade (P10).
func TestRunManaged_HealthCheckFails(t *testing.T) {
	var buf bytes.Buffer
	outcome, err := RunManaged(context.Background(), ManagedConfig{
		Now:         hm(12, 0),
		Window:      wrapWindow(t),
		Upgrade:     func(context.Context) error { return nil },
		HealthCheck: func() error { return errors.New("engine tree unreadable") },
		Stdout:      &buf,
	})
	require.ErrorIs(t, err, ErrHealthCheckFailed)
	assert.Equal(t, OutcomeHealthCheckFailed, outcome)
	assert.Contains(t, err.Error(), "engine tree unreadable")
	assert.Contains(t, buf.String(), "roll back")
}

// TestRunManaged_NilHealthCheck: an upgrade with no health gate still succeeds
// (the caller accepted no gate).
func TestRunManaged_NilHealthCheck(t *testing.T) {
	outcome, err := RunManaged(context.Background(), ManagedConfig{
		Now:     hm(12, 0),
		Window:  wrapWindow(t),
		Upgrade: func(context.Context) error { return nil },
	})
	require.NoError(t, err)
	assert.Equal(t, OutcomeUpgraded, outcome)
}

// TestRunManaged_NilUpgradeRejected: the flow refuses to run with no install
// step wired.
func TestRunManaged_NilUpgradeRejected(t *testing.T) {
	_, err := RunManaged(context.Background(), ManagedConfig{Now: hm(12, 0), Window: wrapWindow(t)})
	require.ErrorIs(t, err, ErrManagedConfig)
}
