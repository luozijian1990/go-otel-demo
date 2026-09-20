#!/usr/bin/env python3
"""Copy current (including uncommitted) sources into a fresh, private Compose project.
Does not start containers or delete resources. Never inherits dependency overrides.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile


def prepare(parent=None):
    source=Path(__file__).resolve().parents[1]
    dest=Path(tempfile.mkdtemp(prefix='commerce-fixes-',dir=parent or '/private/tmp'))
    def ignore(directory,names):
        ignored={'.git','.env','.codex','.playwright-cli','output','logs','node_modules','__pycache__','.DS_Store'}
        if Path(directory).name in ('service-a','service-b','service-c','service-d'):ignored.add('config.yaml')
        return set(names)&ignored
    shutil.copytree(source,dest,dirs_exist_ok=True,ignore=ignore)
    env={k:v for k,v in os.environ.items() if not k.startswith(('BUSINESS_','COMPOSE_','DEMO_'))}
    subprocess.run(['bash','scripts/init-demo-env.sh'],cwd=dest,env=env,check=True)
    project='commerce-fixes-'+secrets.token_hex(4)
    # Explicit --env-file and project; all bind mounts resolve inside this copy.
    subprocess.run(['docker','compose','--env-file',str(dest/'.env'),'-p',project,'config','--quiet'],cwd=dest,env=env,check=True)
    launch=dest/'isolated-compose.sh'
    launch.write_text('#!/bin/sh\nset -eu\ncd "$(dirname "$0")"\nunset BUSINESS_MYSQL_DSN BUSINESS_REDIS_ADDR BUSINESS_REDIS_PASSWORD DEMO_FAULT_SECRET COMPOSE_FILE COMPOSE_PROJECT_NAME COMPOSE_PROFILES COMPOSE_ENV_FILES\nexec docker compose --env-file .env -p '+project+' "$@"\n')
    launch.chmod(0o700)
    (dest/'isolation.json').write_text(json.dumps({'project':project,'source':str(source),'directory':str(dest),'new_volumes':True,'overrides':False,'ports':'default','external_dependencies':False},indent=2))
    print(dest)
    return dest

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--parent');a=p.parse_args();prepare(a.parent)
