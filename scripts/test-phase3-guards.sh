#!/usr/bin/env bash
#
# test-phase3-guards.sh — run the Phase-3 acceptance guards as one named target.
#
# WHAT THIS GUARDS
#   Two invariants that must never regress as the Mirror gains a live provider
#   and an invocation surface (docs/mvp/acceptance-criteria.md phases 3–7):
#
#     1. Phrase blocklist + hypothesis framing — every agent-authored message
#        stays hypothesis-safe and free of the coaching/diagnostic phrases the
#        Safety gate forbids (internal/agents/safety: the blocklist regression,
#        the phrase-file check, and the agent-prompts-are-clean sweep).
#     2. Insight provenance — every written insight carries full provenance
#        (raw entry ids, processed artifact id, reflection prompt version, and
#        the user-response kind); a provenance gap is rejected at the storage
#        boundary and the validate/reflect write paths stamp it end-to-end.
#
#   This is a thin wrapper: it runs the existing Go tests that own those
#   guarantees, so `bash scripts/test-phase3-guards.sh` is a single green/red
#   signal for the Phase-3 acceptance line (referenced by the T-plan AC-11
#   verifier). It is NOT a substitute for `magex test` — that remains the full
#   race + coverage gate.
#
# WHAT IT NEVER DOES
#   No network. No mutation of any Ledger. It only runs read-only Go tests.
#
# USAGE
#   scripts/test-phase3-guards.sh          # run all Phase-3 guards
#
#   GO   Override the go binary (default: go on PATH).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
GO="${GO:-go}"

cd "$REPO_ROOT"

echo "== Phase-3 guard 1/3: phrase blocklist + hypothesis framing (internal/agents/safety) =="
"$GO" test ./internal/agents/safety/... -count=1

echo "== Phase-3 guard 2/3: insight provenance at the storage boundary (internal/storage) =="
"$GO" test ./internal/storage/... -run 'Insight|Provenance' -count=1

echo "== Phase-3 guard 3/3: provenance on the validate/reflect/pipeline write paths (internal/router) =="
"$GO" test ./internal/router/... -run 'Validate|Reflect|PipelineE2E' -count=1

echo "== Phase-3 guards: all green =="
