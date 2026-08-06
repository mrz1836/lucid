package notify

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// TestVerifyPresent_MalformedBaseFailsToBuildRequest surfaces a request-build
// failure (a malformed base URL) as a clear error, never a silent verify — the
// read-back never claims a message is present when it never issued the GET.
func TestVerifyPresent_MalformedBaseFailsToBuildRequest(t *testing.T) {
	d := New("tok", "U123", "W456", stubDoer{})
	d.base = "://not-a-url"

	err := d.VerifyPresent(context.Background(), engine.ChannelUser, "777")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build read-back request")
}

// TestVerifyPresent_MalformedResponseErrors: a 2xx read-back whose body is not
// JSON is a clear parse error, so a delivery is never recorded as verified off
// an unreadable response.
func TestVerifyPresent_MalformedResponseErrors(t *testing.T) {
	d, _ := newStubServer(t, http.StatusOK, "not-json")

	err := d.VerifyPresent(context.Background(), engine.ChannelUser, "777")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse read-back response")
}
