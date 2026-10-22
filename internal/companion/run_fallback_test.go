package companion

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
	"github.com/mrz1836/lucid/internal/engine/templates"
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

// outageFake is a scripted provider whose single call is an outage.
func outageFake() *provider.Fake {
	return &provider.Fake{Script: []provider.Exchange{{Err: fmt.Errorf("synthetic backend down: %w", provider.ErrUnavailable)}}}
}

// newComposeRunner wires a Runner over a REAL Composer (the compose_test
// fixtures) whose provider builder returns p, so a worker test drives the whole
// compose → fallback → deliver → report path rather than a scripted Result.
func newComposeRunner(t *testing.T, store *storage.Adapter, p provider.Provider, del Deliverer) *Runner {
	t.Helper()
	comp := New(Deps{
		Companion: writePrompts(t),
		Provider:  defaultProvider(),
		Numbers:   sampleNumbers(),
		Verdict:   fakeVerdict{},
		Chain:     store,
		Build:     func(config.ProviderConfig) (provider.Provider, error) { return p, nil },
	})
	return &Runner{compose: comp, deliver: del, store: store}
}

// jobLoggingTo returns a job whose attempt logger writes JSON lines to buf, the
// way flywheel hands each attempt its own logger.
func jobLoggingTo(buf *bytes.Buffer) *flywheel.Job[companionArgs] {
	return &flywheel.Job[companionArgs]{Logger: slog.New(slog.NewJSONHandler(buf, nil))}
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

// TestMorningWorker_CLIOutageFallbackLogsMarksAndPings replays the real
// regression end to end: the claude CLI exits 1 with "Not logged in" on stdout,
// the morning still goes out as the deterministic fallback, and the worker
// makes it loud — a warn line naming the CLI's reason, a compose_fallback
// marker, and one user-channel ping that names the message and the reason but
// never the Go error chain.
func TestMorningWorker_CLIOutageFallbackLogsMarksAndPings(t *testing.T) {
	store := newStore(t)
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, store, cliOutageProvider(), del)
	now := at(2026, 7, 6, 6, 0)
	var buf bytes.Buffer

	res, err := morningWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(&buf))
	require.NoError(t, err, "a fallback send is a delivered send, not a job failure")

	require.Len(t, del.Sends, 1, "the fallback still goes out")
	assert.Equal(t, engine.ChannelUser, del.Sends[0].Channel)
	assert.Contains(t, del.Sends[0].Text, fallbackInterpMorning)
	rec, ok, rerr := store.ReadCompanionReceipt(string(ModeMorning))
	require.NoError(t, rerr)
	require.True(t, ok, "the receipt is persisted before the report runs")
	assert.Equal(t, del.Verifies[0], rec.MessageID)

	line := logLine(t, &buf, "compose fell back")
	assert.Equal(t, "WARN", line["level"])
	assert.Equal(t, "companion-morning", line["daemon"])
	assert.Equal(t, composekit.FallbackProviderOutage, line["reason"])
	assert.Contains(t, line["error"], notLoggedIn, "the CLI's own words reach the log")
	assert.Contains(t, buf.String(), notLoggedIn)

	m := fallbackMarker(t, res)
	assert.Equal(t, composekit.FallbackProviderOutage, m.Reason)
	assert.Contains(t, m.Error, notLoggedIn)

	require.Len(t, del.Alerts, 1, "exactly one ping")
	ping := del.Alerts[0]
	assert.Equal(t, engine.ChannelUser, ping.Channel, "the ping goes to the user channel")
	assert.Contains(t, ping.Text, "morning companion")
	assert.Contains(t, ping.Text, notLoggedIn, "the ping names the CLI-reported reason")
	assert.NotContains(t, ping.Text, "claudecli:", "never the Go error chain")
	assert.NotContains(t, ping.Text, "provider:")

	claim, ok, cerr := store.ReadComposeFallbackPing()
	require.NoError(t, cerr)
	require.True(t, ok, "the ping claimed the day")
	assert.Equal(t, "2026-07-06", claim.Date)
	assert.Equal(t, "companion-morning", claim.Daemon)
	assert.Equal(t, composekit.FallbackProviderOutage, claim.Reason)
}

// TestNightWorker_UnusableReplyFallbackLogsMarksAndPings: a model that answers
// with nothing usable is a fallback too — logged and marked unusable_reply with
// no provider error, and pinged as such.
func TestNightWorker_UnusableReplyFallbackLogsMarksAndPings(t *testing.T) {
	store := newStore(t)
	del := &lucidtest.FakeDeliverer{}
	p := &provider.Fake{Script: []provider.Exchange{{Content: "  \n\t  "}}}
	r := newComposeRunner(t, store, p, del)
	now := at(2026, 7, 6, 19, 0)
	var buf bytes.Buffer

	res, err := nightWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(&buf))
	require.NoError(t, err)

	chain, cerr := store.ReadChainConfig()
	require.NoError(t, cerr)
	require.Len(t, del.Sends, 1, "the fallback still goes out")
	assert.Contains(t, del.Sends[0].Text, templates.Bell(chain.Label), "the night fallback closes with the Bell")

	line := logLine(t, &buf, "compose fell back")
	assert.Equal(t, "WARN", line["level"])
	assert.Equal(t, "companion-night", line["daemon"])
	assert.Equal(t, composekit.FallbackUnusableReply, line["reason"])
	assert.Empty(t, line["error"], "an unusable reply carries no provider error")

	m := fallbackMarker(t, res)
	assert.Equal(t, composekit.FallbackUnusableReply, m.Reason)
	assert.Empty(t, m.Error)

	require.Len(t, del.Alerts, 1)
	assert.Equal(t, engine.ChannelUser, del.Alerts[0].Channel)
	assert.Contains(t, del.Alerts[0].Text, "night companion")
	assert.Contains(t, del.Alerts[0].Text, "unusable")
}

// TestWorker_ModelSendCarriesNoMarker: a model-composed send is not a fallback
// — no marker on the job run, no warn line, no ping.
func TestWorker_ModelSendCarriesNoMarker(t *testing.T) {
	cases := []struct {
		name string
		mode Mode
		now  time.Time
	}{
		{"morning", ModeMorning, at(2026, 7, 6, 6, 0)},
		{"night", ModeNight, at(2026, 7, 6, 19, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			del := &lucidtest.FakeDeliverer{}
			p := &provider.Fake{Script: []provider.Exchange{{Content: slotReply("Steady.", "Start small.")}}}
			r := newComposeRunner(t, newStore(t), p, del)
			var buf bytes.Buffer

			var w flywheel.Worker[companionArgs] = morningWorker{r: r, clock: models.NewFixedClock(tc.now)}
			if tc.mode == ModeNight {
				w = nightWorker{r: r, clock: models.NewFixedClock(tc.now)}
			}
			res, err := w.Work(ctxAt(tc.now), jobLoggingTo(&buf))
			require.NoError(t, err)

			require.Len(t, del.Sends, 1)
			assert.Nil(t, res.Output, "a model-composed send carries no compose_fallback marker")
			assert.NotContains(t, buf.String(), "compose fell back")
			assert.Empty(t, del.Alerts)
		})
	}
}

// TestWorker_IdempotentSkipCarriesNoMarker: a retry that finds the window
// already delivered is a skip, not a send — even when the original was a
// fallback it is not reported a second time.
func TestWorker_IdempotentSkipCarriesNoMarker(t *testing.T) {
	store := newStore(t)
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, store, outageFake(), del)
	now := at(2026, 7, 6, 6, 0)
	_, err := morningWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(&bytes.Buffer{}))
	require.NoError(t, err)

	retry := now.Add(2 * time.Minute)
	var buf bytes.Buffer
	res, err := morningWorker{r: r, clock: models.NewFixedClock(retry)}.Work(ctxAt(retry), jobLoggingTo(&buf))
	require.NoError(t, err)
	assert.Nil(t, res.Output, "an idempotent skip carries no marker")
	assert.NotContains(t, buf.String(), "compose fell back")
	assert.Len(t, del.Sends, 1, "no second send")
	assert.Len(t, del.Alerts, 1, "only the first fallback pinged")
}

// TestWorker_FallbackPingDedupSurvivesFreshRunner: the once-a-day ping is held
// on disk, so a scheduler restart (a fresh adapter and a fresh runner over the
// same home) does not ping again the same day — the later fallback is still
// logged and marked — and the next local day pings again.
func TestWorker_FallbackPingDedupSurvivesFreshRunner(t *testing.T) {
	home, store := lucidtest.Ledger(t, lucidtest.NestedHome(), lucidtest.WithEngine())
	day := at(2026, 7, 6, 6, 0)

	del1 := &lucidtest.FakeDeliverer{}
	r1 := newComposeRunner(t, store, outageFake(), del1)
	_, err := morningWorker{r: r1, clock: models.NewFixedClock(day)}.Work(ctxAt(day), jobLoggingTo(&bytes.Buffer{}))
	require.NoError(t, err)
	require.Len(t, del1.Alerts, 1, "the first fallback of the day pings")

	// A restart: nothing in memory survives, only the Ledger on disk.
	store2 := storage.New(home)
	del2 := &lucidtest.FakeDeliverer{}
	r2 := newComposeRunner(t, store2, outageFake(), del2)
	night := at(2026, 7, 6, 19, 0)
	var buf bytes.Buffer
	res, err := nightWorker{r: r2, clock: models.NewFixedClock(night)}.Work(ctxAt(night), jobLoggingTo(&buf))
	require.NoError(t, err)

	require.Len(t, del2.Sends, 1, "the night fallback still goes out")
	assert.Empty(t, del2.Alerts, "a second fallback the same local day is not pinged")
	assert.Equal(t, "companion-night", logLine(t, &buf, "compose fell back")["daemon"], "still logged")
	assert.Equal(t, composekit.FallbackProviderOutage, fallbackMarker(t, res).Reason, "still marked")
	assert.Equal(t, "companion-night", logLine(t, &buf, "compose fallback ping suppressed")["daemon"])

	del3 := &lucidtest.FakeDeliverer{}
	r3 := newComposeRunner(t, storage.New(home), outageFake(), del3)
	next := night.AddDate(0, 0, 1)
	_, err = nightWorker{r: r3, clock: models.NewFixedClock(next)}.Work(ctxAt(next), jobLoggingTo(&bytes.Buffer{}))
	require.NoError(t, err)
	require.Len(t, del3.Alerts, 1, "the next local day pings again")
	assert.Contains(t, del3.Alerts[0].Text, "night companion")
}

// TestBackstopWorker_FallbackIsReported: the morning backstop re-runs the same
// card, so a fallback it delivers is reported as the morning companion's.
func TestBackstopWorker_FallbackIsReported(t *testing.T) {
	store := newStore(t)
	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, store, cliOutageProvider(), del)
	now := at(2026, 7, 6, 7, 0) // the primary morning send never went out
	var buf bytes.Buffer

	res, err := morningBackstopWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), jobLoggingTo(&buf))
	require.NoError(t, err)

	require.Len(t, del.Sends, 1)
	assert.True(t, strings.HasPrefix(del.Sends[0].Text, backstopNote), "the backstop's own note leads")
	line := logLine(t, &buf, "compose fell back")
	assert.Equal(t, "companion-morning", line["daemon"])
	assert.Equal(t, composekit.FallbackProviderOutage, line["reason"])
	assert.Equal(t, composekit.FallbackProviderOutage, fallbackMarker(t, res).Reason)
	require.Len(t, del.Alerts, 1)
	assert.Contains(t, del.Alerts[0].Text, "morning companion")
	assert.Contains(t, del.Alerts[0].Text, notLoggedIn)
}

// TestWorker_FallbackWithoutJobLoggerUsesDefault: a nil job (or one with no
// logger) never panics — the report falls back to slog.Default() and still
// returns the marker.
func TestWorker_FallbackWithoutJobLoggerUsesDefault(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	del := &lucidtest.FakeDeliverer{}
	r := newComposeRunner(t, newStore(t), outageFake(), del)
	now := at(2026, 7, 6, 6, 0)

	res, err := morningWorker{r: r, clock: models.NewFixedClock(now)}.Work(ctxAt(now), nil)
	require.NoError(t, err)
	assert.Equal(t, composekit.FallbackProviderOutage, fallbackMarker(t, res).Reason)
	assert.Equal(t, "companion-morning", logLine(t, &buf, "compose fell back")["daemon"])

	assert.Nil(t, loggerOf(nil))
	assert.Nil(t, loggerOf(&flywheel.Job[companionArgs]{}))
	l := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	assert.Same(t, l, loggerOf(&flywheel.Job[companionArgs]{Logger: l}))
}
