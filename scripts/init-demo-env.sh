#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ -e .env ]; then echo '.env already exists; left unchanged'; exit 0; fi
umask 077
set -C
printf 'DEMO_FAULT_SECRET=%s\n' "$(openssl rand -hex 32)" > .env
echo 'Created local demo key; no model credentials required.'
