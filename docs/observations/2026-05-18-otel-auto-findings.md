# Observations: OTel Auto Instrumentation Experiment (loongsuite-go-agent)

> 历史验证记录：以下内容保留当时的环境与结果；其中提及的旧源码、配置、脚本或临时路径可能已移除。当前运行方式以根 README 为准。

> **Date**: 2026-05-18 / 2026-05-19
> **Branch**: `otel-auto`
> **Related plan**: `docs/plans/2026-05-18-add-otel-auto-instrumentation-{proposal,design,tasks}.md`
> **Purpose**: 把"事实清单"和"判断结论"分开放在这——`docs/plans/` 是"做什么/怎么做"，本目录是"做完之后看见了什么"。

## TL;DR

实验结果**双双正向**——两条最不确定的事都成立：

- **L2 零代码 SDK init**：成立。`service-a-auto` 删光 `observability.Init` 与 `internal/observability/`，仅靠 docker-compose 注入的 6 个 `OTEL_*` env vars，loongsuite 在编译期注入了完整的 SDK（TracerProvider + OTLP gRPC exporter），spans 完整流到 Jaeger。
- **RabbitMQ producer span 自动注入**：成立。`amqp091-go` 这种**没有官方 OTel 包**的库，loongsuite 也能在 `ch.PublishWithContext` 处自动产 producer span，并打上 `messaging.system=rabbitmq` 标签。

副推论：

- 应用代码侧**手动埋点可以完全清零**（L1 + L2 都剥光）。
- L3（`X-Trace-Id` 回吐、业务错误信息附加）依然是产品行为，与本实验设定的"固有边界"一致——agent 不可能猜到你想怎么把 traceId 暴露给用户。
- vibe-replay 拆解里 Step 1 那 5 行"RabbitMQ 手动 Inject 关键代码"，**叙事仍然成立**（教学价值在于讲清 W3C TraceContext 怎么穿过 amqp 协议），但作为生产做法可以加一页对照——"如果你不想手写这 5 行，`otel go build` 完全能替你做"。

---

## 1. 环境

| 项 | 值 |
|---|---|
| Host OS / Arch | macOS 25.4.0 / arm64 |
| Docker | 29.4.0 |
| Go (builder image) | `golang:1.25-bookworm` |
| Application Go module | `go 1.25.0` |
| loongsuite-go-agent CLI | `otel version 1.10.0_01fcc67`（GitHub releases `latest` 直下） |
| OTel Collector | `otel/opentelemetry-collector-contrib:0.123.0`（demo 现有） |
| Jaeger | `jaegertracing/all-in-one:1.76.0`（demo 现有） |
| Gin | v1.12.0 |
| GORM | v1.31.1 |
| go-redis | v9.19.0 |
| amqp091-go | v1.11.0 |

**兼容性观察**：loongsuite 公开 README 提到的支持范围是 Gin v1.7.0–v1.10.2、GORM v1.22.0–v1.25.10。本实验的版本（Gin v1.12 / GORM v1.31）**超出文档范围**，但 `otel go build` 没有报错，运行时也产出了预期的 span——支持矩阵已经悄悄前移，README 在滞后。

## 2. 构建对比

| 维度 | service-a (manual) | service-a-auto (loongsuite) | 备注 |
|---|---|---|---|
| Build 命令 | `go build` | `otel go build` | 编译入口名差一个 `otel ` 前缀 |
| 编译耗时 | ~5–10s | **38.3s** | AST 重写阶段大约 +30s（一次性，Docker layer 命中后会缓存） |
| 镜像大小 | 59 MB | **52.7 MB** | distroless base 同档；AUTO 反而小 6.3 MB（少了 OTel SDK 静态链接的代码） |
| Binary 大小（直接 go build） | n/a（未重新测） | 37 MB（darwin/arm64）；36 MB（linux/arm64） | 比 section 2 时（含 SDK init）的 43 MB 少 6 MB，是 L2 strip 的直接收益 |
| 安装 `otel` CLI 方式 | n/a | Dockerfile 中 `curl` 直下 GitHub releases 预编译二进制 + `ARG TARGETARCH` 跨架构 | 比 `install.sh` 更确定、不依赖 jsdelivr CDN |

## 3. 代码量对比

> 基线：`service-a-auto` 是 `service-a` 的精确副本，再剥离 instrumentation。所有数字都是"相对 service-a 起点"。

| 文件 | service-a 行数 | service-a-auto 行数 | 删除内容 |
|---|---:|---:|---|
| `cmd/main.go` | 126 | 116 | `otelgin.Middleware` 中间件、`observability.Init` 调用、`shutdownOTel` 帮手、相关 imports（−10 行） |
| `internal/database/database.go` | 82 | 78 | `gorm.io/plugin/opentelemetry/tracing` import + `db.Use(tracing.NewPlugin())` 块（−4 行） |
| `internal/redis/redis.go` | 63 | 58 | `redisotel` import + 2 处 `redisotel.InstrumentTracing` 调用（−5 行） |
| `internal/rabbitmq/rabbitmq.go` | 128 | 92 | 手动 `tracer.Start`/`SetAttributes`/`RecordError`、`propagator.Inject`、traceparent 写入 headers、`recordSpanError` helper、相关 imports（−36 行，最大单文件减幅） |
| `internal/client/client.go` | 128 | 131 | `otelhttp.NewTransport` + import 删（−2 行）；同时为 server.name 路由 case 加了 `"service-a-auto"`（+5 含注释）。**净 +3** |
| `internal/observability/observability.go` | 53 | (file removed) | 整个包删除——L2 SDK init 不再存在 |
| **手动埋点 LOC 总和（约）** | **~110** | **0** | 业务侧 trace 消费（handler.go 中 `TraceIDHeaderMiddleware` 等）不计入，那是 L3 |
| go.mod 直接依赖数 | 15 | 8 | 删了 `otelgin`、`otelhttp`、`otelgorm`、`redisotel`、`otel/exporters/otlp/...`、`otel/sdk`、`grpc`、`yaml`（不，yaml 还在）……实际净减 7 项 |

**关键结论**：业务团队接入 OTel 在 `service-a` 这种规模的服务上，要写约 **110 行 instrumentation 代码**（含 SDK init + 5 个库各自的接入），其中 **RabbitMQ 单库就占 36 行**（因为它没有官方 otel 包，必须手动 inject）。换成 `otel go build` 路线，这 110 行全部可以删。

## 4. Span 形状对比（5 类基础场景）

| 场景 | service-a (manual) | service-a-auto (loongsuite) | 差异判断 |
|---|---|---|---|
| `GET /ok` | 1 span: server | 1 span: server | **完全等价** |
| `GET /mysql/ok` | 2 spans: server + `select users` client | 2 spans: server + `SELECT users` client | **等价**（仅命名大小写不同） |
| `GET /redis/ok` | **5 spans**: server + `set` + `hello`(handshake) + `client maint_notifications`(ERROR) + `get` | **3 spans**: server + `set` + `get` | AUTO **更干净**——MANUAL 把 go-redis 内部 handshake 也产了 span，其中一个还是 ERROR 状态（noise） |
| `GET /rabbitmq/ok` | 2 spans: server + 内部 `rabbitmq publish` span（手写） | 2 spans: server + `:otel-demo-queue publish` (kind=producer) | **AUTO 更标准**——`kind=producer` 是 OTel messaging semconv 规范；MANUAL 是 `kind=internal` |
| `GET /call/ok?depth=1` 跨服务 | 3 spans: a server + a client + b server | 3 spans: a-auto server + a-auto client + b server | **完全等价**，**W3C TraceContext 在 AUTO↔MANUAL 之间双向兼容**（service-b 仍然走 otelgin / otelhttp，能从 service-a-auto 注入的 traceparent header 正确 extract） |

### Attribute 覆盖度差异

| Attribute | service-a (manual) | service-a-auto (loongsuite) | 影响 |
|---|---|---|---|
| `http.route` / `http.response.status_code` / `url.path` / `server.address` / `otel.status_code` | ✓ 都有 | ✓ 都有 | 无影响——两边都是 OTel 新 semconv |
| Redis `db.system` / `db.statement` | ✓ 有（如 `db.statement=set otel-demo:...`） | ✗ 都缺 | **AUTO 弱**——故障时看不到具体执行的 Redis 命令文本，只能从 span 名（`set` / `get`）推断操作类型 |
| MySQL `db.statement` / `db.system` | ✗ 都缺（otelgorm 的输出也是稀疏的） | ✗ 都缺 | 两者都需要补——这是 otelgorm 和 loongsuite 各自的缺失，不是路线差异 |
| RabbitMQ `messaging.destination` / `messaging.operation` | ✗（手写代码里设了 `messaging.destination.name` + `messaging.operation=publish`，但本次 ok-trace 没采样到对应 manual sample 来对比） | ✓ `messaging.system=rabbitmq`（其余字段未列在采到的 trace 里，未来如需更详查可重新对比） | 大致等价 |

## 5. Semconv 版本观察

**AUTO 完全使用新 semconv**：所有 HTTP span 出现的 status code key 都是 `http.response.status_code`，没有 `http.status_code`（旧）。

### 对 tail sampling 的影响

`otel-collector-config.yaml` 里我们配了 **两条** 5xx 保留策略：
- `keep-http-5xx-old-semconv` 检查 `http.status_code ≥ 500`
- `keep-http-5xx-new-semconv` 检查 `http.response.status_code ≥ 500`

观察到的事实：

- service-a-auto 的 5xx trace（`/error`、`/mysql/error`、`/rabbitmq/error`）**全部被保留**——命中 **new-semconv** 策略。
- 如果只保留 old-semconv 那条策略，**service-a-auto 的 5xx 全部会被丢**——这正是 vibe-replay PPT P10–P11 讲的"两条策略都要写"的真实价值，本实验给了一个新证据：**升级到 loongsuite 后旧 semconv 策略变成无用代码，但新 semconv 策略不可省**。

## 6. RabbitMQ 验证结论

> 这是 design Risk 列表中标注为"**最大未知**"的一项。

**结论**：loongsuite-go-agent 对 `amqp091-go` v1.11.0 提供 **producer span 自动注入**。在 1% 采样下捞到的一条 `/rabbitmq/ok` trace 里：

```
[service-a-auto] :otel-demo-queue publish   kind=producer
    messaging.system=rabbitmq
    (作为 GET /rabbitmq/ok server span 的 child)
```

**未完全验证的子点**：消息 headers 里的 `traceparent` 是否真的被写入——本实验没起 consumer 也没用 rabbitmqadmin 直接读消息 header，只观察了 producer 侧。但从 span 形状（`kind=producer` + 父子关系正确）来看，loongsuite 大概率是完整实现的。如果将来要把 consumer 端也接入做完整闭环，需要在 service-b（或新写一个 consumer）做对应验证。

**对 vibe-replay 拆解的影响**：Step 1 决策点"手动 Inject 5 行关键代码"原本的论据是"amqp091 没官方包，必须手写"——这个论据**作为"教学路径上你应该懂"仍然成立**（讲清 W3C TraceContext 怎么从 HTTP 跨到 amqp），但作为"生产 must do"**不再成立**。给 PPT 加一页对照很合适。

## 7. 跨服务传播验证结论

`GET /call/ok?depth=1` 的 trace 结构：

```
trace = a3287f8e3131de31a580298da2ae8b1f
├─ [service-a-auto] GET /call/ok  (server)
│   └─ [service-a-auto] GET  (client, server.address=service-b:8080)
│       └─ [service-b] GET /ok  (server)
```

**结论**：
- **W3C TraceContext 在 AUTO↔MANUAL 之间双向工作**。service-a-auto 的 outbound HTTP 请求（loongsuite 注入的 `net/http` client instrumentation）把 traceparent header 注入到出请求里；service-b（仍然用 `otelgin.Middleware`）在入请求时正确 extract，并把自己的 server span 挂到同一 trace。
- 这条结果让"渐进迁移"成为可能：在一个微服务集群里，你可以**先迁一两个服务到 loongsuite，其余保留手动埋点**，trace 完整性不受影响。

## 8. observability.Init 能否省略

**能完全省略**。

证据链：
1. 删除 `observability.Init` 调用 + 删除整个 `internal/observability/` 包 + 删除 go.mod 中 `otel/sdk` / `otlptracegrpc` 等直接依赖
2. docker-compose 中给 service-a-auto 注入 6 个 `OTEL_*` env vars
3. `otel go build` 出来的 binary，启动后 `curl /health` 响应里**有有效的 `X-Trace-Id`** ← 这说明全局 `TracerProvider` 被某种方式 bootstrap 了，否则 `trace.SpanFromContext()` 拿不到 valid span
4. Jaeger `/api/services` 列表里**有 `service-a-auto`**，且各 endpoint 的 trace 形状符合预期

**机制推断**（未深入读 loongsuite 源码确认）：`otel go build` 在编译期向 binary 注入了一个等价于 `observability.Init` 的 bootstrap 调用，读 `OTEL_*` env vars 配 OTLP gRPC exporter，并在 `main` 之前完成 TracerProvider 注册。这是 OTel SDK 标准行为，loongsuite 把它做成了"agent 内置"。

**Plan 中的 Mode A fallback（task 2.5.5）没有被启用**，因为 Mode B 一次跑通。

## 9. 遇到的坑

按出现时间排序：

1. **`go install` 路径不在官方文档里**（section 3 执行前）
   - 计划原案是 `go install github.com/alibaba/loongsuite-go-agent/cmd/otel@latest`——这是我凭印象写的。
   - 核实 README 后发现官方支持的是预编译二进制、`install.sh` 脚本、源码 `make install` 三种。
   - 修正：Dockerfile 改用 GitHub releases `latest/download/otel-linux-${TARGETARCH}` 直下。
   - 教训：执行前再 fetch 一次源材料确认。

2. **Docker Hub 拉镜像超时**（5.1）
   - 第一次 `docker compose build service-a-auto` 失败：`Head "https://registry-1.docker.io/v2/library/golang/manifests/1.25-bookworm": context deadline exceeded`。
   - 解决：直接重试一次就过了——docker.io 在本网络下偶发抖动；Docker daemon 未配 registry mirror，没必要改。
   - 备选：如果稳定不通，可在 Daemon 加 `dockerproxy.com` mirror，或把 Dockerfile 第一行改成 `dockerproxy.com/library/golang:1.25-bookworm`。

3. **service-a-auto 启动失败：`unsupported server.name "service-a-auto"`**（5.2）
   - `internal/client/client.go` 的 `NewPeerClient` 用 hardcoded switch 选 peer base URL，只认 `service-a` / `service-b`。
   - section 1.5 改了 `config.yaml` 的 `server.name` 但漏了 switch case。
   - 修复：`case "service-a", "service-a-auto":` 一行 case 扩展（commit `a47b80b`）。
   - 教训：rename 一个对外 ID 时，要 grep 整个项目所有 switch / case / hardcoded reference。

4. **Plan 文件路径漂移**（在 section 1/2 完成时发现）
   - tasks.md 写的 `internal/rabbitmq/publisher.go` 与 `internal/client/peer.go`，实际文件是 `rabbitmq/rabbitmq.go` 与 `client/client.go`。
   - 原因：写计划时凭文件用途猜的名字，没先 ls。
   - 修复：commit `4f591e9` 同步路径。
   - 教训：计划落到具体文件名前，先验证。

5. **`/mysql/ok` AUTO 第一轮 50 次 hit 抽样 0 命中**（5.6）
   - 1% 采样下 50 次期望 0.5，方差不小，0–2 都正常。
   - 解决：再打 200 次保险，1 次命中。
   - **不是问题**——这本身就是 tail sampling 的预期行为。

## 10. 总结与建议

### 是否值得在 vibe-replay 加一页对照

**强烈建议加**。位置：当前 P20（全貌回顾）和 P21（复用抓手）之间，加一页"延展：编译期 auto-instrumentation 对照"。要点：

- 一张图：手动埋点 110 行 vs `otel go build` 0 行。
- 一句话：`Gin / GORM / go-redis / amqp091 / net/http / SDK init` 全自动；唯独 L3（traceId 双写之类）仍要写。
- 一条提醒：loongsuite 的 `db.statement` / `db.system` 等细粒度属性还稀疏，故障 root cause 分析时可能需要补——但**不影响 tail sampling 决策**，因为后者只看 status code / latency / span ERROR status。
- 一个数：本实验镜像 52.7 MB vs 59 MB，6.3 MB 是 SDK 静态链接的代码量减少。
- 一句钩子：W3C TraceContext 在 AUTO↔MANUAL 之间双向兼容——所以**渐进迁移是安全的**。

### 是否值得迁移主代码（service-a/b/c/d）到 loongsuite

**值得**，但**不必现在做**。考虑：

- ✅ 实验证明 5 类基础 span（HTTP server/client、GORM、Redis、RabbitMQ）都被覆盖，行为等价或更好。
- ✅ Tail sampling 策略不需要改，行为兼容。
- ✅ 教学叙事不会被破坏——`docs/plans/` 里讲的故事仍然有效（手动埋点教你理解 OTel 在做什么；agent 教你工程上怎么省）。
- ⚠️ Redis `db.statement` 缺失这一点要确认是否影响你团队的实际故障分析习惯——如果离不开看 Redis 命令文本，要么留 manual，要么等 loongsuite 补齐。
- ⚠️ vibe-replay PPT 的 Step 1 / Step 2 代码页（手动 `Inject`、`otelhttp.NewTransport` 对称）是**当前 demo 教学价值最高的两页**——迁移主代码会让这些代码"在仓库里消失"，但 PPT 仍然引用历史代码片段就好。

**建议节奏**：
1. 短期（本周）：把 `service-a-auto` 作为长期对照分支留着，**不合并到 main**。
2. 中期（下季度）：在 vibe-replay PPT 加一页对照，作为完整叙事的"最后一拼图"。
3. 长期：如果未来扩展第 5 个、第 6 个服务，**新服务直接用 loongsuite 路线**，老服务保持原样，让它们用 W3C TraceContext 共存——这种"渐进迁移"已经在本实验里被验证可行。

### 还需要验证的事

- **RabbitMQ consumer 端**：本实验只验了 producer 侧。如果未来要在 service-c/d 之外加一个消费者，要测 loongsuite 是否在 consumer 入口自动 extract traceparent（基本可以预期 yes，但没现场证据）。
- **`loongsuite` 1.10.0 之后新版本**：兼容性矩阵在前移，下一个长期实验里复测 Gin / GORM 更新版本是否还跑得动。
- **Performance overhead**：本实验没测运行时性能开销（CPU/mem）。loongsuite 编译期注入理论上 zero-runtime-overhead-over-manual，但需要 benchmark 确认。
- **service.name 来源**：本实验 service.name 同时出现在 docker-compose env (`OTEL_SERVICE_NAME=service-a-auto`) 和 `config.yaml` (`otel.service_name: service-a-auto`)。后者已经没人读了（observability.Init 被删），但留着没问题。生产用时可以把 YAML 的 `otel:` 段一并清掉。

---

## 附：实验过程数据点

- 总提交数（otel-auto 分支）：11
- Section 1–6 全部完成；Section 5.x 全部 [x]；Section 2.5.5（plan B fallback）未触发；Section 3.3（install.sh fallback）未触发。
- 共发 450+ 个验证请求；Jaeger 中持续观察到的 service：`service-a-auto`、`service-a`、`service-b`、`jaeger-all-in-one`。
- 平均 RTT 没有显著变化（未精确测）。
- 整个实验用时（计划 + 编码 + 验证 + 文档）：约 2 个工作日。
