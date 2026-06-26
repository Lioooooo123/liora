#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE="${1:-$ROOT}"
DAEMON_ADDR="${LIORA_TUI_SMOKE_DAEMON_ADDR:-127.0.0.1:19090}"
LLM_ADDR="${LIORA_TUI_SMOKE_LLM_ADDR:-127.0.0.1:19091}"
TMP_DIR="$(mktemp -d)"

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

# Fake OpenAI-compatible chat server; keeps TUI smoke deterministic and keyless.
python3 - "$LLM_ADDR" >"$TMP_DIR/llm.log" 2>&1 <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

host, port = sys.argv[1].split(":")

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({
            "choices": [
                {"message": {"role": "assistant", "content": "list ."}}
            ]
        }).encode())

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

(
  cd "$ROOT"
  LIORA_HOME="$TMP_DIR/home" go run ./cmd/coding-agent \
    -workspace "$WORKSPACE" \
    -daemon \
    -daemon-addr "$DAEMON_ADDR" \
    -llm-api-key test-key \
    -llm-base-url "http://$LLM_ADDR" \
    -llm-model test-model
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

STREAM_OUT="$TMP_DIR/stream.out"
printf '/tools\n看看目录\n/timeline\n/exit\n' | (
  cd "$ROOT"
  LIORA_HOME="$TMP_DIR/home" go run ./cmd/coding-agent \
    -workspace "$WORKSPACE" \
    -interactive \
    -tui-daemon \
    -daemon-addr "$DAEMON_ADDR" \
    -llm-api-key test-key \
    -llm-base-url "http://$LLM_ADDR" \
    -llm-model test-model
) >"$STREAM_OUT"

grep -q 'Plan' "$STREAM_OUT"
grep -q 'Tools' "$STREAM_OUT"
grep -q 'MCP tools' "$STREAM_OUT"
grep -q 'mcp fake echo <json arguments>' "$STREAM_OUT"
grep -q 'Timeline session_' "$STREAM_OUT"
grep -q 'user: 看看目录' "$STREAM_OUT"
grep -q 'tool.result' "$STREAM_OUT"

CANCEL_OUT="$TMP_DIR/cancel.out"
printf 'run sleep 10\n/cancel\n/exit\n' | (
  cd "$ROOT"
  LIORA_HOME="$TMP_DIR/home-cancel" go run ./cmd/coding-agent \
    -workspace "$WORKSPACE" \
    -interactive \
    -tui-daemon \
    -daemon-addr "$DAEMON_ADDR" \
    -llm-api-key test-key \
    -llm-base-url "http://$LLM_ADDR" \
    -llm-model test-model
) >"$CANCEL_OUT"

grep -q 'Cancelled task' "$CANCEL_OUT"
grep -q 'cancelled' "$CANCEL_OUT"

echo "tui smoke ok: daemon=$DAEMON_ADDR llm=$LLM_ADDR"
