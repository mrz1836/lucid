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
