# Proposal: Add OTel Auto Instrumentation Experiment (loongsuite-go-agent)

## Why

当前 4 个 Go 服务全部使用**手动 SDK 埋点**——`otelgin`、`otelhttp`、`otelgorm`、`redisotel`，RabbitMQ 还要手写 producer span + 把 traceparent 注入到 message headers。这条路稳定、库覆盖最全，但有两个痛点：

1. **每接一个新依赖都要写埋点代码**：业务团队学习成本不低，容易漏写（尤其是 RabbitMQ 这类没有官方 otel 包的库）。
2. **教学演示之外，生产团队更关心"能不能不改代码就埋"**：vibe-replay 拆解里如果只讲手动埋点，缺一块对照——观众会问"那阿里那个不是不用改代码吗？"

阿里巴巴开源的 `loongsuite-go-agent`（原 `opentelemetry-go-auto-instrumentation`）提供了一条**编译期 AST 重写**的路径：把 `go build` 换成 `otel go build`，工具利用 Go toolchain 的 `-toolexec` 钩子在编译时把埋点代码注入到 Go 源码（含 stdlib 与依赖）里。覆盖 Gin / GORM / go-redis / amqp091 / net/http 等 70+ 库，**正好和本 demo 的全部依赖匹配**。

2025 年 Alibaba + Datadog + Quesma 联合成立了 OTel Go Compile-Time Instrumentation SIG，把 loongsuite-go-agent 与 Datadog Orchestrion 的能力整合到 OTel 上游，所以这条路线在走 vendor-neutral 化。

本提案不是要替换主代码，而是**做一次并行实验**：在不动 `service-a` 主代码的前提下，新增一个 `service-a-auto` 副本，去掉所有手动埋点、改用 `otel go build` 构建，端口分配到 `18087`，与现有 `service-a:18080` 同时跑，**对比两条路线产出的 trace**。

## What Changes

- **Add** `service-a-auto/`：从 `service-a/` 复制，去掉手动埋点相关代码（不含库依赖剥离，go.mod 暂保留以减少风险）。
- **Add** `service-a-auto/Dockerfile`：基于 `golang:1.25-bookworm` builder 安装 `otel` CLI（loongsuite-go-agent 的命令名），构建时用 `otel go build` 替代原 `go build`。
- **Add** `service-a-auto/config.yaml`：`server.name` 与 `otel.service_name` 改为 `service-a-auto`，方便 Jaeger 中按 service 名筛选两条 trace 做对比。`peer.service_a_url` 仍指 `service-a:8080`（这样跨服务调用还能复用 service-a 的 `/ok` 等端点）。
- **Modify** `docker-compose.yml`：新增 `service-a-auto` service，本机端口 `18087:8080`，复用 otel-collector。**不**改 traefik，避免把实验暴露到网关层；只走容器内网络/直连端口。
- **Add** `docs/observations/2026-05-18-otel-auto-findings.md`：实验结果文档——记录 trace 形状差异、semconv key 名差异、删了多少行代码、RabbitMQ 是否需要手动 Inject、其他坑。

## Capabilities（实验交付物）

### 总成功标准：**零代码改动接入 OTel**

本实验的指导原则是——**未来的 Go 服务接入 OTel 不应该改任何应用代码，只在 build 时换一条命令 + 在部署时注入 env vars**。沿这条原则，把可观测性能力分成三层来界定责任边界：

| 层 | 内容 | 期望由谁负责 | 本实验验证目标 |
|---|---|---|---|
| **L1 库埋点** | gin server / http client / gorm / redis / amqp091 等框架钩子 | `otel go build` 编译期 AST 注入 | **必须 100% 自动**，应用代码 0 行手写埋点 |
| **L2 SDK 初始化** | TracerProvider、OTLP exporter、Resource、Propagator 注册 | OTel SDK 的 env-based bootstrap（`OTEL_SERVICE_NAME` / `OTEL_EXPORTER_OTLP_*` 等） | **尝试 0 行代码**；若 env-only 不工作，回退保留 `observability.Init` 并把"L2 floor"作为 finding |
| **L3 业务侧 trace 消费** | 把 traceId 写进 response header、错误 JSON 体；业务事件 `span.AddEvent` | 应用代码——**这是产品行为，不是埋点** | 不在本实验范围；明确写下 L3 是零代码原则的**固有边界** |

### 具体交付物

- **可启动**：`docker compose up --build service-a-auto` 能成功构建并运行，进程能向 otel-collector 上报 trace。
- **trace 落地**：在 Jaeger UI 按 service name `service-a-auto` 能搜到一条以上 trace。
- **L1 验证 · 5 类 span**：以下端点每个至少能在 Jaeger 中看到对应子 span：
  - `GET :18087/ok`             → server span
  - `GET :18087/mysql/ok`       → server + GORM SQL span
  - `GET :18087/redis/ok`       → server + Redis SET/GET span
  - `GET :18087/rabbitmq/ok`    → server + RabbitMQ publish span（**关键验证点**——loongsuite 是否自动写 traceparent 进 message headers）
  - `GET :18087/call/ok?depth=1`→ server + HTTP client span + service-a server span（**跨服务传播验证点**）
- **L2 验证 · 双模式对比**（见 design Decision 2）：
  - 模式 A：保留 `observability.Init`（保守，确定能上报）
  - 模式 B：删除 `observability.Init`，纯靠 env vars
  - 二者择一为最终方案；若 B 跑通，零代码原则完整落地；若 B 不通，记录 L2 floor 作为 finding。
- **对比报告**：以表格方式列出 `service-a` vs `service-a-auto` 在以下维度的差异：
  - span 名字（`HTTP GET /ok` vs `GET /ok` 之类）
  - attributes（`http.status_code` vs `http.response.status_code` 之类的 semconv 版本差异）
  - L2 SDK 初始化是否可省（env-only 是否够用）
  - 删除的代码行数（手动埋点 + 可选 SDK init 占的代码量）
  - 构建产物体积差异
  - 构建时间差异

## Impact

- **不影响**现有 `service-a` / `b` / `c` / `d` 行为：实验完全旁路。
- **不影响** Traefik 路由、demo UI、Python 分析脚本：因为 `service-a-auto` 不走网关。
- **不影响** otel-collector 配置：tail sampling 策略对所有 service 一视同仁，新 service 自动受同样规则约束（**这本身也是验证点：service-a-auto 产的 trace 能不能被现有 tail sampling 正确捞到？**）。
- **新增端口占用**：`18087`（与 service-d 的 `18085` 错开）。
- **构建时间**：`service-a-auto` 因为要先安装 `otel` CLI + 编译期 AST 重写，预计比 `service-a` 慢 30–60s。仅本地实验，可接受。
- **风险**：loongsuite-go-agent 对当前依赖版本（Gin v1.12.0、GORM v1.31.1、Go 1.25.0）的支持矩阵需在本实验中验证；若不兼容，实验失败本身就是有价值的 finding，进入 observations doc。

## Non-Goals

- 不替换 `service-a`/`b`/`c`/`d` 的主代码。
- 不为 `service-b` / `c` / `d` 各做一份 auto 副本；本次只验证 `service-a` 一个服务，因为它覆盖了所有依赖类型。
- 不修改 `otel-collector-config.yaml` 的 tail sampling 策略。
- 不引入 Datadog Orchestrion 或 OTel eBPF (OBI) 做横向对比；那是另一个 proposal。
- 不更新 vibe-replay 幻灯片；实验结果出来后再单独决定要不要加对照页（见 follow-up）。

## Follow-up（实验结束后再决定）

- 是否在 `go-otel-demo-vibe-replay.html` 中加一页"延展：编译期 auto-instrumentation 对照"。
- 是否把整套 demo 主代码迁移到 loongsuite（仅当实验证明 trace 形状/语义与手写埋点等价，且 RabbitMQ 这种没有官方 otel 包的库也能被自动覆盖）。
