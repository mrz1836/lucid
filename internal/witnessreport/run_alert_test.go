package witnessreport

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/lucidtest"
)

// TestWitnessAlert_SendFailureIsLogged: when the loud alert itself cannot be
// delivered, the failure is logged at warn rather than silently dropped — and the
// fire's own result is exactly what it is when the alert succeeds.
func TestWitnessAlert_SendFailureIsLogged(t *testing.T) {
	type alertRun struct {
		out Outcome
		err error
		del *lucidtest.FakeDeliverer
		log *bytes.Buffer
	}
	fire := func(alertErr error) alertRun {
		var buf bytes.Buffer
		del := &lucidtest.FakeDeliverer{AlertErr: alertErr}
		r, _ := newRunner(t, &runFakeComposer{report: sampleReport()}, del)
		r.log = slog.New(slog.NewJSONHandler(&buf, nil))
		// Monday 09:00 + 49h is past the 48h cut-off: the fire alerts instead.
		out, err := r.Fire(context.Background(), reportNow().Add(49*time.Hour))
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
	assert.Equal(t, engine.ChannelUser, del.Alerts[0].Channel, "alerts go to the user channel")
	assert.Contains(t, logBuf.String(), `"msg":"alert send failed"`)
	assert.Contains(t, logBuf.String(), `"level":"WARN"`)
	assert.Contains(t, logBuf.String(), `"daemon":"witness"`)
	assert.Contains(t, logBuf.String(), "synthetic alert channel down")
	assert.Empty(t, ok.log.String(), "a delivered alert logs nothing")
}

// TestWitnessRunner_LoggerDefaults: with no injected logger the runner logs
// through slog.Default(), so production lines land in the supervised log.
func TestWitnessRunner_LoggerDefaults(t *testing.T) {
	r := &Runner{}
	assert.Same(t, slog.Default(), r.logger())
	l := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	r.log = l
	assert.Same(t, l, r.logger())
}
