package companion

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/lucidtest"
)

// TestCompanionAlert_SendFailureIsLogged: when the loud alert itself cannot be
// delivered, the failure is logged at warn rather than silently dropped — and the
// fire's own result is exactly what it is when the alert succeeds.
func TestCompanionAlert_SendFailureIsLogged(t *testing.T) {
	type alertRun struct {
		out Outcome
		err error
		del *lucidtest.FakeDeliverer
		log *bytes.Buffer
	}
	fire := func(alertErr error) alertRun {
		var buf bytes.Buffer
		del := &lucidtest.FakeDeliverer{AlertErr: alertErr}
		r, _ := newRunner(t, &fakeComposer{res: Result{Text: "GOOD MORNING"}}, del)
		r.log = slog.New(slog.NewJSONHandler(&buf, nil))
		out, err := r.Fire(context.Background(), ModeMorning, at(2026, 7, 6, 10, 30))
		return alertRun{out: out, err: err, del: del, log: &buf}
	}

	ok := fire(nil)
	failed := fire(errors.New("synthetic alert channel down"))
	out, del, logBuf := failed.out, failed.del, failed.log

	require.NoError(t, failed.err)
	assert.Equal(t, ok.out, out, "a failed alert never changes the fire's result")
	assert.Equal(t, ok.err, failed.err)
	assert.Equal(t, flynode.SkipPastCutoff, out.SkipReason)
	require.Len(t, del.Alerts, 1, "the past-cut-off alert was attempted")
	assert.Contains(t, logBuf.String(), `"msg":"alert send failed"`)
	assert.Contains(t, logBuf.String(), `"level":"WARN"`)
	assert.Contains(t, logBuf.String(), `"daemon":"companion"`)
	assert.Contains(t, logBuf.String(), "synthetic alert channel down")
	assert.Empty(t, ok.log.String(), "a delivered alert logs nothing")
}

// TestCompanionRunner_LoggerDefaults: with no injected logger the runner logs
// through slog.Default(), so production lines land in the supervised log.
func TestCompanionRunner_LoggerDefaults(t *testing.T) {
	r := &Runner{}
	assert.Same(t, slog.Default(), r.logger())
	l := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	r.log = l
	assert.Same(t, l, r.logger())
}
