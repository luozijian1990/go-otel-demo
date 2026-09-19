#!/usr/bin/env python3
import runpy
import sys
from pathlib import Path
root=next(p for p in Path(__file__).resolve().parents if (p/'scripts/observability/query.py').exists())
sys.path.insert(0,str(root/'scripts/observability'))
sys.argv.insert(1,'logs')
runpy.run_path(str(root/'scripts/observability/query.py'),run_name='__main__')
