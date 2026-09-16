# Tasks: Add Chain Services

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Design:** @docs/plans/2026-05-18-add-chain-services-design.md

**Goal:** 新增 service-c/service-d 和 6 条复杂链路，让 Jaeger 能展示多跳、fan-out、慢请求、深层错误和降级成功场景。

---

## 1. service-c Redis 下游服务

- [x] 1.1 创建 `service-c/go.mod`、`service-c/go.sum`、`service-c/Dockerfile`、`service-c/config.yaml`：沿用 service-a/service-b 的 Go 版本、GOPROXY、OTLP 配置方式 → `server.name` 固定为 `service-c` → 不包含 MySQL/RabbitMQ 必填配置。

- [x] 1.2 创建 `service-c/internal/config/config.go`：只解析 `server`、`otel`、`redis` 配置 → 校验 `server.name`、`server.port`、`otel.service_name`、`otel.endpoint`、`redis.addr` → 不校验 MySQL/RabbitMQ。

- [x] 1.3 创建 `service-c/internal/observability/observability.go`：沿用现有 OTLP gRPC exporter 初始化 → service name 使用 YAML → 返回 graceful shutdown 函数。

- [x] 1.4 创建 `service-c/internal/redis/redis.go`：使用 go-redis client 和 redisotel instrumentation → 不在启动时 ping Redis → 实现 `SetGet(ctx,key,value)`、`BrokenOperation(ctx)`、`SlowOperation(ctx,delay)`。

- [x] 1.5 创建 `service-c/internal/handler/handler.go`：注册 `/health`、`/redis/ok`、`/redis/error`、`/redis/slow` → 成功返回 JSON → 错误时 `RecordError`、设置 span status ERROR、返回 JSON 错误。

- [x] 1.6 创建 `service-c/cmd/main.go`：加载 `-config` YAML → 初始化 OTel、Redis、Gin、otelgin → HTTP server graceful shutdown → Redis close graceful → 不因为 RabbitMQ/MySQL 缺失失败。

## 2. service-d MySQL 下游服务

- [x] 2.1 创建 `service-d/go.mod`、`service-d/go.sum`、`service-d/Dockerfile`、`service-d/config.yaml`：沿用 service-a/service-b 的 Go 版本、GOPROXY、OTLP 配置方式 → `server.name` 固定为 `service-d` → 不包含 Redis/RabbitMQ 必填配置。

- [x] 2.2 创建 `service-d/internal/config/config.go`：只解析 `server`、`otel`、`mysql` 配置 → 校验 `server.name`、`server.port`、`otel.service_name`、`otel.endpoint`、`mysql.dsn` → 不校验 Redis/RabbitMQ。

- [x] 2.3 创建 `service-d/internal/model/user.go`：沿用 users 表模型 → 字段保持 `id`、`name`、`email`、`created_at`、`updated_at`。

- [x] 2.4 创建 `service-d/internal/database/database.go`：使用 GORM MySQL 和 tracing plugin → 启动时 AutoMigrate users → 表为空时 seed 测试数据 → 实现 `ListUsers(ctx)`、`QueryBrokenTable(ctx)`、`SlowListUsers(ctx,delay)`。

- [x] 2.5 创建 `service-d/internal/handler/handler.go`：注册 `/health`、`/mysql/ok`、`/mysql/error`、`/mysql/slow` → 成功返回 JSON → 错误时 `RecordError`、设置 span status ERROR、返回 JSON 错误。

- [x] 2.6 创建 `service-d/cmd/main.go`：加载 `-config` YAML → 初始化 OTel、MySQL、Gin、otelgin → HTTP server graceful shutdown → MySQL 初始化失败时服务启动失败并打印清楚日志。

## 3. service-a 入口链路

- [x] 3.1 修改 `service-a/internal/config/config.go`：在 `PeerConfig` 增加 `service_c_url`、`service_d_url` 字段 → 保持现有 `service_a_url`、`service_b_url` 兼容 → 校验时至少保留 service-a 调 service-b 所需配置。

- [x] 3.2 修改 `service-a/config.yaml`：在 `peer` 下增加 `service_c_url: http://service-c:8080` 和 `service_d_url: http://service-d:8080` → 保持现有 MySQL/Redis/RabbitMQ 配置不变。

- [x] 3.3 修改 `service-a/internal/client/client.go` 或新增链路 client：支持调用指定 base URL 和 path → 使用 otelhttp transport → 保留 trace context propagation → 非 2xx 返回 status、body、error。

- [x] 3.4 修改 `service-a/internal/handler/handler.go` 注册 6 个外部入口：`/chain/redis/ok`、`/chain/mysql/error`、`/chain/fanout/ok`、`/chain/fanout/error`、`/chain/slow/redis`、`/chain/degrade/ok`。

- [x] 3.5 修改 `service-a/internal/handler/handler.go` 实现链路入口方法：每个方法只调用 `service-b` 对应 `/chain/*` path → 不直接调用 `service-c`/`service-d` → 透传 `service-b` 响应 body/status → 错误时记录当前 span ERROR。

## 4. service-b 编排链路

- [x] 4.1 修改 `service-b/internal/config/config.go`：在 `PeerConfig` 增加 `service_c_url`、`service_d_url` 字段 → 保持现有 service-a/service-b 配置兼容 → 校验 service-b 编排所需的 service-c/service-d URL。

- [x] 4.2 修改 `service-b/config.yaml`：在 `peer` 下增加 `service_c_url: http://service-c:8080` 和 `service_d_url: http://service-d:8080` → 保持现有外置依赖配置不变。

- [x] 4.3 修改 `service-b/internal/client/client.go` 或新增 downstream client：支持 `Call(ctx, baseURL, path)` → 使用 otelhttp transport → 返回 status、body、error → 非 2xx 不丢 body。

- [x] 4.4 修改 `service-b/internal/handler/handler.go` 注册 6 个编排接口：路径与 service-a 的 `/chain/*` 保持一致 → 这些接口由 service-a 调用，不作为推荐外部入口。

- [x] 4.5 实现 `service-b /chain/redis/ok`：调用 `service-c /redis/ok` → 返回 200 和分支结果 → 不调用 MySQL。

- [x] 4.6 实现 `service-b /chain/mysql/error`：调用 `service-d /mysql/error` → 收到非 2xx 时记录当前 span ERROR → 返回 500，响应保留 downstream body。

- [x] 4.7 实现 `service-b /chain/fanout/ok`：并发调用 `service-c /redis/ok` 和 `service-d /mysql/ok` → 两个分支均成功时返回 200 → 响应包含两个分支的 status/body。

- [x] 4.8 实现 `service-b /chain/fanout/error`：并发调用 `service-c /redis/ok` 和 `service-d /mysql/error` → MySQL 分支失败时记录当前 span ERROR → 最终返回 500 → 响应同时包含 Redis 成功结果和 MySQL 错误结果。

- [x] 4.9 实现 `service-b /chain/slow/redis`：并发调用 `service-c /redis/slow` 和 `service-d /mysql/ok` → Redis 慢操作约 4 秒 → 两个分支成功时返回 200 → 响应包含每个分支耗时或状态。

- [x] 4.10 实现 `service-b /chain/degrade/ok`：并发调用 `service-c /redis/error` 和 `service-d /mysql/ok` → Redis 分支失败时记录当前 span ERROR 并写入 `redis_error` → 最终仍返回 200 → 响应包含 MySQL 成功结果和 Redis 错误。

## 5. Docker Compose 与 Traefik

- [x] 5.1 修改 `docker-compose.yml` 增加 `service-c`：build `./service-c` → command `-config /etc/go-otel-demo/config.yaml` → mount `./service-c/config.yaml` → 暴露本地端口可选，例如 `18084:8080`。

- [x] 5.2 修改 `docker-compose.yml` 增加 `service-d`：build `./service-d` → command `-config /etc/go-otel-demo/config.yaml` → mount `./service-d/config.yaml` → 暴露本地端口可选，例如 `18085:8080`。

- [x] 5.3 修改 `docker-compose.yml` 中 `traefik.depends_on`：增加 `service-c`、`service-d` → 不增加 MySQL/Redis/RabbitMQ depends_on。

- [x] 5.4 修改 `traefik/dynamic.yaml`：可选增加 `/service-c`、`/service-d` router 和 strip prefix middleware → 不改变 `/service-a`、`/service-b` 路由。

## 6. 脚本与文档

- [x] 6.1 修改 `scripts/trace-demo.sh`：新增 mode 或 endpoint 列表覆盖 6 个 `/chain/*` 场景 → 默认仍请求 service-a → 保持现有参数兼容。

- [x] 6.2 修改 `README.md` 增加复杂链路章节：说明 service-a/service-b/service-c/service-d 职责 → 列出 6 个 curl 示例 → 标明 RabbitMQ async 不在第一阶段。

- [x] 6.3 修改 `README.md` 增加 Jaeger 观察点：深层 MySQL 错误看 `service-d` span → Redis 慢请求看 `service-c` span → 降级 200 看 ERROR span 保留。

- [x] 6.4 如果 `web/index.html` 已经由 add-demo-ui 计划实现，则补充复杂链路按钮：添加 6 个 `/service-a/chain/*` 按钮 → 保持同源请求 → 不引入新的前端依赖；如果 UI 尚未实现，则不在本任务中创建 UI。

## 7. 验证

- [x] 7.1 分别在 `service-a`、`service-b`、`service-c`、`service-d` 执行 `go mod tidy` → 确认依赖完整。

- [x] 7.2 分别在 `service-a`、`service-b`、`service-c`、`service-d` 执行 `go test ./...` → 确认编译和单元测试通过。

- [x] 7.3 执行 `docker compose config` → 确认 compose 文件和服务引用有效。

- [x] 7.4 执行 `docker compose build service-a service-b service-c service-d` → 确认四个 Go 服务镜像可构建。

- [x] 7.5 启动 stack 后请求 `http://localhost:10080/service-a/chain/redis/ok` → 返回 200 → Jaeger 中看到 `traefik -> service-a -> service-b -> service-c -> redis`。

- [x] 7.6 请求 `http://localhost:10080/service-a/chain/mysql/error` → 返回 500 → Jaeger 中最深层错误为 `service-d -> mysql`。

- [x] 7.7 请求 `http://localhost:10080/service-a/chain/fanout/ok` → 返回 200 → Jaeger 中看到 `service-c` 和 `service-d` 分支。

- [x] 7.8 请求 `http://localhost:10080/service-a/chain/fanout/error` → 返回 500 → 响应同时包含 Redis 成功和 MySQL 错误 → Jaeger root cause 指向 `service-d`。

- [x] 7.9 请求 `http://localhost:10080/service-a/chain/slow/redis` → 返回 200 且耗时约 4 秒以上 → tail sampling 后 Jaeger 保留 trace → 慢点指向 `service-c /redis/slow`。

- [x] 7.10 请求 `http://localhost:10080/service-a/chain/degrade/ok` → 返回 200 且响应包含 `redis_error` → tail sampling 后 Jaeger 保留 trace → trace 内存在 ERROR span。

## Decision→Task 映射检查

- Decision 1 新增两个专职下游服务 → Task 1.1-1.6, 2.1-2.6 ✓
- Decision 2 service-a 外部入口、service-b 编排 → Task 3.4, 3.5, 4.4-4.10 ✓
- Decision 3 fan-out 并发且不引入新并发依赖 → Task 4.7, 4.8, 4.9, 4.10 ✓
- Decision 4 降级成功返回 200 且保留 ERROR span → Task 4.10, 7.10 ✓
- Decision 5 第一阶段不做 RabbitMQ async → Task 6.2 已明确文档边界 ✓
- Decision 6 service-c/service-d 独立 Go module 和 Docker build → Task 1.1, 2.1, 5.1, 5.2, 7.4 ✓
- Risk 2 fan-out 错误聚合 → Task 4.7-4.10 已要求保留每个分支 status/body/error ✓
- Risk 3 降级成功误判完全成功 → Task 4.10, 7.10 已要求 ERROR span 和 `redis_error` ✓
- Risk 4 外置依赖不可用 → Task 1.4, 2.6, 6.2 已体现启动和文档约束 ✓
- Risk 5 成功链路可能被采样丢弃 → Task 6.3, 7.9, 7.10 已聚焦错误/慢请求验证 ✓
