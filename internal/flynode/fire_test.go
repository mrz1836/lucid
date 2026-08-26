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

// staleReceipt scripts the guard to find a prior delivery, so the read-back
// branch (present / provably-absent / indeterminate) is what a case then drives.
func staleReceipt(h *harness) { h.guard = Receipt{MessageID: "old-id", Channel: "user", Found: true} }

// fullDelivery is the ordered log of a clean compose-and-deliver with a
// post-send hook. Several branches converge on it after their own preamble.
var fullDelivery = []string{"guard", "compose", "send", "verify:new-id", "write", "postsend"} //nolint:gochecknoglobals // shared expected-order fixture

// fireCase drives one Fire branch: it scripts the harness, runs Fire with the
// given cut-off/late offsets from fireNow (zero ⇒ the on-time default of +1h),
// and asserts the ordered send-path log, the loud alerts, and any branch-specific
// outcome. The dozen-plus near-identical branch funcs this replaced were each
// "setup → assert log → assert alerts"; the table keeps every branch a row and
// the shared assertions in one place — and is the natural home for a new branch.
type fireCase struct {
	name        string
	setup       func(*harness)
	cutoffOff   time.Duration // added to fireNow() for CutoffAt (0 ⇒ +1h, not stale)
	lateOff     time.Duration // added to fireNow() for LateAt   (0 ⇒ +1h, on time)
	canceledCtx bool          // run under an already-canceled context
	wantErr     bool
	wantLog     []string
	wantAlerts  []string // nil ⇒ assert no alert fired
	check       func(t *testing.T, fired Fired[string], h *harness, err error)
}

func TestFire(t *testing.T) {
	t.Parallel()
	cases := []fireCase{
		{
			name:       "past the cut-off: alert only, never a send",
			cutoffOff:  -time.Hour,
			wantLog:    []string{"alert"},
			wantAlerts: []string{"cutoff-alert"},
			check: func(t *testing.T, f Fired[string], _ *harness, _ error) {
				assert.True(t, f.Skipped)
				assert.Equal(t, SkipPastCutoff, f.SkipReason)
			},
		},
		{
			name:    "a live receipt skips before composing or sending",
			setup:   staleReceipt, // verify of old-id succeeds (no scripted error)
			wantLog: []string{"guard", "verify:old-id"},
			check: func(t *testing.T, f Fired[string], _ *harness, _ error) {
				assert.True(t, f.Skipped)
				assert.Equal(t, SkipAlreadyDelivered, f.SkipReason)
				assert.Equal(t, "old-id", f.MessageID)
				assert.Equal(t, "user", f.Channel)
			},
		},
		{
			name: "a provably-absent receipt (404) falls through to a fresh delivery",
			setup: func(h *harness) {
				staleReceipt(h)
				h.verifyER["old-id"] = ErrMessageAbsent
			},
			wantLog: []string{"guard", "verify:old-id", "compose", "send", "verify:new-id", "write", "postsend"},
			check: func(t *testing.T, f Fired[string], _ *harness, _ error) {
				assert.True(t, f.Delivered)
				assert.Equal(t, "new-id", f.MessageID)
			},
		},
		{
			name: "a wrapped provable-absence still re-delivers (errors.Is, not identity)",
			setup: func(h *harness) {
				staleReceipt(h)
				h.verifyER["old-id"] = fmt.Errorf("companion: verify morning delivery: %w", ErrMessageAbsent)
			},
			wantLog: []string{"guard", "verify:old-id", "compose", "send", "verify:new-id", "write", "postsend"},
			check:   func(t *testing.T, f Fired[string], _ *harness, _ error) { assert.True(t, f.Delivered) },
		},
		{
			name: "an indeterminate probe alerts and does not re-post",
			setup: func(h *harness) {
				staleReceipt(h)
				h.verifyER["old-id"] = errors.New("discord 503") // indeterminate, not ErrMessageAbsent
			},
			wantErr:    true,
			wantLog:    []string{"guard", "verify:old-id", "alert"},
			wantAlerts: []string{"guard-alert"},
		},
		{
			name:       "a compose failure alerts and never sends",
			setup:      func(h *harness) { h.compER = errors.New("no prompt") },
			wantErr:    true,
			wantLog:    []string{"guard", "compose", "alert"},
			wantAlerts: []string{"compose-alert"},
		},
		{
			name:    "a late fire applies the prefix to the payload before Send",
			lateOff: -time.Hour,
			wantLog: []string{"guard", "compose", "lateprefix", "send", "verify:new-id", "write", "postsend"},
			check: func(t *testing.T, f Fired[string], h *harness, _ error) {
				assert.True(t, f.Late)
				assert.Equal(t, "LATE:body", h.sentBody, "the late prefix is applied before Send")
				assert.Equal(t, "LATE:body", f.Payload)
			},
		},
		{
			name:    "an on-time fire sends the composed payload unprefixed",
			wantLog: fullDelivery,
			check: func(t *testing.T, f Fired[string], h *harness, _ error) {
				assert.False(t, f.Late)
				assert.Equal(t, "body", h.sentBody)
				assert.NotContains(t, h.log, "lateprefix")
			},
		},
		{
			name:       "a send failure alerts and does not verify or write",
			setup:      func(h *harness) { h.sendER = errors.New("discord down") },
			wantErr:    true,
			wantLog:    []string{"guard", "compose", "send", "alert"},
			wantAlerts: []string{"send-alert"},
		},
		{
			name:       "a post-send verify failure alerts and saves no receipt",
			setup:      func(h *harness) { h.verifyER["new-id"] = errors.New("not present") },
			wantErr:    true,
			wantLog:    []string{"guard", "compose", "send", "verify:new-id", "alert"},
			wantAlerts: []string{"verify-alert"},
		},
		{
			// H3: a verified send whose receipt cannot be persisted is the one
			// failure that leaves the idempotency guard blind, so it BOTH returns
			// the error AND alerts loudly. The post-send hook is still skipped.
			name:       "a receipt-write failure alerts loudly and skips post-send (H3)",
			setup:      func(h *harness) { h.writeER = errors.New("disk full") },
			wantErr:    true,
			wantLog:    []string{"guard", "compose", "send", "verify:new-id", "write", "alert"},
			wantAlerts: []string{"receipt-alert"},
		},
		{
			// Sc-6: a stop signal cancels the run ctx; the guard observes it and
			// the path stops there — no compose, no send, and the canceled ctx is
			// itself the loud signal, so no alert.
			name:        "a canceled context stops the path at the guard, no alert",
			setup:       func(h *harness) { h.guardER = context.Canceled },
			canceledCtx: true,
			wantErr:     true,
			wantLog:     []string{"guard"},
			check:       func(t *testing.T, _ Fired[string], _ *harness, err error) { require.ErrorIs(t, err, context.Canceled) },
		},
		{
			name:    "the happy path delivers in the fixed order and runs post-send",
			wantLog: fullDelivery,
			check: func(t *testing.T, f Fired[string], h *harness, _ error) {
				assert.True(t, f.Delivered)
				assert.Equal(t, "new-id", f.MessageID)
				assert.Equal(t, "user", f.Channel)
				assert.Equal(t, "body", f.Payload)
				assert.Equal(t, "body", h.postSeen, "PostSend sees the delivered payload")
			},
		},
		{
			name:    "a nil PostSend hook is simply skipped",
			setup:   func(h *harness) { h.postSet = false },
			wantLog: []string{"guard", "compose", "send", "verify:new-id", "write"},
			check:   func(t *testing.T, f Fired[string], _ *harness, _ error) { assert.True(t, f.Delivered) },
		},
		{
			name:    "a receipt-read failure returns loudly before composing, no alert",
			setup:   func(h *harness) { h.guardER = errors.New("read receipt failed") },
			wantErr: true,
			wantLog: []string{"guard"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// run scripts a harness for the case, fires with the case's timing, and asserts
// the ordered log, the alerts, and any branch-specific outcome. Extracted from
// the loop so each row stays a plain data literal and TestFire stays flat.
func (tc fireCase) run(t *testing.T) {
	t.Parallel()
	h := newHarness()
	if tc.setup != nil {
		tc.setup(h)
	}
	cutoffOff := cmpOr(tc.cutoffOff, time.Hour)
	lateOff := cmpOr(tc.lateOff, time.Hour)

	ctx := context.Background()
	if tc.canceledCtx {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}

	fired, err := Fire(ctx, fireNow(), h.delivery(fireNow().Add(cutoffOff), fireNow().Add(lateOff)))

	if tc.wantErr {
		require.Error(t, err)
	} else {
		require.NoError(t, err)
	}
	assert.Equal(t, tc.wantLog, h.log, "the send-path steps ran in the expected order")
	assert.Equal(t, tc.wantAlerts, alertsOrEmpty(h.alerts), "the loud alerts fired as expected")
	if tc.check != nil {
		tc.check(t, fired, h, err)
	}
}

// cmpOr returns v when it is non-zero, else fallback — the on-time timing default
// (a zero offset means "use the +1h not-stale / on-time default").
func cmpOr(v, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}
	return v
}

// alertsOrEmpty normalizes a nil alert slice to nil so a case that expects no
// alert (wantAlerts nil) compares equal to a harness that fired none.
func alertsOrEmpty(alerts []string) []string {
	if len(alerts) == 0 {
		return nil
	}
	return alerts
}
