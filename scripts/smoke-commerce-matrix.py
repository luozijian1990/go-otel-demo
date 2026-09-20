#!/usr/bin/env python3
"""Operator-only deterministic integration matrix. Requires prepare-isolated-commerce.py output.
No AI diagnosis; no deletion. Retains report and test-created paid records.
"""
import argparse
import base64
import datetime as dt
import hashlib
import hmac
import json
from pathlib import Path
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from argparse import Namespace
sys.path.insert(0,str(Path(__file__).parent/'observability'))
import query


def utc():return dt.datetime.now(dt.timezone.utc).isoformat()

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--isolation',type=Path,required=True,help='isolation.json from a fresh prepared copy')
    p.add_argument('--env-file',type=Path)
    p.add_argument('--base-url',default='http://localhost:18086')
    p.add_argument('--ui-urls',nargs=2,default=['http://localhost:18083','http://localhost:18086/ui/'])
    p.add_argument('--jaeger-url',default='http://localhost:16686');p.add_argument('--loki-url',default='http://localhost:13100');p.add_argument('--prometheus-url',default='http://localhost:19090')
    p.add_argument('--report',type=Path,required=True);p.add_argument('--wait',type=int,default=45)
    a=p.parse_args()
    marker=json.loads(a.isolation.read_text())
    if not marker['project'].startswith('commerce-fixes-') or marker['external_dependencies'] or not marker['new_volumes']:p.error('requires isolated demo project')
    for url in [a.base_url,*a.ui_urls,a.jaeger_url,a.loki_url,a.prometheus_url]:
        u=urllib.parse.urlsplit(url)
        if u.hostname not in ('localhost','127.0.0.1') or u.scheme!='http' or u.username:p.error('isolated loopback HTTP URLs only')
    if not 1<=a.wait<=60:p.error('wait must be 1..60 seconds')
    keyfile=a.env_file or a.isolation.parent/'.env'
    key=next(x.split('=',1)[1].strip() for x in keyfile.read_text().splitlines() if x.startswith('DEMO_FAULT_SECRET='))
    def call(path,body=None,headers=None):
        req=urllib.request.Request(a.base_url.rstrip('/')+path,data=json.dumps(body).encode() if body is not None else None,headers={'Content-Type':'application/json',**(headers or {})})
        try:r=urllib.request.urlopen(req,timeout=20)
        except urllib.error.HTTPError as exc:r=exc
        with r:return r.status,json.load(r),r.headers
    def stock():
        status,b,_=call('/inventory/SKU-001');assert status==200,'stock baseline unavailable';return b['available']
    cases=json.loads((Path(__file__).parent/'commerce-matrix.json').read_text())
    cases += [dict(id='healthy-'+b,business=b,status=201 if b=='order' else 200) for b in ('product','inventory','order','payment')]
    report={'started':utc(),'isolation':marker,'cases':[],'ui':[],'ai_diagnosis':'not_run'}
    for case in cases:
        row={'case_id':case['id'],'start':utc(),'result':'not_run'};report['cases'].append(row)
        try:
            before=stock();oid,rid,run=[uuid.uuid4().hex for _ in range(3)];business=case['business'];row.update(request_id=rid,order_id=oid,stock_before=before)
            order={'id':oid,'sku':'SKU-001','quantity':1}
            if business=='payment':assert call('/orders',order)[0]==201,'payment preparation failed'
            headers={'X-Request-Id':rid,'X-Experiment-Id':run}
            if 'action' in case:
                ctl={'run':run,'target':case['target'],'action':case['action'],'exp':int(time.time())+25}
                raw=base64.urlsafe_b64encode(json.dumps(ctl,separators=(',',':')).encode()).decode().rstrip('=')
                headers['X-Demo-Fault-Token']=raw+'.'+hmac.new(key.encode(),raw.encode(),hashlib.sha256).hexdigest()
            path,body={'product':('/products/SKU-001',None),'inventory':('/inventory/SKU-001',None),'order':('/orders',order),'payment':('/orders/'+oid+'/pay',{})}[business]
            start=time.time();status,b,h=call(path,body,headers);elapsed=time.time()-start;tid=h.get('X-Trace-Id');row.update(http_status=status,trace_id=tid,duration_ms=round(elapsed*1000),main_end=utc())
            assert status==case['status'],'unexpected HTTP status'
            assert tid and len(tid)==32,'missing actual TraceId'
            if 'action' in case:
                try:receipt=json.loads(base64.urlsafe_b64decode(h.get('X-Demo-Receipt','')+'==='))
                except (ValueError,TypeError):receipt={}
                row['injection_confirmed']=receipt=={'service':case['target'],'action':case['action'],'applied':True,'effect':True}
                if not row['injection_confirmed']:row['result']='injection_not_confirmed';raise AssertionError('injection not confirmed')
                if case['action'] in ('slow_sql','application_delay'):assert elapsed>=2.9,'delay did not occur'
            after=stock();row['stock_after']=after
            if business=='order' and status==201:
                assert b['state']=='awaiting_payment' and after==before-1
                assert call('/orders/'+oid+'/cancel',{})[1]['state']=='cancelled'
                assert stock()==before
            elif business=='payment':
                state=call('/orders/'+oid)[1]['state'];row['order_state']=state
                if status==500:assert state=='payment_failed' and after==before,'compensation not confirmed'
                else:
                    assert state=='paid' and after==before-1
                    assert call('/orders/'+oid+'/pay',{})[1]['state']=='paid'
                    assert stock()==after,'duplicate payment changed stock'
            else:assert after==before,'unexpected stock change'
            # Poll new log evidence after collector readiness; never substitute old files.
            deadline=time.monotonic()+a.wait
            while True:
                logs=query.logs(Namespace(service=None,trace_id=tid,request_id=None,run_id=None,limit=300,url=a.loki_url),start-2,time.time()+1)
                services={x['data']['metadata'].get('service_name') for x in logs['evidence']}
                if 'traefik' in services and services.intersection(query.BUSINESS_SERVICES):break
                if time.monotonic()>=deadline:raise AssertionError('new gateway/business log correlation missing')
                time.sleep(1)
            row['log_services']=sorted(x for x in services if x);row['log_evidence']=logs
            row['metric_window']={'start_unix':start-30,'end_unix':time.time()}
            row['metrics']=query.metrics(Namespace(service=case.get('target',business+'-service'),url=a.prometheus_url),start-30,time.time())
            row['limitations']=['Single request: metric rates blend neighboring windows and do not prove a per-request causal join.','Healthy traces may not be retained.']
            if 'action' in case:
                trace=query.traces(Namespace(service=None,trace_id=tid,url=a.jaeger_url,wait=a.wait),start,time.time()+1)
                row['trace_evidence']=trace;assert trace['status']=='available','fault trace unavailable after deadline'
                if case['action']=='missing_table':assert '1146' in json.dumps(trace)+json.dumps(logs),'native SQL cause absent'
                if case['action']=='cache_refused':
                    text=json.dumps(logs);assert 'fallback_attempted' in text and 'fallback_succeeded' in text and 'connection refused' in text
                if case['action']=='slow_sql':assert 'SLEEP(3)' in json.dumps(trace),'actual SQL wait span missing'
                if case['action']=='application_delay':assert 'SLEEP(3)' not in json.dumps(trace),'application delay mislabeled SQL'
            row['result']='pass'
        except AssertionError as exc:
            if row['result']=='not_run':row['result']='assertion_failed'
            row['reason']=str(exc)
        except Exception as exc:
            row['result']='environment_error';row['reason']=type(exc).__name__ # Never dump tokens/DSNs/requests.
        row['end']=utc();print(row['case_id']+': '+row['result'],flush=True)
        a.report.parent.mkdir(parents=True,exist_ok=True);a.report.write_text(json.dumps(report,indent=2,ensure_ascii=False))
    for url in a.ui_urls:
        try:
            with urllib.request.urlopen(url,timeout=5) as r:assert r.status==200 and b'commerce.js' in r.read()
            api=urllib.parse.urljoin(url,'/demo/runs')
            with urllib.request.urlopen(api,timeout=5) as r:assert r.status==200
            report['ui'].append({'url':url,'result':'pass'})
        except Exception as exc:report['ui'].append({'url':url,'result':'environment_error','reason':type(exc).__name__})
    try:
        report['metrics']=query.metrics(Namespace(service=None,url=a.prometheus_url),time.time()-120,time.time())
        up=query.read_json(a.prometheus_url,'/api/v1/query',{'query':'up{job="go-services"}'})
        report['scrape']=up;assert len(up['data']['result'])==4 and all(x['value'][1]=='1' for x in up['data']['result'])
        report['metrics_result']='pass'
    except Exception as exc:report['metrics_result']='environment_error'
    report['end']=utc();a.report.write_text(json.dumps(report,indent=2,ensure_ascii=False))
    return 0 if all(x['result']=='pass' for x in report['cases']+report['ui']) and report['metrics_result']=='pass' else 1
if __name__=='__main__':sys.exit(main())
