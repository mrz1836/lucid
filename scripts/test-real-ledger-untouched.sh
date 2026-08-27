#!/usr/bin/env bash
#
# test-real-ledger-untouched.sh — prove a wrapped run never grows the real Ledger.
#
# WHAT THIS GUARDS
#   The real ~/.lucid Ledger must never gain synthetic or fat-fingered data
#   while a test or verification step runs. Running the lucid verification flow
#   once minted seven synthetic test eras plus one accidental `list` era
#   straight into the real registries/eras/ — this harness makes that class of
#   accident an immediate, loud failure.
#
#   It snapshots the real Ledger's registries BEFORE the wrapped command, runs
#   the command with LUCID_REFUSE_REAL_HOME=1 exported (so the built binary
#   refuses to resolve the real home at all — see internal/storage/adapter.go),
#   then re-snapshots and FAILS if any registry file is new or any registry file
#   grew. registries/eras/*.json is called out by name because that is the tree
#   the original leak polluted.
#
#   The two guards are belt-and-suspenders: the refuse signal structurally
#   blocks writes to the real home, and this snapshot check proves — after the
#   fact — that nothing slipped through by any path.
#
# WHAT IT NEVER DOES
#   No network. It never writes to the real ~/.lucid itself: the source Ledger is
#   read-only here, snapshotted from $HOME/.lucid regardless of any incoming
#   LUCID_HOME so an isolated override can never hide real-Ledger growth. The
#   only thing that touches the Ledger is the wrapped command — which the refuse
#   guard blocks from writing the real home anyway.
#
# USAGE
#   scripts/test-real-ledger-untouched.sh [command ...]
#
#   With no arguments the wrapped command defaults to `magex test`. Any command
#   may be wrapped, e.g.:
#     scripts/test-real-ledger-untouched.sh magex test
#     scripts/test-real-ledger-untouched.sh go test ./internal/... -count=1
#
#   The harness exit status is non-zero when the wrapped command fails OR when
#   the real Ledger registries grew; it is 0 only when the command passed and
#   the Ledger is byte-for-byte as many entries as it started with.
#
set -euo pipefail

TAB=$'\t'

# Real Ledger registries — deliberately $HOME/.lucid, IGNORING any LUCID_HOME so
# an isolated override cannot mask growth of the real Ledger.
REAL_HOME="${HOME:?HOME must be set to locate the real ~/.lucid Ledger}"
REAL_REG="$REAL_HOME/.lucid/registries"

# metric_for prints a per-file entry metric: the array length for a JSON array
# registry file (true entry count), otherwise the byte count — which catches any
# content growth in an object-shaped registry file (e.g. an amended era).
metric_for() {
  local f=$1 kind
  kind=$(jq -r 'type' "$f" 2>/dev/null) || kind=invalid
  if [ "$kind" = array ]; then
    jq 'length' "$f" 2>/dev/null
  else
    wc -c < "$f" | tr -d '[:space:]'
  fi
}

# snapshot_registries writes "<relative-path><TAB><metric>" lines, C-sorted by
# path, for every *.json under the registries tree. A missing tree yields an
# empty snapshot (a fresh Ledger has no registries yet — that is not a failure).
snapshot_registries() {
  local reg=$1 out=$2 rel
  : > "$out"
  [ -d "$reg" ] || return 0
  while IFS= read -r rel; do
    rel=${rel#./}
    printf '%s%s%s\n' "$rel" "$TAB" "$(metric_for "$reg/$rel")" >> "$out"
  done < <(cd "$reg" && find . -type f -name '*.json' | LC_ALL=C sort)
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
BEFORE="$WORK/before.tsv"
AFTER="$WORK/after.tsv"

# ── Before ───────────────────────────────────────────────────────────────────
snapshot_registries "$REAL_REG" "$BEFORE"
before_count=$(wc -l < "$BEFORE" | tr -d '[:space:]')
printf '== real Ledger snapshot: %s registry file(s) under %s\n' "$before_count" "$REAL_REG"
if [ -d "$REAL_REG/eras" ]; then
  era_count=$(find "$REAL_REG/eras" -type f -name '*.json' | wc -l | tr -d '[:space:]')
  printf '   eras: %s chapter(s)\n' "$era_count"
fi

# ── Wrapped run (doubly protected: refuse guard exported) ─────────────────────
if [ "$#" -eq 0 ]; then
  set -- magex test
fi
printf '== running (LUCID_REFUSE_REAL_HOME=1): %s\n' "$*"
cmd_rc=0
if LUCID_REFUSE_REAL_HOME=1 "$@"; then
  cmd_rc=0
else
  cmd_rc=$?
fi

# ── After + diff ─────────────────────────────────────────────────────────────
snapshot_registries "$REAL_REG" "$AFTER"

# New registry files: present in AFTER, absent from BEFORE (the eras-leak shape).
new_files=$(LC_ALL=C comm -13 \
  <(cut -d"$TAB" -f1 "$BEFORE" | LC_ALL=C sort) \
  <(cut -d"$TAB" -f1 "$AFTER"  | LC_ALL=C sort) || true)

# Grown registry files: a path in BOTH whose metric increased.
grown=$(LC_ALL=C join -t"$TAB" \
  <(LC_ALL=C sort "$BEFORE") \
  <(LC_ALL=C sort "$AFTER") \
  | awk -F"$TAB" 'NF==3 && ($3+0) > ($2+0) { printf "%s (%s -> %s entries)\n", $1, $2, $3 }' || true)

grew=0
if [ -n "$new_files" ] || [ -n "$grown" ]; then
  grew=1
  {
    printf '\n'
    printf 'FAIL: the real Ledger grew during the wrapped run — %s\n' "$REAL_REG"
    if [ -n "$new_files" ]; then
      printf '  new registry file(s):\n'
      while IFS= read -r p; do
        [ -n "$p" ] || continue
        case "$p" in
          eras/*) printf '    ⚠ %s   <- a NEW ERA was minted in the REAL Ledger\n' "$p" ;;
          *)      printf '    ⚠ %s\n' "$p" ;;
        esac
      done <<< "$new_files"
    fi
    if [ -n "$grown" ]; then
      printf '  grown registry file(s):\n'
      while IFS= read -r p; do
        [ -n "$p" ] || continue
        case "$p" in
          eras/*) printf '    ⚠ %s   <- a REAL ERA was written during the run\n' "$p" ;;
          *)      printf '    ⚠ %s\n' "$p" ;;
        esac
      done <<< "$grown"
    fi
    printf '  Point LUCID_HOME at an isolated t.TempDir(); never resolve the real ~/.lucid in a test/verification run.\n'
  } >&2
else
  printf '== real Ledger unchanged: no new registry files, no growth (%s file(s))\n' "$before_count"
fi

# ── Verdict ──────────────────────────────────────────────────────────────────
status=0
if [ "$cmd_rc" -ne 0 ]; then
  printf 'FAIL: wrapped command exited %s: %s\n' "$cmd_rc" "$*" >&2
  status="$cmd_rc"
fi
if [ "$grew" -ne 0 ]; then
  status=1
fi

if [ "$status" -eq 0 ]; then
  printf '\ntest-real-ledger-untouched: PASS (command green, real Ledger untouched)\n'
else
  printf '\ntest-real-ledger-untouched: FAIL\n' >&2
fi
exit "$status"
