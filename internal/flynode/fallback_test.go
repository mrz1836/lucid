package flynode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonLogger returns a debug-level JSON slog logger writing into a fresh buffer,
// so a test can decode exactly what [ReportFallback] logged.
func jsonLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// logLines decodes every JSON log line in buf.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &m))
		lines = append(lines, m)
	}
	return lines
}

// pingRecorder is an in-memory Alert/Claim pair: it records every ping text and
// answers claims with a scripted verdict, counting the claims made.
type pingRecorder struct {
	pings    []string
	claims   int
	claimed  bool
	claimErr error
}

func (p *pingRecorder) alert(text string) { p.pings = append(p.pings, text) }

func (p *pingRecorder) claim(FallbackReport) (bool, error) {
	p.claims++
	return p.claimed, p.claimErr
}

// outageReport is a synthetic delivered-outage report wired to rec.
func outageReport(rec *pingRecorder) FallbackReport {
	return FallbackReport{
		Daemon: "companion-morning",
		Reason: "provider_outage",
		Error:  "synthetic outage: no model reachable",
		Day:    "2026-01-05",
		Ping:   "Lucid morning companion went out as the generic fallback.",
		Claim:  rec.claim,
		Alert:  rec.alert,
	}
}

// TestReportFallback_LogsWarnLine: every reported fallback emits one WARN
// "compose fell back" line carrying the daemon, reason, and provider error.
func TestReportFallback_LogsWarnLine(t *testing.T) {
	logger, buf := jsonLogger()
	rec := &pingRecorder{claimed: true}

	ReportFallback(context.Background(), logger, outageReport(rec))

	lines := logLines(t, buf)
	require.Len(t, lines, 1)
	assert.Equal(t, "WARN", lines[0]["level"])
	assert.Equal(t, "compose fell back", lines[0]["msg"])
	assert.Equal(t, "companion-morning", lines[0]["daemon"])
	assert.Equal(t, "provider_outage", lines[0]["reason"])
	assert.Equal(t, "synthetic outage: no model reachable", lines[0]["error"])
	assert.Equal(t, []string{"Lucid morning companion went out as the generic fallback."}, rec.pings)
}

// TestReportFallback_ReturnsMarker: the job-run output is the compose_fallback
// marker, and it serializes with the documented keys.
func TestReportFallback_ReturnsMarker(t *testing.T) {
	logger, _ := jsonLogger()
	res := ReportFallback(context.Background(), logger, outageReport(&pingRecorder{claimed: true}))

	want := FallbackMarker{ComposeFallback: true, Reason: "provider_outage", Error: "synthetic outage: no model reachable"}
	require.Equal(t, want, res.Output)

	b, err := json.Marshal(res.Output)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"compose_fallback":true`)
	assert.Contains(t, string(b), `"reason":"provider_outage"`)
	assert.Contains(t, string(b), `"error":"synthetic outage: no model reachable"`)
}

// TestReportFallback_NoPingWhenPingEmpty: a fallback with no ping text (a witness
// safety trip, which already alerts) is logged and marked, but never claims or
// pings. A missing Alert hook behaves the same.
func TestReportFallback_NoPingWhenPingEmpty(t *testing.T) {
	logger, buf := jsonLogger()
	rec := &pingRecorder{claimed: true}
	r := outageReport(rec)
	r.Reason, r.Error, r.Ping = "safety_tripped", "", ""

	res := ReportFallback(context.Background(), logger, r)

	assert.Empty(t, rec.pings, "an empty ping is never sent")
	assert.Zero(t, rec.claims, "an empty ping never consumes the day's claim")
	assert.Equal(t, FallbackMarker{ComposeFallback: true, Reason: "safety_tripped"}, res.Output)
	require.Len(t, logLines(t, buf), 1)

	noAlert := outageReport(rec)
	noAlert.Alert = nil
	ReportFallback(context.Background(), logger, noAlert)
	assert.Zero(t, rec.claims, "with no Alert hook there is nothing to claim")
}

// TestReportFallback_SuppressedPingStillLogsAndMarks: when another fallback
// already pinged today, this one is not pinged — but it is still logged (warn,
// plus an info line naming the suppression) and still marked.
func TestReportFallback_SuppressedPingStillLogsAndMarks(t *testing.T) {
	logger, buf := jsonLogger()
	rec := &pingRecorder{claimed: false}

	res := ReportFallback(context.Background(), logger, outageReport(rec))

	assert.Empty(t, rec.pings, "a suppressed claim does not ping")
	assert.Equal(t, 1, rec.claims)
	assert.Equal(t, FallbackMarker{ComposeFallback: true, Reason: "provider_outage", Error: "synthetic outage: no model reachable"}, res.Output)
	lines := logLines(t, buf)
	require.Len(t, lines, 2)
	assert.Equal(t, "compose fell back", lines[0]["msg"])
	assert.Equal(t, "INFO", lines[1]["level"])
	assert.Equal(t, "compose fallback ping suppressed", lines[1]["msg"])
	assert.Equal(t, "companion-morning", lines[1]["daemon"])
}

// TestReportFallback_ClaimErrorFailsOpen: a dedup-state failure is logged at
// warn and the ping still goes out — whatever verdict accompanied the error.
func TestReportFallback_ClaimErrorFailsOpen(t *testing.T) {
	for _, verdict := range []bool{true, false} {
		logger, buf := jsonLogger()
		rec := &pingRecorder{claimed: verdict, claimErr: errors.New("synthetic receipt failure")}

		res := ReportFallback(context.Background(), logger, outageReport(rec))

		require.Len(t, rec.pings, 1, "a dedup-state error fails open (verdict %v)", verdict)
		assert.Equal(t, FallbackMarker{ComposeFallback: true, Reason: "provider_outage", Error: "synthetic outage: no model reachable"}, res.Output)
		lines := logLines(t, buf)
		require.Len(t, lines, 2)
		assert.Equal(t, "WARN", lines[1]["level"])
		assert.Equal(t, "compose fallback ping state failed", lines[1]["msg"])
		assert.Equal(t, "synthetic receipt failure", lines[1]["error"])
	}
}

// TestReportFallback_NilClaimAlwaysPings: with no dedup hook the ping is sent.
func TestReportFallback_NilClaimAlwaysPings(t *testing.T) {
	logger, _ := jsonLogger()
	rec := &pingRecorder{}
	r := outageReport(rec)
	r.Claim = nil

	ReportFallback(context.Background(), logger, r)
	assert.Len(t, rec.pings, 1)
}

// TestReportFallback_NilLoggerUsesDefault: a nil logger (a worker run with no job
// or no job logger) falls back to slog.Default() rather than panicking.
func TestReportFallback_NilLoggerUsesDefault(t *testing.T) {
	logger, buf := jsonLogger()
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	res := ReportFallback(context.Background(), nil, outageReport(&pingRecorder{claimed: true}))

	assert.Equal(t, "provider_outage", res.Output.(FallbackMarker).Reason)
	lines := logLines(t, buf)
	require.NotEmpty(t, lines)
	assert.Equal(t, "compose fell back", lines[0]["msg"])
}
