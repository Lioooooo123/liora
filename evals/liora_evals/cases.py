from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


@dataclass(frozen=True)
class EvalCase:
    name: str
    input: str
    files: dict[str, str] = field(default_factory=dict)
    expected_files: dict[str, str] = field(default_factory=dict)
    expected_absent_files: list[str] = field(default_factory=list)
    expected_tools: list[str] = field(default_factory=list)
    forbidden_tools: list[str] = field(default_factory=list)
    allowed_changed_files: list[str] = field(default_factory=list)
    required_successful_tools: list[str] = field(default_factory=list)
    required_event_types: list[str] = field(default_factory=list)
    max_tool_calls: int | None = None
    max_duration_ms: int | None = None
    profiles: list[str] = field(default_factory=lambda: ["deterministic"])
    scripted_steps: list[dict[str, Any]] = field(default_factory=list)

    @property
    def expected_output(self) -> str:
        return json.dumps(
            {
                "status": "completed",
                "files": self.expected_files,
                "absent_files": self.expected_absent_files,
                "required_tools": self.expected_tools,
                "forbidden_tools": self.forbidden_tools,
                "allowed_changed_files": self.allowed_changed_files,
                "required_successful_tools": self.required_successful_tools,
                "required_event_types": self.required_event_types,
                "max_tool_calls": self.max_tool_calls,
                "max_duration_ms": self.max_duration_ms,
            },
            ensure_ascii=False,
            sort_keys=True,
        )


def load_cases(path: Path | None = None, profile: str | None = None) -> list[EvalCase]:
    if path is None:
        path = Path(__file__).resolve().parents[1] / "cases" / "coding.json"
    raw_cases = json.loads(path.read_text(encoding="utf-8"))
    cases = [EvalCase(**raw_case) for raw_case in raw_cases]
    names = [case.name for case in cases]
    if len(names) != len(set(names)):
        raise ValueError("benchmark case names must be unique")
    if profile is not None:
        cases = [case for case in cases if profile in case.profiles]
    return cases
