# Proposal: Add Grafana Observability Stack

## Why

在现有 Traefik + Go + OTel Demo 基础上，增加可选 Grafana 观测栈，用 Grafana Explore 从 Traefik access log 中查看 `TraceId`，并直接跳转到 Tempo trace，验证“日志定位问题 → Trace 追踪根因”的排障流程。

## What Changes

- **Add** `docker-compose.grafana.yml`：作为可选 overlay compose，新增 Grafana、Tempo、Loki、Alloy。
- **Add** `tempo/tempo.yaml`：配置 Tempo 接收 OTLP trace。
- **Add** `loki/loki.yaml`：配置本地 Demo Loki。
- **Add** `alloy/config.alloy`：采集 `logs/traefik/access.log` 并写入 Loki。
- **Add** `grafana/provisioning/datasources/datasources.yaml`：预置 Loki 和 Tempo datasource，并配置 Loki derived fields 关联 `TraceId` 到 Tempo。
- **Modify** `otel-collector-config.yaml`：新增 `otlp/tempo` exporter，并在 traces pipeline 中同时导出到 Jaeger 和 Tempo。
- **Modify** `README.md`：增加可选 Grafana 观测栈启动方式、LogQL 查询和 TraceId 跳转说明。

## Capabilities

- 使用 `docker compose -f docker-compose.yml -f docker-compose.grafana.yml up --build` 启动增强观测栈。
- Grafana 访问地址为 `http://localhost:3000`。
- Grafana Explore 中使用 Loki 查询 Traefik access log。
- 通过 LogQL 查询 5xx 请求和慢请求。
- 日志中的 `TraceId` 可直接跳转到 Tempo trace。
- Jaeger 保持可用，Tempo 作为 Grafana 内的 trace 查询后端。

## Impact

- 新增可选本地端口：
  - `3000`：Grafana。
  - `3200`：Tempo HTTP/API。
  - `3100`：Loki HTTP/API。
- 增加本地容器资源占用。
- 不替换 Jaeger，不改变已有 Jaeger UI 验证路径。
- 不把 `TraceId` 作为 Loki label，避免高基数索引问题。
- 不引入生产级持久化、权限、告警或 dashboard。
