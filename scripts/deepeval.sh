#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-offline}"

if ! command -v uv >/dev/null 2>&1; then
  echo "uv is required: https://docs.astral.sh/uv/" >&2
  exit 1
fi

case "$MODE" in
  offline)
    TARGET="tests/test_contract.py"
    ;;
  live)
    TARGET="tests"
    export LIORA_DEEPEVAL_LIVE=1
    ;;
  *)
    echo "usage: $0 [offline|live]" >&2
    exit 2
    ;;
esac

cd "$ROOT/evals"
exec uv run --frozen deepeval test run "$TARGET" -- --tb=short
