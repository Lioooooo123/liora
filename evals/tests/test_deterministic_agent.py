from pathlib import Path

import pytest
from deepeval import assert_test
from deepeval.test_case import LLMTestCase

from liora_evals import (
    AgentContractMetric,
    LioraDaemon,
    ScriptedLLMServer,
    load_cases,
)


pytestmark = [pytest.mark.deterministic]


@pytest.fixture(scope="module")
def deterministic_daemon():
    repo_root = Path(__file__).resolve().parents[2]
    cases = load_cases(profile="deterministic")
    with ScriptedLLMServer(cases) as model:
        with LioraDaemon(
            repo_root,
            llm_base_url=model.base_url,
            llm_api_key="eval-key",
            llm_model="eval-model",
        ) as daemon:
            yield daemon


@pytest.mark.parametrize(
    "case", load_cases(profile="deterministic"), ids=lambda case: case.name
)
def test_deterministic_agent_case(deterministic_daemon, case):
    test_case = LLMTestCase(
        input=case.input,
        actual_output=deterministic_daemon.run_case(case),
        expected_output=case.expected_output,
    )
    assert_test(test_case, [AgentContractMetric()], run_async=False)
