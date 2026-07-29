package flynode

import (
	"context"
	"time"
)

// Skip reasons a [Fire] can report. They match the string values each daemon
// previously defined locally, so a daemon that maps Fired.SkipReason straight
// into its own Outcome keeps its observable skip values unchanged.
const (
	SkipPastCutoff       = "past-cutoff"
	SkipAlreadyDelivered = "already-delivered"
)

// Receipt is what a [Delivery.Guard] found for the bucket a fire belongs to: a
// prior delivery's message id and the channel it landed in, with Found true
// only when a matching receipt still names a message. [Fire] verifies that
// message is still present before honoring it as an idempotent skip. The
// channel is carried (rather than assumed) because it differs by daemon — the
// message daemons post to a fixed user channel, the witness report to whichever
// channel its mode selected.
type Receipt struct {
	MessageID string
	Channel   string
	Found     bool
}

// Fired records how one [Fire] resolved. It is exactly one of: Delivered (with
// the created MessageID, the Channel it landed in, whether it was Late, and the
// composed Payload), or Skipped (SkipReason [SkipPastCutoff] or
// [SkipAlreadyDelivered]). Each daemon maps it onto its own Outcome, reading the
// daemon-specific fields (Fallback, SafetyTripped, delivered text) off Payload.
type Fired[P any] struct {
	Delivered  bool
	MessageID  string
	Channel    string
	Late       bool
	Skipped    bool
	SkipReason string
	Payload    P
}

// Delivery is the per-daemon policy [Fire] runs. Every axis that differs between
// the companion, the workout slot, and the weekly report is injected here, so
// Fire itself names no notifier, provider, or payload type — the text-vs-embed
// send split dissolves because Send/Verify/Compose/Alert are closures.
//
// Channel is the logical channel a fresh send targets; CutoffAt and LateAt are
// the pre-computed stale/late instants (LateAt is consulted only when LatePrefix
// is set). Guard reads the idempotency receipt; Compose builds the payload
// (loud errors already wrapped); LatePrefix marks a backfilled payload (nil for
// daemons with no late note); Send delivers and returns the created id; Verify
// reads a message back; WriteReceipt persists the delivery; PostSend runs any
// after-send hook (nil for none). Alert is the best-effort loud ping, and the
// four *Alert strings are the messages Fire pings at each failure site.
type Delivery[P any] struct {
	Channel      string
	CutoffAt     time.Time
	LateAt       time.Time
	Guard        func(ctx context.Context, now time.Time) (Receipt, error)
	Compose      func(ctx context.Context, now time.Time) (P, error)
	LatePrefix   func(P) P
	Send         func(ctx context.Context, channel string, payload P) (string, error)
	Verify       func(ctx context.Context, channel, messageID string) error
	WriteReceipt func(now time.Time, channel, messageID string) error
	Alert        func(text string)
	PostSend     func(payload P)
	CutoffAlert  string
	ComposeAlert string
	SendAlert    string
	VerifyAlert  string
}

// Fire runs the send-path reliability sequence every message daemon shares, in
// the one order that keeps "never a silent miss" true:
//
//  1. Past the cut-off, a message now would read as stale: alert and refuse — no
//     send.
//  2. Idempotency: when a prior receipt's message still reads back, skip rather
//     than double-post; a receipt whose message is gone falls through to a fresh
//     delivery so the bucket is never left silently empty.
//  3. Compose. A loud failure alerts and returns — never an empty send.
//  4. Prefix a late note on a backfilled fire (when LatePrefix is set).
//  5. Send, then verify the message is present, then persist the receipt. A
//     failure at send or verify alerts and returns; every one leaves the loud
//     signal in the supervised log.
//  6. Run any post-send hook — only after the receipt is persisted, and never
//     unwinding the successful send.
//
// The order is load-bearing: reordering it opens a silent double-send or a
// silent miss. Every daemon-specific axis is injected through d.
func Fire[P any](ctx context.Context, now time.Time, d Delivery[P]) (Fired[P], error) {
	if !now.Before(d.CutoffAt) {
		d.Alert(d.CutoffAlert)
		return Fired[P]{Skipped: true, SkipReason: SkipPastCutoff}, nil
	}

	rec, err := d.Guard(ctx, now)
	if err != nil {
		return Fired[P]{}, err
	}
	if rec.Found {
		if d.Verify(ctx, rec.Channel, rec.MessageID) == nil {
			return Fired[P]{Skipped: true, SkipReason: SkipAlreadyDelivered, MessageID: rec.MessageID, Channel: rec.Channel}, nil
		}
	}

	late := d.LatePrefix != nil && now.After(d.LateAt)

	payload, err := d.Compose(ctx, now)
	if err != nil {
		d.Alert(d.ComposeAlert)
		return Fired[P]{}, err
	}
	if late {
		payload = d.LatePrefix(payload)
	}

	id, err := d.Send(ctx, d.Channel, payload)
	if err != nil {
		d.Alert(d.SendAlert)
		return Fired[P]{}, err
	}
	if verr := d.Verify(ctx, d.Channel, id); verr != nil {
		d.Alert(d.VerifyAlert)
		return Fired[P]{}, verr
	}
	if werr := d.WriteReceipt(now, d.Channel, id); werr != nil {
		return Fired[P]{}, werr
	}

	if d.PostSend != nil {
		d.PostSend(payload)
	}

	return Fired[P]{Delivered: true, MessageID: id, Channel: d.Channel, Late: late, Payload: payload}, nil
}
