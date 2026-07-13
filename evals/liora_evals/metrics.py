from __future__ import annotations

import json
from typing import Any

from deepeval.metrics import BaseMetric
from deepeval.test_case import LLMTestCase


class AgentContractMetric(BaseMetric):
    """Deterministically score a Liora run against its workspace contract."""

    def __init__(self, threshold: float = 1.0):
        self.threshold = threshold
        self.score: float | None = None
        self.success: bool | None = None
        self.reason: str | None = None
        self.error: str | None = None
        self.async_mode = False
        self.strict_mode = threshold == 1.0
        self.include_reason = True
        self.evaluation_model = "deterministic"

    def measure(self, test_case: LLMTestCase) -> float:
        try:
            actual = _parse_object(test_case.actual_output, "actual_output")
            expected = _parse_object(test_case.expected_output, "expected_output")
            checks = _contract_checks(actual, expected)
            passed = [name for name, ok in checks if ok]
            failed = [name for name, ok in checks if not ok]
            self.score = len(passed) / len(checks) if checks else 1.0
            self.success = self.score >= self.threshold
            self.reason = (
                f"通过 {len(passed)}/{len(checks)} 项"
                + (f"；失败：{', '.join(failed)}" if failed else "")
            )
            return self.score
        except Exception as exc:
            self.error = str(exc)
            self.success = False
            raise

    async def a_measure(self, test_case: LLMTestCase) -> float:
        return self.measure(test_case)

    def is_successful(self) -> bool:
        if self.error is not None or self.score is None:
            self.success = False
        else:
            self.success = self.score >= self.threshold
        return self.success

    @property
    def __name__(self) -> str:
        return "Liora Agent Contract"


def _parse_object(value: str | None, field: str) -> dict[str, Any]:
    if not value:
        raise ValueError(f"{field} must contain a JSON object")
    parsed = json.loads(value)
    if not isinstance(parsed, dict):
        raise ValueError(f"{field} must contain a JSON object")
    return parsed


def _contract_checks(
    actual: dict[str, Any], expected: dict[str, Any]
) -> list[tuple[str, bool]]:
    checks: list[tuple[str, bool]] = [
        (
            f"status={expected.get('status', 'completed')}",
            actual.get("status") == expected.get("status", "completed"),
        )
    ]

    actual_files = actual.get("files") or {}
    for path, content in (expected.get("files") or {}).items():
        checks.append((f"file:{path}", actual_files.get(path) == content))

    called_tools = set(actual.get("tools_called") or [])
    for tool in expected.get("expected_tools") or []:
        checks.append((f"tool:{tool}", tool in called_tools))

    allowed = set(expected.get("allowed_changed_files") or [])
    changed = set(actual.get("changed_files") or [])
    checks.append(("no-unexpected-mutations", changed <= allowed))
    return checks
