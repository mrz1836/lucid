package witnessreport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	flywheel "github.com/mrz1836/go-flywheel"
	"github.com/mrz1836/go-foundation/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/composekit"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/flynode"
	"github.com/mrz1836/lucid/internal/lucidtest"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/provider/claudecli"
	"github.com/mrz1836/lucid/internal/storage"
)

// notLoggedIn is the reason the claude CLI reports when it cannot find its
// keychain credential — the real failure these tests replay.
const notLoggedIn = "Not logged in · Please run /login"

// cliNotLoggedIn is a claudecli process stub replaying the observed failure
// shape: the is_error envelope on stdout and a non-zero exit.
func cliNotLoggedIn(context.Context, string, []string, []byte) ([]byte, error) {
	return []byte(`{"is_error":true,"result":"` + notLoggedIn + `","subtype":"success"}`), errors.New("exit status 1")
}

// cliOutageProvider is a real claude CLI backend over the not-logged-in stub.
func cliOutageProvider() provider.Provider {
	return claudecli.New("test", claudecli.WithRunner(cliNotLoggedIn))
}

// newComposeRunner wires a preview-mode Runner on the default Monday-09:00 mark
// over a REAL Composer (the synthetic miss-heavy fixture week and prompt files)
// whose provider builder returns p, so a worker test drives the whole compose →
// fallback → deliver → report path rather than a scripted Report.
func newComposeRunner(t *testing.T, store *storage.Adapter, p provider.Provider, del Deliverer) *Runner {
	t.Helper()
	dir := t.TempDir()
	numbers, records := fixtureReaders(t, "miss-heavy-week.json")
	comp := New(Deps{
		SystemPrompt: writeFile(t, dir, "system.md", "You are the witness-report voice."),
		Template:     writeFile(t, dir, "template.md", "Weekly witness report template."),
		Provider:     config.ProviderConfig{Backend: "fake"},
		Numbers:      numbers,
		Records:      records,
		Build:        func(config.ProviderConfig) (provider.Provider, error) { return p, nil },
	})
	return &Runner{
		compose: comp, deliver: del, store: store,
		mode: config.WitnessReportModePreview, weekday: 1, markHM: "09:00",
	}
}

// jobLoggingTo returns a job whose attempt logger writes JSON lines to buf, the
// way flywheel hands each attempt its own logger.
func jobLoggingTo(buf *bytes.Buffer) *flywheel.Job[weeklyArgs] {
	return &flywheel.Job[weeklyArgs]{Logger: slog.New(slog.NewJSONHandler(buf, nil))}
}

// logLine returns the single JSON log line whose msg is msg, failing the test
// when there is not exactly one.
func logLine(t *testing.T, buf *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	var found []map[string]any
	sc := bufio.NewScanner(strings.NewReader(buf.String()))
	for sc.Scan() {
		var line map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &line))
		if line["msg"] == msg {
			found = append(found, line)
		}
	}
	require.Lenf(t, found, 1, "exactly one %q line in %s", msg, buf.String())
	return found[0]
}

// fallbackMarker asserts res carries the compose_fallback job-run marker and
// returns it.
func fallbackMarker(t *testing.T, res flywheel.Result) flynode.FallbackMarker {
	t.Helper()
	m, ok := res.Output.(flynode.FallbackMarker)
	require.Truef(t, ok, "Output is a FallbackMarker, got %T", res.Output)
	assert.True(t, m.ComposeFallback)
	raw, err := json.Marshal(res.Output)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"compose_fallback":true`)
	return m
}

// workAt runs the weekly worker once at now with a job logging to buf.
func workAt(t *testing.T, r *Runner, now time.Time, buf *bytes.Buffer) flywheel.Result {
	t.Helper()
	res, err := weeklyWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(buf))
	require.NoError(t, err, "a fallback send is a delivered send, not a job failure")
	return res
}

// TestWeeklyWorker_CLIOutageFallbackLogsMarksAndPings replays the real
// regression end to end: the claude CLI exits 1 with "Not logged in" on stdout,
// the metrics-only report still goes out, and the worker makes it loud — a warn
// line naming the CLI's reason, a compose_fallback marker, and one user-channel
// ping that names the report and the reason but never the Go error chain.
func TestWeeklyWorker_CLIOutageFallbackLogsMarksAndPings(t *testing.T) {
	store := runStore(t)
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, store, cliOutageProvider(), del)
	var buf bytes.Buffer

	res := workAt(t, r, reportNow(), &buf)

	require.Len(t, del.Embeds, 1, "the metrics-only report still goes out")
	rec, ok, rerr := store.ReadWitnessReportReceipt()
	require.NoError(t, rerr)
	require.True(t, ok, "the receipt is persisted before the report runs")
	assert.Equal(t, del.Verifies[0], rec.MessageID)

	line := logLine(t, &buf, "compose fell back")
	assert.Equal(t, "WARN", line["level"])
	assert.Equal(t, "witness", line["daemon"])
	assert.Equal(t, composekit.FallbackProviderOutage, line["reason"])
	assert.Contains(t, line["error"], notLoggedIn, "the CLI's own words reach the log")

	m := fallbackMarker(t, res)
	assert.Equal(t, composekit.FallbackProviderOutage, m.Reason)
	assert.Contains(t, m.Error, notLoggedIn)

	require.Len(t, del.Alerts, 1, "exactly one ping")
	ping := del.Alerts[0]
	assert.Equal(t, engine.ChannelUser, ping.Channel, "the ping goes to the user channel")
	assert.Contains(t, ping.Text, "weekly witness report")
	assert.Contains(t, ping.Text, notLoggedIn, "the ping names the CLI-reported reason")
	assert.NotContains(t, ping.Text, "claudecli:", "never the Go error chain")
	assert.NotContains(t, ping.Text, "provider:")

	claim, ok, cerr := store.ReadComposeFallbackPing()
	require.NoError(t, cerr)
	require.True(t, ok, "the ping claimed the day")
	assert.Equal(t, "2026-07-13", claim.Date)
	assert.Equal(t, "witness", claim.Daemon)
}

// TestWeeklyWorker_UnusableReplyFallbackLogsMarksAndPings: a model that answers
// with nothing usable is a fallback too — logged and marked unusable_reply with
// no provider error, and pinged as such.
func TestWeeklyWorker_UnusableReplyFallbackLogsMarksAndPings(t *testing.T) {
	del := &lucidtest.FakeDeliverer{}
	p := &provider.Fake{Script: []provider.Exchange{{Content: "   \n  \n"}}}
	r := newComposeRunner(t, runStore(t), p, del)
	var buf bytes.Buffer

	res := workAt(t, r, reportNow(), &buf)

	require.Len(t, del.Embeds, 1, "the deterministic report still goes out")

	line := logLine(t, &buf, "compose fell back")
	assert.Equal(t, "WARN", line["level"])
	assert.Equal(t, "witness", line["daemon"])
	assert.Equal(t, composekit.FallbackUnusableReply, line["reason"])
	assert.Empty(t, line["error"], "an unusable reply carries no provider error")

	m := fallbackMarker(t, res)
	assert.Equal(t, composekit.FallbackUnusableReply, m.Reason)
	assert.Empty(t, m.Error)

	require.Len(t, del.Alerts, 1)
	assert.Equal(t, engine.ChannelUser, del.Alerts[0].Channel)
	assert.Contains(t, del.Alerts[0].Text, "weekly witness report")
	assert.Contains(t, del.Alerts[0].Text, "unusable")
}

// TestWeeklyWorker_SafetyTripLogsMarksAndAlertsOnce: a reply carrying a
// private-detail marker trips the witness-safe scan. The metrics-only report
// still delivers; the fallback is logged and marked safety_tripped; and the
// existing safety-trip review alert stays the ONLY alert — no second ping, and
// the once-a-day ping is not spent.
func TestWeeklyWorker_SafetyTripLogsMarksAndAlertsOnce(t *testing.T) {
	// A synthetic wikilink — a journal-citation shape the scan always rejects.
	unsafeReply := `%%FAULTS%%
Two days slipped midweek.

%%NARRATIVE%%
This ties back to [[synthetic-note]].

%%ASKS%%
- Check in on me Wednesday.`
	store := runStore(t)
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, store, &provider.Fake{Script: []provider.Exchange{{Content: unsafeReply}}}, del)
	var buf bytes.Buffer

	res := workAt(t, r, reportNow(), &buf)

	require.Len(t, del.Embeds, 1, "the metrics-only report still goes out")

	line := logLine(t, &buf, "compose fell back")
	assert.Equal(t, "WARN", line["level"])
	assert.Equal(t, "witness", line["daemon"])
	assert.Equal(t, composekit.FallbackSafetyTripped, line["reason"])
	assert.Empty(t, line["error"])

	assert.Equal(t, composekit.FallbackSafetyTripped, fallbackMarker(t, res).Reason)

	require.Len(t, del.Alerts, 1, "exactly one alert: the existing safety-trip review alert")
	assert.Contains(t, del.Alerts[0].Text, "tripped the witness-safe scan")
	assert.Equal(t, engine.ChannelUser, del.Alerts[0].Channel)

	_, ok, err := store.ReadComposeFallbackPing()
	require.NoError(t, err)
	assert.False(t, ok, "a safety trip never claims the day's ping")
}

// TestWeeklyWorker_FallbackPingSuppressedAfterAnotherDaemonPinged: the
// once-a-day ping is shared across every daemon — when another daemon already
// pinged today, a witness fallback the same local day is logged and marked but
// not pinged.
func TestWeeklyWorker_FallbackPingSuppressedAfterAnotherDaemonPinged(t *testing.T) {
	store := runStore(t)
	require.NoError(t, store.WriteComposeFallbackPing(storage.ComposeFallbackPing{
		Date:   engine.DateString(engine.DateOf(reportNow())),
		Daemon: "companion-morning",
		Reason: composekit.FallbackProviderOutage,
	}))
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, store, cliOutageProvider(), del)
	var buf bytes.Buffer

	res := workAt(t, r, reportNow(), &buf)

	require.Len(t, del.Embeds, 1, "the report still goes out")
	assert.Empty(t, del.Alerts, "another daemon already pinged today")
	assert.Equal(t, "witness", logLine(t, &buf, "compose fell back")["daemon"], "still logged")
	assert.Equal(t, composekit.FallbackProviderOutage, fallbackMarker(t, res).Reason, "still marked")
	assert.Equal(t, "witness", logLine(t, &buf, "compose fallback ping suppressed")["daemon"])
}

// TestWeeklyWorker_ModelSendCarriesNoMarker: a model-composed report is not a
// fallback — no marker on the job run, no warn line, no ping.
func TestWeeklyWorker_ModelSendCarriesNoMarker(t *testing.T) {
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, runStore(t), &provider.Fake{Script: []provider.Exchange{{Content: wellFormedReply}}}, del)
	var buf bytes.Buffer

	res := workAt(t, r, reportNow(), &buf)

	require.Len(t, del.Embeds, 1)
	assert.Nil(t, res.Output, "a model-composed send carries no compose_fallback marker")
	assert.NotContains(t, buf.String(), "compose fell back")
	assert.Empty(t, del.Alerts)
}

// TestWeeklyWorker_IdempotentSkipCarriesNoMarker: a retry that finds the week
// already delivered is a skip, not a send — even when the original was a
// fallback it is not reported a second time.
func TestWeeklyWorker_IdempotentSkipCarriesNoMarker(t *testing.T) {
	del := &lucidtest.FakeDeliverer{}
	outage := &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("synthetic backend down: %w", provider.ErrUnavailable)}}}
	r := newComposeRunner(t, runStore(t), outage, del)
	workAt(t, r, reportNow(), &bytes.Buffer{})

	var buf bytes.Buffer
	res := workAt(t, r, reportNow().Add(2*time.Minute), &buf)
	assert.Nil(t, res.Output, "an idempotent skip carries no marker")
	assert.NotContains(t, buf.String(), "compose fell back")
	assert.Len(t, del.Embeds, 1, "no second send")
	assert.Len(t, del.Alerts, 1, "only the first fallback pinged")
}

// TestWeeklyWorker_AutoModePingGoesToUserChannel: in auto mode the report posts
// to the friend-facing witness channel, but the fallback ping still goes to the
// operator's user channel — never the witness channel.
func TestWeeklyWorker_AutoModePingGoesToUserChannel(t *testing.T) {
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, runStore(t), cliOutageProvider(), del)
	r.mode = config.WitnessReportModeAuto

	workAt(t, r, reportNow(), &bytes.Buffer{})

	require.Len(t, del.Embeds, 1)
	assert.Equal(t, engine.ChannelWitness, del.Embeds[0].Channel, "auto mode posts the report to the witness channel")
	require.Len(t, del.Alerts, 1)
	assert.Equal(t, engine.ChannelUser, del.Alerts[0].Channel, "the ping never reaches the witness channel")
}

// TestWeeklyWorker_FallbackWithoutJobLoggerUsesDefault: a nil job (or one with
// no logger) never panics — the report falls back to slog.Default() and still
// returns the marker.
func TestWeeklyWorker_FallbackWithoutJobLoggerUsesDefault(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, runStore(t), cliOutageProvider(), del)
	now := reportNow()

	res, err := weeklyWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), nil)
	require.NoError(t, err)
	assert.Equal(t, composekit.FallbackProviderOutage, fallbackMarker(t, res).Reason)
	assert.Equal(t, "witness", logLine(t, &buf, "compose fell back")["daemon"])

	assert.Nil(t, loggerOf(nil))
	assert.Nil(t, loggerOf(&flywheel.Job[weeklyArgs]{}))
	l := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	assert.Same(t, l, loggerOf(&flywheel.Job[weeklyArgs]{Logger: l}))
}
