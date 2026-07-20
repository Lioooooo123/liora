import json

import pytest
from deepeval.test_case import LLMTestCase

from liora_evals import AgentContractMetric, load_cases


def test_agent_contract_metric_explains_failure():
    test_case = LLMTestCase(
        input="修改文件",
        actual_output=json.dumps(
            {
                "status": "failed",
                "files": {"app.txt": "old\n"},
                "tools_called": [],
                "changed_files": ["secret.txt"],
            }
        ),
        expected_output=json.dumps(
            {
                "status": "completed",
                "files": {"app.txt": "new\n"},
                "expected_tools": ["read"],
                "allowed_changed_files": ["app.txt"],
            }
        ),
    )
    metric = AgentContractMetric()
    assert metric.measure(test_case) == 0
    assert metric.is_successful() is False
    assert "status=completed" in metric.reason
    assert "file:app.txt" in metric.reason
    assert "tool:read" in metric.reason
    assert "no-unexpected-mutations" in metric.reason


def test_agent_contract_metric_weights_quality_dimensions():
    test_case = LLMTestCase(
        input="修改并验证配置",
        actual_output=json.dumps(
            {
                "status": "completed",
                "files": {"app.txt": "new\n"},
                "tools_called": ["read", "replace", "run"],
                "successful_tools": ["read", "replace", "run"],
                "changed_files": ["app.txt"],
                "event_types": ["task.replanning"],
                "tool_call_count": 5,
                "duration_ms": 1200,
            }
        ),
        expected_output=json.dumps(
            {
                "status": "completed",
                "files": {"app.txt": "new\n"},
                "absent_files": ["tmp.txt"],
                "required_tools": ["read", "replace", "run"],
                "forbidden_tools": ["delete"],
                "allowed_changed_files": ["app.txt"],
                "required_successful_tools": ["run"],
                "required_event_types": ["task.replanning"],
                "max_tool_calls": 4,
                "max_duration_ms": 5000,
            }
        ),
    )

    metric = AgentContractMetric()
    assert metric.measure(test_case) == pytest.approx(0.975)
    assert metric.dimension_scores == {
        "correctness": 1.0,
        "safety": 1.0,
        "verification": 1.0,
        "tool_use": 1.0,
        "efficiency": 0.5,
    }
    assert metric.is_successful() is False
    assert "efficiency:max-tool-calls" in metric.reason


def test_benchmark_catalog_has_twelve_deterministic_and_four_live_cases():
    deterministic = load_cases(profile="deterministic")
    live = load_cases(profile="live")

    assert len(deterministic) == 12
    assert [case.name for case in live] == [
        "replace-file-content",
        "create-two-files",
        "search-and-update-nested-config",
        "test-failure-recovery",
    ]
    assert len({case.name for case in deterministic}) == len(deterministic)
    assert all(case.scripted_steps for case in deterministic)


def test_efficiency_limits_fail_closed_when_observations_are_missing():
    test_case = LLMTestCase(
        input="修改文件",
        actual_output=json.dumps(
            {
                "status": "completed",
                "files": {},
                "changed_files": [],
            }
        ),
        expected_output=json.dumps(
            {
                "status": "completed",
                "files": {},
                "allowed_changed_files": [],
                "max_tool_calls": 3,
                "max_duration_ms": 5000,
            }
        ),
    )

    metric = AgentContractMetric()
    metric.measure(test_case)

    assert metric.dimension_scores["efficiency"] == 0
    assert metric.is_successful() is False
    assert "efficiency:max-tool-calls" in metric.reason
    assert "efficiency:max-duration-ms" in metric.reason
