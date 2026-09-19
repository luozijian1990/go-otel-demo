import asyncio
import base64
from dataclasses import asdict
import hashlib
import hmac
import json
import os
import random
import secrets
import time
import uuid
import httpx
from pydantic import Field
from typing import Literal
from .contracts import Strict, IncidentContext, EvidenceBundle, validate_result
from .scenarios import CATALOGUE, HEALTHY, GroundTruth
from .evidence import Collector, view
from .analyzer import Analyzer

TERMINAL={"completed","cancelled","interrupted","environment_unhealthy","injection_failed"}

class CreateRun(Strict):
    mode: Literal["single","episode"]="single"
    visibility: Literal["learning","blind"]="learning"
    seed: int | None = None
    auto_analyze: bool=True
    baseline_seconds: int=Field(60,ge=5,le=120)
    incident_seconds: int=Field(60,ge=5,le=120)
    recovery_seconds: int=Field(30,ge=5,le=120)
    target_rps: float=Field(2,ge=.1,le=5)
    fault_ratio: float=Field(.5,ge=.1,le=1)
    max_concurrency: int=Field(8,ge=1,le=8)
    max_total_requests: int=Field(1000,ge=1,le=1000)

class Manager:
    def __init__(self,store,collector=None,analyzer=None,transport=None):
        self.store=store;self.collector=collector or Collector();self.analyzer=analyzer or Analyzer();self.transport=transport
        self.tasks={};self.analysis_tasks={};self.active=None
        self.gateway=os.getenv("BUSINESS_GATEWAY","http://traefik")

    def create(self,params,healthy=False,key=None):
        if key:
            for r in self.store.list("run"):
                if r.get("idempotency_key")==key:
                    if self.store.get("spec",r["id"])["params"]!=params.model_dump() or self.store.get("spec",r["id"])["healthy"]!=healthy:raise ValueError("idempotency key reused with different input")
                    return r
        if self.active:raise ValueError("another experiment is active")
        self.store.prune()
        seed=params.seed if params.seed is not None else secrets.randbits(63)
        spec=HEALTHY if healthy else random.Random(seed).choice(CATALOGUE)
        id=str(uuid.uuid4())
        row={"id":id,"status":"queued","mode":params.mode,"visibility":params.visibility,"created_at":time.time(),"evidence_ready":False,"request_count":0,"skipped_requests":0,"idempotency_key":key}
        truth=GroundTruth({"scenario_id":spec.scenario_id,"version":spec.version,"root_service":spec.target,"root_component":spec.root_component,"cause_code":spec.cause_code,"impact_class":spec.impact_class},[])
        self.store.put("run",id,row)
        self.store.put("spec",id,{"params":params.model_dump(),"healthy":healthy,"seed":seed,"scenario":asdict(spec)})
        self.store.put("truth",id,asdict(truth))
        self.active=id;self.tasks[id]=asyncio.create_task(self.execute(id,params,spec,seed))
        return row

    def update(self,id,**changes):
        row=self.store.get("run",id);row.update(changes);self.store.put("run",id,row);return row

    def token(self,id,spec):
        secret=os.getenv("DEMO_FAULT_SECRET","")
        if len(secret)<32:raise ValueError("fault signing key is not configured")
        body=base64.urlsafe_b64encode(json.dumps({"run":id,"target":spec.target,"action":spec.action,"exp":int(time.time())+20},separators=(",",":")).encode()).decode().rstrip("=")
        return body+"."+hmac.new(secret.encode(),body.encode(),hashlib.sha256).hexdigest()

    async def execute(self,id,params,spec,seed):
        requests=[];receipts=[];windows={};rng=random.Random(seed);pending=set();total=0;skipped=0
        start=time.time()
        async def send(client,phase,inject):
            rid=str(uuid.uuid4());headers={"Host":"localhost","X-Request-Id":rid,"X-Experiment-Id":id}
            if inject:headers["X-Demo-Fault-Token"]=self.token(id,spec)
            t=time.time();record={"request_id":rid,"phase":phase,"start_at":t}
            try:
                response=await client.post(self.gateway+"/service-a/exercise",headers=headers)
                record.update(status=response.status_code,trace_id=response.headers.get("x-trace-id"))
                receipt=response.headers.get("x-demo-receipt")
                if inject:
                    decoded=json.loads(base64.urlsafe_b64decode(receipt+"="*(-len(receipt)%4))) if receipt else {}
                    valid=decoded.get("service")==spec.target and decoded.get("action")==spec.action and decoded.get("applied") is True and decoded.get("effect") is True
                    valid=bool(valid and response.status_code==(500 if spec.impact_class=="failed" else 200))
                    receipts.append({"request_id":rid,"valid":valid,"receipt":decoded})
            except (httpx.HTTPError,ValueError,TypeError) as exc:
                record.update(status=None,error=type(exc).__name__)
                if inject:receipts.append({"request_id":rid,"valid":False,"receipt":{}})
            record["end_at"]=time.time();record["duration_ms"]=(record["end_at"]-t)*1000
            requests.append(record);self.store.put("requests",id,{"items":requests})
            self.update(id,request_count=len(requests),business_result=record)
        try:
            self.update(id,status="preparing",start_at=start)
            async with httpx.AsyncClient(timeout=12,follow_redirects=False,transport=self.transport) as client:
                # Uninjected preflight traverses the identical topology.
                await send(client,"preflight",False)
                if requests[-1]["status"]!=200:
                    self.update(id,status="environment_unhealthy")
                else:
                    phases=[("incident",0)] if params.mode=="single" else [("baseline",params.baseline_seconds),("incident",params.incident_seconds),("recovery",params.recovery_seconds)]
                    for phase,seconds in phases:
                        self.update(id,status="running" if phase=="incident" else phase)
                        phase_start=time.time();deadline=time.monotonic()+seconds;next_send=time.monotonic()
                        while True:
                            if total>=params.max_total_requests:break
                            inject=bool(spec.action and phase=="incident" and (params.mode=="single" or rng.random()<params.fault_ratio))
                            if len(pending)<params.max_concurrency:
                                task=asyncio.create_task(send(client,phase,inject));pending.add(task);task.add_done_callback(pending.discard);total+=1
                            else:skipped+=1
                            if params.mode=="single":break
                            next_send+=1/params.target_rps
                            if next_send>=deadline:break
                            await asyncio.sleep(max(0,next_send-time.monotonic()))
                        if pending:await asyncio.gather(*pending)
                        if seconds:await asyncio.sleep(max(0,deadline-time.monotonic()))
                        windows[phase]=(phase_start,time.time())
                    noncontrol=[r for r in requests if r["phase"]!="preflight"]
                    normal_ok=all(r["status"]==200 for r in requests if r["request_id"] not in {x["request_id"] for x in receipts})
                    valid=all(r["valid"] for r in receipts) and bool(receipts) if spec.action else bool(noncontrol) and normal_ok
                    valid=bool(valid and normal_ok)
                    truth=self.store.get("truth",id);truth.update(receipts=receipts,valid=valid,reason="confirmed" if valid else "effect not confirmed or normal traffic failed")
                    self.store.put("truth",id,truth)
                    self.update(id,status="completed" if valid else "injection_failed")
            end=time.time()
            observed=sorted([r for r in requests if r["phase"]!="preflight"],key=lambda r:(r["status"]!=500,-r["duration_ms"]))
            incident=IncidentContext(run_id=id,start_at=start,end_at=end,windows=windows,request_ids=[r["request_id"] for r in observed],trace_ids=list(dict.fromkeys(r["trace_id"] for r in observed if r.get("trace_id")))[:5],observed_symptoms=[{"status":r["status"],"duration_ms":r["duration_ms"]} for r in observed[:10]],completed_requests=len(observed))
            self.store.put("incident",id,incident.model_dump())
            self.update(id,end_at=end,windows=windows,skipped_requests=skipped,actual_rps=len(observed)/max(.001,end-start),representative_trace_id=incident.trace_ids[0] if incident.trace_ids else None,analysis_state="collecting")
            bundle=await self.collector.collect(incident)
            self.store.put("evidence",id,bundle.model_dump())
            self.update(id,evidence_ready=True,signals={k:v.model_dump() for k,v in bundle.availability.items()},analysis_state="ready")
            if params.auto_analyze:self.start_analysis(id,"all")
        except asyncio.CancelledError:
            for task in pending:task.cancel()
            if pending:await asyncio.gather(*pending,return_exceptions=True)
            self.update(id,status="cancelled",end_at=time.time())
            raise
        except Exception as exc:
            self.update(id,status="interrupted",error=type(exc).__name__,end_at=time.time())
        finally:
            final=self.store.get("run",id)
            if final["status"] in ("cancelled","interrupted","environment_unhealthy"):
                truth=self.store.get("truth",id)
                truth.update(receipts=receipts,valid=False,reason=final["status"])
                self.store.put("truth",id,truth)
            if self.active==id:self.active=None
            self.tasks.pop(id,None)

    def start_analysis(self,id,mode):
        if len(self.analysis_tasks)>=3:raise ValueError("analysis queue full")
        if sum(a["run_id"]==id for a in self.store.list("analysis"))>=12:raise ValueError("maximum 12 revisions per run")
        bundle=view(EvidenceBundle.model_validate(self.store.get("evidence",id)),mode)
        aid=str(uuid.uuid4());revision=1+sum(a["run_id"]==id for a in self.store.list("analysis"))
        row={"id":aid,"run_id":id,"status":"analyzing","mode":mode,"revision":revision,"bundle_hash":bundle.bundle_hash,"source":"http" if self.analyzer.configured() else "external"}
        self.store.put("analysis",aid,row,id)
        async def work():
            try:
                result=await self.analyzer.analyze(bundle);row.update(result)
                if row["status"]=="completed":row["frozen_at"]=time.time()
            except asyncio.CancelledError:
                row.update(status="interrupted");raise
            except Exception as exc:
                row.update(status="failed",error=type(exc).__name__)
            finally:
                self.store.put("analysis",aid,row,id);self.analysis_tasks.pop(aid,None)
        self.analysis_tasks[aid]=asyncio.create_task(work())
        return row

    def submit(self,aid,value):
        row=self.store.get("analysis",aid)
        if row["status"]!="awaiting_external":raise ValueError("result is frozen or analysis is not awaiting external input")
        bundle=view(EvidenceBundle.model_validate(self.store.get("evidence",row["run_id"])),row["mode"])
        result=validate_result(value,bundle)
        row.update(status="completed",source="external",result=result.model_dump(),frozen_at=time.time())
        self.store.put("analysis",aid,row,row["run_id"]);return row

    async def close(self):
        tasks=list(self.tasks.values())+list(self.analysis_tasks.values())
        for task in tasks:task.cancel()
        await asyncio.gather(*tasks,return_exceptions=True)
