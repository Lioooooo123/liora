#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ARCHIVE="${1:-}"
if [ -z "$ARCHIVE" ]; then
  echo "usage: scripts/release-smoke.sh dist/liora_<version>_<goos>_<goarch>.tar.gz" >&2
  exit 2
fi
if [ ! -f "$ARCHIVE" ]; then
  echo "archive not found: $ARCHIVE" >&2
  exit 2
fi

"$SCRIPT_DIR/release-supply-chain-audit.sh" "$ARCHIVE" >/tmp/liora-release-supply-chain.log

WORK_DIR=$(mktemp -d)
INSTALL_DIR="$WORK_DIR/bin"
trap 'rm -rf "$WORK_DIR"' EXIT

tar -xzf "$ARCHIVE" -C "$WORK_DIR"
PACKAGE_DIR="$(find "$WORK_DIR" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
if [ ! -x "$PACKAGE_DIR/install.sh" ]; then
  echo "package is missing executable install.sh" >&2
  exit 1
fi
for path in \
  "$PACKAGE_DIR/README.md" \
  "$PACKAGE_DIR/docs/README.md" \
  "$PACKAGE_DIR/docs/liora-1.0-plan.md" \
  "$PACKAGE_DIR/docs/coding-agent-architecture-plan.md" \
  "$PACKAGE_DIR/docs/tech-stack-selection.md" \
  "$PACKAGE_DIR/docs/release.md" \
  "$PACKAGE_DIR/docs/mvp-exit-benchmark.md" \
  "$PACKAGE_DIR/docs/v0.1-exit-audit.md" \
  "$PACKAGE_DIR/bin/liora"
do
  if [ ! -e "$path" ]; then
    echo "package is missing $path" >&2
    exit 1
  fi
done

LIORA_INSTALL_DIR="$INSTALL_DIR" "$PACKAGE_DIR/install.sh" >/tmp/liora-release-install.log
"$INSTALL_DIR/liora" -version | grep -q 'liora '
LIORA_HOME="$WORK_DIR/home" "$INSTALL_DIR/liora" -doctor >"$WORK_DIR/doctor.log"
grep -q 'Liora doctor' "$WORK_DIR/doctor.log"
grep -q 'database: ok' "$WORK_DIR/doctor.log"

SMOKE_WORKSPACE="$WORK_DIR/arbitrary-workspace"
mkdir -p "$SMOKE_WORKSPACE"
printf 'installed smoke\n' >"$SMOKE_WORKSPACE/workspace-smoke.txt"
(
  cd "$WORK_DIR"
  LIORA_HOME="$WORK_DIR/home" "$INSTALL_DIR/liora" \
    -workspace "$SMOKE_WORKSPACE" \
    -prompt 'list .'
) >"$WORK_DIR/installed-workspace-smoke.log"
grep -q 'workspace-smoke.txt' "$WORK_DIR/installed-workspace-smoke.log"

printf 'release smoke ok: %s\n' "$ARCHIVE"
