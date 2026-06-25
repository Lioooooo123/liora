#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE="${1:-$ROOT}"
ADDR="${LIORA_DAEMON_ADDR:-127.0.0.1:18080}"

go run ./cmd/coding-agent -daemon -daemon-addr "$ADDR" &
PID="$!"
trap 'kill "$PID" >/dev/null 2>&1 || true' EXIT

for _ in $(seq 1 50); do
  if curl -fsS "http://$ADDR/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

TASK_JSON="$(
  curl -fsS "http://$ADDR/v1/tasks" \
    -H 'Content-Type: application/json' \
    -d "{\"workspace\":\"$WORKSPACE\",\"prompt\":\"run pwd\",\"natural\":false}"
)"

TASK_ID="$(printf '%s' "$TASK_JSON" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
if [[ -z "$TASK_ID" ]]; then
  echo "missing task id in response: $TASK_JSON" >&2
  exit 1
fi

curl -fsS "http://$ADDR/v1/tasks/$TASK_ID/events" >/dev/null
curl -fsS "http://$ADDR/v1/tasks/$TASK_ID/events/stream" | grep -q 'event: sandbox.run'

APPLY_WORKSPACE="$(mktemp -d)"
APPLY_TASK_JSON="$(
  curl -fsS "http://$ADDR/v1/tasks" \
    -H 'Content-Type: application/json' \
    -d "{\"workspace\":\"$APPLY_WORKSPACE\",\"prompt\":\"manual patch\",\"natural\":false,\"run_async\":true}"
)"
APPLY_TASK_ID="$(printf '%s' "$APPLY_TASK_JSON" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
PATCH='--- a/smoke.txt
+++ b/smoke.txt
@@ -0,0 +1 @@
+ok
'
curl -fsS "http://$ADDR/v1/tasks/$APPLY_TASK_ID/diff" >/dev/null 2>&1 || true
curl -fsS "http://$ADDR/v1/tasks/$APPLY_TASK_ID/apply" \
  -H 'Content-Type: application/json' \
  -d "$(printf '{"patch":%s}' "$(printf '%s' "$PATCH" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')")" >/dev/null
grep -q '^ok$' "$APPLY_WORKSPACE/smoke.txt"
echo "daemon smoke ok: $TASK_ID apply=$APPLY_TASK_ID"
