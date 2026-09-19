#!/bin/sh
# Real requests/backends only. Never calls a paid provider or deletes volumes.
set -eu
cd "$(dirname "$0")/.."
exec python3 scripts/smoke-aiops.py "$@"
