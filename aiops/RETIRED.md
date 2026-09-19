# Retired runtime

This directory is retained as migration history, not a running application.
The business demo uses four Go services and direct Jaeger/Loki/Prometheus query Skills.
Compose no longer starts aiops-api. The UI does not call these endpoints.
Do not run the old model provider/evaluator against the new business demo.
Shared trace extraction now lives in scripts/observability/trace_cleaner.py.
