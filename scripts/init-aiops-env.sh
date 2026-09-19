#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ -e .env ]; then echo '.env already exists; left unchanged'; exit 0; fi
umask 077
# noclobber prevents concurrent initialization from overwriting a key.
set -C
printf 'DEMO_FAULT_SECRET=%s\nAIOPS_LLM_MODE=external\n' "$(openssl rand -hex 32)" > .env
echo 'Created private .env (ignored by Git).'
