# Proposal: Add Traefik Gateway

## Why

在现有 Go OTel Demo 前面增加 Traefik，用 Traefik 作为统一入口，并通过 Traefik access log 中的 `TraceId` 辅助定位 Jaeger trace。

## What Changes

- **Add** `traefik/traefik.yaml`：Traefik 静态配置，开启 web entrypoint、dashboard、JSON access log、OpenTelemetry tracing。
- **Add** `traefik/dynamic.yaml`：Traefik file provider 动态路由，把 `/service-a/*` 转发到 `service-a:8080`，把 `/service-b/*` 转发到 `service-b:8080`。
- **Modify** `docker-compose.yml`：新增 `traefik` 服务，挂载配置文件和 access log 目录，暴露网关端口和 dashboard 端口。
- **Modify** `scripts/trace-demo.sh`：增加通过 Traefik 调用 service-a/service-b 的快捷参数。
- **Modify** `README.md`：补充 Traefik 入口、access log 路径、TraceId 到 Jaeger 的排障流程。

## Capabilities

- 通过 `http://localhost:10080/service-a/ok` 访问 service-a。
- 通过 `http://localhost:10080/service-b/ok` 访问 service-b。
- Traefik 生成 root/server span，并通过 OTLP gRPC 上报到 `otel-collector:4317`。
- Traefik JSON access log 写到本地 `logs/traefik/access.log`。
- access log 中保留 `TraceId` / `SpanId`，方便手工复制到 Jaeger 查询。
- 保留 service-a/service-b 原本的直连端口 `18080` / `18081`，便于对比和 debug。

## Impact

- 新增本地端口：
  - `10080`：Traefik web entrypoint。
  - `18082`：Traefik dashboard/API，仅用于本地 Demo。
- 新增本地日志目录：`logs/traefik/`。
- 不引入 Loki/ELK/Grafana 日志检索。
- 不改变 Go 服务自身 OpenTelemetry 初始化方式。
- 不改变现有 Collector tail sampling 策略。
