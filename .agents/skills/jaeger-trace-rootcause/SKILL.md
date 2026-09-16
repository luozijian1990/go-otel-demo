---
name: jaeger-trace-rootcause
description: Use when analyzing Jaeger TraceId values, 5xx requests, slow requests, OpenTelemetry spans, or root cause questions involving Traefik/gateway traces, Go services, and Jaeger.
---

# Jaeger Trace Rootcause

## Overview

Use this skill to turn a provided TraceId or pasted gateway log entry into a focused root-cause analysis. Model the production workflow: obtain the TraceId from the user or a log/search system first, then fetch Jaeger data by API and reason over cleaned JSON.

## When to Use

Use this skill when the user asks to:

- analyze a Jaeger trace or TraceId
- inspect a pasted Traefik/gateway log error or slow request
- explain why a 5xx request happened
- explain where a request was slow
- correlate a TraceId from logs with Jaeger
- summarize OpenTelemetry spans from this demo

Do not use this skill for generic code bugs that do not involve trace/log data.

## Required Context

Run from this repository root (`go-otel-demo`).

Expected local files and services:

- `.agents/skills/jaeger-trace-rootcause/scripts/analyze-jaeger-trace.py`
- Jaeger API at `http://localhost:16686`

If Jaeger is not running, say that trace extraction needs the local compose stack and suggest `docker compose up -d`.

## Workflow

### 1. Obtain TraceId

Use one of these production-like inputs:

- User provides a TraceId directly.
- User pastes a gateway/application log line that contains a TraceId.
- User provides a log search API/query result that contains a TraceId.

Do not read `logs/traefik/access.log` or other local log files by default. That is a demo convenience, not the normal production workflow. If no TraceId or log content is provided, ask for a TraceId or a pasted log entry.

### 2. Extract Clean JSON

Fetch Jaeger data by TraceId:

```bash
python3 .agents/skills/jaeger-trace-rootcause/scripts/analyze-jaeger-trace.py --trace-id TRACE_ID
```

If the command fails because local network access is sandboxed, rerun it with the required approval. The bundled script should be used in TraceId mode for this skill; it calls the local Jaeger API and returns cleaned JSON. The repository-root `scripts/analyze-jaeger-trace.py` remains a separate CLI entrypoint; keep the two copies synchronized when changing the analyzer.

### 3. Analyze Only the Cleaned JSON

Base conclusions on these fields:

- `trace.entry_span`
- `trace.error_spans`
- `trace.slow_spans`
- `trace.top_slowest_spans`
- `trace.exception_logs`
- `trace.spans[*].service`, `operation`, `duration_ms`, `parent_span_id`, `tags`

Treat Traefik 5xx spans as gateway propagation unless the Traefik span itself has a distinct routing/proxy failure. Prefer exception logs and downstream service spans for root cause.

If the user pasted a gateway log line, use it only as request context: method, path, status, duration, TraceId. Do not treat log text alone as root cause evidence when span data disagrees.

### 4. Report Format

Use this concise structure:

```markdown
**结论**
<one-sentence root cause or best current hypothesis>

**证据**
- TraceId: `<trace_id>`
- Entry: `<method/path/status/duration if known>`
- Failing span: `<service> <operation>`
- Exception: `<message if present>`

**调用链**
- `<service> <operation>` `<duration_ms>ms` `<status/error if relevant>`

**下一步**
- <specific checks: service logs, config, dependency, SQL, Redis, RabbitMQ, routing>
```

If the JSON does not support a firm root cause, explicitly say it is a hypothesis and list what additional signal is needed.

## Interpretation Rules

- Prefer spans with `exception_logs` over gateway spans for root cause.
- For 5xx, identify the deepest application span with `is_error=true` or exception logs.
- For slow requests, identify the largest `duration_ms` span and whether it is gateway, service handler, DB, Redis, RabbitMQ, or peer HTTP.
- Do not claim ordinary successful traces should be present in Jaeger; tail sampling may drop them.
- Do not paste the full raw Jaeger response. Use the cleaned JSON and quote only compact evidence.

## Useful Commands

Generate a fresh error trace through Traefik:

```bash
curl -sS -D /tmp/trace-error-headers.txt -o /tmp/trace-error-body.txt -w "%{http_code}\n" http://localhost:18086/service-a/error
sleep 12
python3 .agents/skills/jaeger-trace-rootcause/scripts/analyze-jaeger-trace.py --trace-id TRACE_ID_FROM_RESPONSE
```

Generate a fresh slow trace through Traefik:

```bash
curl -sS -D /tmp/trace-slow-headers.txt -o /tmp/trace-slow-body.txt -w "%{http_code}\n" http://localhost:18086/service-a/slow
sleep 12
python3 .agents/skills/jaeger-trace-rootcause/scripts/analyze-jaeger-trace.py --trace-id TRACE_ID_FROM_RESPONSE
```

Read `X-Trace-Id` from the saved response headers (or `trace_id` from the error body). The `sleep 12` accounts for Collector `tail_sampling.decision_wait: 10s`.
