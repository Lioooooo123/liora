import json

import pytest
from deepeval import assert_test
from deepeval.test_case import LLMTestCase

from liora_evals import AgentContractMetric, load_cases


@pytest.mark.parametrize("case", load_cases(), ids=lambda case: case.name)
def test_agent_contract_fixture(case):
    actual_output = json.dumps(
        {
            "status": "completed",
            "files": case.expected_files,
            "tools_called": case.expected_tools,
            "changed_files": case.allowed_changed_files,
        },
        ensure_ascii=False,
    )
    test_case = LLMTestCase(
        input=case.input,
        actual_output=actual_output,
        expected_output=case.expected_output,
    )
    assert_test(test_case, [AgentContractMetric()], run_async=False)


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
