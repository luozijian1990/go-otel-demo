from contextlib import asynccontextmanager
import os
from pathlib import Path
from urllib.parse import urlsplit
from typing import Literal
import httpx
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse
from .storage import Store
from .run_manager import Manager, CreateRun
from .contracts import Strict
from .evaluator import evaluate, summary

def create_app(db_path=None,manager_factory=Manager):
    @asynccontextmanager
    async def lifespan(app):
        path=db_path or os.getenv("AIOPS_DB","/data/aiops.sqlite")
        Path(path).parent.mkdir(parents=True,exist_ok=True)
        app.state.store=Store(path);app.state.manager=manager_factory(app.state.store)
        yield
        await app.state.manager.close();app.state.store.db.close()
    app=FastAPI(lifespan=lifespan)

    @app.middleware("http")
    async def boundary(request:Request,call_next):
        if request.method in ("POST","PUT","DELETE"):
            host=request.headers.get("host","").split(":")[0]
            allowed={"localhost","127.0.0.1","testserver","aiops-api"}
            origin=request.headers.get("origin")
            if host not in allowed or (origin and (urlsplit(origin).hostname not in {"localhost","127.0.0.1"} or urlsplit(origin).netloc!=request.headers.get("host"))):
                return JSONResponse({"detail":"untrusted origin or host"},403)
            if "application/json" not in request.headers.get("content-type",""):
                return JSONResponse({"detail":"JSON required"},415)
            data=bytearray()
            async for chunk in request.stream():
                data.extend(chunk)
                if len(data)>100_000:return JSONResponse({"detail":"body too large"},413)
            request._body=bytes(data)
        try:return await call_next(request)
        except KeyError:return JSONResponse({"detail":"record not found"},404)
        except ValueError as exc:return JSONResponse({"detail":str(exc)[:200]},409)

    @app.get("/health")
    def health():return {"status":"ok"}

    @app.get("/ready")
    async def ready():
        checks={}
        async with httpx.AsyncClient(timeout=2,follow_redirects=False) as client:
            for name,url in {"jaeger":os.getenv("JAEGER_URL","http://jaeger:16686")+"/api/services","loki":os.getenv("LOKI_URL","http://loki:3100")+"/ready","prometheus":os.getenv("PROMETHEUS_URL","http://prometheus:9090")+"/-/ready"}.items():
                try:checks[name]=(await client.get(url)).status_code==200
                except httpx.HTTPError:checks[name]=False
        return {"status":"ready" if all(checks.values()) else "partial","dependencies":checks,"model_configured":app.state.manager.analyzer.configured()}

    @app.post("/api/v1/runs/{kind}",status_code=202)
    async def create(kind:Literal["random","healthy"],params:CreateRun,request:Request):
        return app.state.manager.create(params,kind=="healthy",request.headers.get("idempotency-key"))

    @app.get("/api/v1/runs")
    async def history(offset:int=0,limit:int=30):
        return app.state.store.list("run")[max(0,offset):max(0,offset)+min(100,max(1,limit))]

    @app.get("/api/v1/runs/{id}")
    async def get_run(id:str):
        row=app.state.store.get("run",id)
        row["analyses"]=[a for a in app.state.store.list("analysis") if a["run_id"]==id]
        return row

    @app.get("/api/v1/runs/{id}/requests")
    async def requests(id:str,offset:int=0,limit:int=100):return app.state.store.get("requests",id)["items"][max(0,offset):max(0,offset)+min(200,max(1,limit))]

    @app.post("/api/v1/runs/{id}/cancel")
    async def cancel(id:str):
        row=app.state.store.get("run",id)
        task=app.state.manager.tasks.get(id)
        if task:
            task.cancel()
            import asyncio
            await asyncio.gather(task,return_exceptions=True)
        return app.state.store.get("run",id)

    @app.get("/api/v1/runs/{id}/evidence")
    async def evidence(id:str):return app.state.store.get("evidence",id)

    class AnalysisInput(Strict):
        mode:Literal["trace-only","trace+logs","all"]="all"

    @app.post("/api/v1/runs/{id}/analyses",status_code=202)
    async def analyze(id:str,params:AnalysisInput):return app.state.manager.start_analysis(id,params.mode)

    @app.get("/api/v1/analyses/{id}")
    async def analysis(id:str):return app.state.store.get("analysis",id)

    @app.get("/api/v1/analyses/{id}/evidence")
    async def analysis_evidence(id:str):
        from .evidence import view
        from .contracts import EvidenceBundle
        a=app.state.store.get("analysis",id)
        return view(EvidenceBundle.model_validate(app.state.store.get("evidence",a["run_id"])),a["mode"])

    @app.post("/api/v1/analyses/{id}/result")
    async def submit(id:str,result:dict):return app.state.manager.submit(id,result)

    @app.get("/api/v1/runs/{id}/ground-truth")
    async def truth(id:str):
        row=app.state.store.get("run",id)
        if row["visibility"]=="blind" and not any(a["run_id"]==id and a["status"]=="completed" for a in app.state.store.list("analysis")):
            raise HTTPException(409,"blind run requires a frozen diagnosis before reveal")
        return app.state.store.get("truth",id)

    @app.get("/api/v1/analyses/{id}/evaluation")
    async def evaluation(id:str):
        a=app.state.store.get("analysis",id)
        if a["status"]!="completed":return {"status":"unscorable","reason":"no frozen diagnosis"}
        return evaluate(a,app.state.store.get("truth",a["run_id"]))

    @app.get("/api/v1/summary")
    async def report():
        runs=app.state.store.list("run")
        return summary(runs,app.state.store.list("analysis"),{r["id"]:app.state.store.get("truth",r["id"]) for r in runs})
    return app

app=create_app()
