import asyncio
import base64
import json
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch
import httpx
from fastapi.testclient import TestClient
from aiops.app.main import create_app
from aiops.app.run_manager import Manager
from aiops.app.contracts import EvidenceBundle
from aiops.tests.test_contracts import result

class EmptyCollector:
    # Backend test double, explicitly not a model and never an accuracy sample.
    async def collect(self,incident):
        return EvidenceBundle(run_id=incident.run_id,incident=incident,availability={},collected_at=time.time(),bundle_hash="test-snapshot")

class APITest(unittest.TestCase):
    def test_real_lifecycle_external_freeze_and_blind_gate(self):
        def business(req):
            return httpx.Response(200,headers={"X-Trace-Id":"a"*32},json={"status":"ok"})
        def factory(store):return Manager(store,collector=EmptyCollector(),transport=httpx.MockTransport(business))
        with tempfile.TemporaryDirectory() as d,patch.dict("os.environ",{"AIOPS_LLM_MODE":"external"}),TestClient(create_app(str(Path(d)/"state.sqlite"),factory)) as client:
            r=client.post("/api/v1/runs/healthy",json={"visibility":"blind"},headers={"Idempotency-Key":"first"});self.assertEqual(r.status_code,202);id=r.json()["id"]
            self.assertEqual(client.post("/api/v1/runs/healthy",json={"visibility":"blind"},headers={"Idempotency-Key":"first"}).json()["id"],id)
            for _ in range(100):
                row=client.get("/api/v1/runs/"+id).json()
                if row["analyses"] and row["analyses"][0]["status"]=="awaiting_external":break
                time.sleep(.01)
            self.assertEqual(row["status"],"completed")
            self.assertEqual(client.get(f"/api/v1/runs/{id}/ground-truth").status_code,409)
            a=row["analyses"][0];b=EvidenceBundle.model_validate(client.get(f"/api/v1/runs/{id}/evidence").json())
            self.assertEqual(client.post(f'/api/v1/analyses/{a["id"]}/result',json=result(b)).status_code,200)
            self.assertEqual(client.post(f'/api/v1/analyses/{a["id"]}/result',json=result(b)).status_code,409)
            self.assertEqual(client.get(f"/api/v1/runs/{id}/ground-truth").status_code,200)
            self.assertEqual(client.get(f'/api/v1/analyses/{a["id"]}/evaluation').json()["status"],"mismatch")
            self.assertEqual(client.get('/api/v1/summary').json()["groups"]["http"]["completed"],0)
            revision=client.post(f"/api/v1/runs/{id}/analyses",json={"mode":"trace-only"}).json();self.assertEqual(revision["revision"],2)

    def test_csrf_bounds_cancel_and_episode_exclusion(self):
        async def business(req):
            await asyncio.sleep(.05)
            return httpx.Response(200,json={"status":"ok"})
        def factory(store):return Manager(store,collector=EmptyCollector(),transport=httpx.MockTransport(business))
        with tempfile.TemporaryDirectory() as d,TestClient(create_app(str(Path(d)/"state.sqlite"),factory)) as client:
            self.assertEqual(client.post('/api/v1/runs/healthy',json={},headers={"Origin":"https://evil.example"}).status_code,403)
            self.assertEqual(client.post('/api/v1/runs/healthy',json={"target_rps":999}).status_code,422)
            r=client.post('/api/v1/runs/healthy',json={"mode":"episode"}).json()
            self.assertEqual(client.post('/api/v1/runs/random',json={}).status_code,409)
            self.assertEqual(client.post('/api/v1/runs/'+r['id']+'/cancel',json={}).json()["status"],"cancelled")
            self.assertEqual(client.post('/api/v1/runs/'+r['id']+'/cancel',json={}).status_code,200)
