package workout

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

// newComposeRunner wires a Runner over a REAL Composer (the compose_test
// fixtures: the synthetic program, prompts, and a streak-3 metrics fold) whose
// provider builder returns p, so a worker test drives the whole compose →
// fallback → deliver → report path rather than a scripted Result.
func newComposeRunner(t *testing.T, store *storage.Adapter, p provider.Provider, del Deliverer) *Runner {
	t.Helper()
	comp := New(Deps{
		Workout:      writeWorkoutConfig(t),
		Metrics:      fakeMetrics{m: engine.Metrics{CurrentStreak: 3}},
		Observations: &fakeObs{},
		Injuries:     fakeInjuries{},
		Build:        func(config.ProviderConfig) (provider.Provider, error) { return p, nil },
	})
	return &Runner{compose: comp, deliver: del, store: store, slot: "12:00"}
}

// jobLoggingTo returns a job whose attempt logger writes JSON lines to buf, the
// way flywheel hands each attempt its own logger.
func jobLoggingTo(buf *bytes.Buffer) *flywheel.Job[workoutArgs] {
	return &flywheel.Job[workoutArgs]{Logger: slog.New(slog.NewJSONHandler(buf, nil))}
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

// TestDailyWorker_CLIOutageFallbackLogsMarksAndPings replays the real
// regression end to end: the claude CLI exits 1 with "Not logged in" on stdout,
// the workout card still goes out as the deterministic render, and the worker
// makes it loud — a warn line naming the CLI's reason, a compose_fallback
// marker, and one user-channel ping that names the message and the reason but
// never the Go error chain.
func TestDailyWorker_CLIOutageFallbackLogsMarksAndPings(t *testing.T) {
	store := newStore(t)
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, store, cliOutageProvider(), del)
	now := mustTime(t, mondayNoon)
	var buf bytes.Buffer

	res, err := dailyWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(&buf))
	require.NoError(t, err, "a fallback send is a delivered send, not a job failure")

	require.Len(t, del.Sends, 1, "the fallback still goes out")
	assert.Equal(t, engine.ChannelUser, del.Sends[0].Channel)
	assert.NotContains(t, del.Sends[0].Text, emojiCoach, "the deterministic card carries no model note")
	rec, ok, rerr := store.ReadCompanionReceipt(receiptWindow)
	require.NoError(t, rerr)
	require.True(t, ok, "the receipt is persisted before the report runs")
	assert.Equal(t, del.Verifies[0], rec.MessageID)

	line := logLine(t, &buf, "compose fell back")
	assert.Equal(t, "WARN", line["level"])
	assert.Equal(t, "workout", line["daemon"])
	assert.Equal(t, composekit.FallbackProviderOutage, line["reason"])
	assert.Contains(t, line["error"], notLoggedIn, "the CLI's own words reach the log")

	m := fallbackMarker(t, res)
	assert.Equal(t, composekit.FallbackProviderOutage, m.Reason)
	assert.Contains(t, m.Error, notLoggedIn)

	require.Len(t, del.Alerts, 1, "exactly one ping")
	ping := del.Alerts[0]
	assert.Equal(t, engine.ChannelUser, ping.Channel, "the ping goes to the user channel")
	assert.Contains(t, ping.Text, "workout card")
	assert.Contains(t, ping.Text, notLoggedIn, "the ping names the CLI-reported reason")
	assert.NotContains(t, ping.Text, "claudecli:", "never the Go error chain")
	assert.NotContains(t, ping.Text, "provider:")

	claim, ok, cerr := store.ReadComposeFallbackPing()
	require.NoError(t, cerr)
	require.True(t, ok, "the ping claimed the day")
	assert.Equal(t, "2026-07-20", claim.Date)
	assert.Equal(t, "workout", claim.Daemon)
	assert.Equal(t, composekit.FallbackProviderOutage, claim.Reason)
}

// TestDailyWorker_UnusableReplyFallbackLogsMarksAndPings: a model that answers
// with nothing usable is a fallback too — logged and marked unusable_reply with
// no provider error, and pinged as such.
func TestDailyWorker_UnusableReplyFallbackLogsMarksAndPings(t *testing.T) {
	store := newStore(t)
	del := &lucidtest.FakeDeliverer{}
	p := &provider.Fake{Script: []provider.Exchange{{Content: "  \n\t  "}}}
	r := newComposeRunner(t, store, p, del)
	now := mustTime(t, mondayNoon)
	var buf bytes.Buffer

	res, err := dailyWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(&buf))
	require.NoError(t, err)

	require.Len(t, del.Sends, 1, "the fallback still goes out")

	line := logLine(t, &buf, "compose fell back")
	assert.Equal(t, "WARN", line["level"])
	assert.Equal(t, "workout", line["daemon"])
	assert.Equal(t, composekit.FallbackUnusableReply, line["reason"])
	assert.Empty(t, line["error"], "an unusable reply carries no provider error")

	m := fallbackMarker(t, res)
	assert.Equal(t, composekit.FallbackUnusableReply, m.Reason)
	assert.Empty(t, m.Error)

	require.Len(t, del.Alerts, 1)
	assert.Equal(t, engine.ChannelUser, del.Alerts[0].Channel)
	assert.Contains(t, del.Alerts[0].Text, "workout card")
	assert.Contains(t, del.Alerts[0].Text, "unusable")
}

// TestDailyWorker_ModelSendCarriesNoMarker: a model-phrased send is not a
// fallback — no marker on the job run, no warn line, no ping.
func TestDailyWorker_ModelSendCarriesNoMarker(t *testing.T) {
	del := &lucidtest.FakeDeliverer{}
	p := &provider.Fake{Script: []provider.Exchange{{Content: "Steady work today — pick what fits."}}}
	r := newComposeRunner(t, newStore(t), p, del)
	now := mustTime(t, mondayNoon)
	var buf bytes.Buffer

	res, err := dailyWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(&buf))
	require.NoError(t, err)

	require.Len(t, del.Sends, 1)
	assert.Contains(t, del.Sends[0].Text, emojiCoach, "the model phrased the note")
	assert.Nil(t, res.Output, "a model-phrased send carries no compose_fallback marker")
	assert.NotContains(t, buf.String(), "compose fell back")
	assert.Empty(t, del.Alerts)
}

// TestDailyWorker_IdempotentSkipCarriesNoMarker: a retry that finds the day
// already delivered is a skip, not a send — even when the original was a
// fallback it is not reported a second time.
func TestDailyWorker_IdempotentSkipCarriesNoMarker(t *testing.T) {
	store := newStore(t)
	del := &lucidtest.FakeDeliverer{}
	outage := &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("synthetic backend down: %w", provider.ErrUnavailable)}}}
	r := newComposeRunner(t, store, outage, del)
	now := mustTime(t, mondayNoon)
	_, err := dailyWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(&bytes.Buffer{}))
	require.NoError(t, err)

	retry := now.Add(2 * time.Minute)
	var buf bytes.Buffer
	res, err := dailyWorker{r: r, clock: models.NewFixedClock(retry)}.Work(ctxAt(retry), jobLoggingTo(&buf))
	require.NoError(t, err)
	assert.Nil(t, res.Output, "an idempotent skip carries no marker")
	assert.NotContains(t, buf.String(), "compose fell back")
	assert.Len(t, del.Sends, 1, "no second send")
	assert.Len(t, del.Alerts, 1, "only the first fallback pinged")
}

// TestDailyWorker_FallbackPingSuppressedAfterAnotherDaemonPinged: the
// once-a-day ping is shared across every daemon — when another daemon already
// pinged today, a workout fallback the same local day is logged and marked but
// not pinged.
func TestDailyWorker_FallbackPingSuppressedAfterAnotherDaemonPinged(t *testing.T) {
	store := newStore(t)
	now := mustTime(t, mondayNoon)
	require.NoError(t, store.WriteComposeFallbackPing(storage.ComposeFallbackPing{
		Date:   engine.DateString(engine.DateOf(now)),
		Daemon: "companion-morning",
		Reason: composekit.FallbackProviderOutage,
	}))
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, store, cliOutageProvider(), del)
	var buf bytes.Buffer

	res, err := dailyWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(&buf))
	require.NoError(t, err)

	require.Len(t, del.Sends, 1, "the fallback still goes out")
	assert.Empty(t, del.Alerts, "another daemon already pinged today")
	assert.Equal(t, "workout", logLine(t, &buf, "compose fell back")["daemon"], "still logged")
	assert.Equal(t, composekit.FallbackProviderOutage, fallbackMarker(t, res).Reason, "still marked")
	assert.Equal(t, "workout", logLine(t, &buf, "compose fallback ping suppressed")["daemon"])

	claim, ok, cerr := store.ReadComposeFallbackPing()
	require.NoError(t, cerr)
	require.True(t, ok)
	assert.Equal(t, "companion-morning", claim.Daemon, "the earlier claim stands")
}

// TestDailyWorker_FallbackWithoutJobLoggerUsesDefault: a nil job (or one with no
// logger) never panics — the report falls back to slog.Default() and still
// returns the marker.
func TestDailyWorker_FallbackWithoutJobLoggerUsesDefault(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, newStore(t), cliOutageProvider(), del)
	now := mustTime(t, mondayNoon)

	res, err := dailyWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), nil)
	require.NoError(t, err)
	assert.Equal(t, composekit.FallbackProviderOutage, fallbackMarker(t, res).Reason)
	assert.Equal(t, "workout", logLine(t, &buf, "compose fell back")["daemon"])

	assert.Nil(t, loggerOf(nil))
	assert.Nil(t, loggerOf(&flywheel.Job[workoutArgs]{}))
	l := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	assert.Same(t, l, loggerOf(&flywheel.Job[workoutArgs]{Logger: l}))
}
