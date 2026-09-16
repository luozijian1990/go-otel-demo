#!/usr/bin/env bash

set -u
set -o pipefail

BASE_URL="http://localhost:18080"
BASE_URL_EXPLICIT=0
MODE="all"
COUNT=1
SLEEP_SECONDS=0
TIMEOUT_SECONDS=20
DRY_RUN=0
FAIL_FAST=0

usage() {
  cat <<'EOF'
Usage:
  scripts/trace-demo.sh [options]

Options:
  --base-url URL       Target service base URL. Default: http://localhost:18080
  --service a|b        Shortcut for --base-url http://localhost:18080 or http://localhost:18081
  --via-traefik SVC    Route through Traefik. SVC must be service-a or service-b
  --mode MODE          all|health|ok|error|slow|mysql|redis|rabbitmq|call|full|chain. Default: all
  --count N            Repeat selected mode N times. Default: 1
  --sleep SECONDS      Sleep between requests. Default: 0
  --timeout SECONDS    curl max time per request. Default: 20
  --dry-run            Print requests without executing curl
  --fail-fast          Stop at the first failed curl command
  -h, --help           Show help

Examples:
  scripts/trace-demo.sh --mode all
  scripts/trace-demo.sh --service b --mode call
  scripts/trace-demo.sh --via-traefik service-a --mode error --count 5
  scripts/trace-demo.sh --via-traefik service-a --mode slow --count 3 --timeout 15
  scripts/trace-demo.sh --mode ok --count 100 --sleep 0.05
  scripts/trace-demo.sh --mode error --count 5
  scripts/trace-demo.sh --mode slow --count 3 --timeout 15
  scripts/trace-demo.sh --mode full
EOF
}

fail() {
  echo "error: $*" >&2
  exit 1
}

is_positive_int() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
    *) [ "$1" -gt 0 ] ;;
  esac
}

require_value() {
  local option="$1"
  local value="${2:-}"
  if [ -z "$value" ]; then
    fail "$option requires a value"
  fi
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --base-url)
      require_value "$1" "${2:-}"
      BASE_URL="${2%/}"
      BASE_URL_EXPLICIT=1
      shift 2
      ;;
    --service)
      require_value "$1" "${2:-}"
      case "$2" in
        a|service-a)
          if [ "$BASE_URL_EXPLICIT" -eq 0 ]; then
            BASE_URL="http://localhost:18080"
          fi
          ;;
        b|service-b)
          if [ "$BASE_URL_EXPLICIT" -eq 0 ]; then
            BASE_URL="http://localhost:18081"
          fi
          ;;
        *) fail "--service must be a or b" ;;
      esac
      shift 2
      ;;
    --via-traefik)
      require_value "$1" "${2:-}"
      case "$2" in
        a|service-a)
          if [ "$BASE_URL_EXPLICIT" -eq 0 ]; then
            BASE_URL="http://localhost:18086/service-a"
          fi
          ;;
        b|service-b)
          if [ "$BASE_URL_EXPLICIT" -eq 0 ]; then
            BASE_URL="http://localhost:18086/service-b"
          fi
          ;;
        *) fail "--via-traefik must be service-a or service-b" ;;
      esac
      shift 2
      ;;
    --mode)
      require_value "$1" "${2:-}"
      MODE="$2"
      shift 2
      ;;
    --count)
      require_value "$1" "${2:-}"
      is_positive_int "$2" || fail "--count must be a positive integer"
      COUNT="$2"
      shift 2
      ;;
    --sleep)
      require_value "$1" "${2:-}"
      SLEEP_SECONDS="$2"
      shift 2
      ;;
    --timeout)
      require_value "$1" "${2:-}"
      is_positive_int "$2" || fail "--timeout must be a positive integer"
      TIMEOUT_SECONDS="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --fail-fast)
      FAIL_FAST=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

case "$MODE" in
  all|health|ok|error|slow|mysql|redis|rabbitmq|call|full|chain) ;;
  *) fail "--mode must be one of: all, health, ok, error, slow, mysql, redis, rabbitmq, call, full, chain" ;;
esac

if ! command -v curl >/dev/null 2>&1; then
  fail "curl is required"
fi

request_paths_for_mode() {
  case "$1" in
    health)
      printf '%s\n' "/health"
      ;;
    ok)
      printf '%s\n' "/ok"
      ;;
    error)
      printf '%s\n' "/error" "/mysql/error" "/redis/error" "/rabbitmq/error" "/call/error" "/full/error"
      ;;
    slow)
      printf '%s\n' "/slow" "/call/slow"
      ;;
    mysql)
      printf '%s\n' "/mysql/ok" "/mysql/error"
      ;;
    redis)
      printf '%s\n' "/redis/ok" "/redis/error"
      ;;
    rabbitmq)
      printf '%s\n' "/rabbitmq/ok" "/rabbitmq/error"
      ;;
    call)
      printf '%s\n' "/call/ok" "/call/error" "/call/slow"
      ;;
    full)
      printf '%s\n' "/full/ok" "/full/error"
      ;;
    chain)
      printf '%s\n' \
        "/chain/redis/ok" \
        "/chain/mysql/error" \
        "/chain/fanout/ok" \
        "/chain/fanout/error" \
        "/chain/slow/redis" \
        "/chain/degrade/ok"
      ;;
    all)
      printf '%s\n' \
        "/health" \
        "/ok" \
        "/error" \
        "/slow" \
        "/mysql/ok" \
        "/mysql/error" \
        "/redis/ok" \
        "/redis/error" \
        "/rabbitmq/ok" \
        "/rabbitmq/error" \
        "/call/ok" \
        "/call/error" \
        "/call/slow" \
        "/full/ok" \
        "/full/error" \
        "/chain/redis/ok" \
        "/chain/mysql/error" \
        "/chain/fanout/ok" \
        "/chain/fanout/error" \
        "/chain/slow/redis" \
        "/chain/degrade/ok"
      ;;
  esac
}

run_request() {
  local path="$1"
  local url="${BASE_URL}${path}"

  if [ "$DRY_RUN" -eq 1 ]; then
    printf '[dry-run] curl --max-time %s %s\n' "$TIMEOUT_SECONDS" "$url"
    return 0
  fi

  local started
  local status
  local elapsed
  local curl_status

  started="$(date +%s)"
  printf 'GET %-28s ' "$path"

  status="$(
    curl \
      --silent \
      --show-error \
      --output /tmp/trace-demo-response.$$ \
      --write-out '%{http_code}' \
      --max-time "$TIMEOUT_SECONDS" \
      "$url"
  )"
  curl_status=$?
  elapsed=$(( $(date +%s) - started ))

  if [ "$curl_status" -ne 0 ]; then
    printf 'curl_error=%s elapsed=%ss\n' "$curl_status" "$elapsed"
    if [ "$FAIL_FAST" -eq 1 ]; then
      rm -f /tmp/trace-demo-response.$$
      exit "$curl_status"
    fi
    rm -f /tmp/trace-demo-response.$$
    return "$curl_status"
  fi

  printf 'status=%s elapsed=%ss\n' "$status" "$elapsed"
  if [ -s /tmp/trace-demo-response.$$ ]; then
    sed 's/^/  body: /' /tmp/trace-demo-response.$$
    printf '\n'
  fi
  rm -f /tmp/trace-demo-response.$$
  return 0
}

echo "base_url=$BASE_URL mode=$MODE count=$COUNT sleep=${SLEEP_SECONDS}s timeout=${TIMEOUT_SECONDS}s dry_run=$DRY_RUN"

overall_status=0
iteration=1
while [ "$iteration" -le "$COUNT" ]; do
  if [ "$COUNT" -gt 1 ]; then
    echo "iteration=$iteration/$COUNT"
  fi

  while IFS= read -r path; do
    [ -n "$path" ] || continue
    if ! run_request "$path"; then
      overall_status=1
      if [ "$FAIL_FAST" -eq 1 ]; then
        exit 1
      fi
    fi
    if [ "$SLEEP_SECONDS" != "0" ]; then
      sleep "$SLEEP_SECONDS"
    fi
  done <<EOF
$(request_paths_for_mode "$MODE")
EOF

  iteration=$((iteration + 1))
done

exit "$overall_status"
