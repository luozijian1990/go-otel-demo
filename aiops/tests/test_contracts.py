import asyncio
import json
import tempfile
import unittest
from pathlib import Path
import httpx
from aiops.app.contracts import IncidentContext, EvidenceBundle, validate_result
from aiops.app.evidence import Collector, sanitize, view
from aiops.app.analyzer import payload, Analyzer
from aiops.app.storage import Store
from aiops.app.trace_cleaner import clean_trace

def bundle():
    return EvidenceBundle(run_id="opaque",incident=IncidentContext(run_id="opaque",start_at=10,end_at=11),availability={},collected_at=12,bundle_hash="abc",evidence_index={"L1":{"source":"logs"}},logs=[{"evidence_id":"L1","message":"native error"}])

def result(b=None):
    return {"evidence_bundle_hash":(b or bundle()).bundle_hash,"status":"inconclusive","summary":"Insufficient evidence","root_service":"unknown","root_component":"unknown","cause_code":"unknown","impact_class":"unknown","confidence":"low","root_evidence_ids":[]}

class ContractsTest(unittest.TestCase):
    def test_boundary_and_references(self):
        b=bundle();self.assertEqual(validate_result(result(b),b).status,"inconclusive")
        for field in ("seed","scenario_id","ground_truth","expected_root_service"):
            with self.assertRaises(ValueError):EvidenceBundle.model_validate({**b.model_dump(),field:"secret"})
        for change in ({"evidence_bundle_hash":"other"},{"root_evidence_ids":["T999"]},{"status":"diagnosed"},{"status":"no_anomaly"}):
            with self.assertRaises(ValueError):validate_result({**result(b),**change},b)

    def test_sanitization_ablation_budget(self):
        self.assertEqual(sanitize({"seed":4,"ground_truth":"x","error":"Error 1146: table not found","X-Demo-Fault-Token":"secret"}),{"error":"Error 1146: table not found"})
        b=bundle();b.logs*=2000
        data=payload(b);self.assertLess(len(json.dumps(data,ensure_ascii=False).encode()),65536)
        self.assertFalse(view(b,"trace-only").logs)
        self.assertEqual(view(b,"all").bundle_hash,b.bundle_hash)

    def test_missing_model_is_not_diagnosis(self):
        from unittest.mock import patch
        with patch.dict("os.environ",{"AIOPS_LLM_MODE":"external"}):
            self.assertEqual(asyncio.run(Analyzer().analyze(bundle()))["status"],"awaiting_external")

    def test_restart_interrupts_without_replay(self):
        with tempfile.TemporaryDirectory() as d:
            path=str(Path(d)/"state.sqlite");s=Store(path)
            s.put("run","r",{"id":"r","status":"running"});s.put("analysis","a",{"id":"a","run_id":"r","status":"analyzing"},"r");s.db.close()
            s=Store(path);self.assertEqual(s.get("run","r")["status"],"interrupted");self.assertEqual(s.get("analysis","a")["status"],"interrupted");s.db.close()

    def test_missing_backends_partial_not_business_failure(self):
        def handle(request):
            if "jaeger" in request.url.host:return httpx.Response(200,json={"data":[]})
            if "loki" in request.url.host:return httpx.Response(503)
            return httpx.Response(200,json={"status":"success","data":{"result":[]}})
        c=Collector(budget=0,transport=httpx.MockTransport(handle))
        incident=IncidentContext(run_id="opaque",start_at=100,end_at=101,trace_ids=["a"*32],completed_requests=1)
        b=asyncio.run(c.collect(incident))
        self.assertEqual(b.availability["traces"].status,"not_found_after_deadline")
        self.assertEqual(b.availability["logs"].status,"unavailable")
        self.assertEqual(b.availability["metrics"].status,"insufficient_samples")
        self.assertFalse(b.traces);self.assertEqual(len(b.bundle_hash),64)

    def test_trace_semantics_fanout_and_events(self):
        def span(id,start,duration,parent=None,logs=None,refs=None):
            return {"spanID":id,"startTime":start,"duration":duration,"processID":"p","references":refs if refs is not None else ([{"refType":"CHILD_OF","spanID":parent}] if parent else []),"logs":logs or [],"tags":[]}
        raw={"data":[{"traceID":"t","processes":{"p":{"serviceName":"service-a"}},"spans":[span("root",0,10000,logs=[{"fields":[{"key":"event","value":"ordinary successful request"}]}]),span("c",1000,5000,"root"),span("d",2000,6000,"root",[{"timestamp":2001,"fields":[{"key":"exception.message","value":"native failure"}]}]),span("orphan",1,2,"missing"),span("follow",1,2,refs=[{"refType":"FOLLOWS_FROM","spanID":"root"}]),span("cross",1,2,refs=[{"refType":"CHILD_OF","spanID":"root","traceID":"other"}])]}]}
        raw["data"][0]["spans"][0]["tags"]=[{"key":"http.response.status_code","value":200}]
        cleaned=clean_trace(raw);spans={s["span_id"]:s for s in cleaned["spans"]}
        self.assertFalse(spans["root"]["is_error"]);self.assertTrue(spans["d"]["is_error"])
        self.assertEqual(spans["root"]["exclusive_ms"],3)
        self.assertIsNone(spans["follow"]["parent_span_id"]);self.assertIsNone(spans["cross"]["parent_span_id"])
        self.assertEqual(cleaned["exception_logs"][0]["fields"]["timestamp_us"],2001)
        self.assertIn("missing parent: missing",cleaned["limitations"])

    def test_analyzer_import_graph_has_no_oracle(self):
        import ast
        root=Path(__file__).parents[1]/"app"
        for name in ("analyzer.py","evidence.py","contracts.py","trace_cleaner.py"):
            tree=ast.parse((root/name).read_text())
            imports=[n.module or "" for n in ast.walk(tree) if isinstance(n,ast.ImportFrom)]
            self.assertFalse(set(imports)&{"scenarios","run_manager","storage","evaluator"})
