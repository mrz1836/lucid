package ollama_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/provider/ollama"
)

// chatReplyJSON is a well-formed non-streaming /api/chat success body carrying
// the assistant message content.
func chatReplyJSON(content string) string {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type reply struct {
		Model   string  `json:"model"`
		Message message `json:"message"`
		Done    bool    `json:"done"`
	}
	b, err := json.Marshal(reply{
		Model:   "qwen2.5:14b",
		Message: message{Role: "assistant", Content: content},
		Done:    true,
	})
	if err != nil {
		panic(err) // unreachable: reply holds only string/bool fields
	}
	return string(b)
}

// TestComplete_Success drives the happy path and asserts the invocation
// contract: POST to /api/chat, stream:false, the model, the System mapped to a
// leading `system` message, the role-tagged Messages passed through, and
// .message.content parsed into Content.
func TestComplete_Success(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatReplyJSON("a grounded reflection"))
	}))
	defer srv.Close()

	p := ollama.New(srv.URL, "qwen2.5:14b")
	resp, err := p.Complete(context.Background(), provider.Request{
		Intent: "reflection.propose",
		System: "You are a careful mirror.",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "what stuck with me?"},
			{Role: provider.RoleAssistant, Content: "tell me more"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "a grounded reflection", resp.Content)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/chat", gotPath)
	assert.Equal(t, "qwen2.5:14b", gotBody["model"])
	assert.Equal(t, false, gotBody["stream"])

	msgs, ok := gotBody["messages"].([]any)
	require.True(t, ok, "messages must be an array")
	require.Len(t, msgs, 3, "System becomes a leading message + the two turns")
	assert.Equal(t, map[string]any{"role": "system", "content": "You are a careful mirror."}, msgs[0])
	assert.Equal(t, map[string]any{"role": "user", "content": "what stuck with me?"}, msgs[1])
	assert.Equal(t, map[string]any{"role": "assistant", "content": "tell me more"}, msgs[2])
}

// TestComplete_NoSystemOmitsLeadingMessage confirms an absent System yields no
// leading system message — only the authorized turns are sent.
func TestComplete_NoSystemOmitsLeadingMessage(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = io.WriteString(w, chatReplyJSON("ok"))
	}))
	defer srv.Close()

	p := ollama.New(srv.URL, "qwen2.5:14b")
	_, err := p.Complete(context.Background(), provider.Request{
		Intent:   "intake.decide",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	require.NoError(t, err)

	msgs, ok := gotBody["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 1)
	assert.Equal(t, map[string]any{"role": "user", "content": "hi"}, msgs[0])
}

// TestComplete_TrailingSlashBaseURL: a base URL with a trailing slash still
// posts to exactly one /api/chat (no doubled slash).
func TestComplete_TrailingSlashBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, chatReplyJSON("ok"))
	}))
	defer srv.Close()

	p := ollama.New(srv.URL+"/", "qwen2.5:14b")
	_, err := p.Complete(context.Background(), provider.Request{Intent: "intake.decide"})
	require.NoError(t, err)
	assert.Equal(t, "/api/chat", gotPath)
}

// TestComplete_TimeoutFromHungDaemon: a handler that sleeps past a short
// deadline is the binary-skew hang class — the call must map to ErrTimeout,
// never an unbounded wait.
func TestComplete_TimeoutFromHungDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Simulate the binary-skew hang: never answer. The bounded fallback keeps
		// the handler from wedging Close() if the server is slow to notice the
		// client's canceled connection — the client's own deadline is what the
		// assertion turns on.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	p := ollama.New(srv.URL, "qwen2.5:14b", ollama.WithTimeout(20*time.Millisecond))
	_, err := p.Complete(context.Background(), provider.Request{Intent: "intake.decide"})
	require.ErrorIs(t, err, provider.ErrTimeout)
	assert.NotErrorIs(t, err, provider.ErrUnavailable)
}

// TestComplete_CanceledContext: an explicit caller cancel surfaces as the
// context error, not a transport sentinel — a canceled call is the caller's,
// not "no model reachable".
func TestComplete_CanceledContext(t *testing.T) {
	// The pre-canceled context makes the client return before it dials, so the
	// handler never runs; the bounded fallback only guards against a slow Close.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := ollama.New(srv.URL, "qwen2.5:14b")
	_, err := p.Complete(ctx, provider.Request{Intent: "intake.decide"})
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, provider.ErrUnavailable)
	assert.NotErrorIs(t, err, provider.ErrTimeout)
}

// TestComplete_UnavailableMappings covers every failure that means "no model
// reachable": a refused connection (daemon down), a 404 missing/unpulled model,
// a non-2xx status, an unparseable body, and an empty message content.
func TestComplete_UnavailableMappings(t *testing.T) {
	t.Run("connection refused (daemon down)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close() // now nothing is listening → dial fails

		p := ollama.New(url, "qwen2.5:14b")
		_, err := p.Complete(context.Background(), provider.Request{})
		require.ErrorIs(t, err, provider.ErrUnavailable)
		assert.NotErrorIs(t, err, provider.ErrTimeout)
	})

	t.Run("missing model 404", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"model 'qwen2.5:14b' not found, try pulling it first"}`)
		}))
		defer srv.Close()

		p := ollama.New(srv.URL, "qwen2.5:14b")
		_, err := p.Complete(context.Background(), provider.Request{})
		require.ErrorIs(t, err, provider.ErrUnavailable)
		assert.NotErrorIs(t, err, provider.ErrTimeout)
	})

	t.Run("non-2xx status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		p := ollama.New(srv.URL, "qwen2.5:14b")
		_, err := p.Complete(context.Background(), provider.Request{})
		require.ErrorIs(t, err, provider.ErrUnavailable)
	})

	t.Run("unparseable body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "not json — a stray daemon notice")
		}))
		defer srv.Close()

		p := ollama.New(srv.URL, "qwen2.5:14b")
		_, err := p.Complete(context.Background(), provider.Request{})
		require.ErrorIs(t, err, provider.ErrUnavailable)
	})

	t.Run("empty message content", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, chatReplyJSON(""))
		}))
		defer srv.Close()

		p := ollama.New(srv.URL, "qwen2.5:14b")
		_, err := p.Complete(context.Background(), provider.Request{})
		require.ErrorIs(t, err, provider.ErrUnavailable)
	})

	t.Run("unbuildable request (bad base URL)", func(t *testing.T) {
		// A control character in the base URL fails http.NewRequestWithContext
		// before any dial — a construction failure, mapped to ErrUnavailable.
		p := ollama.New("http://127.0.0.1\n:11434", "qwen2.5:14b")
		_, err := p.Complete(context.Background(), provider.Request{})
		require.ErrorIs(t, err, provider.ErrUnavailable)
	})
}

// TestWithHTTPClient_NilKeepsDefault: WithHTTPClient(nil) is a no-op so a
// mis-wired option can't drop the default transport.
func TestWithHTTPClient_NilKeepsDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, chatReplyJSON("ok"))
	}))
	defer srv.Close()

	p := ollama.New(srv.URL, "qwen2.5:14b", ollama.WithHTTPClient(nil))
	resp, err := p.Complete(context.Background(), provider.Request{})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}

// TestWithHTTPClient_Injected confirms an injected client is used verbatim.
func TestWithHTTPClient_Injected(t *testing.T) {
	var served bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		_, _ = io.WriteString(w, chatReplyJSON("ok"))
	}))
	defer srv.Close()

	p := ollama.New(srv.URL, "qwen2.5:14b", ollama.WithHTTPClient(&http.Client{Timeout: time.Second}))
	_, err := p.Complete(context.Background(), provider.Request{})
	require.NoError(t, err)
	assert.True(t, served, "the injected client must reach the test server")
}

// interface guard mirrored in a test so a signature regression is caught here
// too, not only at the package.
var _ provider.Provider = (*ollama.Provider)(nil)
