# Design: Go OpenTelemetry Local Demo

## Context

当前目录用于新建一个本地可运行 Demo。目标不是生产模板，而是一个能快速验证链路追踪、错误追踪、慢请求采样、跨服务 context propagation 的最小完整系统。

Demo 由两个 Go HTTP 服务、一个 OpenTelemetry Collector、一个 Jaeger all-in-one 组成。MySQL、Redis、RabbitMQ 使用宿主机外置服务，默认地址通过 `host.docker.internal` 从容器访问。

## Goals

- 两个 Go 服务 `service-a` 与 `service-b` 都使用 Gin，容器内监听 `8080`，本地分别映射到 `18080` 与 `18081`。
- 两个服务都接入 OpenTelemetry Go SDK，并通过 OTLP gRPC 将 trace 发往 `otel-collector:4317`。
- Gin 服务端使用 `otelgin`，跨服务 HTTP client 使用 `otelhttp`，确保 trace context 在 `service-a` 与 `service-b` 间传播。
- MySQL 使用 GORM，并接入 GORM OpenTelemetry instrumentation。
- Redis 使用 `go-redis`，并接入 Redis OpenTelemetry instrumentation。
- RabbitMQ 使用 `amqp091-go`，publish/错误路径手动创建 span，并尽量向 message headers 注入 `traceparent`。
- MySQL 启动时执行 `AutoMigrate` 创建 `users` 表，并在空表时插入测试数据。
- Redis 与 RabbitMQ 不可用时不阻止服务启动；调用相关接口时返回 JSON 错误。
- Collector 使用 `tail_sampling` 后接 `batch`：5xx、ERROR、超过 3 秒慢请求 100% 保留，其他正常请求约 1% 保留。
- README 提供 database 创建 SQL、启动命令、接口测试命令和 Jaeger 验证方式。

## Non-Goals

- 不在 `docker-compose.yml` 中启动 MySQL、Redis、RabbitMQ。
- 不为 RabbitMQ 配置健康检查，不让 Go 服务依赖 RabbitMQ 启动顺序。
- 不实现业务鉴权、复杂领域模型、前端页面或生产级配置中心。
- 不实现 RabbitMQ consume 后台常驻 worker；本 Demo 重点验证 publish span 和 header 注入。
- 不做 Go 服务内 1% head sampling；采样策略集中放到 Collector。
- 不依赖仓库根目录 shared 代码，避免单服务 Docker build 时无法访问上级上下文。

## Decisions

### Decision 1: 两个服务使用独立 Go module，不使用 shared 目录

**选择**：`service-a` 和 `service-b` 各自包含完整代码与 `go.mod`，通过配置区分服务名和对端地址。

**理由**：用户要求 `build: ./service-a` 与 `build: ./service-b` 能正常工作。Docker build context 默认只能访问当前服务目录，如果共享代码放在根目录，会导致构建上下文访问问题。少量重复代码在 Demo 项目中可接受，换来更直接可靠的构建方式。

### Decision 2: MySQL 是启动必需依赖，Redis/RabbitMQ 是接口级依赖

**选择**：服务启动时连接 MySQL、执行迁移、seed 数据；Redis 只创建 client 不强制 ping；RabbitMQ 初始化只保存配置，接口调用时再连接或发布。

**理由**：users 表和 MySQL 查询是核心 Demo 能力，启动时失败能尽早暴露 database 未创建或 DSN 错误。Redis 与 RabbitMQ 按需求不能阻止服务启动，尤其 RabbitMQ 可能本机未启动，因此错误必须延迟到接口调用时返回。

### Decision 3: Go 服务 always-on 产生 trace，采样集中在 Collector tail_sampling

**选择**：Go SDK 使用 parent-based always-on sampler，不做 1% head sampling。

**理由**：tail sampling 需要看到完整 trace 后才能基于状态码、span status、latency 决定保留。服务端提前丢弃 99% trace 会导致 5xx、ERROR、慢请求也可能在到达 Collector 前丢失。

### Decision 4: Collector pipeline 顺序固定为 `tail_sampling -> batch`

**选择**：traces pipeline 中 processors 按 `tail_sampling` 在前、`batch` 在后。

**理由**：tail sampling 需要先基于 trace 内容做采样决策，再批量导出。把 batch 放前面会让采样行为不符合预期，也不符合用户明确要求。

### Decision 5: HTTP 跨服务调用统一走 `otelhttp.Transport`

**选择**：封装一个 peer client，内部使用 `http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}`，所有 `/call/*` 与 `/full/*` 的对端请求都复用它。

**理由**：`otelhttp` 会自动创建 client span 并注入 W3C TraceContext headers，能最稳定地在 Jaeger 中看到 service-a 到 service-b 或反向调用的同一条 trace。

### Decision 6: RabbitMQ publish 采用手动 span 与 header 注入

**选择**：RabbitMQ 模块在 `Publish` 内手动 `tracer.Start(ctx, "rabbitmq publish")`，出错时 `RecordError` 和设置 ERROR，并用全局 propagator 注入 headers。

**理由**：Go 生态里 RabbitMQ instrumentation 不如 HTTP/GORM/Redis 统一成熟。手动 span 能满足 Demo 目标，并能清楚控制错误场景、headers、queue/exchange/routing_key 属性。

### Decision 7: Handler 层统一负责 HTTP 错误语义和当前 span 错误状态

**选择**：所有 handler 在返回 500 前都对当前 span 执行 `RecordError` 和 `SetStatus(codes.Error, ...)`，响应统一 JSON。

**理由**：Collector 的 ERROR tail sampling 依赖 span status。只返回 HTTP 500 而不设置 span status，可能无法覆盖用户要求的 “span status = ERROR 的 trace 100% 保留” 验证场景。

## Risks / Trade-offs

### Risk 1: Collector tail sampling 对 HTTP 5xx 的字段匹配依赖 instrumentation 属性名

**场景**：不同版本 instrumentation 对 HTTP status code 属性可能使用 `http.status_code` 或语义约定新字段。

**缓解**：Collector 配置优先同时覆盖 HTTP 状态码策略和 span status ERROR 策略；handler 错误路径强制设置 span status ERROR，即使 HTTP 属性策略不匹配，也能保留错误 trace。

### Risk 2: RabbitMQ 未启动会让 `/rabbitmq/ok` 返回 500

**场景**：用户本机没有 RabbitMQ，直接调用 RabbitMQ 接口会看到错误。

**缓解**：README 明确说明这是预期行为；`/full/ok` 中 RabbitMQ 错误只写入 `rabbitmq_error`，不影响主流程返回 200；服务启动不检查 RabbitMQ。

### Risk 3: MySQL database 未手工创建会导致服务启动失败

**场景**：用户未执行 `CREATE DATABASE otel_demo...`，GORM 连接失败。

**缓解**：README 将创建 SQL 放在启动步骤之前；启动日志输出清晰错误。这个失败符合需求，因为只要求表结构自动创建，不要求 database 自动创建。

### Risk 4: 正常 `/ok` 只有 1% 保留，单次测试可能在 Jaeger 中看不到

**场景**：用户只 curl 一次 `/ok`，因为 tail sampling 1% 正常采样，Jaeger 中可能没有 trace。

**缓解**：README 提醒多次请求 `/ok` 或重点用 `/error`、`/slow` 验证 100% 保留策略。

### Risk 5: 两个服务代码重复

**场景**：`service-a` 与 `service-b` 独立 module 会产生重复内部模块。

**缓解**：这是为保证 Docker build context 简单可靠而做的 Demo 级取舍。未来如果要生产化，可把 compose build context 提升到根目录，再引入 shared module 或 Go workspace。
