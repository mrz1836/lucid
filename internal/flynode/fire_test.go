package flynode

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fireNow is the reference instant every Fire branch test fires at. Cut-off and
// late thresholds are placed before or after it to drive each branch.
func fireNow() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }

// harness scripts every Delivery closure and records, in order, the send-path
// steps Fire ran — so a test asserts both the outcome and that the fixed order
// (and the "never silent" alert floor) held.
type harness struct {
	log      []string
	alerts   []string
	guard    Receipt
	guardER  error
	compER   error
	composed string
	sentBody string // payload Send actually received (post late-prefix)
	sendID   string
	sendER   error
	writeER  error
	verifyER map[string]error // per-message-id verify result
	postSeen string           // payload PostSend observed
	postSet  bool             // whether a PostSend hook is wired
}

func newHarness() *harness {
	return &harness{sendID: "new-id", composed: "body", verifyER: map[string]error{}, postSet: true}
}

// delivery builds a Delivery[string] whose payload is the message body, wiring
// every closure to record into h. cutoff/lateAt are placed relative to
// fireNow() by the caller; a non-late test sets lateAt in the future.
func (h *harness) delivery(cutoff, lateAt time.Time) Delivery[string] {
	d := Delivery[string]{
		Channel:  "user",
		CutoffAt: cutoff,
		LateAt:   lateAt,
		Guard: func(context.Context, time.Time) (Receipt, error) {
			h.log = append(h.log, "guard")
			return h.guard, h.guardER
		},
		Compose: func(context.Context, time.Time) (string, error) {
			h.log = append(h.log, "compose")
			return h.composed, h.compER
		},
		LatePrefix: func(s string) string {
			h.log = append(h.log, "lateprefix")
			return "LATE:" + s
		},
		Send: func(_ context.Context, _, payload string) (string, error) {
			h.log = append(h.log, "send")
			h.sentBody = payload
			return h.sendID, h.sendER
		},
		Verify: func(_ context.Context, _, id string) error {
			h.log = append(h.log, "verify:"+id)
			return h.verifyER[id]
		},
		WriteReceipt: func(time.Time, string, string) error {
			h.log = append(h.log, "write")
			return h.writeER
		},
		Alert: func(text string) {
			h.log = append(h.log, "alert")
			h.alerts = append(h.alerts, text)
		},
		CutoffAlert:  "cutoff-alert",
		ComposeAlert: "compose-alert",
		SendAlert:    "send-alert",
		VerifyAlert:  "verify-alert",
		GuardAlert:   "guard-alert",
		ReceiptAlert: "receipt-alert",
	}
	if h.postSet {
		d.PostSend = func(payload string) {
			h.log = append(h.log, "postsend")
			h.postSeen = payload
		}
	}
	return d
}

// onTime returns a cut-off in the future and a late threshold in the future, so
// a fire at fireNow() proceeds and is on time.
func onTime() (cutoff, lateAt time.Time) {
	return fireNow().Add(time.Hour), fireNow().Add(time.Hour)
}

func TestFire_PastCutoff_AlertsAndSkipsNoSend(t *testing.T) {
	h := newHarness()
	fired, err := Fire(context.Background(), fireNow(), h.delivery(fireNow().Add(-time.Hour), fireNow().Add(time.Hour)))
	require.NoError(t, err)
	assert.True(t, fired.Skipped)
	assert.Equal(t, SkipPastCutoff, fired.SkipReason)
	assert.Equal(t, []string{"alert"}, h.log, "past the cut-off: alert only, never a send")
	assert.Equal(t, []string{"cutoff-alert"}, h.alerts)
}

func TestFire_AlreadyDelivered_SkipsNoSend(t *testing.T) {
	h := newHarness()
	h.guard = Receipt{MessageID: "old-id", Channel: "user", Found: true} // verify of old-id succeeds (no scripted error)
	cutoff, lateAt := onTime()
	fired, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.NoError(t, err)
	assert.True(t, fired.Skipped)
	assert.Equal(t, SkipAlreadyDelivered, fired.SkipReason)
	assert.Equal(t, "old-id", fired.MessageID)
	assert.Equal(t, "user", fired.Channel)
	assert.Equal(t, []string{"guard", "verify:old-id"}, h.log, "a live receipt skips before composing or sending")
}

func TestFire_StaleReceipt_ReDelivers(t *testing.T) {
	h := newHarness()
	h.guard = Receipt{MessageID: "old-id", Channel: "user", Found: true}
	// The receipt's message is PROVABLY gone (a clean 404 → ErrMessageAbsent);
	// only this verdict falls through to a fresh delivery.
	h.verifyER["old-id"] = ErrMessageAbsent
	cutoff, lateAt := onTime()
	fired, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.NoError(t, err)
	assert.True(t, fired.Delivered)
	assert.Equal(t, "new-id", fired.MessageID)
	assert.Equal(t, []string{"guard", "verify:old-id", "compose", "send", "verify:new-id", "write", "postsend"}, h.log,
		"a provably-absent receipt falls through to a fresh delivery")
	assert.Empty(t, h.alerts, "a clean re-delivery never alerts")
}

// TestFire_IndeterminateReceiptProbe_AlertsAndDoesNotReSend is the H1 guard: a
// prior receipt whose read-back is INDETERMINATE (a transport blip, a 5xx/429 —
// not a clean 404) leaves the message's presence unknown, so Fire must NOT
// re-post (which could double-send). It alerts and returns the retryable error
// without composing or sending; the supervised retry re-probes.
func TestFire_IndeterminateReceiptProbe_AlertsAndDoesNotReSend(t *testing.T) {
	h := newHarness()
	h.guard = Receipt{MessageID: "old-id", Channel: "user", Found: true}
	h.verifyER["old-id"] = errors.New("discord 503") // indeterminate, not ErrMessageAbsent
	cutoff, lateAt := onTime()
	_, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.Error(t, err, "an indeterminate probe is a retryable error, not a skip")
	assert.NotContains(t, h.log, "send", "presence is unknown, so it must not re-post")
	assert.NotContains(t, h.log, "compose", "and must not even compose a fresh message")
	assert.Equal(t, []string{"guard", "verify:old-id", "alert"}, h.log)
	assert.Equal(t, []string{"guard-alert"}, h.alerts)
}

// TestFire_IndeterminateWrapsErrMessageAbsent proves the classification is by
// errors.Is, not identity: an ErrMessageAbsent wrapped in daemon context (as the
// real Verify closures wrap it) still falls through to a fresh delivery.
func TestFire_WrappedErrMessageAbsent_ReDelivers(t *testing.T) {
	h := newHarness()
	h.guard = Receipt{MessageID: "old-id", Channel: "user", Found: true}
	h.verifyER["old-id"] = fmt.Errorf("companion: verify morning delivery: %w", ErrMessageAbsent)
	cutoff, lateAt := onTime()
	fired, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.NoError(t, err)
	assert.True(t, fired.Delivered)
	assert.Empty(t, h.alerts, "a wrapped provable-absence is a clean re-delivery")
}

func TestFire_ComposeError_AlertsAndErrorsNoSend(t *testing.T) {
	h := newHarness()
	h.compER = errors.New("no prompt")
	cutoff, lateAt := onTime()
	_, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.Error(t, err)
	assert.Equal(t, []string{"guard", "compose", "alert"}, h.log, "a compose failure alerts and never sends")
	assert.Equal(t, []string{"compose-alert"}, h.alerts)
}

func TestFire_Late_AppliesPrefixToPayload(t *testing.T) {
	h := newHarness()
	fired, err := Fire(context.Background(), fireNow(), h.delivery(fireNow().Add(time.Hour), fireNow().Add(-time.Hour)))
	require.NoError(t, err)
	assert.True(t, fired.Late)
	assert.Equal(t, "LATE:body", h.sentBody, "the late prefix is applied to the payload before Send")
	assert.Equal(t, "LATE:body", fired.Payload)
	assert.Equal(t, []string{"guard", "compose", "lateprefix", "send", "verify:new-id", "write", "postsend"}, h.log)
}

func TestFire_OnTime_NoLatePrefix(t *testing.T) {
	h := newHarness()
	cutoff, lateAt := onTime()
	fired, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.NoError(t, err)
	assert.False(t, fired.Late)
	assert.Equal(t, "body", h.sentBody, "an on-time fire sends the composed payload unprefixed")
	assert.NotContains(t, h.log, "lateprefix")
}

func TestFire_SendError_AlertsAndErrors(t *testing.T) {
	h := newHarness()
	h.sendER = errors.New("discord down")
	cutoff, lateAt := onTime()
	_, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.Error(t, err)
	assert.Equal(t, []string{"guard", "compose", "send", "alert"}, h.log, "a send failure alerts and does not verify or write")
	assert.Equal(t, []string{"send-alert"}, h.alerts)
}

func TestFire_VerifyError_AlertsAndDoesNotSaveReceipt(t *testing.T) {
	h := newHarness()
	h.verifyER["new-id"] = errors.New("not present")
	cutoff, lateAt := onTime()
	_, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.Error(t, err)
	assert.NotContains(t, h.log, "write", "a verify failure must not persist a receipt")
	assert.NotContains(t, h.log, "postsend", "and must not run the post-send hook")
	assert.Equal(t, []string{"guard", "compose", "send", "verify:new-id", "alert"}, h.log)
	assert.Equal(t, []string{"verify-alert"}, h.alerts)
}

// TestFire_WriteReceiptError_AlertsAndErrorsWithoutPostSend is the H3 guard: a
// verified send whose receipt cannot be persisted is the one failure that leaves
// the idempotency guard blind, so Fire both returns the error AND fires the loud
// alert (a retry could otherwise re-post invisibly). The post-send hook is still
// skipped — it runs only after the receipt is saved.
func TestFire_WriteReceiptError_AlertsAndErrorsWithoutPostSend(t *testing.T) {
	h := newHarness()
	h.writeER = errors.New("disk full")
	cutoff, lateAt := onTime()
	_, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.Error(t, err)
	assert.NotContains(t, h.log, "postsend", "the post-send hook runs only after the receipt is saved")
	assert.Equal(t, []string{"receipt-alert"}, h.alerts, "a write-receipt failure now alerts loudly (H3)")
	assert.Equal(t, []string{"guard", "compose", "send", "verify:new-id", "write", "alert"}, h.log)
}

// TestFire_CanceledContext_Guard: a context canceled before Fire runs surfaces
// through the first ctx-aware closure (the guard here) as a loud error and stops
// the send path — no compose, no send. It is the Fire-level cancellation guard
// the send daemons rely on for a clean shutdown mid-run (error-states Sc-6).
func TestFire_CanceledContext_Guard(t *testing.T) {
	h := newHarness()
	h.guardER = context.Canceled // the guard observes the canceled ctx
	cutoff, lateAt := onTime()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Fire(ctx, fireNow(), h.delivery(cutoff, lateAt))
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"guard"}, h.log, "a canceled ctx stops the path at the guard — no compose, no send")
	assert.Empty(t, h.alerts, "the canceled ctx is itself the loud signal; no alert")
}

func TestFire_HappyPath_OrdersDeliversAndRunsPostSend(t *testing.T) {
	h := newHarness()
	cutoff, lateAt := onTime()
	fired, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.NoError(t, err)
	assert.True(t, fired.Delivered)
	assert.Equal(t, "new-id", fired.MessageID)
	assert.Equal(t, "user", fired.Channel)
	assert.Equal(t, "body", fired.Payload)
	assert.Equal(t, "body", h.postSeen, "PostSend sees the delivered payload")
	assert.Equal(t, []string{"guard", "compose", "send", "verify:new-id", "write", "postsend"}, h.log,
		"the fixed order: guard → compose → send → verify → write receipt → post-send")
	assert.Empty(t, h.alerts, "a clean delivery never alerts")
}

func TestFire_NoPostSendHook_IsSkipped(t *testing.T) {
	h := newHarness()
	h.postSet = false // companion/workout wire no post-send hook
	cutoff, lateAt := onTime()
	fired, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.NoError(t, err)
	assert.True(t, fired.Delivered)
	assert.Equal(t, []string{"guard", "compose", "send", "verify:new-id", "write"}, h.log, "a nil PostSend is simply skipped")
}

func TestFire_GuardError_ReturnsWithoutSendOrAlert(t *testing.T) {
	h := newHarness()
	h.guardER = errors.New("read receipt failed")
	cutoff, lateAt := onTime()
	_, err := Fire(context.Background(), fireNow(), h.delivery(cutoff, lateAt))
	require.Error(t, err)
	assert.Equal(t, []string{"guard"}, h.log, "a receipt-read failure returns loudly before composing")
	assert.Empty(t, h.alerts, "the read error is itself the loud signal; no alert")
}
