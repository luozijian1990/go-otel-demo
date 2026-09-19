#!/usr/bin/env python3
"""Export the API's immutable sanitized snapshot. No oracle endpoints."""
import argparse
import json
import urllib.request
from pathlib import Path

def main():
    p=argparse.ArgumentParser();p.add_argument('--run-id',required=True);p.add_argument('--base-url',default='http://localhost:18086');p.add_argument('--output',required=True)
    args=p.parse_args()
    from uuid import UUID
    id=str(UUID(args.run_id))
    with urllib.request.urlopen(args.base_url.rstrip('/')+'/aiops/api/v1/runs/'+id+'/evidence',timeout=10) as r:data=json.load(r)
    Path(args.output).write_text(json.dumps(data,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    print(data['bundle_hash'])
if __name__=='__main__':main()
