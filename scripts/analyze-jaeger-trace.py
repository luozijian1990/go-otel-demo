#!/usr/bin/env python3
"""Compatibility entrypoint for the shared standard-library trace cleaner."""
import runpy
from pathlib import Path
_root = next(p for p in Path(__file__).resolve().parents if (p / "scripts/observability/trace_cleaner.py").exists())
globals().update(runpy.run_path(str(_root / "scripts/observability/trace_cleaner.py"), run_name="__main__" if __name__ == "__main__" else "trace_cleaner"))
