#!/usr/bin/env bash
#
# backup.sh — copy the ADR-0002 backup set out of a lucid Ledger home.
#
# WHAT THIS BACKS UP
#   The backup set is the primary data that exists nowhere else and must
#   survive forever (docs/adr/0002-storage.md; docs/mvp/local-runtime.md
#   §Rebuildability): raw entries, the observation event log, the registries,
#   the engine tree MINUS its derived status.json, and the append-only export
#   log. Everything else under ~/.lucid/ is rebuildable — processed/,
#   insights/, reflections/, engine/status.json, and the rest of projections/ —
#   and is deliberately NOT copied, along with the people/ and sessions/
#   indexes and lucid.json (all reconstructable, not testimony).
#
#   This inclusion set is the single source of truth shared with the Go deploy
#   package (deploy.BackupManifest); `--print-manifest` emits it and a test
#   cross-checks the two so the shell and Go can never drift.
#
# WHAT IT NEVER DOES
#   No network. No mutation of the source Ledger. It only reads ~/.lucid/ and
#   writes copies into a destination directory the caller names.
#
# USAGE
#   backup.sh --dest <dir> [--home <lucid-home>] [--dry-run]
#   backup.sh --print-manifest
#
#   --home           Ledger home to back up. Default: $LUCID_HOME, else ~/.lucid
#   --dest           Destination directory for the backup set (required for a
#                    real or dry-run copy). Use a fresh/empty directory.
#   --dry-run        Print what would be copied; touch nothing.
#   --print-manifest Print the canonical ADR-0002 include set and exit.
#
set -euo pipefail

LUCID_HOME="${LUCID_HOME:-$HOME/.lucid}"
DEST=""
DRY_RUN=0
PRINT_MANIFEST=0

usage() {
  sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
  case "$1" in
    --home) LUCID_HOME="${2:?--home needs a path}"; shift 2 ;;
    --dest) DEST="${2:?--dest needs a path}"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    --print-manifest) PRINT_MANIFEST=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'backup: unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

# print_manifest emits the canonical ADR-0002 backup set, one entry per line,
# as: "<type>\t<home-relative-path>[\texclude=<csv>]". It MUST stay identical
# to deploy.BackupManifest() (a Go test asserts the equality).
print_manifest() {
  printf 'dir\traw\n'
  printf 'dir\tobservations\n'
  printf 'dir\tregistries\n'
  printf 'dir\tengine\texclude=engine/status.json\n'
  printf 'file\tprojections/exports.log\n'
}

if [ "$PRINT_MANIFEST" -eq 1 ]; then
  print_manifest
  exit 0
fi

if [ -z "$DEST" ]; then
  printf 'backup: --dest is required (destination directory for the backup set)\n' >&2
  exit 2
fi
if [ ! -d "$LUCID_HOME" ]; then
  printf 'backup: Ledger home not found: %s\n' "$LUCID_HOME" >&2
  exit 1
fi

# backup_dir copies a home-relative directory into DEST, preserving structure.
# An absent source directory is skipped with a note (a fresh Ledger may not
# have every tree yet) — never an error.
backup_dir() {
  local rel="$1" src="$LUCID_HOME/$1"
  if [ ! -d "$src" ]; then
    printf 'skip (absent): %s/\n' "$rel" >&2
    return 0
  fi
  if [ "$DRY_RUN" -eq 1 ]; then
    printf 'would copy dir:  %s/\n' "$rel"
    return 0
  fi
  mkdir -p "$DEST/$(dirname "$rel")"
  cp -R "$src" "$DEST/$rel"
}

# backup_file copies a single home-relative file into DEST.
backup_file() {
  local rel="$1" src="$LUCID_HOME/$1"
  if [ ! -f "$src" ]; then
    printf 'skip (absent): %s\n' "$rel" >&2
    return 0
  fi
  if [ "$DRY_RUN" -eq 1 ]; then
    printf 'would copy file: %s\n' "$rel"
    return 0
  fi
  mkdir -p "$DEST/$(dirname "$rel")"
  cp "$src" "$DEST/$rel"
}

# prune removes a home-relative path from the DEST copy — the one exclusion is
# the derived engine/status.json, which rides along in the engine/ dir copy.
prune() {
  local rel="$1"
  if [ "$DRY_RUN" -eq 1 ]; then
    printf 'would exclude:   %s\n' "$rel"
    return 0
  fi
  rm -f "$DEST/$rel"
}

[ "$DRY_RUN" -eq 1 ] || mkdir -p "$DEST"

backup_dir raw
backup_dir observations
backup_dir registries
backup_dir engine
prune engine/status.json
backup_file projections/exports.log

if [ "$DRY_RUN" -eq 1 ]; then
  printf 'backup: dry run — nothing written (ADR-0002 set from %s)\n' "$LUCID_HOME" >&2
else
  printf 'backup: ADR-0002 set copied from %s to %s\n' "$LUCID_HOME" "$DEST" >&2
fi
