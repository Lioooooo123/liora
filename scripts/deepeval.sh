#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-deterministic}"

if ! command -v uv >/dev/null 2>&1; then
  echo "uv is required: https://docs.astral.sh/uv/" >&2
  exit 1
fi

case "$MODE" in
  contract)
    TARGET="tests/test_contract.py"
    RUNNER="pytest"
    ;;
  deterministic)
    TARGET="tests"
    RUNNER="deepeval"
    MARKER="not live"
    ;;
  live)
    TARGET="tests/test_live_agent.py"
    RUNNER="deepeval"
    MARKER=""
    export LIORA_DEEPEVAL_LIVE=1
    ;;
  *)
    echo "usage: $0 [contract|deterministic|live]" >&2
    exit 2
    ;;
esac

cd "$ROOT/evals"
if [[ "$RUNNER" == "pytest" ]]; then
  exec uv run --frozen pytest "$TARGET" --tb=short
fi
if [[ -n "$MARKER" ]]; then
  exec uv run --frozen deepeval test run "$TARGET" -- --tb=short -m "$MARKER"
fi
exec uv run --frozen deepeval test run "$TARGET" -- --tb=short
