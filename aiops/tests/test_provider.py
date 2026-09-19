"""HTTP adapter protocol tests with explicit test doubles; no real model accuracy claim."""
import asyncio
import json
import unittest
from unittest.mock import patch
import httpx
from aiops.app.analyzer import Analyzer
from aiops.tests.test_contracts import bundle, result

class ProviderTest(unittest.TestCase):
    def test_complete_outgoing_payload_and_retry(self):
        seen=[];b=bundle()
        def fake_model(request):
            body=json.loads(request.content);seen.append(body)
            content='not json' if len(seen)==1 else json.dumps(result(b))
            return httpx.Response(200,json={"choices":[{"message":{"content":content}}],"usage":{"total_tokens":20}})
        env={"AIOPS_LLM_MODE":"http","AIOPS_LLM_BASE_URL":"https://model.invalid/v1","AIOPS_LLM_MODEL":"fixture-protocol-test","AIOPS_LLM_API_KEY":"test-key"}
        with patch.dict("os.environ",env):out=asyncio.run(Analyzer(httpx.MockTransport(fake_model)).analyze(b))
        self.assertEqual(out["status"],"completed");self.assertEqual(out["attempts"],2)
        payload=json.dumps(seen)
        for forbidden in ("scenario_id","expected_","ground_truth","X-Demo-Fault-Token","test-key","seed"):
            self.assertNotIn(forbidden,payload)
        self.assertEqual(seen[0]["messages"][0],seen[1]["messages"][0])

    def test_invalid_result_fails_after_one_retry(self):
        seen=[]
        def fake_model(request):
            seen.append(request)
            return httpx.Response(200,json={"choices":[{"message":{"content":"{}"}}]})
        env={"AIOPS_LLM_MODE":"http","AIOPS_LLM_BASE_URL":"https://model.invalid/v1","AIOPS_LLM_MODEL":"fixture-protocol-test","AIOPS_LLM_API_KEY":"test-key"}
        with patch.dict("os.environ",env),self.assertRaises(ValueError):asyncio.run(Analyzer(httpx.MockTransport(fake_model)).analyze(bundle()))
        self.assertEqual(len(seen),2)
