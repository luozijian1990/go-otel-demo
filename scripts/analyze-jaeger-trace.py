#!/usr/bin/env python3

import argparse
import json
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional


DEFAULT_ACCESS_LOG = "logs/traefik/access.log"
DEFAULT_JAEGER_URL = "http://localhost:16686"

USEFUL_TAG_EXACT = {
    "error",
    "otel.status_code",
    "span.kind",
    "entry_point",
    "server.address",
    "server.port",
}

USEFUL_TAG_PREFIXES = (
    "http.",
    "url.",
    "db.",
    "redis.",
    "messaging.",
    "rpc.",
    "traefik.",
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Fetch a Jaeger trace by TraceId and print cleaned analysis-friendly JSON.",
    )
    parser.add_argument("--trace-id", help="TraceId to fetch from Jaeger. If omitted, choose one from Traefik access log.")
    parser.add_argument("--access-log", default=DEFAULT_ACCESS_LOG, help=f"Traefik JSON access log path. Default: {DEFAULT_ACCESS_LOG}")
    parser.add_argument("--jaeger-url", default=DEFAULT_JAEGER_URL, help=f"Jaeger base URL. Default: {DEFAULT_JAEGER_URL}")
    parser.add_argument(
        "--mode",
        choices=("latest-error", "latest-slow"),
        default="latest-error",
        help="How to choose TraceId from access log when --trace-id is omitted. Default: latest-error",
    )
    parser.add_argument("--slow-threshold-ms", type=int, default=3000, help="Slow request threshold in milliseconds. Default: 3000")
    parser.add_argument(
        "--format",
        choices=("json", "markdown"),
        default="json",
        help="Output format. Default: json. Use markdown only for a quick human-readable summary.",
    )
    parser.add_argument("--output-json", action="store_true", help="Compatibility alias for --format json.")
    parser.add_argument("--fixture-file", help="Read a local Jaeger API JSON fixture instead of calling Jaeger. Mainly for tests.")
    return parser.parse_args()


def load_json_lines_reverse(path: str) -> Iterable[Dict[str, Any]]:
    log_path = Path(path)
    if not log_path.exists():
        raise FileNotFoundError(f"access log not found: {path}")

    lines = log_path.read_text(encoding="utf-8", errors="replace").splitlines()
    for line in reversed(lines):
        line = line.strip()
        if not line:
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(value, dict):
            yield value


def find_access_log_entry(path: str, mode: str, slow_threshold_ms: int) -> Dict[str, Any]:
    threshold_ns = slow_threshold_ms * 1_000_000
    for entry in load_json_lines_reverse(path):
        trace_id = entry.get("TraceId")
        if not trace_id:
            continue

        status = as_int(entry.get("DownstreamStatus"))
        duration = as_int(entry.get("Duration"))
        if mode == "latest-error" and status is not None and status >= 500:
            return entry
        if mode == "latest-slow" and duration is not None and duration > threshold_ns:
            return entry

    if mode == "latest-error":
        raise ValueError(f"no 5xx entry with TraceId found in {path}")
    raise ValueError(f"no slow entry above {slow_threshold_ms}ms with TraceId found in {path}")


def fetch_trace(jaeger_url: str, trace_id: str) -> Dict[str, Any]:
    base = jaeger_url.rstrip("/")
    url = f"{base}/api/traces/{trace_id}"
    request = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"Jaeger API returned HTTP {exc.code} for {url}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"failed to connect to Jaeger API at {url}: {exc.reason}") from exc
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"Jaeger API returned invalid JSON for trace {trace_id}") from exc


def clean_trace(raw: Dict[str, Any], slow_threshold_ms: int = 3000) -> Dict[str, Any]:
    traces = raw.get("data") or []
    if not traces:
        raise ValueError("Jaeger response contains no trace data")

    trace = traces[0]
    processes = trace.get("processes") or {}
    process_services = {
        process_id: process.get("serviceName", process_id)
        for process_id, process in processes.items()
        if isinstance(process, dict)
    }

    cleaned_spans: List[Dict[str, Any]] = []
    exception_logs: List[Dict[str, Any]] = []
    for span in sorted(trace.get("spans") or [], key=lambda item: item.get("startTime", 0)):
        service = process_services.get(span.get("processID"), span.get("processID", "unknown"))
        tags = clean_tags(span.get("tags") or [])
        logs = clean_exception_logs(span.get("logs") or [])
        parent_id = parent_span_id(span.get("references") or [])
        duration_us = as_int(span.get("duration")) or 0
        duration_ms = round(duration_us / 1000, 3)

        cleaned = {
            "span_id": span.get("spanID"),
            "parent_span_id": parent_id,
            "service": service,
            "operation": span.get("operationName"),
            "duration_ms": duration_ms,
            "start_time": span.get("startTime"),
            "tags": tags,
            "is_error": span_is_error(tags, logs),
        }
        if logs:
            cleaned["exception_logs"] = logs
            for log in logs:
                exception_logs.append({
                    "span_id": span.get("spanID"),
                    "service": service,
                    "operation": span.get("operationName"),
                    "fields": log,
                })
        cleaned_spans.append(cleaned)

    if not cleaned_spans:
        raise ValueError("Jaeger trace contains no spans")

    entry_span = next((span for span in cleaned_spans if not span["parent_span_id"]), cleaned_spans[0])
    error_spans = [span for span in cleaned_spans if span["is_error"]]
    slow_spans = sorted(
        [span for span in cleaned_spans if span["duration_ms"] >= slow_threshold_ms],
        key=lambda span: span["duration_ms"],
        reverse=True,
    )
    top_slowest = sorted(cleaned_spans, key=lambda span: span["duration_ms"], reverse=True)[:5]

    return {
        "trace_id": trace.get("traceID"),
        "entry_span": entry_span,
        "span_count": len(cleaned_spans),
        "services": sorted({span["service"] for span in cleaned_spans}),
        "spans": cleaned_spans,
        "error_spans": error_spans,
        "slow_spans": slow_spans,
        "top_slowest_spans": top_slowest,
        "exception_logs": exception_logs,
    }


def clean_tags(tags: List[Dict[str, Any]]) -> Dict[str, Any]:
    cleaned: Dict[str, Any] = {}
    for tag in tags:
        key = tag.get("key")
        if not key or not useful_tag(key):
            continue
        cleaned[key] = tag.get("value")
    return cleaned


def useful_tag(key: str) -> bool:
    return key in USEFUL_TAG_EXACT or any(key.startswith(prefix) for prefix in USEFUL_TAG_PREFIXES)


def clean_exception_logs(logs: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    cleaned: List[Dict[str, Any]] = []
    for log in logs:
        fields = {}
        for field in log.get("fields") or []:
            key = field.get("key")
            if key == "event" or (key and key.startswith("exception.")):
                fields[key] = field.get("value")
        if fields:
            cleaned.append(fields)
    return cleaned


def parent_span_id(references: List[Dict[str, Any]]) -> Optional[str]:
    for reference in references:
        if reference.get("refType") == "CHILD_OF" and reference.get("spanID"):
            return reference["spanID"]
    for reference in references:
        if reference.get("spanID"):
            return reference["spanID"]
    return None


def span_is_error(tags: Dict[str, Any], logs: List[Dict[str, Any]]) -> bool:
    if tags.get("error") is True:
        return True
    if str(tags.get("otel.status_code", "")).upper() == "ERROR":
        return True
    status = as_int(tags.get("http.response.status_code") or tags.get("http.status_code"))
    if status is not None and status >= 500:
        return True
    return bool(logs)


def as_int(value: Any) -> Optional[int]:
    if value is None:
        return None
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def duration_ns_to_ms(value: Any) -> Optional[float]:
    duration = as_int(value)
    if duration is None:
        return None
    return round(duration / 1_000_000, 3)


def build_markdown_report(cleaned: Dict[str, Any], access_log_entry: Optional[Dict[str, Any]], slow_threshold_ms: int) -> str:
    lines: List[str] = []
    lines.append(f"# Trace Analysis: {cleaned['trace_id']}")
    lines.append("")
    lines.append("## 结论摘要")
    lines.append(summary_sentence(cleaned, access_log_entry, slow_threshold_ms))
    lines.append("")

    if access_log_entry:
        lines.append("## 入口请求")
        lines.append(f"- RequestPath: `{access_log_entry.get('RequestPath', '-')}`")
        lines.append(f"- DownstreamStatus: `{access_log_entry.get('DownstreamStatus', '-')}`")
        duration_ms = duration_ns_to_ms(access_log_entry.get("Duration"))
        lines.append(f"- Duration: `{duration_ms if duration_ms is not None else '-'}ms`")
        lines.append(f"- TraceId: `{access_log_entry.get('TraceId', cleaned['trace_id'])}`")
        lines.append("")

    lines.append("## 错误 Span")
    if cleaned["error_spans"]:
        lines.extend(span_table(cleaned["error_spans"]))
    else:
        lines.append("- 未发现 ERROR span 或 5xx span。")
    lines.append("")

    lines.append(f"## 慢 Span (阈值 {slow_threshold_ms}ms)")
    if cleaned["slow_spans"]:
        lines.extend(span_table(cleaned["slow_spans"]))
    else:
        lines.append("- 未发现超过阈值的 span。下面列出 Top 5 耗时 span：")
        lines.extend(span_table(cleaned["top_slowest_spans"]))
    lines.append("")

    lines.append("## 调用链")
    lines.extend(span_table(cleaned["spans"]))
    lines.append("")

    lines.append("## 关键证据")
    evidence = evidence_lines(cleaned)
    lines.extend(evidence if evidence else ["- 没有 exception log；请结合错误 span tags 和服务日志继续确认。"])
    lines.append("")

    lines.append("## 下一步建议")
    lines.extend(recommendation_lines(cleaned))
    lines.append("")
    return "\n".join(lines)


def summary_sentence(cleaned: Dict[str, Any], access_log_entry: Optional[Dict[str, Any]], slow_threshold_ms: int) -> str:
    if cleaned["exception_logs"]:
        first = cleaned["exception_logs"][0]
        message = first["fields"].get("exception.message", "exception recorded")
        return f"- 该 trace 的关键异常出现在 `{first['service']}` 的 `{first['operation']}`：{message}。Traefik 的 5xx span 是入口层传播结果，优先检查该业务 span。"
    if cleaned["error_spans"]:
        first = cleaned["error_spans"][0]
        return f"- 该 trace 包含错误 span，首个错误出现在 `{first['service']}` 的 `{first['operation']}`，但未发现 exception log；可优先检查该 span 及其下游调用。"
    if cleaned["slow_spans"]:
        first = cleaned["slow_spans"][0]
        return f"- 该 trace 存在超过 {slow_threshold_ms}ms 的慢 span，最慢点是 `{first['service']}` 的 `{first['operation']}`，耗时 {first['duration_ms']}ms。"
    if access_log_entry:
        return "- access log 中存在该 TraceId，但清洗后的 trace 未发现明显错误或慢 span。"
    return "- 未发现明显错误或慢 span。"


def span_table(spans: List[Dict[str, Any]]) -> List[str]:
    lines = ["| Service | Operation | Duration(ms) | Error | Span | Parent |", "|---|---|---:|---|---|---|"]
    for span in spans:
        lines.append(
            f"| `{span['service']}` | `{span['operation']}` | {span['duration_ms']} | {span['is_error']} | `{span['span_id']}` | `{span['parent_span_id'] or '-'}` |"
        )
    return lines


def evidence_lines(cleaned: Dict[str, Any]) -> List[str]:
    lines: List[str] = []
    for item in cleaned["exception_logs"]:
        fields = item["fields"]
        message = fields.get("exception.message") or fields.get("event")
        lines.append(f"- `{item['service']}` `{item['operation']}`: {message}")
    for span in cleaned["error_spans"]:
        status = span["tags"].get("http.response.status_code") or span["tags"].get("otel.status_code")
        route = span["tags"].get("http.route") or span["tags"].get("url.path") or span["tags"].get("url.full")
        if status or route:
            lines.append(f"- `{span['service']}` `{span['operation']}` tags: status=`{status}`, route=`{route}`")
    return lines


def recommendation_lines(cleaned: Dict[str, Any]) -> List[str]:
    services = ", ".join(cleaned["services"])
    lines = [f"- 先检查涉及服务的业务日志和错误处理路径：`{services}`。"]
    tag_keys = {key for span in cleaned["spans"] for key in span["tags"].keys()}
    if any(key.startswith("db.") for key in tag_keys):
        lines.append("- trace 中包含数据库 span，若错误或耗时集中在 DB span，优先检查 SQL、索引和 MySQL 连接状态。")
    if any(key.startswith("redis.") for key in tag_keys):
        lines.append("- trace 中包含 Redis span，若错误或耗时集中在 Redis span，优先检查 Redis 地址、连接和命令结果。")
    if any(key.startswith("messaging.") for key in tag_keys):
        lines.append("- trace 中包含 RabbitMQ/messaging span，若错误集中在 publish，优先检查 RabbitMQ 是否启动、exchange/queue/routing_key 是否正确。")
    if not cleaned["error_spans"] and cleaned["top_slowest_spans"]:
        slowest = cleaned["top_slowest_spans"][0]
        lines.append(f"- 当前最慢 span 是 `{slowest['service']}` `{slowest['operation']}`，可以从该操作向下游依赖继续拆分耗时。")
    return lines


def main() -> int:
    args = parse_args()
    access_log_entry = None
    trace_id = args.trace_id

    try:
        if not trace_id:
            access_log_entry = find_access_log_entry(args.access_log, args.mode, args.slow_threshold_ms)
            trace_id = access_log_entry["TraceId"]

        if args.fixture_file:
            raw = json.loads(Path(args.fixture_file).read_text(encoding="utf-8"))
        else:
            raw = fetch_trace(args.jaeger_url, trace_id)
        cleaned = clean_trace(raw, args.slow_threshold_ms)
        if args.output_json or args.format == "json":
            print(json.dumps({"access_log": access_log_entry, "trace": cleaned}, ensure_ascii=False, indent=2))
        else:
            print(build_markdown_report(cleaned, access_log_entry, args.slow_threshold_ms))
        return 0
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
