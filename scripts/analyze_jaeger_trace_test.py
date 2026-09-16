import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


def load_module():
    script_path = Path(__file__).with_name("analyze-jaeger-trace.py")
    spec = importlib.util.spec_from_file_location("analyze_jaeger_trace", script_path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class AnalyzeJaegerTraceTest(unittest.TestCase):
    def test_find_latest_error_entry_from_access_log(self):
        module = load_module()
        lines = [
            {"TraceId": "ok-trace", "DownstreamStatus": 200, "Duration": 1000, "RequestPath": "/service-a/ok"},
            {"TraceId": "first-error", "DownstreamStatus": 500, "Duration": 2000, "RequestPath": "/service-a/error"},
            {"TraceId": "latest-error", "DownstreamStatus": 503, "Duration": 3000, "RequestPath": "/service-b/error"},
        ]
        with tempfile.NamedTemporaryFile("w", delete=False) as handle:
            for line in lines:
                handle.write(json.dumps(line) + "\n")
            path = handle.name

        entry = module.find_access_log_entry(path, "latest-error", 3000)

        self.assertEqual(entry["TraceId"], "latest-error")
        self.assertEqual(entry["RequestPath"], "/service-b/error")

    def test_clean_trace_keeps_error_evidence_and_drops_noise(self):
        module = load_module()
        raw = {
            "data": [{
                "traceID": "abc123",
                "spans": [{
                    "traceID": "abc123",
                    "spanID": "root",
                    "operationName": "EntryPoint",
                    "references": [],
                    "duration": 10000,
                    "startTime": 1,
                    "processID": "p1",
                    "tags": [
                        {"key": "http.response.status_code", "value": 500},
                        {"key": "network.peer.address", "value": "127.0.0.1"},
                        {"key": "otel.status_code", "value": "ERROR"},
                    ],
                    "logs": [],
                }, {
                    "traceID": "abc123",
                    "spanID": "child",
                    "operationName": "GET /error",
                    "references": [{"refType": "CHILD_OF", "spanID": "root"}],
                    "duration": 3000,
                    "startTime": 2,
                    "processID": "p2",
                    "tags": [
                        {"key": "http.route", "value": "/error"},
                        {"key": "error", "value": True},
                    ],
                    "logs": [{
                        "fields": [
                            {"key": "event", "value": "exception"},
                            {"key": "exception.message", "value": "intentional failure"},
                        ]
                    }],
                }],
                "processes": {
                    "p1": {"serviceName": "traefik", "tags": [{"key": "telemetry.sdk.version", "value": "1.0"}]},
                    "p2": {"serviceName": "service-a", "tags": []},
                },
            }]
        }

        cleaned = module.clean_trace(raw)

        self.assertEqual(cleaned["trace_id"], "abc123")
        self.assertEqual(cleaned["entry_span"]["service"], "traefik")
        self.assertEqual(cleaned["error_spans"][0]["service"], "traefik")
        self.assertEqual(cleaned["exception_logs"][0]["fields"]["exception.message"], "intentional failure")
        root_tags = cleaned["spans"][0]["tags"]
        self.assertIn("http.response.status_code", root_tags)
        self.assertNotIn("network.peer.address", root_tags)

    def test_cli_output_json_by_default_with_fixture(self):
        script_path = Path(__file__).with_name("analyze-jaeger-trace.py")
        result = subprocess.run(
            [
                sys.executable,
                str(script_path),
                "--trace-id",
                "abc123",
                "--fixture-file",
                str(Path(__file__).with_name("fixtures") / "jaeger_trace_error.json"),
            ],
            check=True,
            text=True,
            capture_output=True,
        )

        payload = json.loads(result.stdout)

        self.assertIn("trace", payload)
        self.assertEqual(payload["trace"]["trace_id"], "abc123")
        self.assertEqual(payload["trace"]["exception_logs"][0]["fields"]["exception.message"], "intentional failure")


if __name__ == "__main__":
    unittest.main()
