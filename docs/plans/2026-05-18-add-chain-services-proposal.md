# Proposal: Add Chain Services

## Why

把当前两服务 demo 扩展成更接近生产排障的多跳链路，让一个入口请求可以跨服务传播 trace context，并把错误或慢点定位到 Redis/MySQL 下游依赖。

## What Changes

- Add `service-c`
  - 新增独立 Go 服务，容器内端口 8080。
  - 只承担 Redis 下游服务职责。
  - 暴露 Redis 成功、错误、慢操作接口。
  - 使用 YAML 配置、Gin、otelgin、go-redis、redisotel、OTLP gRPC。

- Add `service-d`
  - 新增独立 Go 服务，容器内端口 8080。
  - 只承担 MySQL 下游服务职责。
  - 暴露 MySQL 成功、错误、慢查询接口。
  - 使用 YAML 配置、Gin、otelgin、GORM、GORM OpenTelemetry instrumentation、OTLP gRPC。

- Modify `service-a`
  - 新增 6 个外部入口接口：
    - `GET /chain/redis/ok`
    - `GET /chain/mysql/error`
    - `GET /chain/fanout/ok`
    - `GET /chain/fanout/error`
    - `GET /chain/slow/redis`
    - `GET /chain/degrade/ok`
  - 这些接口只调用 `service-b`，不直接访问 Redis/MySQL。

- Modify `service-b`
  - 新增链路编排接口。
  - 根据场景调用 `service-c` 和 `service-d`。
  - fan-out 场景同时调用 Redis 服务和 MySQL 服务。
  - 降级场景返回 200，但保留下游错误信息和 error span。

- Modify shared configuration shape per service module
  - `service-a` 和 `service-b` 的 peer 配置增加 `service_c_url`、`service_d_url`。
  - `service-c` 只需要 Redis、OTel、server 配置。
  - `service-d` 只需要 MySQL、OTel、server 配置。

- Modify `docker-compose.yml`
  - 增加 `service-c`、`service-d` build/service。
  - 不增加 MySQL、Redis、RabbitMQ 容器。
  - `traefik` depends_on 增加新服务。

- Modify `traefik/dynamic.yaml`
  - 可选暴露 `/service-c`、`/service-d` 路由用于手动调试。
  - 用户推荐入口仍然是 `/service-a/chain/*`。

- Modify `README.md`
  - 增加复杂链路场景说明、curl 示例、Jaeger 观察点。

## Capabilities

- **Multi-hop Redis Success**
  - `a -> b -> c -> redis`
  - 验证超过两跳的 trace context 传播。

- **Deep MySQL Error Root Cause**
  - `a -> b -> d -> mysql(error)`
  - 验证入口 500 的根因在最下游 DB span。

- **Fan-out Success**
  - `a -> b -> c(redis ok)` 和 `b -> d(mysql ok)`
  - 验证 Jaeger 树状链路和聚合服务形态。

- **Fan-out Partial Failure**
  - `a -> b -> c(redis ok)` 和 `b -> d(mysql error)`
  - 验证最终 500 时能定位到 `service-d` 的 MySQL span。

- **Slow Critical Path**
  - `a -> b -> c(redis slow 4s)` 和 `b -> d(mysql ok)`
  - 验证慢请求 tail sampling 和关键路径分析。

- **Degraded Success With Error Span**
  - `a -> b -> c(redis error)` 和 `b -> d(mysql ok)`
  - 最终返回 200，但响应中带 `redis_error`，trace 中保留 ERROR span。

## Impact

- **Files**
  - 新增 `service-c/**`
  - 新增 `service-d/**`
  - 修改 `service-a/internal/config/config.go`
  - 修改 `service-a/internal/client/client.go`
  - 修改 `service-a/internal/handler/handler.go`
  - 修改 `service-a/config.yaml`
  - 修改 `service-b/internal/config/config.go`
  - 修改 `service-b/internal/client/client.go`
  - 修改 `service-b/internal/handler/handler.go`
  - 修改 `service-b/config.yaml`
  - 修改 `docker-compose.yml`
  - 修改 `traefik/dynamic.yaml`
  - 修改 `README.md`
  - 可选修改 `scripts/trace-demo.sh`

- **Database**
  - 不新增业务表。
  - `service-d` 继续使用 `users` 表和 AutoMigrate 机制。

- **Cache**
  - `service-c` 新增 demo Redis key 前缀，例如 `otel-demo:chain:*`。

- **External Dependencies**
  - 继续使用外置 MySQL 和 Redis。
  - 不在第一阶段新增 RabbitMQ 异步消费链路。

- **Downstream Services**
  - `service-a` 新增依赖 `service-b` 的链路编排接口。
  - `service-b` 新增依赖 `service-c` 和 `service-d`。
  - `service-c` 依赖 Redis。
  - `service-d` 依赖 MySQL。

