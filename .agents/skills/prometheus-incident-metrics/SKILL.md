---
name: prometheus-incident-metrics
description: Query Prometheus business-service metrics to compare request rate, errors, latency, dependency failures and fallback before, during and after an incident.
---

# Prometheus incident evidence

Use bounded, fixed read-only PromQL templates:

```bash
python3 .agents/skills/prometheus-incident-metrics/scripts/query.py --service order-service --start START_UTC --end END_UTC
```

Omit --service to compare all four services. Default URL: http://localhost:19090. --lookback defaults to 300 seconds; maximum window is one hour. Run separate queries for baseline, incident and recovery, keeping their original timestamps instead of replacing baseline with the latest minute.

Metrics correlate by service and time window. Never add request/run/trace/order IDs as labels or claim that increase()==1 must correspond to one click. Native units are included in each evidence item. Rolling 1m rates blend adjacent phases; low request counts and short windows make P95 unreliable. NaN, missing scrape, absent series and actual zero must remain distinct.

Compare HTTP errors with dependency errors and fallback. A 200 response with cache fallback can be a degraded business flow. Application delay is not automatically database or Redis delay. Do not infer host bottlenecks without relevant measurements.

Return evidence IDs, expressions, windows, sample limitations, observed comparisons and counter-evidence. Backend unavailability is a collection failure, not proof of a service outage.

Business templates support only four business services; --service traefik is rejected before querying. Use gateway logs/traces. fallback_rate measures attempts/second (demo_fallback_total); fallback_success_rate and fallback_failure_rate measure terminal results/second (demo_fallback_results_total). Cache misses are not fallback attempts. Dependency outcome="rejected" counts business rejections; outcome="error" remains technical failure. In-flight attempts may not yet have terminal results.
