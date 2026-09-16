# Proposal: Go OpenTelemetry Local Demo

## Why

需要一个完整可运行的本地 Demo，用来验证 Go 服务接入 OpenTelemetry 后，HTTP、GORM、Redis、RabbitMQ、跨服务调用在 Jaeger 中形成可观察的 trace，并通过 Collector tail sampling 保留关键异常和慢请求。

## What Changes

- **Add** `docker-compose.yml`：编排 `service-a`、`service-b`、`otel-collector`、`jaeger`，不包含 MySQL、Redis、RabbitMQ。
- **Add** `otel-collector-config.yaml`：配置 OTLP receiver、tail sampling、batch、OTLP exporter 到 Jaeger。
- **Add** `README.md`：说明外置依赖、启动方式、接口测试、Jaeger 验证方式。
- **Add** `service-a/`：独立 Go module，包含 Gin HTTP 服务、OpenTelemetry、MySQL/GORM、Redis、RabbitMQ、跨服务 client。
- **Add** `service-b/`：与 `service-a` 同结构的独立 Go module，通过配置区分服务名与对端调用。
- **Add** `service-a/config.yaml` 与 `service-b/config.yaml`：集中配置 server、otel、peer、mysql、redis、rabbitmq。

## Capabilities

- **基础请求追踪**：`/ok`、`/error`、`/slow` 可分别产生成功、错误、慢请求 trace。
- **数据库追踪**：`/mysql/ok` 产生 GORM 查询 span，`/mysql/error` 产生数据库错误 span。
- **Redis 追踪**：`/redis/ok` 产生 SET/GET span，`/redis/error` 产生 Redis 错误 span。
- **RabbitMQ 追踪**：`/rabbitmq/ok` 手动创建 publish span 并注入 trace context，`/rabbitmq/error` 产生 publish 错误 span。
- **跨服务传播**：`/call/ok`、`/call/error`、`/call/slow` 在 `service-a` 与 `service-b` 间传播 trace context。
- **组合链路**：`/full/ok` 与 `/full/error` 在一次请求内覆盖 MySQL、Redis、RabbitMQ、跨服务 HTTP。
- **尾部采样验证**：Collector 对 5xx、ERROR、慢请求 100% 保留，对其他正常请求约 1% 保留。

## Impact

- **数据库**：要求用户手工创建 `otel_demo` database；服务启动时自动创建 `users` 表并 seed 测试数据。
- **缓存**：默认连接宿主机 Redis `host.docker.internal:6379`；Redis 不可用不影响服务启动，但相关接口返回错误。
- **消息队列**：默认连接宿主机 RabbitMQ `host.docker.internal:5672`；RabbitMQ 不可用不影响服务启动。
- **端口**：`service-a` 暴露 `18080:8080`，`service-b` 暴露 `18081:8080`，Jaeger UI 暴露 `16686`，Collector 暴露 `4317/4318`。
- **镜像构建**：两个 Go 服务必须能在各自目录独立 `docker build`，不依赖上级目录代码。
