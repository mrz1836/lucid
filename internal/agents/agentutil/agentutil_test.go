package agentutil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

type reply struct {
	Done bool   `json:"done"`
	Text string `json:"text"`
}

func TestCompleteJSON_DecodesTrimmedReply(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Content: "  \n{\"done\":true,\"text\":\"hi\"}\n "},
	}}
	got, err := CompleteJSON[reply](context.Background(), p, provider.Request{Intent: "t"})
	require.NoError(t, err)
	assert.Equal(t, reply{Done: true, Text: "hi"}, got)
	require.Len(t, p.Requests, 1)
	assert.Equal(t, "t", p.Requests[0].Intent)
}

func TestCompleteJSON_TransportErrorIsVerbatim(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Err: provider.ErrUnavailable},
	}}
	got, err := CompleteJSON[reply](context.Background(), p, provider.Request{})
	require.Error(t, err)
	require.ErrorIs(t, err, provider.ErrUnavailable, "transport sentinel must stay comparable")
	assert.Equal(t, reply{}, got, "failure returns the zero value")
}

func TestCompleteJSON_MalformedReplyIsParseError(t *testing.T) {
	p := &provider.Fake{Script: []provider.Exchange{
		{Content: "{not json"},
	}}
	got, err := CompleteJSON[reply](context.Background(), p, provider.Request{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrParse, "a decode failure is classified as a parse error")
	require.NotErrorIs(t, err, provider.ErrUnavailable)
	assert.Equal(t, reply{}, got)
}

func TestCompleteJSON_PartialParseStillReturnsZero(t *testing.T) {
	// A reply that starts valid then breaks must not leak a half-populated
	// value: callers rely on the zero value on error.
	p := &provider.Fake{Script: []provider.Exchange{
		{Content: `{"done":true,"text":`},
	}}
	got, err := CompleteJSON[reply](context.Background(), p, provider.Request{})
	require.Error(t, err)
	assert.Equal(t, reply{}, got)
}

func TestCompleteJSON_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &provider.Fake{Script: []provider.Exchange{{Content: "{}"}}}
	_, err := CompleteJSON[reply](ctx, p, provider.Request{})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}
