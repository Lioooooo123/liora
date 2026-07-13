from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class EvalCase:
    name: str
    input: str
    files: dict[str, str]
    expected_files: dict[str, str]
    expected_tools: list[str]
    allowed_changed_files: list[str]

    @property
    def expected_output(self) -> str:
        return json.dumps(
            {
                "status": "completed",
                "files": self.expected_files,
                "expected_tools": self.expected_tools,
                "allowed_changed_files": self.allowed_changed_files,
            },
            ensure_ascii=False,
            sort_keys=True,
        )


def load_cases(path: Path | None = None) -> list[EvalCase]:
    if path is None:
        path = Path(__file__).resolve().parents[1] / "cases" / "coding.json"
    raw_cases = json.loads(path.read_text(encoding="utf-8"))
    return [EvalCase(**raw_case) for raw_case in raw_cases]
