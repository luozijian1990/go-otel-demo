# Design: Add Traefik Gateway

## Context

当前 Demo 已包含 `service-a`、`service-b`、`otel-collector`、`jaeger`，并通过两个 Go 服务直接暴露端口验证 trace。下一步增加 Traefik，目标是模拟更真实的入口网关场景：请求先进入 Traefik，再转发到 Go 服务，并在 access log 中拿到 `TraceId`。

用户当前不需要日志检索系统，日志只需要写成本地 JSON 文件，手工查看即可。

## Goals

- 新增 Traefik 服务，作为两个 Go 服务前面的统一 HTTP 入口。
- Traefik 通过路径路由访问服务：
  - `/service-a/*` 转发到 `service-a:8080`。
  - `/service-b/*` 转发到 `service-b:8080`。
- Traefik 使用 strip prefix，让 Go 服务继续收到原有路径，例如 `/service-a/ok` 到上游变成 `/ok`。
- Traefik 开启 OpenTelemetry tracing，OTLP gRPC 上报到 `otel-collector:4317`。
- Traefik access log 使用 JSON 格式，写到 `logs/traefik/access.log`。
- 保留 access log 默认字段，确保包含 `TraceId`、`SpanId`、`DownstreamStatus`、`Duration`、`RequestPath` 等字段。
- 保留 service-a/service-b 直连端口，便于 debug 时绕过 Traefik。
- 更新 `scripts/trace-demo.sh`，支持一键通过 Traefik 打 service-a 或 service-b。
- 增加一个轻量 Python 提取脚本：从 Traefik access log 获取 `TraceId` 或接收用户传入的 `TraceId`，拉取 Jaeger trace JSON，清洗无意义字段，并默认输出适合 AI 继续分析的结构化 JSON。

## Non-Goals

- 不接入 Loki、ELK、OpenSearch、Grafana 日志检索。
- 不把 Traefik access log 通过 OTLP logs 发到 Collector。
- 不引入 HTTPS、ACME、证书、鉴权或生产级 dashboard 安全配置。
- 不删除 service-a/service-b 原有直连端口。
- 不修改 Go 服务的业务接口路径。
- 不修改现有 Collector tail sampling 策略。
- 不在 Python 分析脚本中调用外部大模型 API；脚本只负责确定性抓取、清洗和基础归纳，AI 分析由当前对话完成。

## Decisions

### Decision 1: 使用路径前缀路由，而不是 Host 路由

**选择**：通过 `/service-a/*` 和 `/service-b/*` 区分后端服务。

**理由**：Demo 本地访问不需要配置 DNS 或手写 Host header，`curl http://localhost:10080/service-a/ok` 更直接。Host 路由更接近生产，但本地 Demo 操作成本更高。

### Decision 2: 使用 strip prefix，保持 Go 服务接口不变

**选择**：Traefik 对 `/service-a` 和 `/service-b` 做 `stripPrefix`。

**理由**：Go 服务已经实现 `/ok`、`/error`、`/slow` 等接口。网关路径只应该是入口层路由细节，不应该迫使服务端 handler 增加 `/service-a` 前缀。

### Decision 3: Traefik tracing sampleRate 设为 1.0

**选择**：Traefik 侧 100% 产生 trace，把是否保留交给 Collector tail sampling。

**理由**：用户主要关注错误和慢请求。Collector 需要看到完整 trace 才能基于 5xx、ERROR、latency 做 tail sampling。如果 Traefik 先采样丢弃，Jaeger 中可能缺少入口网关 span。

### Decision 4: access log 先写本地 JSON 文件

**选择**：`accessLog.format=json`，`filePath=/logs/access.log`，通过 volume 映射到 `logs/traefik/access.log`。

**理由**：当前只是 Demo，用户明确暂时手工看 access log。不引入日志检索系统可以降低复杂度，同时 JSON log 已经足够用 `jq` 过滤慢请求和 5xx。

### Decision 5: 保留直连端口

**选择**：继续保留 `18080:8080` 和 `18081:8080`。

**理由**：调试时可以快速判断问题在 Traefik 层还是 Go 服务层。删除直连端口会让 Demo 排障路径变长。

### Decision 6: 先做确定性 Python 脚本，不直接做 skill

**选择**：新增 `scripts/analyze-jaeger-trace.py`，实现 access log 查找、Jaeger API 拉取、trace JSON 清洗和结构化 JSON 输出；Markdown 仅作为可选人工摘要。

**理由**：这部分有明确输入输出，适合先用脚本做成可重复工具。最终根因分析仍交给 AI 读取 JSON 完成，这样脚本通用性更强，也避免把主观分析逻辑固化在脚本里。

## Risks / Trade-offs

### Risk 1: access log 里有 TraceId，但 Jaeger 查不到

**场景**：正常请求被 Collector tail sampling 丢弃。

**缓解**：README 明确说明慢请求和错误请求按现有策略会保留；普通成功请求可能查不到。用户当前只关注慢请求和错误请求，这个行为可接受。

### Risk 2: dashboard 使用 insecure 模式

**场景**：Traefik dashboard 暴露在本地 `18082`，没有鉴权。

**缓解**：仅用于本地 Demo，README 标注不要用于生产。生产环境应关闭 insecure dashboard 或加认证。

### Risk 3: 日志目录权限问题

**场景**：容器无法写入挂载目录。

**缓解**：使用目录挂载 `./logs/traefik:/logs`，让 Docker 自动创建目录；如仍遇到权限问题，README 给出检查路径。

### Risk 4: Trace JSON 太大导致 AI 分析噪声高

**场景**：Jaeger API 返回大量 SDK、网络、空字段，直接贴给 AI 会稀释关键错误。

**缓解**：Python 脚本只保留 service、operation、duration、status、http/db/redis/rabbitmq 关键 tag、exception log 和父子关系，丢弃 telemetry SDK、空 warnings、低价值 network 噪声字段，默认输出 JSON 供 AI 分析。
