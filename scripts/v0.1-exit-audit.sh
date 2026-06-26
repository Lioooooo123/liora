#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE="${1:-$ROOT}"
SKIP_GIT_CLEAN=0
if [[ "${1:-}" == "--skip-git-clean" ]]; then
  SKIP_GIT_CLEAN=1
  WORKSPACE="${2:-$ROOT}"
fi

GO_TOOLCHAIN="${GOTOOLCHAIN:-local}"
AUDIT_TMP="$(mktemp -d)"
trap 'rm -rf "$AUDIT_TMP"' EXIT

echo "[1/7] go test"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go test -count=1 ./...
)

echo "[2/7] diff check"
(
  cd "$ROOT"
  git diff --check
)

echo "[3/7] daemon smoke"
(
  cd "$ROOT"
  LIORA_HOME="$(mktemp -d)" \
  LIORA_DAEMON_ADDR="${LIORA_AUDIT_DAEMON_ADDR:-127.0.0.1:19401}" \
  GOTOOLCHAIN="$GO_TOOLCHAIN" \
  ./scripts/daemon-smoke.sh "$WORKSPACE"
)

echo "[4/7] tui smoke"
(
  cd "$ROOT"
  LIORA_TUI_SMOKE_DAEMON_ADDR="${LIORA_AUDIT_TUI_DAEMON_ADDR:-127.0.0.1:19402}" \
  LIORA_TUI_SMOKE_LLM_ADDR="${LIORA_AUDIT_TUI_LLM_ADDR:-127.0.0.1:19403}" \
  GOTOOLCHAIN="$GO_TOOLCHAIN" \
  ./scripts/tui-smoke.sh "$WORKSPACE"
)

echo "[5/7] coding eval"
(
  cd "$ROOT"
  LIORA_EVAL_DAEMON_ADDR="${LIORA_AUDIT_EVAL_DAEMON_ADDR:-127.0.0.1:19404}" \
  LIORA_EVAL_LLM_ADDR="${LIORA_AUDIT_EVAL_LLM_ADDR:-127.0.0.1:19405}" \
  GOTOOLCHAIN="$GO_TOOLCHAIN" \
  ./scripts/coding-eval.sh
)

echo "[6/7] install and package"
ARCHIVE="$(
  cd "$ROOT"
  INSTALL_DIR="$AUDIT_TMP/install/bin"
  LIORA_INSTALL_DIR="$INSTALL_DIR" GOTOOLCHAIN="$GO_TOOLCHAIN" ./scripts/install-local.sh >"$AUDIT_TMP/install.log"
  "$INSTALL_DIR/liora" -version
  GOTOOLCHAIN="$GO_TOOLCHAIN" ./scripts/package-release.sh
)"
ARCHIVE_PATH="$(printf '%s\n' "$ARCHIVE" | tail -n 1)"

echo "[7/7] release smoke"
(
  cd "$ROOT"
  ./scripts/release-smoke.sh "$ARCHIVE_PATH"
)

if [[ "$SKIP_GIT_CLEAN" != "1" ]]; then
  STATUS="$(cd "$ROOT" && git status --short --branch)"
  printf '%s\n' "$STATUS"
  BRANCH="$(cd "$ROOT" && git rev-parse --abbrev-ref HEAD)"
  if [[ "$BRANCH" != "main" ]]; then
    echo "v0.1 exit audit must run on main, got $BRANCH" >&2
    exit 1
  fi
  if ! UPSTREAM="$(cd "$ROOT" && git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null)"; then
    echo "main has no upstream configured" >&2
    exit 1
  fi
  COUNTS="$(cd "$ROOT" && git rev-list --left-right --count "HEAD...$UPSTREAM")"
  AHEAD="$(printf '%s' "$COUNTS" | awk '{print $1}')"
  BEHIND="$(printf '%s' "$COUNTS" | awk '{print $2}')"
  if [[ "$AHEAD" != "0" || "$BEHIND" != "0" ]]; then
    echo "branch is not synchronized with upstream $UPSTREAM (ahead=$AHEAD behind=$BEHIND)" >&2
    exit 1
  fi
  if [[ -n "$(cd "$ROOT" && git status --porcelain)" ]]; then
    echo "working tree is not clean" >&2
    exit 1
  fi
fi

echo "v0.1 exit audit ok: $ARCHIVE_PATH"
