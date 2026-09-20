#!/bin/sh
# Unit/static by default. Integration only against the hard-coded isolated test DB.
set -eu
cd "$(dirname "$0")/.."
case "${1:-unit}" in
 unit) unset COMMERCE_ISOLATED_MYSQL ;;
 integration) export COMMERCE_ISOLATED_MYSQL=1 ;;
 *) echo 'usage: scripts/test-commerce.sh [unit|integration]' >&2; exit 2 ;;
esac
GO_BIN=${GO_BIN:-go}
export GOCACHE=${GOCACHE:-/private/tmp/go-otel-demo-go-cache}
(cd commerce && "$GO_BIN" test -race ./...)
for service in service-a service-b service-c service-d; do
 (cd "$service" && "$GO_BIN" test ./...)
done
python3 -m unittest discover -s scripts -p '*_test.py'
node --check web/commerce.js
node --test web/commerce.test.cjs
docker compose config --quiet
