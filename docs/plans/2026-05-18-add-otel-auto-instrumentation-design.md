# Design: Add OTel Auto Instrumentation Experiment

## Context

`service-a` 当前包含 6 处手动埋点代码：

| 文件 | 代码 | 作用 |
|---|---|---|
| `cmd/main.go` | `otelgin.Middleware(...)` | HTTP server 埋点 |
| `internal/database/database.go` | `db.Use(tracing.NewPlugin())` | GORM SQL 埋点 |
| `internal/redis/redis.go` | `redisotel.InstrumentTracing(rdb)` | Redis 命令埋点 |
| `internal/rabbitmq/publisher.go` | 手动 `Start` span + `Inject` 到 headers | RabbitMQ producer 埋点 |
| `internal/client/peer.go` | `otelhttp.NewTransport(...)` | HTTP client 埋点（跨服务传播） |
| `internal/observability/observability.go` | OTLP gRPC exporter + TracerProvider 注册 | SDK 配置（**不是埋点本身**） |

本实验的目标是**保留 `observability.go`、删掉前 5 处**，让 loongsuite-go-agent 通过编译期 AST 重写注入等价或近似的埋点，验证 Jaeger 中的 trace 是否仍然完整。

## 指导原则：零代码改动接入 OTel

把可观测性接入分成三层来界定责任：

- **L1 库埋点**（gin / gorm / redis / amqp091 / http client）：必须由 `otel go build` 100% 自动注入，应用代码 0 行手写。
- **L2 SDK 初始化**（TracerProvider / OTLP exporter / Propagator）：**优先尝试 env-based bootstrap**（`OTEL_SERVICE_NAME`、`OTEL_EXPORTER_OTLP_ENDPOINT` 等），让应用代码也是 0 行；若 env-only 不工作，回退保留 `observability.Init` 并记录 finding。
- **L3 业务侧 trace 消费**（`TraceIDHeaderMiddleware`、错误体里的 `trace_id`、`span.AddEvent`、`recordError`）：**明确不在零代码范围内**——这是产品行为，agent 不可能猜到你想怎么把 traceId 暴露给用户。

这条原则把整个实验的判定标准从"trace 能不能跑出来"提升到"**剥到什么程度还能跑出来**"——L1 一定能剥；L2 试着剥；L3 故意不剥。

## Goals

- 在 `service-a-auto/` 中**删除 L1 的全部 5 处**手动埋点（已在 section 2 完成）。
- **尝试**在 `service-a-auto/` 中**也删除 L2 的 `observability.Init`**（section 2.5），运行时全靠 env vars + loongsuite 自身的 bootstrap。
- 不动 L3（handler.go 中的 `TraceIDHeaderMiddleware`、`recordError`、`addTraceID`、`SpanFromContext`）。
- Dockerfile 中安装 `otel` CLI，`go build` 改为 `otel go build`。
- docker-compose 的 `service-a-auto` block 中显式设置 `OTEL_*` env vars，把 L2 的所有配置从 YAML 搬到环境变量。
- 通过对比 service-a 与 service-a-auto 在 Jaeger 中的 trace，得到一份**事实清单**，回答以下问题：
  1. 自动注入的 span 名字、attributes、span.kind 与手动埋点是否一致？
  2. tail sampling 的两条 5xx 策略（旧 / 新 semconv）是否都能命中？
  3. RabbitMQ 这种没有官方 otel 包的库，loongsuite 能不能自动注入 producer span 并写 traceparent 到 message headers？**这是最不确定的一点**。
  4. 跨服务调用时 traceparent 是否能被自动注入到 outbound HTTP header？
  5. **`observability.Init` 真的能 0 行代码省掉吗**？（L2 floor 在哪？）

## Non-Goals

- 不剥离 go.mod 中的 OTel 相关 import 行：即使 loongsuite 不需要它们，留着也不会造成功能问题（最多多一点编译时间）。剥离改动面大、收益小，不在本实验内做。
- 不修改 `internal/handler/handler.go`：里面的 `TraceIDHeaderMiddleware`、`recordError`（手动 `span.RecordError` + `SetStatus`）、`addTraceID` 等都是**业务层 trace 操作**，不是埋点框架级。这些是用户代码，loongsuite 不会覆盖；保留它们才能让 X-Trace-Id header 与错误 JSON 体里的 `trace_id` 字段继续工作（不然 demo UI 闭环就断了）。
- 不让 `service-a-auto` 也接 chain 多跳链路：本实验只验证 5 类基础 span（HTTP server / GORM / Redis / RabbitMQ / HTTP client），不涉及多服务编排。

## Decisions

### Decision 1: 副本路径与模块名

选择：目录命名为 `service-a-auto/`，Go module 名改为 `go-otel-demo/service-a-auto`。

理由：与 `service-a` 完全平级，目录命名一眼能看出"这是 auto 版"。模块名同步重命名是为了让两个服务的 `internal/` 包能在同一 docker network 里独立存在（虽然不直接互相 import，但 Go 工具链对模块名敏感）。

拒绝方案：用 git replace directive 让 `service-a-auto` 复用 `service-a` 的代码。这样能省掉很多文件复制，但**违背了"实验 vs 主代码并存"的目的**——副本必须能独立编译、独立改、独立销毁，replace 引入的耦合反而拖后腿。

### Decision 2: L2 SDK init 走"激进先试 + 失败回退"双模式

**修订**：原方案"保留 `observability.Init`"已被指导原则覆盖——**先试 0 代码，不行再回退**。

选择：

- **模式 B（默认尝试）**：在 `service-a-auto/cmd/main.go` 中**删除** `observability.Init` 调用与相关 import；删除 `internal/observability/` 包；纯靠 docker-compose 中的 OTEL_* env vars + loongsuite 自身能力 bootstrap SDK。
- **模式 A（fallback plan）**：若模式 B 跑出来 trace 为空 / Jaeger 搜不到 service-a-auto，立刻 revert——把 `observability.Init` 调用与 `internal/observability/` 包加回来，作为 finding 写入 observations："L2 在当前 loongsuite 版本下仍需手动 SDK init"。

理由：

- 用户原则"未来接入 OTel 不需要改代码"在 L2 也应当落地。OTel SDK 自身支持 env-based bootstrap；loongsuite 的官方 examples 里有不少没显式 `observability.Init` 也能跑的情况。**直接验证这条路是否通是本实验的核心价值之一**。
- 模式 A 是 plan B，不是 plan A——成本只有 git revert + `cp -r` 两行 bash。
- 若 B 跑通，零代码原则**完整**落地（L1 + L2 都剥），未来生产服务接 OTel 真的只剩"换 build 命令 + 配 env vars"两件事。
- 若 B 不通，**这个 finding 本身就很有教学价值**：它告诉我们 L2 floor 在哪，也回答了 vibe-replay PPT 里"agent 路线到底能省到什么程度"的问题。

需要在 docker-compose 中提前准备好的 env vars（即使模式 A 也无害）：

```yaml
environment:
  - OTEL_SERVICE_NAME=service-a-auto
  - OTEL_EXPORTER_OTLP_PROTOCOL=grpc
  - OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
  - OTEL_EXPORTER_OTLP_INSECURE=true
  - OTEL_TRACES_SAMPLER=parentbased_always_on
  - OTEL_PROPAGATORS=tracecontext,baggage
```

### Decision 3: 不在 `service-a-auto` 中改 `internal/handler/handler.go`

选择：handler.go 中的 `recordError`、`SetStatus`、`addTraceID`、`TraceIDHeaderMiddleware`、`SpanFromContext` 全部保留。

理由：这些是**业务侧 trace 操作**——读 trace_id、给错误 span 加业务错误信息、把 trace_id 回吐给用户——loongsuite 自动注入的是框架级 span（gin/gorm/redis/amqp/http client），不会替你做"在错误响应里 marshal trace_id"这种业务逻辑。这一层必须保留，且**保留它本身就证明了一个事实：自动埋点不等于完全零代码**。

### Decision 4: Dockerfile 安装 otel CLI 的方式

选择：在 builder 阶段用 `go install github.com/alibaba/loongsuite-go-agent/cmd/otel@latest` 安装。

理由：
- builder 镜像已有 Go 工具链，`go install` 是最干净、最可重复的方式。
- 不依赖 `curl | sudo bash`（在 CI/容器里能少一层 sudo 与 PATH 配置）。
- `@latest` 在本实验阶段够用；实验结束后写入 observations 文档时记录实际使用的版本号。

拒绝方案 1：用 `curl -fsSL https://cdn.jsdelivr.net/gh/alibaba/loongsuite-go-agent@main/install.sh | bash`。优点是和文档一致；缺点是依赖 jsdelivr CDN 可达，且需要 bash 探测平台。

拒绝方案 2：直接 `wget` 一个 release tarball。优点是版本可锁；缺点是要先知道当前最新 release 标签，多一次查询。

**Fallback**：如果 `go install` 因为 module path 不对（loongsuite 主仓改名过）失败，回退到 install.sh。Tasks 里要包含这条 fallback 路径。

### Decision 5: 容器端口与 Jaeger service name

选择：本机 `18087 → 容器 8080`；`otel.service_name: service-a-auto`。

理由：
- `18087` 与现有 `18080–18086`（含 traefik 占用的 `18086:80`）错开，避免冲突。
- service name 后缀 `-auto` 让 Jaeger 中两条 trace 一眼可分。同时这也是给 tail sampling 留出口子——如果以后要对 auto 版做不同采样策略，按 `service.name` 加一条 policy 即可。

### Decision 6: 实验观察文档放在哪

选择：`docs/observations/2026-05-18-otel-auto-findings.md`，新增 `observations/` 目录。

理由：现有 `docs/plans/` 是 SDD 三件套，记录"做什么 / 怎么做 / 一步步做"。**实验产出的事实清单**（"loongsuite 注入的 span 名是这样、跨服务 traceparent 行为是那样"）是另一种文档——既不是 plan，也不是 design。新开 `observations/` 目录把它们分开，避免把 plans 目录搞乱。

## Risks

- **依赖版本兼容性**：当前 `go.mod` 用的是 Gin v1.12.0、GORM v1.31.1、Go 1.25.0；loongsuite 官方支持矩阵公开数据停留在 Gin v1.10.x、GORM v1.25.x。实验中若发现某个库版本不在支持范围，**实验结论本身就是 finding**——记录到 observations，并说明需要降版本还是等 loongsuite 跟进。
- **RabbitMQ 行为**：loongsuite 对 amqp091-go 是否自动注入 producer span 并把 traceparent 写进 message headers，是本实验的**最大未知**。若不行，意味着 RabbitMQ 这一层 loongsuite 替代不了手动埋点——这是 vibe-replay 拆解里 Step 1 关键决策点"手动 Inject 的 5 行"的**重要佐证**。
- **otel CLI 名称冲突**：`otel` 是个非常常见的可执行文件名（otel-collector 二进制有时也叫 otel）。Dockerfile 里要确保 PATH 解析到 loongsuite 的版本。tasks 里加一条 `which otel && otel version` 的预检查。
- **service-a-auto 调用 service-a 的 peer**：跨服务调用时，service-a-auto 的 outbound HTTP 请求依赖 loongsuite 注入 traceparent，service-a 端依赖 `otelgin` extract。两端用不同方式埋点，**这一段恰好是 W3C TraceContext 互操作性的真实验证**。若 trace 在中间断了，要分析是 inject 端缺失还是 extract 端缺失。

## Out-of-Scope Decisions（明确写下来避免反悔）

- 不在本实验中改 `docker-compose.yml` 的 `traefik` 段；不在 traefik 里给 `service-a-auto` 加路由。**理由**：实验是旁路，不应污染网关。
- 不在本实验中给 demo UI（`web/index.html`）加 service-a-auto 的按钮。**理由**：UI 通过 traefik 同源调用，而 service-a-auto 不挂网关；要测就用 `curl localhost:18087/*`。
- 不在本实验中更新 `README.md`。**理由**：实验结论稳定后再更新主 README；现在更新会让"这是个实验"的状态被误读为"主推方案"。
