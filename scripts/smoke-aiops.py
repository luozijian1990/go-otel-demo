#!/usr/bin/env python3
import argparse
import json
import time
import urllib.request

def main():
    p=argparse.ArgumentParser();p.add_argument('--base-url',default='http://localhost:18086');p.add_argument('--all-scenarios',action='store_true');p.add_argument('--episode',action='store_true');a=p.parse_args()
    def api(path,body=None):
        req=urllib.request.Request(a.base_url+'/aiops/api/v1'+path,data=json.dumps(body).encode() if body is not None else None,headers={'Content-Type':'application/json'})
        with urllib.request.urlopen(req,timeout=15) as r:return json.load(r)
    # Seed chooses a scenario; actual applied receipts and backend evidence are checked independently.
    import random
    seeds={}
    for seed in range(100):seeds.setdefault(random.Random(seed).randrange(7),seed)
    choices=[('healthy',None)]+[('random',seeds[i]) for i in (range(7) if a.all_scenarios else [2])]
    for kind,seed in choices:
        params={'auto_analyze':False,'mode':'episode' if a.episode else 'single'}
        if seed is not None:params['seed']=seed
        r=api('/runs/'+kind,params);id=r['id'];deadline=time.monotonic()+(240 if a.episode else 90)
        while time.monotonic()<deadline:
            r=api('/runs/'+id)
            if r.get('evidence_ready'):break
            if r['status'] in ('interrupted','cancelled'):raise RuntimeError(r)
            time.sleep(2)
        assert r.get('evidence_ready'),r
        truth=api('/runs/'+id+'/ground-truth');assert truth['valid'],truth
        b=api('/runs/'+id+'/evidence')
        assert b['logs'],b['availability']
        exact=[row for row in b['logs'] if row['metadata'].get('request_id') in b['incident']['request_ids']]
        assert exact,'logs do not correlate with actual experiment requests'
        if truth['intended']['impact_class'] in ('failed','degraded'):
            assert any(row['metadata'].get('severity_text')=='ERROR' or row['metadata'].get('level')=='ERROR' for row in exact),'no actual error log in snapshot'
        assert any(row.get('series') for rows in b['metrics'].values() if isinstance(rows,list) for row in rows),b['availability']
        if kind!='healthy':assert b['traces'],b['availability']
        print(json.dumps({'run_id':id,'scenario':truth['intended']['scenario_id'],'valid':truth['valid'],'signals':b['availability'],'bundle_hash':b['bundle_hash']}))
    # Both public same-origin paths must proxy API correctly.
    for port in (18083,18086):
        with urllib.request.urlopen('http://localhost:'+str(port)+'/aiops/api/v1/runs',timeout=10) as r:assert r.status==200
    print('Real backend smoke passed; no model calls were made.')
if __name__=='__main__':main()
