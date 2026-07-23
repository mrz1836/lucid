#!/usr/bin/env bash
# test-no-heartbeat.sh — AC-10 verifier for the weekly witness report.
#
# The monthly witness heartbeat is retired: the weekly witness report absorbs
# its all-clear signal. This guard fails if any *live* heartbeat wiring survives
# in the Go tree — the Send kind, the template, the "first run of the month"
# plumbing, or the persisted month marker. It greps for the concrete Go
# identifiers only, so a lowercase "heartbeat" left in a doc comment or a
# CHANGELOG entry (historical narration) does not trip it; a resurrected
# `SendHeartbeat` / `FirstRunOfMonth` / `LastHeartbeatMonth` / `Heartbeat(` does.
#
# Run: bash internal/witnessreport/test-no-heartbeat.sh   (from the repo root)
set -euo pipefail

# Resolve the repo root from this script's own location (internal/witnessreport/)
# so the check works regardless of the caller's working directory.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"

# The live-wiring identifiers. Any of these in a .go file means the heartbeat is
# not fully retired.
pattern='SendHeartbeat|FirstRunOfMonth|LastHeartbeatMonth|Heartbeat\('

# Grep every tracked Go file under internal/. --include scopes to Go source, so
# this script (a .sh) and docs/CHANGELOG never match themselves.
if hits="$(grep -rnE "${pattern}" --include='*.go' "${repo_root}/internal" 2>/dev/null)"; then
	echo "FAIL: live monthly-heartbeat wiring still present in the Go tree:" >&2
	echo "${hits}" >&2
	exit 1
fi

echo "ok: no live monthly-heartbeat wiring in internal/**/*.go (weekly witness report absorbs the all-clear)"
