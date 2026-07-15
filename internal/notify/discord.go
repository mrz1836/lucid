// Package notify holds the concrete [scheduler.Notifier] transport — the one
// place a rendered Engine send actually leaves the machine over the network.
// It is deliberately credential-dumb (ADR-0005): the Discord bot token and the
// two real channel IDs arrive only through injected environment variables, and
// the notifier composes no message text of its own — it POSTs the already
// rendered, pre-committed template string it is handed. No template logic and
// no model package are reachable from here, so the "only the fixed Engine
// templates ever leave" ceiling is preserved structurally.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// Injected environment variables (ADR-0005). The token names the generic
// harness secret — the real Discord bot credential is bound to it in local
// hush and never appears in this repo. The channel IDs resolve the logical
// "user"/"witness" channels to real Discord destinations.
const (
	envHarnessToken   = "LUCID_HARNESS_TOKEN"
	envUserChannel    = "LUCID_USER_CHANNEL_ID"
	envWitnessChannel = "LUCID_WITNESS_CHANNEL_ID"
)

// defaultBaseURL is the Discord REST API root the notifier posts to; tests
// point the base at an httptest server via the same field.
const defaultBaseURL = "https://discord.com/api/v10"

// httpTimeout bounds a single message POST (mirrors the enrichment fetcher's
// 10s budget); errBodyCap bounds how much of a non-2xx response body is echoed
// into the returned error so a failure never dumps an unbounded payload;
// respBodyCap bounds how much of a 2xx body is read when a caller needs the
// created message id (a Discord message object is a few KB — the cap only
// guards against a pathological reply).
const (
	httpTimeout = 10 * time.Second
	errBodyCap  = 512
	respBodyCap = 1 << 16
)

// httpDoer is the one-method seam over [http.Client.Do] so tests can drive the
// notifier against an httptest server without a real socket or token.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// message is the Discord "create message" REST body — a single content field
// carrying the pre-rendered template text and nothing else.
type message struct {
	Content string `json:"content"`
}

// created is the subset of Discord's create-message (and get-message) response
// the notifier reads: the snowflake id of the message. The companion path uses
// it for read-back verification and to persist an idempotent delivery receipt;
// the teeth path ([Discord.Send]) ignores it entirely.
type created struct {
	ID string `json:"id"`
}

// Discord is the concrete Discord-bot [scheduler.Notifier]. It resolves the
// logical channel to a real channel ID and POSTs the rendered text via the bot
// REST API, reading its bot token from the injected env only.
type Discord struct {
	token            string
	userChannelID    string
	witnessChannelID string
	do               httpDoer
	base             string
}

// NewDiscordFromEnv builds a Discord notifier from the injected environment
// (ADR-0005). It errors clearly when the bot token or the user channel ID is
// unset. The witness channel ID may be empty at construction — the Engine only
// sends to it once a witness is confirmed — but a witness send attempted with
// an unset witness channel is a clear [Discord.Send] error, never a mis-send.
func NewDiscordFromEnv() (*Discord, error) {
	token := os.Getenv(envHarnessToken)
	if token == "" {
		return nil, fmt.Errorf("notify: %s is not set", envHarnessToken)
	}
	userID := os.Getenv(envUserChannel)
	if userID == "" {
		return nil, fmt.Errorf("notify: %s is not set", envUserChannel)
	}
	witnessID := os.Getenv(envWitnessChannel)
	return New(token, userID, witnessID, nil), nil
}

// New constructs a Discord notifier explicitly — the seam tests use to inject a
// fake [httpDoer]; a nil doer defaults to a bounded-timeout HTTP client. The
// base URL defaults to the live API; white-box tests reassign the base field to
// an httptest server.
func New(token, userID, witnessID string, do httpDoer) *Discord {
	if do == nil {
		do = &http.Client{Timeout: httpTimeout}
	}
	return &Discord{
		token:            token,
		userChannelID:    userID,
		witnessChannelID: witnessID,
		do:               do,
		base:             defaultBaseURL,
	}
}

// Send POSTs the already-rendered text to the real Discord channel behind the
// logical channel ("user" or "witness"). An unknown logical channel or an
// unresolved (unset) channel ID is an error, never a mis-send; a non-2xx
// response surfaces the status and a short body snippet. It composes nothing —
// the text is the fixed Engine template the scheduler handed it. This is the
// teeth path: it discards the created message id.
func (d *Discord) Send(channel, text string) error {
	_, err := d.post(channel, text)
	return err
}

// SendReturningID POSTs the rendered text exactly like [Discord.Send] but
// parses and returns the snowflake id Discord assigns the created message. The
// companion path uses the id for read-back verification ([Discord.VerifyPresent])
// and to persist an idempotent delivery receipt. An empty id in an otherwise-2xx
// response is an error, so a caller never records a receipt it cannot verify.
func (d *Discord) SendReturningID(channel, text string) (string, error) {
	body, err := d.post(channel, text)
	if err != nil {
		return "", err
	}
	var c created
	if err := json.Unmarshal(body, &c); err != nil {
		return "", fmt.Errorf("notify: parse create-message response: %w", err)
	}
	if c.ID == "" {
		return "", fmt.Errorf("notify: discord create-message response carried no message id")
	}
	return c.ID, nil
}

// VerifyPresent confirms a previously created message id is actually present in
// the channel by GETting it from the Discord REST API — the read-back half of
// the companion's "a real message id reappears in the channel" guarantee. A
// non-2xx status (a 404 for a message that never landed), a body whose id does
// not match, or an empty id argument is a clear error so a delivery is never
// recorded as verified when the message is not really there.
func (d *Discord) VerifyPresent(channel, messageID string) error {
	id, err := d.resolve(channel)
	if err != nil {
		return err
	}
	if messageID == "" {
		return fmt.Errorf("notify: cannot verify an empty message id")
	}

	url := fmt.Sprintf("%s/channels/%s/messages/%s", d.base, id, messageID)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("notify: build read-back request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.token)

	resp, err := d.do.Do(req)
	if err != nil {
		return fmt.Errorf("notify: read-back get from channel %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyCap))
		return fmt.Errorf("notify: read-back of message %s in channel %s returned status %d: %s",
			messageID, id, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, respBodyCap))
	var c created
	if err := json.Unmarshal(body, &c); err != nil {
		return fmt.Errorf("notify: parse read-back response: %w", err)
	}
	if c.ID != messageID {
		return fmt.Errorf("notify: read-back of message %s returned mismatched id %q", messageID, c.ID)
	}
	return nil
}

// post resolves the logical channel, POSTs the rendered text, and returns the
// (bounded) 2xx response body — the shared transport both [Discord.Send] (teeth,
// fire-and-forget) and [Discord.SendReturningID] (companion, needs the created
// id) build on. Its resolve/marshal/request/status behavior and error wording
// are byte-for-byte what Send used before, so the teeth path is unchanged.
func (d *Discord) post(channel, text string) ([]byte, error) {
	id, err := d.resolve(channel)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(message{Content: text})
	if err != nil {
		return nil, fmt.Errorf("notify: marshal message: %w", err)
	}

	url := fmt.Sprintf("%s/channels/%s/messages", d.base, id)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.do.Do(req)
	if err != nil {
		return nil, fmt.Errorf("notify: post to channel %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyCap))
		return nil, fmt.Errorf("notify: discord post to channel %s returned status %d: %s",
			id, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, respBodyCap))
}

// resolve maps a logical channel to its real Discord channel ID. An unknown
// channel name, or a configured-but-empty ID, is an error so a send never
// leaks to the wrong destination.
func (d *Discord) resolve(channel string) (string, error) {
	switch channel {
	case engine.ChannelUser:
		if d.userChannelID == "" {
			return "", fmt.Errorf("notify: user channel id is not configured")
		}
		return d.userChannelID, nil
	case engine.ChannelWitness:
		if d.witnessChannelID == "" {
			return "", fmt.Errorf("notify: witness channel id is not configured")
		}
		return d.witnessChannelID, nil
	default:
		return "", fmt.Errorf("notify: unknown logical channel %q", channel)
	}
}
