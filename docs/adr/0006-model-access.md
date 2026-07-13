# ADR-0006 — Model access: OAuth'd vendor CLIs and local models, never API keys

**Status:** Accepted. The provider *lineup* is configuration; what
this ADR fixes is the auth and transport posture.

## Context

Every model call in Lucid is Mirror-side analysis over a bounded
slice — the MVP agents (Intake, Structuring, Reflection, Safety,
[`../mvp/agent-contracts.md`](../mvp/agent-contracts.md)) and the
interim analyst at retro cadence. The Engine runs modeless by
principle (P9), so model access is never availability-critical; it is,
however, cost- and privacy-sensitive, because the slices are excerpts
of an inner life and because metered API billing taxes exactly the
cadence-bound analysis the design wants to encourage. The owner
already holds subscription seats with OAuth-authenticated CLIs on the
host (Anthropic via Claude Code; OpenAI via the Codex CLI — the same
pattern the reference OpenClaw gateway uses), plus a documented local
model path (Ollama).

## Decision

Model access goes through **locally-authenticated vendor CLIs or a
local model runtime — never raw API keys**:

* **Sanctioned providers at launch:** Anthropic (Claude Code CLI,
  subscription OAuth) and OpenAI (Codex CLI, plan OAuth), plus any
  **local model** served on-host (e.g., Ollama). Headless/one-shot
  invocation modes, with the exact CLI invocation contract pinned per
  provider in the implementation docs.
* **One provider interface, everything swappable.** All agent roles
  call a single provider boundary; which provider backs which role is
  per-instance configuration, changeable without touching contracts.
  ADR-0001's "HTTP calls behind interfaces" generalizes to *process
  or HTTP behind the same interface* — a spawned CLI, a local daemon
  endpoint, or (future) a keyed API are all implementations.
* **No API keys.** Lucid stores no model credentials: OAuth tokens
  live in each vendor CLI's own store, and refresh/expiry is the
  vendor's problem. If a future provider offers key-only access, the
  key lives in `hush` (ADR-0005) and is injected at spawn — recorded
  as an exception, not a new normal.

## Pinned invocation contracts

The MVP ships **two** backends behind the one provider interface, selected by the
`lucid.json` `provider` block (`backend`, `model`, `timeout_seconds`, `endpoint`,
plus a reserved per-role `roles` map — [`../mvp/data-model.md`](../mvp/data-model.md)
§`lucid.json`). A single configured backend serves all four agent roles for now; the
`roles` map reserves per-role overrides so this decision's per-role mandate can be
exercised later without a contract change. Each backend maps the shared
`provider.Request` (System instruction + role-tagged Messages) onto its transport and
the reply back into `provider.Response.Content`; a bounded failure returns the
interface's `ErrTimeout` / `ErrUnavailable` sentinels rather than a raw error.

### Claude Code CLI — `backend: "claude_cli"` (default)

* **Invocation:** a fresh one-shot `claude -p --output-format json --model <model>`
  per call (default model `opus`), the System instruction passed via
  `--system-prompt` and the role-flattened Messages fed on stdin. Each completion is
  one stateless bounded slice, so nothing persists between calls.
* **Reply:** parse the JSON envelope and take `.result` as `Response.Content`.
* **Errors:** a context deadline maps to `ErrTimeout`; a non-zero exit, a spawn
  failure, an `is_error` envelope, expired OAuth / offline, or an empty `.result`
  map to `ErrUnavailable` — the "no model reachable" degradation this ADR requires
  surviving.
* **Auth:** on-host subscription OAuth in the Claude CLI's own store; Lucid holds no
  credential. Zero-setup, so it is the shipped default across every role.

### Local Ollama — `backend: "ollama"`

* **Invocation:** a non-streaming `POST <endpoint>/api/chat` (default endpoint
  `http://localhost:11434`) with `{model, messages, stream: false}`, the System
  instruction mapped to a leading `system` message and the role-tagged Messages
  passed through. Default model `qwen2.5:14b`; any locally-pulled model is a one-line
  config change.
* **Reply:** read `.message.content` as `Response.Content`.
* **Errors:** **every call is deadline-bounded** — a hung daemon (the known
  binary-skew failure class, where a stale `ollama serve` leaves the endpoint hanging
  while `/api/tags` stays healthy) maps to `ErrTimeout`, never an unbounded wait; a
  refused connection, DNS failure, non-2xx status, or an unpulled/missing model maps
  to `ErrUnavailable`.
* **Sovereignty:** the local model is a first-class configuration, not a degraded
  mode — the full-sovereignty path this ADR's Consequences require.

The **Codex CLI** (OpenAI OAuth) remains sanctioned above but is not shipped in this
pillar; the config seam and factory dispatch leave room for it — and any future
backend — to register without a contract change. **No test requires live vendor
auth:** agents are exercised against the `provider.Fake`, and the two real backends
are proven once by a documented manual live-smoke
([`../usage/harness-setup.md`](../usage/harness-setup.md)).

## Consequences

* **Cost:** analysis rides subscriptions already paid for; running
  the Mirror at its documented cadence stops carrying a per-token
  anxiety tax. Subscription rate limits are acceptable by
  construction — P9 bounds model use to review cadence, not runtime.
* **Degradation is already designed:** expired OAuth or an offline
  vendor is the "no model reachable" state the architecture requires
  surviving (P9) — the chain and capture continue; analysis waits.
* **Privacy:** transport choice doesn't change the data contract
  (stateless analyst over named slices, P6). Per-provider data
  handling remains a per-provider consent choice made at calibration;
  the local-model option remains the full-sovereignty path and must
  stay a first-class configuration, not a degraded mode.
* **Testing:** agents are exercised against fakes at the provider
  boundary; no test may require live vendor auth.
* [`../vision.md`](../vision.md) §8's "via API" phrasing reads as
  "programmatic model access"; this ADR is the binding transport
  statement — a wording touch-up can ride the next vision edit.
