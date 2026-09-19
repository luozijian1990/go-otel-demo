"""Read-only backends. No import of control, scenario catalogue or storage."""
import asyncio
import json
import os
import re
import time
import httpx
from .contracts import EvidenceBundle, IncidentContext, SignalState, digest
from .trace_cleaner import clean_trace

FORBIDDEN = re.compile(r"authorization|cookie|password|api.?key|secret|fault.?token|receipt|scenario|expected_|ground.?truth|oracle|seed|injection", re.I)

def sanitize(value):
    if isinstance(value, dict):
        return {k: sanitize(v) for k, v in value.items() if not FORBIDDEN.search(k)}
    if isinstance(value, list):
        return [sanitize(v) for v in value]
    if isinstance(value, str):
        value = re.sub(r"(?i)(authorization|cookie|password|api[_-]?key|x-demo-fault-token)\s*[:=]\s*[^\s,;]+", r"\1=[redacted]", value)
        value = re.sub(r"[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}", "[email]", value)
        value = re.sub(r"(?i)(://)[^/@\s]+:[^/@\s]+@", r"\1[redacted]@", value)
        value = re.sub(r"[^\s:]+:[^\s@]+@tcp\([^)]*\)", "[redacted-dsn]", value)
        # Do not forward arbitrary request bodies/headers/query credentials.
        return value[:4000]
    return value

async def get_json(client, url, params=None):
    async with client.stream("GET", url, params=params) as response:
        response.raise_for_status()
        data = bytearray()
        async for chunk in response.aiter_bytes():
            data.extend(chunk)
            if len(data) > 4_000_000:
                raise ValueError("backend response exceeds 4 MB")
        return json.loads(data)

QUERIES = {
    "request_rate": ('sum by(service_name)(rate(demo_http_requests_total{http_route="/exercise"}[1m]))', "requests/second"),
    "error_percent": ('100*sum by(service_name)(rate(demo_http_requests_total{http_route="/exercise",status_class="5xx"}[1m]))/sum by(service_name)(rate(demo_http_requests_total{http_route="/exercise"}[1m]))', "percent"),
    "p95": ('histogram_quantile(0.95,sum by(service_name,le)(rate(demo_http_request_duration_seconds_bucket{http_route="/exercise"}[1m])))', "seconds"),
    "dependency_errors": ('sum by(service_name,dependency)(rate(demo_dependency_calls_total{outcome="error"}[1m]))', "calls/second"),
    "fallback_rate": ('sum by(service_name)(rate(demo_fallback_total[1m]))', "fallbacks/second"),
    "scrape_up": ('up{job="go-services"}', "boolean"),
}

class Collector:
    def __init__(self, budget=45, transport=None):
        self.budget = budget
        self.transport = transport
        self.jaeger = os.getenv("JAEGER_URL", "http://jaeger:16686")
        self.loki = os.getenv("LOKI_URL", "http://loki:3100")
        self.prom = os.getenv("PROMETHEUS_URL", "http://prometheus:9090")

    async def collect(self, incident: IncidentContext):
        bundle = EvidenceBundle(run_id=incident.run_id, incident=incident, availability={}, collected_at=time.time(), limitations=[
            "Trace sampling flag does not prove storage; missing trace may be delayed, lost or sampled out.",
            "Metrics are window-correlated, may include manual traffic; 1m rates overlap phase boundaries.",
            "Exclusive time is an estimate with incomplete spans and clock skew; longest span is not necessarily root cause.",
        ])
        async with httpx.AsyncClient(timeout=6, follow_redirects=False, transport=self.transport) as client:
            await asyncio.gather(self.traces(client, bundle), self.logs(client, bundle), self.metrics(client, bundle))
        bundle = EvidenceBundle.model_validate(sanitize(bundle.model_dump()))
        bundle.bundle_hash = digest(bundle.model_dump(exclude={"bundle_hash"}))
        return bundle

    async def retry(self, query, ready=lambda data: True):
        deadline = time.monotonic() + self.budget
        attempts, reason, data = 0, "no data before deadline", None
        while True:
            attempts += 1
            try:
                data = await query()
                if data and ready(data):
                    return data, SignalState(status="available", attempts=attempts)
                reason = "no data before deadline"
            except (httpx.HTTPError, ValueError, KeyError, TypeError) as exc:
                reason = type(exc).__name__
            remaining = deadline-time.monotonic()
            if remaining <= 0:
                if data:
                    return data, SignalState(status="partial", reason="some data arrived but completion could not be confirmed", attempts=attempts)
                return data, SignalState(status="not_found_after_deadline" if reason == "no data before deadline" else "unavailable", reason=reason, attempts=attempts)
            await asyncio.sleep(min(remaining, attempts if attempts < 4 else 5))

    async def traces(self, client, b):
        ids = [t for t in b.incident.trace_ids if re.fullmatch(r"[0-9a-f]{32}", t)][:5]
        if not ids:
            b.availability["traces"] = SignalState(status="unavailable", reason="no valid business trace ID")
            return
        async def query():
            result = []
            for tid in ids:
                try:
                    raw = await get_json(client, self.jaeger + "/api/traces/" + tid)
                except httpx.HTTPStatusError as exc:
                    if exc.response.status_code==404:continue
                    raise
                if not raw.get("data"):
                    continue
                if raw["data"][0].get("traceID") != tid:
                    raise ValueError("trace ID mismatch")
                result.append(clean_trace(raw))
            return result
        data, state = await self.retry(query)
        b.availability["traces"] = state
        for n, trace in enumerate((data or [])[:3], 1):
            spans = trace["spans"]
            if len(spans)>200:
                state.truncated = True
                spans = sorted(spans, key=lambda s: (not s["is_error"], -s["duration_ms"]))[:200]
            for i, span in enumerate(spans, 1):
                eid = f"T{n}-S{i}"
                span["evidence_id"] = eid
                b.evidence_index[eid] = {"source":"traces", "trace_id":trace["trace_id"], "span_id":span["span_id"], "unit":"milliseconds", "query":"/api/traces/"+trace["trace_id"]}
            b.traces.append({"trace_id":trace["trace_id"], "spans":spans, "limitations":trace["limitations"]})
        if len(data or []) < len(ids) and data:
            state.status, state.reason = "partial", "some representative traces not found"

    async def logs(self, client, b):
        selector='{service_name=~"service-a|service-b|service-c|service-d"}'
        trace_ids=[t for t in b.incident.trace_ids[:3] if re.fullmatch(r"[0-9a-f]{32}",t)]
        if trace_ids:
            exact=selector+' | trace_id=~'+json.dumps("|".join(trace_ids))
            correlation="exact_trace"
        else:
            exact=selector+' | request_id=~'+json.dumps("|".join(re.escape(r) for r in b.incident.request_ids[:3]))
            correlation="exact_request"
        queries=[(exact,b.incident.start_at-2,b.incident.end_at+2,150 if len(b.incident.windows)>1 else 300,correlation)]
        if len(b.incident.windows)>1:
            for phase,(start,end) in b.incident.windows.items():
                queries.append((selector+' | experiment_id='+json.dumps(b.run_id),start,end,50,"exact_experiment_"+phase))
        async def read():
            async def one(query,start,end,limit,corr):
                raw=await get_json(client,self.loki+"/loki/api/v1/query_range",{"query":query,"start":str(int(start*1e9)),"end":str(int(end*1e9)),"limit":limit,"direction":"forward"})
                rows=[]
                for stream in raw.get("data",{}).get("result",[]):
                    for value in stream.get("values",[]):
                        rows.append({"timestamp_ns":value[0],"message":value[1],"metadata":{**stream["stream"],**(value[2] if len(value)>2 else {})},"correlation":corr,"query":query,"query_limit_reached":False})
                if len(rows)>=limit:
                    for row in rows:row["query_limit_reached"]=True
                return rows
            parts=await asyncio.gather(*(one(*q) for q in queries))
            seen=set();rows=[]
            for part in parts:
                for row in part:
                    key=(row["timestamp_ns"],row["metadata"].get("service_name"),row["message"])
                    if key not in seen:rows.append(row);seen.add(key)
            return rows
        def complete(rows):
            exact_rows=[r for r in rows if r["correlation"] in ("exact_trace","exact_request") and r["message"]=="request completed" and r["metadata"].get("service_name")=="service-a"]
            if trace_ids:
                return set(trace_ids).issubset({r["metadata"].get("trace_id") for r in exact_rows})
            return bool(exact_rows)
        data,state=await self.retry(read,complete)
        b.availability["logs"]=state
        state.truncated=len(data or [])>=300 or any(r["query_limit_reached"] for r in data or [])
        for i,row in enumerate((data or [])[:300],1):
            eid=f"L{i}";row["evidence_id"]=eid;b.logs.append(row)
            b.evidence_index[eid]={"source":"logs","query":row.pop("query"),"timestamp_ns":row["timestamp_ns"],"window":[b.incident.start_at,b.incident.end_at],"unit":"nanoseconds"}

    async def metrics(self, client, b):
        windows=b.incident.windows or {"incident":(b.incident.start_at,b.incident.end_at)}
        b.metrics={"sample_quality":{"completed_requests":b.incident.completed_requests,"minimum_for_p95":20,"window_seconds":60,"correlation":"window only; manual traffic may contribute"}}
        async def read(phase,start,end,name,query,unit):
            try:
                raw=await get_json(client,self.prom+"/api/v1/query_range",{"query":query,"start":max(0,start-60),"end":max(start,end),"step":5})
                if raw.get("status")!="success":raise ValueError("query failed")
                return phase,start,end,name,query,unit,raw.get("data",{}).get("result",[]),None
            except (httpx.HTTPError,ValueError,KeyError) as exc:
                return phase,start,end,name,query,unit,[],type(exc).__name__
        results=await asyncio.gather(*(read(phase,start,end,name,query,unit) for phase,(start,end) in windows.items() for name,(query,unit) in QUERIES.items()))
        errors=[];count=0;healthy_scrapes=0
        for phase,start,end,name,query,unit,series,error in results:
            rows=b.metrics.setdefault(phase,[])
            if error:
                errors.append(error);rows.append({"name":name,"status":"query_failed","reason":error});continue
            eid=f"M{len([k for k in b.evidence_index if k.startswith('M')])+1}"
            remaining=5000-count
            for item in series:
                values=item.get("values",[])[:max(0,remaining)];item["values"]=values;remaining-=len(values);count+=len(values)
                if name=="scrape_up":healthy_scrapes+=sum(v[1]=="1" for v in values)
            rows.append({"evidence_id":eid,"name":name,"series":series,"status":"available" if series else "no_samples","unit":unit})
            b.evidence_index[eid]={"source":"metrics","query":query,"window":[start,end],"unit":unit,"truncated":remaining<=0}
        b.metrics["sample_quality"]["healthy_scrape_samples"]=healthy_scrapes
        low=b.incident.completed_requests<20 or healthy_scrapes<8
        status="partial" if errors and count else "unavailable" if errors else "insufficient_samples" if low or count==0 else "available"
        reason=",".join(errors) or ("low request count or insufficient successful scrapes; P95 unreliable" if status=="insufficient_samples" else None)
        b.availability["metrics"]=SignalState(status=status,reason=reason,attempts=len(results),truncated=count>=5000)

def view(bundle, mode):
    b=bundle.model_copy(deep=True)
    sources={"trace-only":{"traces"},"trace+logs":{"traces","logs"},"all":{"traces","logs","metrics"}}[mode]
    if "logs" not in sources:b.logs=[]
    if "metrics" not in sources:b.metrics={}
    b.evidence_index={k:v for k,v in b.evidence_index.items() if v["source"] in sources}
    b.availability={k:v for k,v in b.availability.items() if k in sources}
    b.limitations.append("Independent ablation view: "+mode+"; snapshot hash refers to original frozen bundle")
    return b
