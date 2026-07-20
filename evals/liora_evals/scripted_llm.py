from __future__ import annotations

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from .cases import EvalCase


class ScriptedLLMServer:
    """Local OpenAI-compatible model that replays benchmark tool calls."""

    def __init__(self, cases: list[EvalCase]):
        self._steps_by_prompt = {
            case.input: case.scripted_steps for case in cases
        }
        self._server: ThreadingHTTPServer | None = None
        self._thread: threading.Thread | None = None
        self.base_url = ""

    def __enter__(self) -> "ScriptedLLMServer":
        steps_by_prompt = self._steps_by_prompt

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                length = int(self.headers.get("Content-Length", "0"))
                raw = self.rfile.read(length).decode() if length else "{}"
                payload = json.loads(raw)
                messages = payload.get("messages") or []
                prompt = _benchmark_prompt(messages, steps_by_prompt)
                completed = sum(
                    1 for message in messages if message.get("role") == "tool"
                )
                steps = steps_by_prompt[prompt]
                if completed >= len(steps):
                    message: dict[str, Any] = {
                        "role": "assistant",
                        "content": "Benchmark task completed.",
                    }
                else:
                    step = steps[completed]
                    message = {
                        "role": "assistant",
                        "content": "",
                        "tool_calls": [
                            {
                                "id": f"benchmark_{completed + 1}",
                                "type": "function",
                                "function": {
                                    "name": step["tool"],
                                    "arguments": json.dumps(
                                        step["arguments"], ensure_ascii=False
                                    ),
                                },
                            }
                        ],
                    }
                body = json.dumps({"choices": [{"message": message}]}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_: object) -> None:
                return

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        host, port = self._server.server_address
        self.base_url = f"http://{host}:{port}"
        self._thread = threading.Thread(
            target=self._server.serve_forever,
            name="liora-scripted-llm",
            daemon=True,
        )
        self._thread.start()
        return self

    def __exit__(self, *_: object) -> None:
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()
        if self._thread is not None:
            self._thread.join(timeout=5)


def _benchmark_prompt(
    messages: list[dict[str, Any]], steps_by_prompt: dict[str, list[dict[str, Any]]]
) -> str:
    user_messages = [
        message.get("content", "")
        for message in messages
        if message.get("role") == "user"
    ]
    for candidate in reversed(user_messages):
        if candidate in steps_by_prompt:
            return candidate
    raise ValueError("request does not contain a registered benchmark prompt")
