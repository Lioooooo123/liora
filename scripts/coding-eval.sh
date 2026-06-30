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

# Fake OpenAI-compatible model that drives the native tool-use loop for the
# deterministic natural-language eval tasks. Each case is a fixed sequence of
# structured tool calls; the server returns the next tool call until every step
# has produced a tool result, then finishes with a plain-text summary. Step
# progress is tracked by counting the tool results already present in the request,
# so it stays stateless across the daemon's per-approval task re-runs.
python3 - "$LLM_ADDR" >"$TMP_DIR/llm.log" 2>&1 <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

host, port = sys.argv[1].split(":")


def steps_for(raw):
    if "docx-case" in raw:
        return [("document", {"path": "assignment.docx"})]
    if "mcp-case" in raw:
        return [("mcp", {"server": "fake", "tool": "echo", "arguments": {"text": "hello from eval"}})]
    if "replan-case" in raw:
        return [("read", {"path": "missing-replan.txt"}), ("read", {"path": "README.md"})]
    if "multi-file" in raw:
        return [
            ("write", {"path": "config/settings.txt", "content": "enabled\n"}),
            ("write", {"path": "docs/guide.txt", "content": "ready\n"}),
        ]
    if "old" in raw and "new" in raw:
        return [
            ("read", {"path": "app.txt"}),
            ("replace", {"path": "app.txt", "old_text": "old", "new_text": "new"}),
        ]
    return [("list", {"path": "."})]


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length).decode() if length else ""
        try:
            payload = json.loads(raw)
        except Exception:
            payload = {}
        messages = payload.get("messages", [])
        completed = sum(1 for m in messages if m.get("role") == "tool")
        steps = steps_for(raw)
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        if completed >= len(steps):
            message = {"role": "assistant", "content": "Done."}
        else:
            name, args = steps[completed]
            message = {
                "role": "assistant",
                "content": "",
                "tool_calls": [{
                    "id": "call_%d" % (completed + 1),
                    "type": "function",
                    "function": {"name": name, "arguments": json.dumps(args)},
                }],
            }
        self.wfile.write(json.dumps({"choices": [{"message": message}]}).encode())

    def log_message(self, *_):
        pass

HTTPServer((host, int(port)), Handler).serve_forever()
PY
LLM_PID="$!"

cat >"$TMP_DIR/fake_mcp.py" <<'PY'
import json
import sys

for line in sys.stdin:
    try:
        req = json.loads(line)
    except Exception:
        continue
    method = req.get("method")
    req_id = req.get("id")
    if method == "notifications/initialized":
        continue
    if method == "initialize":
        result = {
            "protocolVersion": "2025-06-18",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "fake", "version": "0.0.1"},
        }
        print(json.dumps({"jsonrpc": "2.0", "id": req_id, "result": result}), flush=True)
    elif method == "tools/list":
        result = {
            "tools": [{
                "name": "echo",
                "description": "Echo text",
                "inputSchema": {"type": "object"},
            }]
        }
        print(json.dumps({"jsonrpc": "2.0", "id": req_id, "result": result}), flush=True)
    elif method == "tools/call":
        params = req.get("params") or {}
        args = params.get("arguments") or {}
        text = args.get("text", "")
        result = {"content": [{"type": "text", "text": f"mcp echo: {text}"}]}
        print(json.dumps({"jsonrpc": "2.0", "id": req_id, "result": result}), flush=True)
    else:
        error = {"code": -32601, "message": "method not found"}
        print(json.dumps({"jsonrpc": "2.0", "id": req_id, "error": error}), flush=True)
PY
mkdir -p "$TMP_DIR/home"
cat >"$TMP_DIR/home/mcp.json" <<JSON
{
  "servers": {
    "fake": {
      "command": "python3",
      "args": ["$TMP_DIR/fake_mcp.py"]
    }
  }
}
JSON

python3 - "$WORKSPACE/assignment.docx" <<'PY'
import sys
import zipfile

path = sys.argv[1]
xml = """<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Assignment Brief</w:t></w:r></w:p>
    <w:p><w:r><w:t>Build a local companion coding agent.</w:t></w:r></w:p>
  </w:body>
</w:document>
"""
with zipfile.ZipFile(path, "w") as docx:
    docx.writestr("word/document.xml", xml)
PY

(
  cd "$ROOT"
  LIORA_HOME="$TMP_DIR/home" LIORA_PATCH_MODE=1 LIORA_PERMISSION=prompt go run ./apps/cli \
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

MULTI_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "multi-file: create config and guide files",
    "natural": True,
    "run_async": True,
}))
PY
)"
MULTI_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$MULTI_BODY")"
MULTI_TASK_ID="$(printf '%s' "$MULTI_JSON" | json_get task.id)"
MULTI_STREAM_BODY="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$MULTI_TASK_ID/events/stream")"
printf '%s' "$MULTI_STREAM_BODY" | grep -q 'event: task.diff'
if [[ -e "$WORKSPACE/config/settings.txt" || -e "$WORKSPACE/docs/guide.txt" ]]; then
  echo "multi-file patch mode mutated workspace before apply" >&2
  exit 1
fi
MULTI_DIFF_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$MULTI_TASK_ID/diff")"
printf '%s' "$MULTI_DIFF_JSON" | grep -q 'config/settings.txt'
printf '%s' "$MULTI_DIFF_JSON" | grep -q 'docs/guide.txt'
MULTI_PATCH_VALUE="$(printf '%s' "$MULTI_DIFF_JSON" | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["diff"]))')"
MULTI_APPLY_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$MULTI_TASK_ID/apply" \
  -H 'Content-Type: application/json' \
  -d "$(printf '{"patch":%s}' "$MULTI_PATCH_VALUE")")"
printf '%s' "$MULTI_APPLY_JSON" | grep -q 'config/settings.txt'
printf '%s' "$MULTI_APPLY_JSON" | grep -q 'docs/guide.txt'
grep -q '^enabled$' "$WORKSPACE/config/settings.txt"
grep -q '^ready$' "$WORKSPACE/docs/guide.txt"

DOCX_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "docx-case: summarize assignment.docx",
    "natural": True,
    "run_async": True,
}))
PY
)"
DOCX_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$DOCX_BODY")"
DOCX_TASK_ID="$(printf '%s' "$DOCX_JSON" | json_get task.id)"
DOCX_STREAM="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$DOCX_TASK_ID/events/stream")"
[[ "$DOCX_STREAM" == *"event: tool.result"* ]]
[[ "$DOCX_STREAM" == *"document"* ]]
[[ "$DOCX_STREAM" == *"Assignment Brief"* ]]
[[ "$DOCX_STREAM" == *"event: task.completed"* ]]

MCP_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "mcp-case: call the fake echo MCP tool",
    "natural": True,
    "run_async": True,
}))
PY
)"
MCP_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$MCP_BODY")"
MCP_TASK_ID="$(printf '%s' "$MCP_JSON" | json_get task.id)"
MCP_STREAM="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$MCP_TASK_ID/events/stream")"
[[ "$MCP_STREAM" == *"event: permission.requested"* ]]
[[ "$MCP_STREAM" == *"external"* ]]
curl -fsS "http://$DAEMON_ADDR/v1/tasks/$MCP_TASK_ID/approval" \
  -H 'Content-Type: application/json' \
  -d '{"decision":"approve"}' >/dev/null
wait_task_status "$MCP_TASK_ID" "completed"
MCP_EVENTS="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$MCP_TASK_ID/events")"
[[ "$MCP_EVENTS" == *"permission.approved"* ]]
[[ "$MCP_EVENTS" == *"mcp echo: hello from eval"* ]]

REPLAN_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "replan-case: recover from a missing file and read README",
    "natural": True,
    "run_async": True,
}))
PY
)"
REPLAN_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$REPLAN_BODY")"
REPLAN_TASK_ID="$(printf '%s' "$REPLAN_JSON" | json_get task.id)"
REPLAN_STREAM="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$REPLAN_TASK_ID/events/stream")"
[[ "$REPLAN_STREAM" == *"event: task.replanning"* ]]
[[ "$REPLAN_STREAM" == *"missing-replan.txt"* ]]
[[ "$REPLAN_STREAM" == *"event: task.completed"* ]]
curl -fsS "http://$DAEMON_ADDR/v1/tasks/$REPLAN_TASK_ID/events" | grep -q 'README.md'

BIG_OUTPUT_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "run python3 -c 'print(\"x\" * 600000)'",
    "natural": False,
    "run_async": True,
}))
PY
)"
BIG_OUTPUT_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$BIG_OUTPUT_BODY")"
BIG_OUTPUT_TASK_ID="$(printf '%s' "$BIG_OUTPUT_JSON" | json_get task.id)"
BIG_OUTPUT_STREAM="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$BIG_OUTPUT_TASK_ID/events/stream")"
[[ "$BIG_OUTPUT_STREAM" == *"event: tool.result"* ]]
[[ "$BIG_OUTPUT_STREAM" == *"truncated"* ]]
[[ "$BIG_OUTPUT_STREAM" == *"event: task.completed"* ]]

FAIL_BODY="$(python3 - "$WORKSPACE" <<'PY'
import json
import sys
print(json.dumps({
    "workspace": sys.argv[1],
    "prompt": "read missing-eval.txt",
    "natural": False,
    "run_async": True,
}))
PY
)"
FAIL_JSON="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks" -H 'Content-Type: application/json' -d "$FAIL_BODY")"
FAIL_TASK_ID="$(printf '%s' "$FAIL_JSON" | json_get task.id)"
FAIL_STREAM="$(curl -fsS "http://$DAEMON_ADDR/v1/tasks/$FAIL_TASK_ID/events/stream")"
[[ "$FAIL_STREAM" == *"event: tool.result"* ]]
[[ "$FAIL_STREAM" == *'"status":"error"'* ]]
[[ "$FAIL_STREAM" == *"event: task.error"* ]]
[[ "$FAIL_STREAM" == *"missing-eval.txt"* ]]
wait_task_status "$FAIL_TASK_ID" "failed"

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

echo "coding eval ok: task=$TASK_ID session=$SESSION_ID multi=$MULTI_TASK_ID docx=$DOCX_TASK_ID mcp=$MCP_TASK_ID replan=$REPLAN_TASK_ID big_output=$BIG_OUTPUT_TASK_ID fail=$FAIL_TASK_ID approve=$APPROVE_TASK_ID deny=$DENY_TASK_ID cancel=$CANCEL_TASK_ID"
