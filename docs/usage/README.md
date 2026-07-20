# Using Lucid

This directory is the practical, run-it guide for Lucid — how to install the
binary, run your first day, and look up every command. It complements the
conceptual docs: read [`../architecture.md`](../architecture.md) for the design
and [`../vision.md`](../vision.md) for the why; read here to actually drive it.

Lucid is a local-first personal operating system with two cooperating halves —
the **Mirror** (capture, patterns, self-understanding — never enforcement) and
the **Engine** (one committed daily practice with real accountability — never
content) — both writing one user-owned, append-only Ledger under `~/.lucid/`.
Everything below runs on your own host; nothing syncs to a cloud.

## The pages

| Page | What it covers |
|------|----------------|
| [`install.md`](install.md) | Prerequisites, the three install paths (source, `go install`, release binary), verifying the build, and first-run setup (`lucid init`, `LUCID_HOME`). |
| [`getting-started.md`](getting-started.md) | The mental model, the daily morning/evening rhythm, a synthetic first-week walkthrough, config, data & privacy, and what's not yet built. |
| [`commands.md`](commands.md) | The full command reference — every `lucid` CLI command and sub-form, plus the chat/harness slash commands — with synopsis, flags, and examples. |
| [`companion.md`](companion.md) | The optional daily companion — the model-composed morning and night messages: what it is, the two windows, the `companion` config block, testing a fire, and how fallback / missed-fire / alerting keep it never-silent. |
| [`weekly-reflection.md`](weekly-reflection.md) | The read-only weekly deep-dive (`lucid reflect week`) — the projection-only week bundle, the frameworks/lens layer and `provenance.framework` labeling, and the resonance-gated `reflect week apply` path that turns a confirmed pattern into a tracked insight. |
| [`natural-language.md`](natural-language.md) | The voice-first / talk-instead-of-type surface — how a chat harness maps plain language onto the exact documented command, the reads-run / writes-confirm posture, the night close-out step by step, and synthetic side-by-side examples. |
| [`harness-setup.md`](harness-setup.md) | Operator guide for driving the deterministic loop from a chat harness (Path A): install, reachability, loading the skill, channel routing, secrets posture, and end-to-end verification. |

## Two surfaces

Lucid has one core (the `lucid` Go binary) reachable two ways:

- **The CLI** — the deterministic verbs you run directly in a terminal
  (`lucid log`, `lucid closeout`, `lucid status`, …). Agent-free and scriptable.
- **The chat/harness surface** — a thin skill ([`../../skills/lucid/SKILL.md`](../../skills/lucid/SKILL.md))
  that maps chat messages to the same router intents and adds the agentic Mirror
  verbs (`/checkin`, `/reflect`, `/ask`, …). The harness invokes the binary; it
  never reimplements Lucid logic. See [`../mvp/local-runtime.md`](../mvp/local-runtime.md).

New to Lucid? Start with [`install.md`](install.md), then walk
[`getting-started.md`](getting-started.md).
