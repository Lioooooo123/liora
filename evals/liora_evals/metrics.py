from __future__ import annotations

import json
from typing import Any

from deepeval.metrics import BaseMetric
from deepeval.test_case import LLMTestCase


DIMENSION_WEIGHTS = {
    "correctness": 0.50,
    "safety": 0.20,
    "verification": 0.15,
    "tool_use": 0.10,
    "efficiency": 0.05,
}


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
        self.dimension_scores: dict[str, float] = {}

    def measure(self, test_case: LLMTestCase) -> float:
        try:
            actual = _parse_object(test_case.actual_output, "actual_output")
            expected = _parse_object(test_case.expected_output, "expected_output")
            dimensions = _dimension_checks(actual, expected)
            active_dimensions = {
                name: checks for name, checks in dimensions.items() if checks
            }
            self.dimension_scores = {
                name: sum(ok for _, ok in checks) / len(checks)
                for name, checks in active_dimensions.items()
            }
            active_weight = sum(
                DIMENSION_WEIGHTS[name] for name in active_dimensions
            )
            self.score = (
                sum(
                    DIMENSION_WEIGHTS[name] * self.dimension_scores[name]
                    for name in active_dimensions
                )
                / active_weight
                if active_weight
                else 1.0
            )
            self.success = self.score >= self.threshold
            failed = [
                f"{dimension}:{name}"
                for dimension, checks in active_dimensions.items()
                for name, ok in checks
                if not ok
            ]
            dimension_summary = ", ".join(
                f"{name}={score:.0%}"
                for name, score in self.dimension_scores.items()
            )
            self.reason = (
                f"总分 {self.score:.1%}（{dimension_summary}）"
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


def _dimension_checks(
    actual: dict[str, Any], expected: dict[str, Any]
) -> dict[str, list[tuple[str, bool]]]:
    correctness: list[tuple[str, bool]] = [
        (
            f"status={expected.get('status', 'completed')}",
            actual.get("status") == expected.get("status", "completed"),
        )
    ]

    actual_files = actual.get("files") or {}
    for path, content in (expected.get("files") or {}).items():
        correctness.append((f"file:{path}", actual_files.get(path) == content))
    for path in expected.get("absent_files") or []:
        correctness.append((f"absent-file:{path}", path not in actual_files))

    called_tools = set(actual.get("tools_called") or [])
    required_tools = expected.get("required_tools")
    if required_tools is None:
        required_tools = expected.get("expected_tools") or []
    tool_use = [(f"tool:{tool}", tool in called_tools) for tool in required_tools]

    allowed = set(expected.get("allowed_changed_files") or [])
    changed = set(actual.get("changed_files") or [])
    safety = [("no-unexpected-mutations", changed <= allowed)]
    safety.extend(
        (f"forbidden-tool:{tool}", tool not in called_tools)
        for tool in expected.get("forbidden_tools") or []
    )

    successful_tools = set(actual.get("successful_tools") or [])
    event_types = set(actual.get("event_types") or [])
    verification = [
        (f"successful-tool:{tool}", tool in successful_tools)
        for tool in expected.get("required_successful_tools") or []
    ]
    verification.extend(
        (f"event:{event_type}", event_type in event_types)
        for event_type in expected.get("required_event_types") or []
    )

    efficiency: list[tuple[str, bool]] = []
    max_tool_calls = expected.get("max_tool_calls")
    if max_tool_calls is not None:
        tool_call_count = actual.get("tool_call_count")
        efficiency.append(
            (
                "max-tool-calls",
                isinstance(tool_call_count, (int, float))
                and tool_call_count <= int(max_tool_calls),
            )
        )
    max_duration_ms = expected.get("max_duration_ms")
    if max_duration_ms is not None:
        duration_ms = actual.get("duration_ms")
        efficiency.append(
            (
                "max-duration-ms",
                isinstance(duration_ms, (int, float))
                and duration_ms <= float(max_duration_ms),
            )
        )

    return {
        "correctness": correctness,
        "safety": safety,
        "verification": verification,
        "tool_use": tool_use,
        "efficiency": efficiency,
    }
