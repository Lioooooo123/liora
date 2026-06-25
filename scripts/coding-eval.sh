#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DAEMON_ADDR="${LIORA_EVAL_DAEMON_ADDR:-127.0.0.1:19150}"
LLM_ADDR="${LIORA_EVAL_LLM_ADDR:-127.0.0.1:19151}"
TMP_DIR="$(mktemp -d)"
WORKSPACE="$TMP_DIR/workspace"

cleanup() {
  if [[ -n "${DAEMON_PID:-}" ]]; then
    kill "$DAEMON_PID" >/dev/null 2>&1 || true
    wait "$DAEMON_PID" >/dev/null 2>&1 || true
  fi
  if [[ -n "${LLM_PID:-}" ]]; then
    kill "$LLM_PID" >/dev/null 2>&1 || true
    wait "$LLM_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$WORKSPACE"
printf 'hello old agent\n' >"$WORKSPACE/app.txt"
printf 'Liora eval workspace\n' >"$WORKSPACE/README.md"

json_get() {
  python3 -c '
import json
import sys

path = sys.argv[1].split(".")
data = json.load(sys.stdin)
for part in path:
    data = data[part]
print(data)
' "$1"
}

wait_task_status() {
  local task_id="$1"
  local want="$2"
  for _ in $(seq 1 100); do
    local body
    body="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$task_id")"
    local status
    status="$(printf '%s' "$body" | json_get status)"
    if [[ "$status" == "$want" ]]; then
      return 0
    fi
    sleep 0.1
  done
  echo "task $task_id did not reach status $want" >&2
  curl -fsS "http://$DAEMON_ADDR/v1/tasks/$task_id" >&2 || true
  return 1
}

# Fake OpenAI-compatible planner for deterministic natural-language eval tasks.
python3 - "$LLM_ADDR" >"$TMP_DIR/llm.log" 2>&1 <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

host, port = sys.argv[1].split(":")

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length).decode()
        if "old" in raw and "new" in raw:
            content = "read app.txt\nreplace app.txt old new\ndiff"
        elif "目录" in raw or "folder" in raw:
            content = "list ."
        else:
            content = "list ."
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({
            "choices": [
                {"message": {"role": "assistant", "content": content}}
            ]
        }).encode())

    def log_message(self, *_):
        pass

HTTPServer((host, int(port)), Handler).serve_forever()
PY
LLM_PID="$!"

(
  cd "$ROOT"
  LIORA_HOME="$TMP_DIR/home" LIORA_PATCH_MODE=1 LIORA_PERMISSION=prompt go run ./cmd/coding-agent \
    -workspace "$WORKSPACE" \
    -daemon \
    -daemon-addr "$DAEMON_ADDR" \
    -llm-api-key eval-key \
    -llm-base-url "http://$LLM_ADDR" \
    -llm-model eval-model
) >"$TMP_DIR/daemon.log" 2>&1 &
DAEMON_PID="$!"

READY=0
for _ in $(seq 1 100); do
  if curl -fsS "http://$DAEMON_ADDR/healthz" >/dev/null 2>&1; then
    READY=1
    break
  fi
  sleep 0.1
done
if [[ "$READY" != "1" ]]; then
  echo "daemon did not become healthy at http://$DAEMON_ADDR/healthz" >&2
  cat "$TMP_DIR/daemon.log" >&2 || true
  exit 1
fi

TASK_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "把 app.txt 里的 old 改成 new 并输出 diff",
    "natural": True,
    "run_async": True,
}))
PY
)"
TASK_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$TASK_BODY")"
TASK_ID="$(printf '%s' "$TASK_JSON" | json_get task.id)"
SESSION_ID="$(printf '%s' "$TASK_JSON" | json_get task.session_id)"

STREAM_BODY="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$TASK_ID/events/stream")"
printf '%s' "$STREAM_BODY" | grep -q 'event: task.plan_ready'
printf '%s' "$STREAM_BODY" | grep -q 'event: tool.result'
printf '%s' "$STREAM_BODY" | grep -q 'event: task.diff'
printf '%s' "$STREAM_BODY" | grep -q 'event: task.completed'
grep -q '^hello old agent$' "$WORKSPACE/app.txt"

DIFF_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$TASK_ID/diff")"
printf '%s' "$DIFF_JSON" | grep -q 'app.txt'
PATCH_VALUE="$(printf '%s' "$DIFF_JSON" | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["diff"]))')"
APPLY_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$TASK_ID/apply" \
  -H 'Content-Type: application/json' \
  -d "$(printf '{"patch":%s}' "$PATCH_VALUE")")"
printf '%s' "$APPLY_JSON" | grep -q 'app.txt'
grep -q '^hello new agent$' "$WORKSPACE/app.txt"

EVENTS_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$TASK_ID/events")"
printf '%s' "$EVENTS_JSON" | grep -q 'task.patch_applied'
TIMELINE_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/sessions/$SESSION_ID/timeline")"
printf '%s' "$TIMELINE_JSON" | grep -q '把 app.txt'
printf '%s' "$TIMELINE_JSON" | grep -q 'tool_result'
printf '%s' "$TIMELINE_JSON" | grep -q 'diff'

APPROVE_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "run rm -rf build",
    "natural": False,
}))
PY
)"
APPROVE_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$APPROVE_BODY")"
APPROVE_TASK_ID="$(printf '%s' "$APPROVE_JSON" | json_get task.id)"
APPROVE_STATUS="$(printf '%s' "$APPROVE_JSON" | json_get task.status)"
if [[ "$APPROVE_STATUS" != "waiting_user" ]]; then
  echo "expected permission task to wait, got $APPROVE_STATUS" >&2
  exit 1
fi
curl -fsS "http://$DAEMON_ADDR/v1/tasks/$APPROVE_TASK_ID/events/stream" | grep -q 'event: permission.requested'
curl -fsS "http://$DAEMON_ADDR/v1/tasks/$APPROVE_TASK_ID/approval" \
  -H 'Content-Type: application/json' \
  -d '{"decision":"approve"}' >/dev/null
wait_task_status "$APPROVE_TASK_ID" "completed"
curl -fsS "http://$DAEMON_ADDR/v1/tasks/$APPROVE_TASK_ID/events" | grep -q 'permission.approved'

DENY_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "run rm -rf denied-build",
    "natural": False,
}))
PY
)"
DENY_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$DENY_BODY")"
DENY_TASK_ID="$(printf '%s' "$DENY_JSON" | json_get task.id)"
DENY_STATUS="$(printf '%s' "$DENY_JSON" | json_get task.status)"
if [[ "$DENY_STATUS" != "waiting_user" ]]; then
  echo "expected denied permission task to wait, got $DENY_STATUS" >&2
  exit 1
fi
curl -fsS "http://$DAEMON_ADDR/v1/tasks/$DENY_TASK_ID/approval" \
  -H 'Content-Type: application/json' \
  -d '{"decision":"deny","reason":"eval deny"}' >/dev/null
wait_task_status "$DENY_TASK_ID" "cancelled"
curl -fsS "http://$DAEMON_ADDR/v1/tasks/$DENY_TASK_ID/events" | grep -q 'permission.denied'

CANCEL_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "run sleep 10",
    "natural": False,
    "run_async": True,
}))
PY
)"
CANCEL_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$CANCEL_BODY")"
CANCEL_TASK_ID="$(printf '%s' "$CANCEL_JSON" | json_get task.id)"
curl -fsS "http://$DAEMON_ADDR/v1/tasks/$CANCEL_TASK_ID/cancel" \
  -H 'Content-Type: application/json' \
  -d '{"reason":"eval stop"}' >/dev/null
curl -fsS "http://$DAEMON_ADDR/v1/tasks/$CANCEL_TASK_ID/events/stream" | grep -q 'event: task.cancelled'

echo "coding eval ok: task=$TASK_ID session=$SESSION_ID approve=$APPROVE_TASK_ID deny=$DENY_TASK_ID cancel=$CANCEL_TASK_ID"
