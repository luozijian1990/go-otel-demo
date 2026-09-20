#!/usr/bin/env python3
"""Read-only telemetry clients; no imports of demo control or expected answers."""
import argparse
import concurrent.futures
import datetime as dt
import hashlib
import json
import re
import time
import urllib.error
import urllib.parse
import urllib.request
from trace_cleaner import clean_trace

BUSINESS_SERVICES=("order-service","payment-service","inventory-service","product-service")
TRACE_SERVICES=(*BUSINESS_SERVICES,"traefik")
LOG_SERVICES=TRACE_SERVICES
METRIC_SERVICES=BUSINESS_SERVICES

def validate_service(source,service):
    allowed={"traces":TRACE_SERVICES,"logs":LOG_SERVICES,"metrics":METRIC_SERVICES}[source]
    if service and service not in allowed:
        raise ValueError("current business metric templates do not support traefik" if source=="metrics" and service=="traefik" else "unknown service for source")

SENSITIVE=re.compile(r"authorization|cookie|password|api.?key|secret|fault.?token|receipt|scenario|expected_|ground.?truth|seed|injection",re.I)

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args,**kwargs):
        return None

def read_json(base,path,params=None):
    parsed=urllib.parse.urlsplit(base)
    if parsed.scheme not in ("http","https") or parsed.username or parsed.password:
        raise ValueError("use an HTTP(S) backend URL without inline credentials")
    url=base.rstrip("/")+path
    if params:url+="?"+urllib.parse.urlencode(params)
    req=urllib.request.Request(url,headers={"Accept":"application/json"})
    with urllib.request.build_opener(NoRedirect()).open(req,timeout=8) as response:
        raw=response.read(4_000_001)
        if len(raw)>4_000_000:raise ValueError("backend response exceeds 4MB")
        return json.loads(raw)

def scrub(value):
    if isinstance(value,dict):return {k:scrub(v) for k,v in value.items() if not SENSITIVE.search(k) and not re.search(r"http\..*(header|body)",k,re.I)}
    if isinstance(value,list):return [scrub(v) for v in value]
    if isinstance(value,str):
        value=re.sub(r"(?i)(password|api[_-]?key|authorization|cookie|token)\s*[:=]\s*[^\s,;]+",r"\1=[redacted]",value)
        value=re.sub(r"(://)[^/@\s]+:[^/@\s]+@",r"\1[redacted]@",value)
        value=re.sub(r"[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}","[email]",value)
        return value[:6000]
    return value

def evidence(source,data):
    data=scrub(data)
    key=hashlib.sha256(json.dumps(data,sort_keys=True,separators=(",",":"),ensure_ascii=False).encode()).hexdigest()[:16]
    return {"evidence_id":source[0].upper()+"-"+key,"data":data}

def epoch(value):
    # Go emits RFC3339Nano; Python 3.9 accepts only 3/6 fractional digits.
    value=re.sub(r"\.(\d+)(?=Z$|[+-]\d{2}:\d{2}$)",lambda m:'.'+m.group(1)[:6].ljust(6,'0'),value)
    parsed=dt.datetime.fromisoformat(value.replace("Z","+00:00"))
    if parsed.tzinfo is None:raise argparse.ArgumentTypeError("timestamp needs timezone")
    return parsed.timestamp()

def traces(args,start,end):
    validate_service("traces",args.service)
    params=None;path="/api/traces"
    if args.trace_id:path+="/"+args.trace_id
    else:
        if not args.service:raise ValueError("provide --trace-id or --service")
        params={"service":args.service,"start":int(start*1e6),"end":int(end*1e6),"limit":5}
    deadline=time.monotonic()+args.wait;attempts=0;data=[]
    while True:
        attempts+=1
        try:raw=read_json(args.url or "http://localhost:16686",path,params)
        except urllib.error.HTTPError as exc:
            if exc.code!=404:raise
            raw={"data":[]}
        data=raw.get("data") or []
        if data or time.monotonic()>=deadline:break
        time.sleep(min(3,max(0,deadline-time.monotonic())))
    rows=[];truncated=len(data)>3
    for raw in data[:3]:
        if args.trace_id and raw.get("traceID","").lstrip("0")!=args.trace_id.lstrip("0"):raise ValueError("trace ID mismatch")
        clean=clean_trace({"data":[raw]})
        spans=clean["spans"]
        if len(spans)>200:
            spans=sorted(spans,key=lambda s:(not s["is_error"],-s["exclusive_ms"]))[:200];truncated=True
        rows.extend(evidence("traces",{"trace_id":clean["trace_id"],**span}) for span in spans)
    return {"query":{"path":path,"parameters":params},"evidence":rows,"status":"available" if rows else "not_found_after_deadline","attempts":attempts,"truncated":truncated,"limitations":["Missing trace does not prove sampling loss. Ordinary successes retain about 1%.","Exclusive duration unions child intervals; incomplete parents or clock skew limit causality."]}

def logs(args,start,end):
    validate_service("logs",args.service)
    selector='{service_name='+json.dumps(args.service)+'}' if args.service else '{service_name=~"'+'|'.join(LOG_SERVICES)+'"}'
    correlation="time_only"
    for field,value in (("trace_id",args.trace_id),("request_id",args.request_id),("experiment_id",args.run_id)):
        if value:selector+=' | '+field+'='+json.dumps(value);correlation="exact_"+field
    params={"query":selector,"start":str(int(start*1e9)),"end":str(int(end*1e9)),"limit":args.limit,"direction":"forward"}
    raw=read_json(args.url or "http://localhost:13100","/loki/api/v1/query_range",params)
    rows=[]
    for stream in raw.get("data",{}).get("result",[]):
        for value in stream.get("values",[]):
            rows.append(evidence("logs",{"timestamp_ns":value[0],"message":value[1],"metadata":{**stream["stream"],**(value[2] if len(value)>2 else {})},"correlation":correlation}))
    return {"query":params,"evidence":rows[:args.limit],"status":"available" if rows else "no_samples","truncated":len(rows)>=args.limit,"limitations":["Traefik may lack request_id/experiment_id; filters remain AND. Query gateway by TraceId separately; time-only matching is not exact correlation.","Time-only logs are correlated context, not proof they belong to one request.","For window traffic, narrow to incident timestamps or representative TraceIds; a capped run query can contain only early baseline logs."]}

def metric_queries(service):
    match='service_name='+json.dumps(service) if service else 'service_name=~"'+'|'.join(METRIC_SERVICES)+'"'
    return {
      "request_rate":('sum by(service_name,http_route)(rate(demo_http_requests_total{'+match+'}[1m]))',"requests/second"),
      "error_percent":('100*sum by(service_name)(rate(demo_http_requests_total{'+match+',status_class="5xx"}[1m]))/sum by(service_name)(rate(demo_http_requests_total{'+match+'}[1m]))',"percent"),
      "p95":('histogram_quantile(0.95,sum by(service_name,le)(rate(demo_http_request_duration_seconds_bucket{'+match+'}[1m])))',"seconds"),
      "dependency_errors":('sum by(service_name,dependency)(rate(demo_dependency_calls_total{'+match+',outcome="error"}[1m]))',"operations/second"),
      "dependency_mean":('sum by(service_name,dependency)(rate(demo_dependency_duration_seconds_sum{'+match+'}[1m]))/sum by(service_name,dependency)(rate(demo_dependency_duration_seconds_count{'+match+'}[1m]))',"seconds"),
      "fallback_rate":('sum by(service_name)(rate(demo_fallback_total{'+match+'}[1m]))',"attempts/second"),
      "fallback_success_rate":('sum by(service_name)(rate(demo_fallback_results_total{'+match+',outcome="success"}[1m]))',"successes/second"),
      "fallback_failure_rate":('sum by(service_name)(rate(demo_fallback_results_total{'+match+',outcome="failure"}[1m]))',"failures/second"),
      "scrape_up":('up{job="go-services"}',"boolean"),
    }

def metrics(args,start,end):
    validate_service("metrics",args.service)
    def query(item):
        name,(expression,unit)=item
        try:
            raw=read_json(args.url or "http://localhost:19090","/api/v1/query_range",{"query":expression,"start":start,"end":end,"step":max(5,int((end-start)/200))})
            if raw.get("status")!="success":raise ValueError("Prometheus query failed")
            series=raw.get("data",{}).get("result",[])[:20]
            for row in series:row["values"]=row.get("values",[])[:201]
            return evidence("metrics",{"name":name,"query":expression,"unit":unit,"series":series,"status":"available" if series else "no_samples"})
        except (ValueError,urllib.error.URLError,TimeoutError) as exc:return evidence("metrics",{"name":name,"query":expression,"status":"unavailable","reason":str(exc)[:200]})
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:rows=list(pool.map(query,metric_queries(args.service).items()))
    statuses={r["data"]["status"] for r in rows}
    return {"query":"fixed business metric templates; 1m rolling rates","evidence":rows,"status":"partial" if len(statuses)>1 else next(iter(statuses)),"truncated":any(len(r["data"].get("series",[]))==20 for r in rows),"limitations":["Metrics are service/window correlated, never joined by TraceId or order ID.","NaN, missing series and zero traffic are not equivalent to 0% errors.","Single clicks and short windows do not establish statistically reliable P95; rolling 1m windows overlap phases.","scrape_up covers all four services for dependency availability context."]}

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("source",choices=("traces","logs","metrics"));p.add_argument("--service",choices=TRACE_SERVICES)
    p.add_argument("--trace-id");p.add_argument("--request-id");p.add_argument("--run-id")
    p.add_argument("--start",type=epoch);p.add_argument("--end",type=epoch);p.add_argument("--lookback",type=int,default=300)
    p.add_argument("--wait",type=int,default=15);p.add_argument("--limit",type=int,default=100);p.add_argument("--url")
    a=p.parse_args()
    try:validate_service(a.source,a.service)
    except ValueError as exc:p.error(str(exc))
    if a.trace_id and not re.fullmatch(r"[0-9a-f]{16,32}",a.trace_id):p.error("invalid trace ID")
    if any(value and not re.fullmatch(r"[a-zA-Z0-9_-]{1,64}",value) for value in (a.request_id,a.run_id)):p.error("invalid correlation ID")
    if not 0<=a.wait<=45 or not 1<=a.limit<=300 or not 1<=a.lookback<=3600:p.error("query bounds exceeded")
    end=a.end if a.end is not None else time.time();start=a.start if a.start is not None else end-a.lookback
    if not 0<end-start<=3600:p.error("time range must be positive and at most one hour")
    try:result=globals()[a.source](a,start,end)
    except (ValueError,urllib.error.URLError,TimeoutError,KeyError,TypeError,AttributeError) as exc:result={"status":"unavailable","reason":str(exc)[:300],"evidence":[],"limitations":["A telemetry backend error is not proof of a business outage."]}
    result.update(source=a.source,window={"start":dt.datetime.fromtimestamp(start,dt.timezone.utc).isoformat(),"end":dt.datetime.fromtimestamp(end,dt.timezone.utc).isoformat()},collected_at=dt.datetime.now(dt.timezone.utc).isoformat())
    print(json.dumps(scrub(result),ensure_ascii=False,indent=2))

if __name__=="__main__":main()
