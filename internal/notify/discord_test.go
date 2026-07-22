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

func TestSendReturningID_ParsesCreatedID(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, `{"id":"1526225086254682172","channel_id":"U123"}`)

	id, err := d.SendReturningID(engine.ChannelUser, "test fire")
	require.NoError(t, err)
	assert.Equal(t, "1526225086254682172", id)

	// Routes, auths, and bodies exactly like Send — SendReturningID only adds
	// parsing the id off the response.
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/channels/U123/messages", got.path)
	assert.Equal(t, "Bot tok-abc", got.auth)
	assert.JSONEq(t, `{"content":"test fire"}`, got.body)
}

func TestSendReturningID_EmptyIDErrors(t *testing.T) {
	d, _ := newStubServer(t, http.StatusOK, `{}`)

	_, err := d.SendReturningID(engine.ChannelUser, "hi")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no message id")
}

func TestSendReturningID_MalformedResponseErrors(t *testing.T) {
	d, _ := newStubServer(t, http.StatusOK, "not-json")

	_, err := d.SendReturningID(engine.ChannelUser, "hi")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse create-message response")
}

func TestSendReturningID_PropagatesPostError(t *testing.T) {
	d, _ := newStubServer(t, http.StatusForbidden, `{"message":"Missing Access"}`)

	_, err := d.SendReturningID(engine.ChannelUser, "hi")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status")
}

func TestVerifyPresent_PresentSucceeds(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, `{"id":"777"}`)

	err := d.VerifyPresent(engine.ChannelUser, "777")
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/channels/U123/messages/777", got.path)
	assert.Equal(t, "Bot tok-abc", got.auth)
}

func TestVerifyPresent_AbsentErrors(t *testing.T) {
	d, _ := newStubServer(t, http.StatusNotFound, `{"message":"Unknown Message"}`)

	err := d.VerifyPresent(engine.ChannelUser, "777")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status")
}

func TestVerifyPresent_MismatchedIDErrors(t *testing.T) {
	d, _ := newStubServer(t, http.StatusOK, `{"id":"999"}`)

	err := d.VerifyPresent(engine.ChannelUser, "777")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatched id")
}

func TestVerifyPresent_EmptyMessageIDErrorsBeforeAnyGet(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, `{"id":"777"}`)

	err := d.VerifyPresent(engine.ChannelUser, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty message id")
	assert.Empty(t, got.path) // never touched the network
}

func TestVerifyPresent_UnknownChannelErrorsBeforeAnyGet(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, `{"id":"777"}`)

	err := d.VerifyPresent("nope", "777")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown logical channel")
	assert.Empty(t, got.path)
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

// sampleEmbed is a fully populated embed used to assert the wire shape: every
// optional field is set so the marshaled body exercises Color, Fields (inline
// and non-inline), and the nested footer object.
func sampleEmbed() Embed {
	return Embed{
		Title:       "Weekly witness report · Week 2026-W30",
		Description: "streak intact",
		Color:       0x2ECC71, // 3066993
		Fields: []EmbedField{
			{Name: "Streak & adherence", Value: "12 days", Inline: true},
			{Name: "This week", Value: "5/7 logged"},
		},
		Footer: "posted Monday · honest numbers, no fabrication",
	}
}

func TestSendEmbed_UserChannelRoutesAuthsAndEmbedBody(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, "")

	err := d.SendEmbed(engine.ChannelUser, sampleEmbed())
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/channels/U123/messages", got.path)
	assert.Equal(t, "Bot tok-abc", got.auth)
	assert.Equal(t, "application/json", got.ctype)
	// The pre-built embed is POSTed verbatim: color is the decimal form of the
	// hex sidebar, the non-inline field drops its inline key, and Footer is
	// nested as Discord's {"text":...} object rather than a bare string.
	assert.JSONEq(t, `{
		"content": "",
		"embeds": [{
			"title": "Weekly witness report · Week 2026-W30",
			"description": "streak intact",
			"color": 3066993,
			"fields": [
				{"name": "Streak & adherence", "value": "12 days", "inline": true},
				{"name": "This week", "value": "5/7 logged"}
			],
			"footer": {"text": "posted Monday · honest numbers, no fabrication"}
		}]
	}`, got.body)
}

func TestSendEmbed_WitnessChannelRoutesToWitnessID(t *testing.T) {
	d, got := newStubServer(t, http.StatusNoContent, "")

	err := d.SendEmbed(engine.ChannelWitness, sampleEmbed())
	require.NoError(t, err)

	assert.Equal(t, "/channels/W456/messages", got.path)
	assert.Contains(t, got.body, `"embeds"`)
}

func TestSendEmbed_OmitsEmptyOptionalFields(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, "")

	// A title-only embed: color 0, no description, no fields, empty footer all
	// drop off the wire so a minimal embed carries no stray keys.
	err := d.SendEmbed(engine.ChannelUser, Embed{Title: "just a title"})
	require.NoError(t, err)

	assert.JSONEq(t, `{"content":"","embeds":[{"title":"just a title"}]}`, got.body)
	assert.NotContains(t, got.body, "footer")
	assert.NotContains(t, got.body, "color")
	assert.NotContains(t, got.body, "fields")
}

func TestSendEmbed_NonSuccessStatusSurfacesError(t *testing.T) {
	d, _ := newStubServer(t, http.StatusForbidden, `{"message":"Missing Access"}`)

	err := d.SendEmbed(engine.ChannelUser, sampleEmbed())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status")
	assert.Contains(t, err.Error(), "Missing Access")
}

func TestSendEmbed_UnknownLogicalChannelErrorsBeforeAnySend(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, "")

	err := d.SendEmbed("nope", sampleEmbed())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown logical channel")
	assert.Empty(t, got.path) // never touched the network — no mis-send
}

func TestSendEmbed_UnsetWitnessChannelErrorsNeverMisSends(t *testing.T) {
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
	}))
	t.Cleanup(srv.Close)

	d := New("tok", "U123", "", srv.Client()) // witness ID intentionally empty
	d.base = srv.URL

	err := d.SendEmbed(engine.ChannelWitness, sampleEmbed())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "witness channel id is not configured")
	assert.Empty(t, got.path)
}

func TestSendEmbedReturningID_ParsesCreatedID(t *testing.T) {
	d, got := newStubServer(t, http.StatusOK, `{"id":"1526225086254682172","channel_id":"U123"}`)

	id, err := d.SendEmbedReturningID(engine.ChannelUser, sampleEmbed())
	require.NoError(t, err)
	assert.Equal(t, "1526225086254682172", id)

	// Routes and auths exactly like SendEmbed — it only adds parsing the id.
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/channels/U123/messages", got.path)
	assert.Equal(t, "Bot tok-abc", got.auth)
	assert.Contains(t, got.body, `"title":"Weekly witness report · Week 2026-W30"`)
}

func TestSendEmbedReturningID_EmptyIDErrors(t *testing.T) {
	d, _ := newStubServer(t, http.StatusOK, `{}`)

	_, err := d.SendEmbedReturningID(engine.ChannelUser, sampleEmbed())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no message id")
}

func TestSendEmbedReturningID_MalformedResponseErrors(t *testing.T) {
	d, _ := newStubServer(t, http.StatusOK, "not-json")

	_, err := d.SendEmbedReturningID(engine.ChannelUser, sampleEmbed())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse create-message response")
}

func TestSendEmbedReturningID_PropagatesPostError(t *testing.T) {
	d, _ := newStubServer(t, http.StatusForbidden, `{"message":"Missing Access"}`)

	_, err := d.SendEmbedReturningID(engine.ChannelUser, sampleEmbed())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status")
}

// TestContentSend_BodyCarriesNoEmbedsKey pins the byte-for-byte teeth guarantee:
// the omitempty on message.Embeds keeps the content-only send serializing as
// {"content":...} with no embeds key, so the fixed-template teeth path is
// unchanged by the embed addition.
func TestContentSend_BodyCarriesNoEmbedsKey(t *testing.T) {
	t.Run("Send", func(t *testing.T) {
		d, got := newStubServer(t, http.StatusOK, "")
		require.NoError(t, d.Send(engine.ChannelUser, "the bell rings"))
		assert.JSONEq(t, `{"content":"the bell rings"}`, got.body)
		assert.NotContains(t, got.body, "embeds")
	})
	t.Run("SendReturningID", func(t *testing.T) {
		d, got := newStubServer(t, http.StatusOK, `{"id":"42"}`)
		_, err := d.SendReturningID(engine.ChannelUser, "test fire")
		require.NoError(t, err)
		assert.JSONEq(t, `{"content":"test fire"}`, got.body)
		assert.NotContains(t, got.body, "embeds")
	})
}
