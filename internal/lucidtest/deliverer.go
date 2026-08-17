package lucidtest

import (
	"context"
	"fmt"
)

// SendRecord is one delivered message or loud alert a [FakeDeliverer] captured:
// the channel it went to and its text.
type SendRecord struct{ Channel, Text string }

// FakeDeliverer is the shared text-message delivery double the companion and the
// workout daemons drive their node tests with — their Deliverer interfaces are
// byte-identical (SendReturningID / VerifyPresent / Send), and Go's structural
// typing lets this one value satisfy both. It captures every real delivery
// (Sends), every loud alert (Alerts), and every read-back id (Verifies), and
// lets a test steer the failure branches: SendErr fails delivery, VerifyErr
// fails every read-back, and VerifyErrFor fails one specific message id (the
// "receipt's message is gone" case). It needs no token or socket.
type FakeDeliverer struct {
	Sends        []SendRecord
	Alerts       []SendRecord
	Verifies     []string
	SendErr      error
	VerifyErr    error
	VerifyErrFor map[string]error
	idSeq        int
}

// SendReturningID records a real delivery and returns a fresh monotonic id
// ("msg-1", "msg-2", …), or SendErr when one is set.
func (f *FakeDeliverer) SendReturningID(_ context.Context, channel, text string) (string, error) {
	f.Sends = append(f.Sends, SendRecord{channel, text})
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
