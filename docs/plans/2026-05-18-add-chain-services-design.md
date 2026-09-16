# Design: Add Chain Services

## Context

当前 demo 已经有 `service-a` 和 `service-b` 两个 Go 服务，并支持 Gin、OpenTelemetry、Traefik、Jaeger、MySQL、Redis、RabbitMQ、HTTP 跨服务 trace propagation。现有链路适合验证基础 instrumentation，但调用深度和拓扑还不够接近真实生产排障。

本设计新增 `service-c` 和 `service-d`，把链路扩展为 `a -> b -> c/d -> Redis/MySQL`。入口仍然从 `service-a` 发起，`service-b` 承担编排职责，`service-c` 负责 Redis，`service-d` 负责 MySQL。

## Goals

- 增加两个 Go 服务：`service-c` 和 `service-d`。
- 提供 6 条生产风格复杂链路：
  - `GET /chain/redis/ok`
  - `GET /chain/mysql/error`
  - `GET /chain/fanout/ok`
  - `GET /chain/fanout/error`
  - `GET /chain/slow/redis`
  - `GET /chain/degrade/ok`
- 验证 trace context 能跨 `Traefik -> service-a -> service-b -> service-c/service-d -> Redis/MySQL` 传播。
- 验证错误请求最终可以定位到最下游 Redis/MySQL span。
- 验证慢请求可以定位到关键路径中的慢 Redis span。
- 验证入口 200 的降级请求也能通过 ERROR span 被 tail sampling 保留。
- 保持 MySQL、Redis、RabbitMQ 都是外置服务。
- 保持 RabbitMQ 连接失败不影响已有服务启动。

## Non-Goals

- 本阶段不实现 RabbitMQ 异步链路 `a -> b -> publish -> c consume`。
- 本阶段不引入 Kafka、Loki、Tempo、Grafana。
- 本阶段不把四个服务重构成一个 monorepo shared package。
- 本阶段不改变已有 `/ok`、`/error`、`/slow`、`/mysql/*`、`/redis/*`、`/rabbitmq/*`、`/call/*`、`/full/*` 行为。
- 本阶段不把 MySQL/Redis/RabbitMQ 放入 docker-compose。
- 本阶段不要求 `service-c` 连接 MySQL，也不要求 `service-d` 连接 Redis。
- 本阶段不要求用户直接从 Traefik 调用 `service-c`/`service-d`；它们的直接路由仅用于调试。

## Decisions

### Decision 1: 新增两个专职下游服务，而不是继续扩展 service-a/service-b

选择：新增 `service-c` 作为 Redis 下游服务，新增 `service-d` 作为 MySQL 下游服务。

理由：真实排障里根因经常出现在入口服务之外。把 Redis/MySQL 放到独立下游服务后，Jaeger trace 会出现更明确的层级：入口、编排、下游服务、外部依赖。这样 `$jaeger-trace-rootcause` 能验证“不要误判入口服务或编排服务”的分析能力。

拒绝方案：只在 `service-b` 内直接访问 Redis/MySQL。这样也能生成依赖 span，但无法验证多服务深层根因定位。

### Decision 2: service-a 只作为外部入口，service-b 作为编排层

选择：所有 `/chain/*` 外部入口放在 `service-a`；`service-a` 只把请求转发给 `service-b`；`service-b` 决定调用 `service-c`、`service-d`。

理由：这能模拟常见的 gateway/BFF/aggregation 分层。入口服务不直接访问 Redis/MySQL，有利于在 Jaeger 中展示“入口不是根因，编排也可能只是传播错误”。

拒绝方案：在 `service-a` 里同时编排 `service-c` 和 `service-d`。这会减少一跳，不利于验证深层 trace context 传播。

### Decision 3: fan-out 使用并发调用，但不引入新并发依赖

选择：`service-b` fan-out 场景使用标准库并发能力同时调用 `service-c` 和 `service-d`，不新增 `errgroup` 依赖。

理由：并发 fan-out 更接近 BFF/聚合服务，也方便在 Jaeger 中观察 sibling spans 和关键路径。使用标准库可以避免为 demo 增加额外依赖。

拒绝方案：顺序调用。实现简单，但不够贴近真实 fan-out；慢请求关键路径也不明显。

### Decision 4: 降级成功请求返回 200，但 trace 中保留 ERROR span

选择：`GET /chain/degrade/ok` 中，`service-c` Redis 返回错误，`service-b` 记录错误并返回 200，响应里包含 `redis_error`。

理由：生产问题不总是入口 500。很多异常以降级、兜底、部分数据缺失形式存在。让最终 HTTP 200 但 trace 内有 ERROR span，可以验证 tail sampling 的 `span status = ERROR` 策略是否能保留这类请求。

拒绝方案：降级场景也返回 500。这样无法覆盖“用户请求成功但内部发生异常”的排障类型。

### Decision 5: 第一阶段不做 RabbitMQ 异步链路

选择：RabbitMQ async 链路只写入后续计划，不在本阶段实现。

理由：异步链路涉及 publish span、consumer span、message header propagation、ack/nack、消费者生命周期和服务启动时连接失败容忍，复杂度明显高于 HTTP fan-out。当前目标是慢请求和错误请求分析，前 6 条链路已经足够覆盖。

拒绝方案：本阶段同时实现 `a -> b -> publish -> c consume`。这会扩大范围，并让验证重点从 HTTP 多跳排障分散到异步追踪模型。

### Decision 6: service-c/service-d 保持独立 Go module 和 Docker build

选择：复制现有服务的模块结构，分别创建 `service-c`、`service-d`，每个服务都有自己的 `go.mod`、`Dockerfile`、`config.yaml`、`cmd` 和 `internal`。

理由：当前项目已经采用 service-a/service-b 独立模块结构。继续沿用可以保证 docker build 简单、配置独立、演示清晰。

拒绝方案：引入根级 shared module 或 workspace。长期看能减少重复，但本阶段会增加 Go module/workspace 复杂度，不利于 demo 直观运行。

## Chain Contracts

### 1. `GET /chain/redis/ok`

调用链：

```text
Traefik -> service-a /chain/redis/ok
        -> service-b /chain/redis/ok
        -> service-c /redis/ok
        -> Redis SET/GET
```

预期：最终返回 200，响应包含 service-a/service-b/service-c 的调用结果。

### 2. `GET /chain/mysql/error`

调用链：

```text
Traefik -> service-a /chain/mysql/error
        -> service-b /chain/mysql/error
        -> service-d /mysql/error
        -> MySQL broken table query
```

预期：最终返回 500，最深层错误 span 是 `service-d` 的 MySQL span。

### 3. `GET /chain/fanout/ok`

调用链：

```text
Traefik -> service-a /chain/fanout/ok
        -> service-b /chain/fanout/ok
           -> service-c /redis/ok
           -> service-d /mysql/ok
```

预期：最终返回 200，Jaeger 中 `service-c` 和 `service-d` 是 `service-b` 下的 sibling branches。

### 4. `GET /chain/fanout/error`

调用链：

```text
Traefik -> service-a /chain/fanout/error
        -> service-b /chain/fanout/error
           -> service-c /redis/ok
           -> service-d /mysql/error
```

预期：最终返回 500，根因是 `service-d -> MySQL`。

### 5. `GET /chain/slow/redis`

调用链：

```text
Traefik -> service-a /chain/slow/redis
        -> service-b /chain/slow/redis
           -> service-c /redis/slow
           -> service-d /mysql/ok
```

预期：最终返回 200，总耗时约 4 秒以上，关键慢点是 `service-c /redis/slow`。

### 6. `GET /chain/degrade/ok`

调用链：

```text
Traefik -> service-a /chain/degrade/ok
        -> service-b /chain/degrade/ok
           -> service-c /redis/error
           -> service-d /mysql/ok
```

预期：最终返回 200，响应包含 `redis_error`，trace 内至少有一个 ERROR span。

## Risks / Trade-offs

### Risk 1: 四个独立 Go module 会带来重复代码

场景：config、observability、HTTP client、error response 逻辑在四个服务中重复。

缓解：本阶段接受重复，保持 demo 结构直观；只在后续确实难维护时再考虑 shared module。

### Risk 2: fan-out 并发中的错误聚合容易丢失根因

场景：`service-b` 同时调用 `service-c` 和 `service-d`，其中一个失败，另一个成功；如果只返回笼统错误，会让 rootcause 分析缺少上下文。

缓解：响应 JSON 必须保留每个分支的 `status`、`body`、`error`；span 中对失败分支执行 `RecordError` 并设置 `ERROR`。

### Risk 3: 降级成功请求可能被误认为完全成功

场景：入口 HTTP 200，但 Redis 分支失败。如果没有 ERROR span，tail sampling 的错误策略可能无法保留 trace。

缓解：`service-c /redis/error` 必须设置 span status ERROR；`service-b /chain/degrade/ok` 记录降级事件和错误字段，但最终 HTTP 仍返回 200。

### Risk 4: MySQL/Redis 外置依赖不可用会影响部分链路验证

场景：本地 Redis 或 MySQL 未启动，相关链路返回错误。

缓解：README 明确依赖要求；服务启动阶段只强制 MySQL 用于需要 AutoMigrate 的服务，Redis client 创建不 ping；接口调用时返回 JSON 错误。

### Risk 5: 普通成功链路可能被 tail sampling 丢弃

场景：`/chain/redis/ok` 或 `/chain/fanout/ok` 成功请求只有 1% 被保留，用户用 TraceId 查 Jaeger 时可能查不到。

缓解：README 和 UI 文案强调错误、慢请求和 ERROR span 场景更适合验证采样保留；普通成功链路主要用于本地短时间批量触发观察。

