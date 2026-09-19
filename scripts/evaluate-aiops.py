#!/usr/bin/env python3
"""Submit an external diagnosis once, then retrieve independent comparison."""
import argparse
import json
import urllib.request
from pathlib import Path
from uuid import UUID

def main():
    p=argparse.ArgumentParser();p.add_argument('--analysis-id');p.add_argument('--result');p.add_argument('--summary',action='store_true');p.add_argument('--base-url',default='http://localhost:18086')
    a=p.parse_args();base=a.base_url.rstrip('/')+'/aiops/api/v1'
    if a.summary:url=base+'/summary'
    else:
        if not a.analysis_id:p.error('--analysis-id required unless --summary')
        url=base+'/analyses/'+str(UUID(a.analysis_id))
        if a.result:
            body=json.dumps(json.loads(Path(a.result).read_text())).encode()
            req=urllib.request.Request(url+'/result',data=body,headers={'Content-Type':'application/json'},method='POST')
            with urllib.request.urlopen(req,timeout=10) as r:json.load(r)
        url+='/evaluation'
    with urllib.request.urlopen(url,timeout=10) as r:print(json.dumps(json.load(r),ensure_ascii=False,indent=2))
if __name__=='__main__':main()
