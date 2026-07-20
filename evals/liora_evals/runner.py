from __future__ import annotations

import json
import os
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

from .cases import EvalCase


TERMINAL_STATUSES = {"completed", "failed", "cancelled", "lost", "stale"}


class LioraDaemon:
    def __init__(
        self,
        repo_root: Path,
        timeout: float = 120.0,
        *,
        llm_base_url: str | None = None,
        llm_api_key: str | None = None,
        llm_model: str | None = None,
        llm_provider: str | None = None,
    ):
        self.repo_root = repo_root.resolve()
        self.timeout = timeout
        self.llm_base_url = llm_base_url
        self.llm_api_key = llm_api_key
        self.llm_model = llm_model
        self.llm_provider = llm_provider
        self._tempdir: tempfile.TemporaryDirectory[str] | None = None
        self._process: subprocess.Popen[str] | None = None
        self._base_url = ""
        self._binary = Path()

    def __enter__(self) -> "LioraDaemon":
        self._require_live_config()
        self._tempdir = tempfile.TemporaryDirectory(prefix="liora-deepeval-")
        temp_root = Path(self._tempdir.name)
        configured_binary = os.environ.get("LIORA_DEEPEVAL_BINARY")
        if configured_binary:
            self._binary = Path(configured_binary).expanduser().resolve()
        else:
            self._binary = temp_root / "liora"
            subprocess.run(
                ["go", "build", "-o", str(self._binary), "./apps/cli"],
                cwd=self.repo_root,
                check=True,
            )

        port = _free_port()
        self._base_url = f"http://127.0.0.1:{port}"
        env = os.environ.copy()
        env.update(
            {
                "LIORA_HOME": str(temp_root / "home"),
                "LIORA_PATCH_MODE": "0",
                "LIORA_PERMISSION": "auto",
                "PYTHONDONTWRITEBYTECODE": "1",
            }
        )
        command = [
            str(self._binary),
            "-daemon",
            "-daemon-addr",
            f"127.0.0.1:{port}",
        ]
        for flag, value in (
            ("-llm-provider", self.llm_provider),
            ("-llm-base-url", self.llm_base_url),
            ("-llm-api-key", self.llm_api_key),
            ("-llm-model", self.llm_model),
        ):
            if value:
                command.extend([flag, value])
        self._process = subprocess.Popen(
            command,
            cwd=self.repo_root,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        self._wait_until_healthy()
        return self

    def __exit__(self, *_: object) -> None:
        if self._process is not None:
            self._process.terminate()
            try:
                self._process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self._process.kill()
                self._process.wait(timeout=5)
        if self._tempdir is not None:
            self._tempdir.cleanup()

    def run_case(self, case: EvalCase) -> str:
        if self._tempdir is None:
            raise RuntimeError("LioraDaemon must be used as a context manager")
        workspace = Path(self._tempdir.name) / "workspaces" / case.name
        workspace.mkdir(parents=True)
        for relative_path, content in case.files.items():
            target = workspace / relative_path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(content, encoding="utf-8")
        before = _snapshot(workspace)
        started_at = time.monotonic()

        created = self._request(
            "POST",
            "/v1/tasks",
            {
                "workspace": str(workspace),
                "prompt": case.input,
                "natural": True,
                "run_async": True,
            },
        )
        task_id = created["task"]["id"]
        deadline = time.monotonic() + self.timeout
        task: dict[str, Any]
        while True:
            task = self._request("GET", f"/v1/tasks/{task_id}")
            status = task["status"]
            if status in TERMINAL_STATUSES:
                break
            if status == "waiting_user":
                raise RuntimeError(f"task {task_id} unexpectedly requires user input")
            if time.monotonic() >= deadline:
                raise TimeoutError(f"task {task_id} exceeded {self.timeout:.0f}s")
            time.sleep(0.25)

        events = self._request("GET", f"/v1/tasks/{task_id}/events")
        payloads = [_event_payload(event) for event in events]
        tools_called = sorted(
            {
                payload.get("tool")
                for event, payload in zip(events, payloads)
                if event.get("type") == "tool.call" and payload.get("tool")
            }
        )
        successful_tools = sorted(
            {
                payload.get("tool")
                for event, payload in zip(events, payloads)
                if event.get("type") == "tool.result"
                and payload.get("status") == "ok"
                and payload.get("tool")
            }
        )
        after = _snapshot(workspace)
        result = {
            "status": task["status"],
            "files": after,
            "tools_called": tools_called,
            "successful_tools": successful_tools,
            "event_types": sorted(
                {event.get("type") for event in events if event.get("type")}
            ),
            "tool_call_count": sum(
                1 for event in events if event.get("type") == "tool.call"
            ),
            "duration_ms": round((time.monotonic() - started_at) * 1000),
            "changed_files": sorted(
                path for path in set(before) | set(after) if before.get(path) != after.get(path)
            ),
        }
        return json.dumps(result, ensure_ascii=False, sort_keys=True)

    def _request(
        self, method: str, path: str, body: dict[str, Any] | None = None
    ) -> Any:
        data = None
        headers: dict[str, str] = {}
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(
            self._base_url + path, data=data, headers=headers, method=method
        )
        with urllib.request.urlopen(request, timeout=10) as response:
            return json.load(response)

    def _wait_until_healthy(self) -> None:
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if self._process is not None and self._process.poll() is not None:
                output = self._process.stdout.read() if self._process.stdout else ""
                raise RuntimeError(f"Liora daemon exited during startup:\n{output}")
            try:
                self._request("GET", "/healthz")
                return
            except (urllib.error.URLError, TimeoutError):
                time.sleep(0.1)
        raise TimeoutError("Liora daemon did not become healthy within 20s")

    def _require_live_config(self) -> None:
        if self.llm_api_key and self.llm_model:
            return
        missing = [
            name
            for name in ("LIORA_LLM_API_KEY", "LIORA_LLM_MODEL")
            if not os.environ.get(name)
        ]
        if missing:
            raise RuntimeError("live eval requires: " + ", ".join(missing))


def _event_payload(event: dict[str, Any]) -> dict[str, Any]:
    raw = event.get("payload_json") or "{}"
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError:
        return {}
    return payload if isinstance(payload, dict) else {}


def _snapshot(root: Path) -> dict[str, str]:
    snapshot: dict[str, str] = {}
    for path in root.rglob("*"):
        if path.is_file():
            relative = path.relative_to(root).as_posix()
            try:
                snapshot[relative] = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                snapshot[relative] = "<binary>"
    return snapshot


def _free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])
