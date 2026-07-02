#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
WORK_DIR=$(mktemp -d)
INSTALL_DIR="$WORK_DIR/bin"
HOME_DIR="$WORK_DIR/home"
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$HOME_DIR"
(
  cd "$PROJECT_DIR"
  HOME="$HOME_DIR" \
  LIORA_INSTALL_DIR="$INSTALL_DIR" \
  GOTOOLCHAIN="${GOTOOLCHAIN:-local}" \
  ./scripts/install-local.sh
) >"$WORK_DIR/install-local.log"

if [ ! -x "$INSTALL_DIR/liora" ]; then
  echo "local install did not create executable binary" >&2
  exit 1
fi
LIORA_HOME="$WORK_DIR/liora-home" "$INSTALL_DIR/liora" -doctor >"$WORK_DIR/doctor.log"
grep -q 'Liora doctor' "$WORK_DIR/doctor.log"
grep -q 'database: ok' "$WORK_DIR/doctor.log"

SMOKE_WORKSPACE="$WORK_DIR/arbitrary-workspace"
mkdir -p "$SMOKE_WORKSPACE"
printf 'local install smoke\n' >"$SMOKE_WORKSPACE/workspace-smoke.txt"
(
  cd "$WORK_DIR"
  LIORA_HOME="$WORK_DIR/liora-home" "$INSTALL_DIR/liora" \
    -workspace "$SMOKE_WORKSPACE" \
    -prompt 'list .'
) >"$WORK_DIR/local-workspace-smoke.log"
grep -q 'workspace-smoke.txt' "$WORK_DIR/local-workspace-smoke.log"

printf 'local install smoke ok: %s\n' "$INSTALL_DIR/liora"
