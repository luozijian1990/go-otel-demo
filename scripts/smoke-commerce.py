#!/usr/bin/env python3
"""Real local business/telemetry smoke. No model calls or database deletion."""
import argparse
import base64
import hashlib
import hmac
import json
import time
import urllib.request
import urllib.error
import uuid
from pathlib import Path

def main():
    p=argparse.ArgumentParser();p.add_argument('--base-url',default='http://localhost:18086');p.add_argument('--controls',action='store_true');p.add_argument('--env-file',type=Path,default=Path('.env'));p.add_argument('--ui-urls',nargs=2,default=['http://localhost:18083','http://localhost:18086']);a=p.parse_args()
    def call(path,body=None,headers=None):
        req=urllib.request.Request(a.base_url+path,data=json.dumps(body).encode() if body is not None else None,headers={'Content-Type':'application/json',**(headers or {})})
        try:r=urllib.request.urlopen(req,timeout=20)
        except urllib.error.HTTPError as e:r=e
        with r:return r.status,json.load(r),r.headers
    def stock():return call('/inventory/SKU-001')[1]['available']
    def create(id,quantity=1):return call('/orders',{'id':id,'sku':'SKU-001','quantity':quantity})
    initial=stock();id=uuid.uuid4().hex
    assert create(id,2)[0]==201
    assert create(id,2)[0]==200
    assert stock()==initial-2,'duplicate order deducted stock again'
    assert call('/orders/'+id+'/pay',{})[1]['state']=='paid'
    assert call('/orders/'+id+'/pay',{})[1]['state']=='paid'
    id=uuid.uuid4().hex;assert create(id,3)[0]==201
    assert call('/orders/'+id+'/cancel',{})[1]['state']=='cancelled'
    assert call('/orders/'+id+'/cancel',{})[0]==200
    assert stock()==initial-2,'idempotent cancellation did not restore stock exactly once'
    print('Order/payment idempotency and inventory release passed.',flush=True)
    if a.controls:
        # Operator-only verification, never imported by any diagnostic Skill.
        key=next(line.split('=',1)[1].strip() for line in a.env_file.read_text().splitlines() if line.startswith('DEMO_FAULT_SECRET='))
        id=uuid.uuid4().hex;before=stock();assert create(id)[0]==201;run=uuid.uuid4().hex
        data={'run':run,'target':'payment-service','action':'missing_table','exp':int(time.time())+20}
        raw=base64.urlsafe_b64encode(json.dumps(data,separators=(',',':')).encode()).decode().rstrip('=');token=raw+'.'+hmac.new(key.encode(),raw.encode(),hashlib.sha256).hexdigest()
        status,body,headers=call('/orders/'+id+'/pay',{}, {'X-Experiment-Id':run,'X-Request-Id':uuid.uuid4().hex,'X-Demo-Fault-Token':token})
        assert status==500 and body['operation_outcome']=='not_applied',(status,body)
        assert stock()==before,'payment compensation failed'
        assert call('/orders/'+id)[1]['state']=='payment_failed'
        assert headers.get('X-Trace-Id')
        print('Real payment SQL failure, propagation and compensation passed. TraceId='+headers['X-Trace-Id'],flush=True)
    for business in ('product','inventory','order','payment'):
        status,run,_=call('/demo/runs',{'business':business,'traffic':'single','mode':'fault'});assert status==202,(status,run)
        deadline=time.monotonic()+30
        while time.monotonic()<deadline:
            _,r,_=call('/demo/runs/'+run['id'])
            if r['state'] in ('completed','cancelled','environment_unhealthy'):break
            time.sleep(.5)
        assert r['state']=='completed',r
        rows=[x for x in r['requests'] if x['phase']!='preflight'];assert rows and rows[0]['trace_id'],r
        assert len({x['trace_id'] for x in r['requests']})==len(r['requests']),'driver reused control/preflight trace'
        _,answer,_=call('/demo/runs/'+run['id']+'/answer');assert answer['confirmed'],answer
        print(json.dumps({'business':business,'run_id':r['id'],'request':rows[0],'effect_confirmed':True}),flush=True)
    for url in a.ui_urls:
        with urllib.request.urlopen(url.rstrip('/')+'/demo/runs',timeout=5) as r:assert r.status==200
    print('Four business fault flows and both UI API proxies passed. No model diagnosis was fabricated.')

if __name__=='__main__':main()
