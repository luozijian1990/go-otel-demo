# Tasks: Go OpenTelemetry Local Demo

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Design:** @docs/plans/2026-05-15-go-otel-demo-design.md

**Goal:** 生成一个可通过 `docker compose up --build` 本地运行的 Go + Gin + OpenTelemetry + Jaeger + MySQL + Redis + RabbitMQ 链路追踪 Demo。

---

## 1. 根目录与运行编排

- [x] 1.1 创建根目录文件 `docker-compose.yml`，只编排 `service-a`、`service-b`、`otel-collector`、`jaeger`，禁止加入 MySQL、Redis、RabbitMQ，禁止让任一 Go 服务 `depends_on` RabbitMQ。
- [x] 1.2 在 `docker-compose.yml` 中配置 `jaegertracing/all-in-one:1.76.0`，暴露 `16686:16686`、`4317`、`4318`，设置 `COLLECTOR_OTLP_ENABLED=true`。
- [x] 1.3 在 `docker-compose.yml` 中配置 `otel/opentelemetry-collector-contrib`，挂载 `./otel-collector-config.yaml:/etc/otelcol-contrib/config.yaml`，命令使用 `--config=/etc/otelcol-contrib/config.yaml`，暴露 `4317:4317` 与 `4318:4318`。
- [x] 1.4 在 `docker-compose.yml` 中配置 `service-a` 使用 `build: ./service-a`，映射 `18080:8080`，只依赖 `otel-collector`。
- [x] 1.5 在 `docker-compose.yml` 中配置 `service-b` 使用 `build: ./service-b`，映射 `18081:8080`，只依赖 `otel-collector`。
- [x] 1.6 创建 `otel-collector-config.yaml`，配置 `otlp` receiver 同时支持 grpc/http。
- [x] 1.7 在 `otel-collector-config.yaml` 中配置 `tail_sampling` processor，策略包含 HTTP 5xx 100% 保留、span status ERROR 100% 保留、latency 超过 3 秒，100% 保留、其他正常请求只保留 1%。
- [x] 1.8 在 `otel-collector-config.yaml` 中配置 `batch` processor，并确保 pipeline 顺序为 `tail_sampling -> batch`，禁止把 `batch` 放到 `tail_sampling` 前面。
- [x] 1.9 在 `otel-collector-config.yaml` 中配置 `otlp` exporter 指向 `jaeger:4317`，使用 insecure TLS。

## 2. 服务目录骨架

- [x] 2.1 创建 `service-a` 目录，包含 `Dockerfile`、`go.mod`、`config.yaml`、`cmd/main.go`、`internal/config`、`internal/observability`、`internal/database`、`internal/redis`、`internal/rabbitmq`、`internal/handler`、`internal/client`、`internal/model`。
- [x] 2.2 创建 `service-b` 目录，包含与 `service-a` 相同的文件和内部模块。
- [x] 2.3 两个服务都使用独立 Go module，Go 版本声明为 1.22 或以上，禁止依赖仓库根目录或上级 shared 目录，确保 `docker build ./service-a` 和 `docker build ./service-b` 可独立执行。
- [x] 2.4 两个服务的 `Dockerfile` 使用多阶段构建，builder 基于 Go 1.22 或以上，runtime 使用精简镜像，复制 `config.yaml` 和编译产物，默认启动 `/app/server`。

## 3. YAML 配置

- [x] 3.1 在 `service-a/config.yaml` 中配置 `server.name=service-a`、`server.port=8080`、`otel.service_name=service-a`、`otel.endpoint=otel-collector:4317`、`otel.insecure=true`。
- [x] 3.2 在 `service-a/config.yaml` 中配置 `peer.service_b_url=http://service-b:8080`、`peer.service_a_url=http://service-a:8080`。
- [x] 3.3 在 `service-a/config.yaml` 中配置 MySQL DSN 默认指向 `host.docker.internal:3306/otel_demo`。
- [x] 3.4 在 `service-a/config.yaml` 中配置 Redis 默认指向 `host.docker.internal:6379`，password 为空，db 为 0。
- [x] 3.5 在 `service-a/config.yaml` 中配置 RabbitMQ 默认 URL 为 `amqp://guest:guest@host.docker.internal:5672/`，queue 为 `otel-demo-queue`，exchange 为空，routing_key 为 `otel-demo-queue`，enabled 为 true。
- [x] 3.6 在 `service-b/config.yaml` 中复制相同配置，仅将 `server.name` 和 `otel.service_name` 改为 `service-b`。
- [x] 3.7 实现 `internal/config`，从命令行参数或默认路径加载 YAML，缺失配置时返回清晰错误，禁止 panic。

## 4. OpenTelemetry 初始化

- [x] 4.1 实现 `internal/observability` 的初始化函数：读取 service name 与 OTLP endpoint → 创建 OTLP gRPC exporter → 创建 resource → 创建 tracer provider。
- [x] 4.2 在 Go 服务中使用 parent-based always-on sampler，确保本地尽量 100% 产生 trace，禁止设置 1% head sampling。
- [x] 4.3 设置全局 propagator 为 W3C TraceContext 与 Baggage，保证 HTTP 和 RabbitMQ headers 可传播 trace context。
- [x] 4.4 初始化函数返回 shutdown 函数，main 退出时调用并设置超时，支持 graceful shutdown。
- [x] 4.5 Gin router 必须接入 `otelgin` middleware，HTTP client 必须使用 `otelhttp.Transport`。

## 5. MySQL 与 GORM

- [x] 5.1 实现 `internal/model.User`，字段包含 `ID uint64`、`Name string`、`Email string`、`CreatedAt time.Time`、`UpdatedAt time.Time`，映射到 `users` 表。
- [x] 5.2 实现 `internal/database`，使用 GORM MySQL driver 打开配置里的 DSN。
- [x] 5.3 GORM 接入 OpenTelemetry instrumentation，确保查询在当前 trace 下产生数据库 span。
- [x] 5.4 服务启动时执行 `AutoMigrate(&model.User{})`，MySQL 连接或迁移失败时返回错误并让服务启动失败，禁止 panic。
- [x] 5.5 服务启动时检查 `users` 表数量，如果为空则插入几条测试用户数据。
- [x] 5.6 提供 `ListUsers(ctx)` 方法用于 `/mysql/ok`，返回用户列表。
- [x] 5.7 提供 `QueryBrokenTable(ctx)` 或等价方法用于 `/mysql/error`，故意查询不存在表或构造 SQL 错误。

## 6. Redis 模块

- [x] 6.1 实现 `internal/redis`，使用 `go-redis` 创建 client，接入 OpenTelemetry instrumentation。
- [x] 6.2 Redis 初始化只创建 client，不强制 `PING`，Redis 不可用不得导致服务启动失败。
- [x] 6.3 提供 `SetGet(ctx, key, value)` 方法用于 `/redis/ok`，执行 SET 后再 GET。
- [x] 6.4 提供 `BrokenOperation(ctx)` 方法用于 `/redis/error`，通过错误 client、短超时上下文或非法操作触发错误，错误只返回给接口，禁止导致服务崩溃。

## 7. RabbitMQ 模块

- [x] 7.1 实现 `internal/rabbitmq`，使用 `amqp091-go`，初始化阶段只保存配置，禁止强制连接 RabbitMQ。
- [x] 7.2 实现 `Publish(ctx, body)`：调用时连接或复用连接 → 声明 queue → 手动创建 publish span → 注入 trace context 到 AMQP headers → 发布消息。
- [x] 7.3 `Publish(ctx, body)` 遇到连接失败、声明失败、发布失败时必须 `RecordError` 并设置 span status ERROR，返回错误，禁止 panic。
- [x] 7.4 实现 `PublishBroken(ctx)` 用于 `/rabbitmq/error`，发布到非法 exchange 或构造明确错误，返回 500。
- [x] 7.5 如果 `rabbitmq.enabled=false`，RabbitMQ 方法返回清晰的 disabled 错误，服务仍可启动。

## 8. 跨服务 HTTP Client

- [x] 8.1 实现 `internal/client`，创建使用 `otelhttp.Transport` 的 `http.Client`。
- [x] 8.2 根据当前服务名选择对端服务：`service-a` 调 `service-b`，`service-b` 调 `service-a`。
- [x] 8.3 实现 `CallPeer(ctx, path, depth)`：构造对端 URL → 注入 `depth` query → 发起请求 → 读取响应体 → 非 2xx 返回错误。
- [x] 8.4 `CallPeer` 必须使用传入 context，确保 trace context 通过 `otelhttp` 跨服务传播。

## 9. HTTP Handler

- [x] 9.1 实现统一 JSON 响应助手，所有成功与错误都返回 JSON，错误响应至少包含 `error` 字段。
- [x] 9.2 实现 `GET /health`，返回服务健康状态，不检查 RabbitMQ。
- [x] 9.3 实现 `GET /ok`，返回 200，并在当前 span 上添加普通成功事件或属性。
- [x] 9.4 实现 `GET /error`，返回 500，当前 span 必须 `RecordError` 并设置 status ERROR。
- [x] 9.5 实现 `GET /slow`，真实 sleep 5 秒后返回 200，用于触发 tail sampling latency 策略。
- [x] 9.6 实现 `GET /mysql/ok`，调用 GORM 查询 users 表，返回用户列表。
- [x] 9.7 实现 `GET /mysql/error`，调用故意失败的 SQL，返回 500，当前 span 必须记录错误并设置 status ERROR。
- [x] 9.8 实现 `GET /redis/ok`，执行 SET 和 GET，返回 key/value。
- [x] 9.9 实现 `GET /redis/error`，触发 Redis 错误，返回 500，当前 span 必须记录错误并设置 status ERROR。
- [x] 9.10 实现 `GET /rabbitmq/ok`，发布消息；RabbitMQ 不可用时返回 500 但服务不崩；成功时返回 200。
- [x] 9.11 实现 `GET /rabbitmq/error`，触发 RabbitMQ publish 错误，返回 500，当前 span 必须记录错误并设置 status ERROR。
- [x] 9.12 实现 `GET /call/ok`，默认 `depth=1`，当 `depth<=0` 时只返回本服务响应，否则调用对端 `/ok` 并传入递减 depth，禁止无限循环。
- [x] 9.13 实现 `GET /call/error`，调用对端 `/error` 并最终返回 500，trace 中应包含跨服务错误 span。
- [x] 9.14 实现 `GET /call/slow`，调用对端 `/slow` 并返回慢请求结果。
- [x] 9.15 实现 `GET /full/ok`，依次执行 MySQL 成功查询、Redis 成功操作、RabbitMQ publish、调用对端 `/ok`；RabbitMQ 不可用时只在响应中写入 `rabbitmq_error`，主流程仍返回 200。
- [x] 9.16 实现 `GET /full/error`，依次执行 MySQL 错误、Redis 错误、RabbitMQ 错误、调用对端 `/error`，最终返回 500，当前 span 必须记录所有错误并设置 status ERROR。

## 10. main 与生命周期

- [x] 10.1 `cmd/main.go` 加载 YAML 配置，初始化日志输出，所有启动错误打印清晰信息。
- [x] 10.2 `cmd/main.go` 初始化 OpenTelemetry，并通过 `defer` 或退出流程调用 shutdown。
- [x] 10.3 `cmd/main.go` 初始化 MySQL、AutoMigrate、seed 数据；初始化 Redis client；初始化 RabbitMQ wrapper 但不连接。
- [x] 10.4 `cmd/main.go` 注册 Gin router 与全部 handler，监听配置端口。
- [x] 10.5 `cmd/main.go` 捕获 SIGINT/SIGTERM，执行 HTTP server graceful shutdown 与 OpenTelemetry shutdown。
- [x] 10.6 `cmd/main.go` 禁止使用 `panic` 处理可预期错误，启动失败使用日志和非零退出码。

## 11. README

- [x] 11.1 编写 `README.md`，说明项目目标、组件关系、目录结构和外置依赖要求。
- [x] 11.2 写明手工创建 MySQL database 的 SQL：`CREATE DATABASE otel_demo DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;`。
- [x] 11.3 写明启动命令：`docker compose up --build`。
- [x] 11.4 写明 Jaeger UI 地址：`http://localhost:16686`。
- [x] 11.5 写明所有 curl 测试命令，至少覆盖 service-a 的 15 个接口。
- [x] 11.6 写明 RabbitMQ 未启动时的预期行为：服务可启动，RabbitMQ 接口返回 500，`/full/ok` 可返回 200 并标记 `rabbitmq_error`。
- [x] 11.7 写明 Jaeger 验证方式：选择 `service-a` 或 `service-b`，检查 `/error` 和 `/slow` 100% 保留、普通 `/ok` 约 1% 保留、跨服务 trace 串联。

## 12. 验证

- [x] 12.1 在 `service-a` 执行 `go mod tidy`，生成 `go.sum`。
- [x] 12.2 在 `service-b` 执行 `go mod tidy`，生成 `go.sum`。
- [x] 12.3 对两个服务执行 `gofmt -w`。
- [x] 12.4 在 `service-a` 执行 `go test ./...`，确认编译通过。
- [x] 12.5 在 `service-b` 执行 `go test ./...`，确认编译通过。
- [x] 12.6 在根目录执行 `docker compose config`，确认 Compose 配置有效。
- [x] 12.7 如本机 Docker 可用，执行 `docker compose build`，确认两个 Go 服务镜像可构建。
- [ ] 12.8 如本机 MySQL database 已创建，执行 `docker compose up --build`，确认 `service-a`、`service-b`、`otel-collector`、`jaeger` 启动；RabbitMQ 不可用不得阻止服务启动。

---

## Decision→Task 映射检查

- Decision 1：不使用 shared 目录，保证两个服务独立 build → Task 2.3 ✓
- Decision 2：RabbitMQ 启动不强制连接，接口调用时再返回错误 → Task 1.1、7.1、7.3、9.10、11.6 ✓
- Decision 3：MySQL 启动必须连接并 AutoMigrate，Redis 启动不强制 ping → Task 5.4、5.5、6.2、10.3 ✓
- Decision 4：Go 服务本地 100% 产生 trace，采样交给 collector tail_sampling → Task 4.2、1.7、1.8 ✓
- Decision 5：跨服务 HTTP trace context 通过 otelhttp 传播 → Task 4.5、8.1、8.4、9.12、9.13、9.14 ✓
- Decision 6：RabbitMQ 没有成熟 instrumentation 时手动创建 span 并注入 traceparent → Task 7.2、7.3 ✓
- Decision 7：所有错误 JSON 返回、RecordError、span status ERROR → Task 9.1、9.4、9.7、9.9、9.11、9.16 ✓
