#!/usr/bin/env bash
#
# backup_test.sh — end-to-end test for scripts/backup.sh.
#
# Builds a fixture Ledger carrying every tree (the backup set AND every
# rebuildable/omitted tree), runs backup.sh into a fresh destination, and
# asserts the destination holds EXACTLY the ADR-0002 backup set: the primary
# trees present, the derived status.json and every rebuildable/index tree
# absent. No network, no real ~/.lucid/ — a self-contained temp fixture.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKUP="$SCRIPT_DIR/backup.sh"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

HOME_DIR="$WORK/.lucid"
DEST="$WORK/backup-out"

FAILS=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAILS=$((FAILS + 1)); }
ok() { printf 'ok: %s\n' "$1"; }

assert_present() {
  if [ -e "$DEST/$1" ]; then ok "present  $1"; else fail "expected present but absent: $1"; fi
}
assert_absent() {
  if [ ! -e "$DEST/$1" ]; then ok "absent   $1"; else fail "expected absent but present: $1"; fi
}

# ── Build a full fixture Ledger ──────────────────────────────────────────────
mkdir -p "$HOME_DIR"/{raw/2026/07,observations/2026/07,registries/places,engine/days/2026/07,projections,processed,insights,reflections,people,sessions}
printf 'raw entry\n'          > "$HOME_DIR/raw/2026/07/raw_x.md"
printf '{"kind":"pain"}\n'    > "$HOME_DIR/observations/2026/07/obs_2026-07-05.jsonl"
printf '{"key":"place_a"}\n'  > "$HOME_DIR/registries/places/place_a.json"
printf '{"version":1}\n'      > "$HOME_DIR/engine/chain.json"
printf '{"streak":3}\n'       > "$HOME_DIR/engine/status.json"     # derived — must NOT be backed up
printf '{"day":1}\n'          > "$HOME_DIR/engine/days/2026/07/day_2026-07-05.json"
printf 'render path\n'        > "$HOME_DIR/projections/exports.log"
printf 'rebuildable packet\n' > "$HOME_DIR/projections/packet_2026-07-05.md"  # rebuildable — must NOT
printf '{"id":"x"}\n'         > "$HOME_DIR/processed/x.json"       # rebuildable
printf 'insight\n'            > "$HOME_DIR/insights/i_x.md"        # rebuildable
printf 'reflection\n'         > "$HOME_DIR/reflections/reflection_2026_w27.md"  # rebuildable
printf '{"key":"person_a"}\n' > "$HOME_DIR/people/person_a.json"  # index — not in set
printf '{"id":"s"}\n'         > "$HOME_DIR/sessions/s.json"       # index — not in set
printf '{"version":1}\n'      > "$HOME_DIR/lucid.json"            # config — not in set

# ── Run the backup ───────────────────────────────────────────────────────────
"$BACKUP" --home "$HOME_DIR" --dest "$DEST" >/dev/null

# ── Backup set: present ──────────────────────────────────────────────────────
assert_present raw/2026/07/raw_x.md
assert_present observations/2026/07/obs_2026-07-05.jsonl
assert_present registries/places/place_a.json
assert_present engine/chain.json
assert_present engine/days/2026/07/day_2026-07-05.json
assert_present projections/exports.log

# ── Rebuildable / index / config: absent ─────────────────────────────────────
assert_absent engine/status.json
assert_absent projections/packet_2026-07-05.md
assert_absent processed
assert_absent insights
assert_absent reflections
assert_absent people
assert_absent sessions
assert_absent lucid.json

# ── --print-manifest matches the documented set ──────────────────────────────
MANIFEST="$("$BACKUP" --print-manifest)"
EXPECTED="$(printf 'dir\traw\ndir\tobservations\ndir\tregistries\ndir\tengine\texclude=engine/status.json\nfile\tprojections/exports.log')"
if [ "$MANIFEST" = "$EXPECTED" ]; then ok "print-manifest matches"; else fail "print-manifest drifted"; fi

# ── --dest required ──────────────────────────────────────────────────────────
if "$BACKUP" --home "$HOME_DIR" >/dev/null 2>&1; then fail "missing --dest should exit non-zero"; else ok "missing --dest rejected"; fi

if [ "$FAILS" -eq 0 ]; then
  printf '\nbackup_test: PASS\n'
  exit 0
fi
printf '\nbackup_test: %d assertion(s) FAILED\n' "$FAILS" >&2
exit 1
