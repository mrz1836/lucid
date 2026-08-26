package lucidtest

import (
	"context"
	"fmt"

	"github.com/mrz1836/lucid/internal/notify"
)

// SendRecord is one delivered message or loud alert a [FakeDeliverer] captured:
// the channel it went to and its text.
type SendRecord struct{ Channel, Text string }

// EmbedRecord is one delivered rich embed a [FakeDeliverer] captured: the channel
// it went to and the embed value. It is the embed analog of [SendRecord], for the
// witness-report path, which delivers an embed rather than text.
type EmbedRecord struct {
	Channel string
	Embed   notify.Embed
}

// FakeDeliverer is the shared delivery double the companion, workout, and weekly
// witness-report daemons drive their node tests with. Their Deliverer interfaces
// differ only in the send verb — the message daemons send text
// (SendReturningID), the witness report an embed (SendEmbedReturningID) — and
// this one value implements both plus the shared VerifyPresent / Send, so Go's
// structural typing lets it satisfy all three. It captures every text delivery
// (Sends), every embed delivery (Embeds), every loud alert (Alerts), and every
// read-back id (Verifies), and lets a test steer the failure branches: SendErr
// fails delivery, VerifyErr fails every read-back, and VerifyErrFor fails one
// specific message id (the "receipt's message is gone" case). It needs no token
// or socket.
type FakeDeliverer struct {
	Sends        []SendRecord
	Embeds       []EmbedRecord
	Alerts       []SendRecord
	Verifies     []string
	SendErr      error
	VerifyErr    error
	VerifyErrFor map[string]error
	idSeq        int
}

// SendReturningID records a real text delivery and returns a fresh monotonic id
// ("msg-1", "msg-2", …), or SendErr when one is set.
func (f *FakeDeliverer) SendReturningID(_ context.Context, channel, text string) (string, error) {
	f.Sends = append(f.Sends, SendRecord{channel, text})
	if f.SendErr != nil {
		return "", f.SendErr
	}
	f.idSeq++
	return fmt.Sprintf("msg-%d", f.idSeq), nil
}

// SendEmbedReturningID records a real embed delivery and returns a fresh
// monotonic id, or SendErr when one is set — the embed analog of
// [FakeDeliverer.SendReturningID], for the witness-report path.
func (f *FakeDeliverer) SendEmbedReturningID(_ context.Context, channel string, e notify.Embed) (string, error) {
	f.Embeds = append(f.Embeds, EmbedRecord{Channel: channel, Embed: e})
	if f.SendErr != nil {
		return "", f.SendErr
	}
	f.idSeq++
	return fmt.Sprintf("msg-%d", f.idSeq), nil
}

// VerifyPresent records the read-back probe and fails it per VerifyErrFor (for a
// specific id) or VerifyErr (for every id).
func (f *FakeDeliverer) VerifyPresent(_ context.Context, _, messageID string) error {
	f.Verifies = append(f.Verifies, messageID)
	if f.VerifyErrFor != nil {
		if e, ok := f.VerifyErrFor[messageID]; ok {
			return e
		}
	}
	return f.VerifyErr
}

// Send records a loud alert. It never fails: a failed alert would mask the
// original error the caller is already surfacing.
func (f *FakeDeliverer) Send(_ context.Context, channel, text string) error {
	f.Alerts = append(f.Alerts, SendRecord{channel, text})
	return nil
}
