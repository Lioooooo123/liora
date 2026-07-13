import os
from pathlib import Path

import pytest
from deepeval import assert_test
from deepeval.test_case import LLMTestCase

from liora_evals import AgentContractMetric, LioraDaemon, load_cases


pytestmark = [pytest.mark.live]


@pytest.mark.skipif(
    os.environ.get("LIORA_DEEPEVAL_LIVE") != "1",
    reason="set LIORA_DEEPEVAL_LIVE=1 to run real-model evaluations",
)
def test_live_coding_agent():
    repo_root = Path(__file__).resolve().parents[2]
    timeout = float(os.environ.get("LIORA_DEEPEVAL_TIMEOUT", "120"))
    with LioraDaemon(repo_root, timeout=timeout) as daemon:
        for case in load_cases():
            test_case = LLMTestCase(
                input=case.input,
                actual_output=daemon.run_case(case),
                expected_output=case.expected_output,
            )
            assert_test(test_case, [AgentContractMetric()], run_async=False)
