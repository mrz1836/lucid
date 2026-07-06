package provider_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// TestFake_Success replays scripted content in order and records every
// request — the happy path a well-behaved model exercises.
func TestFake_Success(t *testing.T) {
	f := &provider.Fake{Script: []provider.Exchange{
		{Content: `{"done": false, "question": "What stuck with you?"}`},
		{Content: `{"done": true}`},
	}}

	first, err := f.Complete(context.Background(), provider.Request{Intent: "intake.decide", System: "sys-1"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"done": false, "question": "What stuck with you?"}`, first.Content)

	second, err := f.Complete(context.Background(), provider.Request{Intent: "intake.decide", System: "sys-2"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"done": true}`, second.Content)

	assert.Equal(t, 2, f.Calls())
	require.Len(t, f.Requests, 2)
	assert.Equal(t, "sys-1", f.Requests[0].System)
	assert.Equal(t, "sys-2", f.Requests[1].System)
}

// TestFake_MalformedContent shows the fake returning content that is not
// valid JSON — the branch a calling agent must detect and retry. The
// provider itself is a dumb pipe: it returns the garbage verbatim.
func TestFake_MalformedContent(t *testing.T) {
	f := &provider.Fake{Script: []provider.Exchange{{Content: "not json at all"}}}

	resp, err := f.Complete(context.Background(), provider.Request{})
	require.NoError(t, err)
	assert.Equal(t, "not json at all", resp.Content)
}

// TestFake_TimeoutAndUnavailable drives the two transport sentinels so a
// caller can branch on the failure class with errors.Is.
func TestFake_TimeoutAndUnavailable(t *testing.T) {
	f := &provider.Fake{Script: []provider.Exchange{
		{Err: provider.ErrTimeout},
		{Err: provider.ErrUnavailable},
	}}

	_, err := f.Complete(context.Background(), provider.Request{})
	require.ErrorIs(t, err, provider.ErrTimeout)

	_, err = f.Complete(context.Background(), provider.Request{})
	require.ErrorIs(t, err, provider.ErrUnavailable)
}

// TestFake_ContextCanceled shows a canceled context short-circuiting
// before the script advances — the deterministic timeout branch.
func TestFake_ContextCanceled(t *testing.T) {
	f := &provider.Fake{Script: []provider.Exchange{{Content: "unused"}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := f.Complete(ctx, provider.Request{})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, f.Calls(), "a canceled call must not consume a scripted reply")
}

// TestFake_ExhaustedDefaultsLoud confirms an under-scripted fake fails with
// a clear error naming the call count, rather than hanging or returning a
// silent empty reply.
func TestFake_ExhaustedDefaultsLoud(t *testing.T) {
	f := &provider.Fake{Script: []provider.Exchange{{Content: "only one"}}}

	_, err := f.Complete(context.Background(), provider.Request{})
	require.NoError(t, err)

	_, err = f.Complete(context.Background(), provider.Request{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exhausted")
}

// TestFake_ExhaustErrOverride lets a test choose the exhaustion error, e.g.
// to model a model that goes unavailable after N calls.
func TestFake_ExhaustErrOverride(t *testing.T) {
	sentinel := errors.New("scripted exhaustion")
	f := &provider.Fake{ExhaustErr: sentinel}

	_, err := f.Complete(context.Background(), provider.Request{})
	require.ErrorIs(t, err, sentinel)
}
