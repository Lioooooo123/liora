#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
WORK_DIR=$(mktemp -d)
PACKAGE_DIR="$WORK_DIR/package"
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$PACKAGE_DIR/apps" "$PACKAGE_DIR/scripts"
cp "$PROJECT_DIR/package.json" "$PACKAGE_DIR/package.json"
cp "$PROJECT_DIR/go.mod" "$PACKAGE_DIR/go.mod"
cp "$PROJECT_DIR/go.sum" "$PACKAGE_DIR/go.sum"
cp "$PROJECT_DIR/README.md" "$PACKAGE_DIR/README.md"
cp -R "$PROJECT_DIR/apps/cli" "$PACKAGE_DIR/apps/cli"
cp -R "$PROJECT_DIR/internal" "$PACKAGE_DIR/internal"
cp -R "$PROJECT_DIR/scripts/npm" "$PACKAGE_DIR/scripts/npm"
rm -rf "$PACKAGE_DIR/bin"

SMOKE_WORKSPACE="$WORK_DIR/arbitrary-workspace"
mkdir -p "$SMOKE_WORKSPACE"
printf 'npm lazy smoke\n' >"$SMOKE_WORKSPACE/workspace-smoke.txt"

(
  cd "$WORK_DIR"
  LIORA_HOME="$WORK_DIR/home" \
  GOTOOLCHAIN="${GOTOOLCHAIN:-local}" \
  node "$PACKAGE_DIR/scripts/npm/liora.cjs" -doctor
) >"$WORK_DIR/npm-doctor.log"

if [ ! -x "$PACKAGE_DIR/bin/liora" ]; then
  echo "npm lazy build did not create executable binary" >&2
  exit 1
fi
grep -q 'Liora doctor' "$WORK_DIR/npm-doctor.log"
grep -q 'database: ok' "$WORK_DIR/npm-doctor.log"

(
  cd "$WORK_DIR"
  LIORA_HOME="$WORK_DIR/home" \
  GOTOOLCHAIN="${GOTOOLCHAIN:-local}" \
  node "$PACKAGE_DIR/scripts/npm/liora.cjs" \
    -workspace "$SMOKE_WORKSPACE" \
    -prompt 'list .'
) >"$WORK_DIR/npm-workspace-smoke.log"
grep -q 'workspace-smoke.txt' "$WORK_DIR/npm-workspace-smoke.log"

printf 'npm lazy smoke ok: %s\n' "$PACKAGE_DIR/bin/liora"
