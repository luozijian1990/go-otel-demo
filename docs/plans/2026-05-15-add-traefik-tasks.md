# Tasks: Add Traefik Gateway

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Design:** @docs/plans/2026-05-15-add-traefik-design.md

**Goal:** 在现有 Go OTel Demo 前增加 Traefik 网关，生成可手工查看 TraceId 的 JSON access log，并保持 Jaeger trace 可串联到 Go 服务。

---

## 1. Traefik 配置文件

- [x] 1.1 创建 `traefik/traefik.yaml`，配置 `entryPoints.web.address=:80`、`api.dashboard=true`、`api.insecure=true`，明确仅用于本地 Demo。
- [x] 1.2 在 `traefik/traefik.yaml` 中配置 `providers.file.filename=/etc/traefik/dynamic.yaml`，使用 file provider 加载动态路由。
- [x] 1.3 在 `traefik/traefik.yaml` 中配置 `accessLog.format=json`、`accessLog.filePath=/logs/access.log`、`accessLog.fields.defaultMode=keep`，确保日志保留 `TraceId`、`SpanId`、状态码、耗时和请求路径字段。
- [x] 1.4 在 `traefik/traefik.yaml` 中配置 `tracing.serviceName=traefik`、`tracing.sampleRate=1.0`、`tracing.otlp.grpc.endpoint=otel-collector:4317`、`tracing.otlp.grpc.insecure=true`，禁止在 Traefik 侧降低采样率。
- [x] 1.5 创建 `traefik/dynamic.yaml`，配置 `/service-a` 路由到 `http://service-a:8080`，并通过 strip prefix 转发为原始服务路径。
- [x] 1.6 在 `traefik/dynamic.yaml` 中配置 `/service-b` 路由到 `http://service-b:8080`，并通过 strip prefix 转发为原始服务路径。

## 2. Docker Compose

- [x] 2.1 修改 `docker-compose.yml` 新增 `traefik` 服务，镜像固定为 Traefik v3 系列稳定版本，挂载 `traefik/traefik.yaml`、`traefik/dynamic.yaml` 和 `logs/traefik`。
- [x] 2.2 在 `docker-compose.yml` 中为 Traefik 暴露 `10080:80` 作为 Demo 网关入口，暴露 `18082:8080` 作为本地 dashboard/API，禁止替换 service-a/service-b 直连端口。
- [x] 2.3 在 `docker-compose.yml` 中让 Traefik 依赖 `otel-collector`、`service-a`、`service-b`，不增加 MySQL、Redis、RabbitMQ 依赖。
- [x] 2.4 创建 `logs/traefik/.gitkeep`，确保本地 access log 目录存在。

## 3. 流量脚本

- [x] 3.1 修改 `scripts/trace-demo.sh`，新增 `--via-traefik service-a|service-b` 参数，将 base URL 映射为 `http://localhost:10080/service-a` 或 `http://localhost:10080/service-b`。
- [x] 3.2 保持 `--base-url` 优先级明确：如果用户显式传入 `--base-url`，脚本按用户指定地址执行。
- [x] 3.3 更新 `--help` 示例，补充通过 Traefik 造慢请求和错误请求的命令。

## 4. README

- [x] 4.1 更新 `README.md`，说明 Traefik 入口地址 `http://localhost:10080/service-a/ok` 和 `http://localhost:10080/service-b/ok`。
- [x] 4.2 更新 `README.md`，说明 Traefik dashboard 地址 `http://localhost:18082/dashboard/`，并标注仅用于本地 Demo。
- [x] 4.3 更新 `README.md`，说明 access log 路径 `logs/traefik/access.log`。
- [x] 4.4 更新 `README.md`，给出用 `jq` 手工筛选 5xx 和慢请求，并复制 `TraceId` 到 Jaeger 查询的示例。
- [x] 4.5 更新 `README.md`，说明普通请求可能因 tail sampling 查不到，错误和慢请求才是主要排障对象。

## 5. 验证

- [x] 5.1 执行 `docker compose config`，确认 Traefik 服务、端口、volume 和依赖解析正确。
- [x] 5.2 执行 `docker compose build service-a service-b`，确认已有两个 Go 服务镜像仍可构建。
- [x] 5.3 执行 `bash -n scripts/trace-demo.sh`，确认脚本语法正确。
- [x] 5.4 执行 `scripts/trace-demo.sh --via-traefik service-a --mode error --dry-run`，确认脚本生成 Traefik 路径。
- [x] 5.5 如 Docker 与外置 MySQL 可用，执行 `docker compose up` 后请求 Traefik 入口，确认 `logs/traefik/access.log` 产生 JSON 日志并包含 `TraceId`。

## 6. Jaeger Trace 分析脚本

- [x] 6.1 创建 `scripts/analyze-jaeger-trace.py`，支持 `--trace-id`、`--access-log`、`--jaeger-url`、`--mode latest-error|latest-slow`、`--slow-threshold-ms`、`--output-json` 参数。
- [x] 6.2 当用户传入 `--trace-id` 时，脚本直接请求 `GET <jaeger-url>/api/traces/<trace-id>`；当未传入时，从 Traefik access log 中按 mode 找最近一条 5xx 或慢请求并提取 `TraceId`。
- [x] 6.3 脚本清洗 Jaeger trace JSON，只保留 traceID、spanID、parent spanID、serviceName、operationName、duration、startTime、error 状态、关键 tags、exception logs，禁止输出 telemetry SDK 和空 warnings 等低价值字段。
- [x] 6.4 脚本默认输出清洗后的 JSON，供 AI 做最终分析；可选 `--format markdown` 输出简短人工摘要。
- [x] 6.5 更新 `README.md`，说明如何用脚本分析指定 TraceId，以及如何从 `logs/traefik/access.log` 自动选择最近错误/慢请求。
- [x] 6.6 执行 `python3 -m py_compile scripts/analyze-jaeger-trace.py`，确认脚本语法正确。
- [x] 6.7 如本地 Jaeger 正在运行，执行脚本分析一个已存在的 5xx TraceId，确认能输出清洗后的报告。

---

## Decision→Task 映射检查

- Decision 1：使用路径前缀路由，不使用 Host 路由 → Task 1.5、1.6、4.1 ✓
- Decision 2：使用 strip prefix，保持 Go 服务路径不变 → Task 1.5、1.6 ✓
- Decision 3：Traefik tracing sampleRate 设为 1.0 → Task 1.4 ✓
- Decision 4：access log 写本地 JSON 文件 → Task 1.3、2.1、2.4、4.3、4.4 ✓
- Decision 5：保留直连端口 → Task 2.2 ✓
- Decision 6：先做确定性 Python 脚本，不直接做 skill → Task 6.1、6.2、6.3、6.4、6.5、6.6、6.7 ✓
