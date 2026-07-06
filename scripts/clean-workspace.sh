#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
DRY_RUN=0

usage() {
  cat <<'USAGE'
usage: scripts/clean-workspace.sh [--dry-run]

Remove local generated artifacts that are safe to recreate:
  dist/
  bin/
  /cli
  /liora-demo
  packages/*/dist/
  packages/*/*.tsbuildinfo

This script intentionally keeps dependency folders, tool state, and local notes:
  node_modules/
  .omo/
  .superpowers/
  .codegraph/
  implementation-notes.md
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run)
      DRY_RUN=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

remove_path() {
  path="$1"
  if [ ! -e "$path" ] && [ ! -L "$path" ]; then
    return
  fi
  rel=${path#"$PROJECT_DIR"/}
  if [ "$DRY_RUN" -eq 1 ]; then
    printf 'would remove %s\n' "$rel"
  else
    rm -rf -- "$path"
    printf 'removed %s\n' "$rel"
  fi
}

remove_path "$PROJECT_DIR/dist"
remove_path "$PROJECT_DIR/bin"
remove_path "$PROJECT_DIR/cli"
remove_path "$PROJECT_DIR/liora-demo"

for path in "$PROJECT_DIR"/packages/*/dist "$PROJECT_DIR"/packages/*/*.tsbuildinfo; do
  [ -e "$path" ] || [ -L "$path" ] || continue
  remove_path "$path"
done
