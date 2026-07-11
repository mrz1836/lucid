package notify

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// capturedRequest records what the fake Discord endpoint received so tests can
// assert routing, auth, and body exactly.
type capturedRequest struct {
	path   string
	auth   string
	ctype  string
	body   string
	method string
}

// newStubServer returns an httptest server that records the one request it
// receives and replies with the given status/body, plus a Discord notifier
// whose base points at it.
func newStubServer(t *testing.T, status int, reply string) (*Discord, *capturedRequest) {
	t.Helper()
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		got.ctype = r.Header.Get("Content-Type")
		got.body = string(body)
		got.method = r.Method
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)

	d := New("tok-abc", "U123", "W456", srv.Client())
	d.base = srv.URL
	return d, got
}

func TestSend_UserChannelRoutesAuthsAndBody(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, "")

	err := d.Send(engine.ChannelUser, "the bell rings")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/channels/U123/messages", got.path)
	assert.Equal(t, "Bot tok-abc", got.auth)
	assert.Equal(t, "application/json", got.ctype)
	assert.JSONEq(t, `{"content":"the bell rings"}`, got.body)
}

func TestSend_WitnessChannelRoutesToWitnessID(t *testing.T) {
	d, got := newStubServer(t, http.StatusNoContent, "")

	err := d.Send(engine.ChannelWitness, "streak intact")
	require.NoError(t, err)

	assert.Equal(t, "/channels/W456/messages", got.path)
	assert.JSONEq(t, `{"content":"streak intact"}`, got.body)
}

func TestSend_NonSuccessStatusSurfacesError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		reply  string
	}{
		{"client error", http.StatusForbidden, `{"message":"Missing Access"}`},
		{"server error", http.StatusInternalServerError, "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := newStubServer(t, tc.status, tc.reply)

			err := d.Send(engine.ChannelUser, "hi")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "status")
			// The short body snippet is echoed into the error for triage.
			assert.Contains(t, err.Error(), tc.reply[:4])
		})
	}
}

func TestSend_UnknownLogicalChannelErrorsBeforeAnySend(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, "")

	err := d.Send("nope", "hi")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown logical channel")
	// Never touched the network — no mis-send.
	assert.Empty(t, got.path)
}

func TestSend_UnsetWitnessChannelErrorsNeverMisSends(t *testing.T) {
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
	}))
	t.Cleanup(srv.Close)

	d := New("tok", "U123", "", srv.Client()) // witness ID intentionally empty
	d.base = srv.URL

	err := d.Send(engine.ChannelWitness, "should not send")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "witness channel id is not configured")
	assert.Empty(t, got.path)
}

func TestSend_UnsetUserChannelErrors(t *testing.T) {
	d := New("tok", "", "W456", nil)
	err := d.Send(engine.ChannelUser, "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user channel id is not configured")
}

// stubDoer returns a fixed error so the transport-failure branch is exercised
// without a live socket.
type stubDoer struct{ err error }

func (s stubDoer) Do(_ *http.Request) (*http.Response, error) { return nil, s.err }

func TestSend_TransportErrorSurfaces(t *testing.T) {
	d := New("tok", "U123", "W456", stubDoer{err: errors.New("dial refused")})

	err := d.Send(engine.ChannelUser, "hi")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial refused")
}

func TestSend_MalformedBaseFailsToBuildRequest(t *testing.T) {
	d := New("tok", "U123", "W456", stubDoer{})
	d.base = "://not-a-url"

	err := d.Send(engine.ChannelUser, "hi")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build request")
}

func TestNewDefaultsDoerToBoundedClient(t *testing.T) {
	d := New("tok", "U", "W", nil)
	client, ok := d.do.(*http.Client)
	require.True(t, ok, "nil doer should default to *http.Client")
	assert.Equal(t, httpTimeout, client.Timeout)
	assert.Equal(t, defaultBaseURL, d.base)
}

func TestNewDiscordFromEnv(t *testing.T) {
	t.Run("success reads all three vars", func(t *testing.T) {
		t.Setenv(envHarnessToken, "bot-secret")
		t.Setenv(envUserChannel, "U-1")
		t.Setenv(envWitnessChannel, "W-1")

		d, err := NewDiscordFromEnv()
		require.NoError(t, err)
		assert.Equal(t, "bot-secret", d.token)
		assert.Equal(t, "U-1", d.userChannelID)
		assert.Equal(t, "W-1", d.witnessChannelID)
	})

	t.Run("witness may be empty at construction", func(t *testing.T) {
		t.Setenv(envHarnessToken, "bot-secret")
		t.Setenv(envUserChannel, "U-1")
		t.Setenv(envWitnessChannel, "")

		d, err := NewDiscordFromEnv()
		require.NoError(t, err)
		assert.Empty(t, d.witnessChannelID)
	})

	t.Run("missing token errors", func(t *testing.T) {
		t.Setenv(envHarnessToken, "")
		t.Setenv(envUserChannel, "U-1")

		_, err := NewDiscordFromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), envHarnessToken)
	})

	t.Run("missing user channel errors", func(t *testing.T) {
		t.Setenv(envHarnessToken, "bot-secret")
		t.Setenv(envUserChannel, "")

		_, err := NewDiscordFromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), envUserChannel)
	})
}
