package provider

import (
	"context"
	"fmt"
)

// Exchange is one scripted [Fake] reply: either a Content string the fake
// returns as the model's output, or an Err it returns instead (to drive
// the timeout / unavailable / malformed branches an agent must handle).
// When both are set, Err wins.
type Exchange struct {
	Content string
	Err     error
}

// Fake is a deterministic, offline [Provider] for tests. It replays a
// fixed Script of exchanges in order — one per Complete call — and records
// every Request it received so a test can assert what slice an agent sent.
// It opens no socket and needs no auth, satisfying ADR-0006's rule that no
// test may require live vendor access. A fake is single-use per call
// sequence and is not safe for concurrent use.
type Fake struct {
	// Script is the ordered list of replies; call N returns Script[N].
	Script []Exchange
	// ExhaustErr is returned once the script is used up. When nil, an
	// over-call is a distinct error naming how many calls were made, so a
	// test that under-scripts fails loudly instead of hanging.
	ExhaustErr error
	// Requests records every Request passed to Complete, in order, for
	// slice assertions (e.g. that Intake sent only the current thread).
	Requests []Request
	idx      int
}

// Complete returns the next scripted exchange and records the request. It
// honors context cancellation first so a test can exercise a canceled /
// timed-out call deterministically. A scripted Err is returned verbatim
// (wrap [ErrTimeout] / [ErrUnavailable] to drive those branches); an
// empty Content with no Err is a valid — if unusual — model reply.
func (f *Fake) Complete(ctx context.Context, req Request) (Response, error) {
	f.Requests = append(f.Requests, req)
	if err := ctx.Err(); err != nil {
		return Response{}, fmt.Errorf("provider fake: context done: %w", err)
	}
	if f.idx >= len(f.Script) {
		if f.ExhaustErr != nil {
			return Response{}, f.ExhaustErr
		}
		return Response{}, fmt.Errorf("provider fake: script exhausted after %d call(s)", f.idx)
	}
	ex := f.Script[f.idx]
	f.idx++
	if ex.Err != nil {
		return Response{}, ex.Err
	}
	return Response{Content: ex.Content}, nil
}

// Calls reports how many times Complete has been invoked. Tests use it to
// assert an agent made exactly the expected number of model calls (e.g.
// that a deterministic no-LLM path made zero).
func (f *Fake) Calls() int { return f.idx }
