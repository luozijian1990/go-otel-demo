import asyncio
import json
import unittest
import httpx
from aiops.app.contracts import EvidenceBundle,IncidentContext
from aiops.app.evidence import Collector

class CollectionTest(unittest.TestCase):
    def test_exact_logs_do_not_stop_on_preflight_or_partial_branch(self):
        calls=[]
        def backend(req):
            calls.append(str(req.url))
            # First read contains only C completion, second contains actual entry completion.
            service="service-c" if len(calls)==1 else "service-a"
            return httpx.Response(200,json={"data":{"result":[{"stream":{"service_name":service},"values":[["1000000000","request completed",{"trace_id":"a"*32,"request_id":"actual"}]]}]}})
        async def run():
            c=Collector(budget=2,transport=httpx.MockTransport(backend))
            b=EvidenceBundle(run_id="run",incident=IncidentContext(run_id="run",start_at=1,end_at=2,request_ids=["actual"],trace_ids=["a"*32]),availability={},collected_at=3)
            async with httpx.AsyncClient(transport=c.transport) as client:await c.logs(client,b)
            return b
        b=asyncio.run(run())
        self.assertEqual(b.availability["logs"].attempts,2)
        self.assertEqual(b.logs[0]["metadata"]["service_name"],"service-a")
        self.assertTrue(all("trace_id" in url for url in calls))
        self.assertFalse(any("experiment_id" in url for url in calls))

    def test_partial_logs_are_retained_at_deadline(self):
        async def run():
            return await Collector(budget=0).retry(lambda:asyncio.sleep(0,result=[{"native_error":"1146"}]),lambda rows:False)
        data,state=asyncio.run(run())
        self.assertEqual(state.status,"partial");self.assertEqual(data[0]["native_error"],"1146")
