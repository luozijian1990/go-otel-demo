# go-otel-demo：三信号 AIOps 故障实验与评测改造计划

> 日期：2026-09-19  
> 仓库：`luozijian1990/go-otel-demo`  
> 审查基线：`main` / `00129e3519347cba70d30c385b0362f472cd5214`  
> 交付对象：在本地仓库执行改造的 Codex。  
> 文档性质：基于源码静态审查的实现计划，不是已经实现、联调通过或测得准确率的报告。

## 0. 给 Codex 的执行入口

读取本文件后，直接按第 17 节任务顺序实现，而不是再生成一份同义计划。首先检查实际工作区、AGENTS.md、现有测试和 Git 状态；保留用户未提交改动。本文基线之后已有的能力应复用，不得重复实现或回退。

本次目标是完成一个可运行的闭环：

```text
点击 Random Error / 随机故障
  → 服务端随机选择一个受控故障
  → 真实执行 Go / HTTP / MySQL / Redis 操作
  → 采集 logs + traces + metrics
  → AIOps 基于独立证据包诊断
  → 冻结诊断结果
  → 与真实注入结果自动对照
  → UI 展示结论、证据、差异与历史记录
```

必须交付代码、配置、测试和 README。不要只交架构图、假数据页面或固定答案。无法访问 Docker、镜像仓库、模型服务时，继续完成能验证的部分，并准确记录未运行项；不能把 fixture 测试当成真实模型诊断通过。

**边界：这是教学与评测 Demo，不是生产自动修复平台。** 不引入 Kubernetes、Kafka、向量数据库、多 Agent 框架、自动执行 SQL/SSH 或常驻日志扫描消费者。分析端只读，实验端仅执行明确允许的本地故障。

---

## 1. 结论与产品定位

这个方向值得做。重点不在于再增加两个遥测后端，而在于把已有“制造故障 → 查看 trace”的 Demo，变成“有真实答案、能重复实验、能验证诊断结果”的 AIOps 实验场。

第一版应该回答四个问题：

1. 是否定位到真正出问题的服务、组件和错误类型，而不是把网关 500 当根因？
2. 是否能区分直接故障、上游传播、并行健康分支、慢请求以及 HTTP 200 的降级？
3. logs、traces、metrics 是否分别提供了可核查的证据？缺失证据时是否承认不确定？
4. 联合三类信号，是否比只看 trace 更可靠？这个问题通过同一证据快照上的对照实验回答，不预先承诺提升。

保留现有 UI、四个 Go 服务、Traefik、Jaeger、LoongSuite 自动埋点和 Skill。新增能力围绕现有结构增量实现。

## 2. 已核对的仓库现状与改造影响

以下为审查基线中的事实；源码定位见第 20 节。

| 当前内容 | 已核对的行为 | 改造要求 |
|---|---|---|
| `web/index.html` | 原生 HTML/JS；`runScenario()` 调用 `/service-a/...`，显示响应、TraceId、Jaeger 链接 | 保留旧按钮；添加实验、证据、诊断、对照、历史区域；不迁移 React |
| `service-a/internal/handler/handler.go` | 已有基础错误、MySQL/Redis 错误、跨服务调用、fanout、慢请求和降级入口 | 复用底层操作，不让新随机入口直接随机返回错误文案 |
| `service-b/internal/handler/handler.go` | 并行调用 C / Redis 与 D / MySQL；降级场景可 HTTP 200 且包含错误 span | 诊断和评分不能只依赖入口 HTTP 状态 |
| `service-c/internal/redis/redis.go` | Redis 错误连接 `127.0.0.1:1`；慢操作是在应用里 `time.Sleep(4s)` 后 SET/GET | 区分“客户端连接被拒绝”“应用等待”“Redis 服务端慢”，不得混为一谈 |
| `service-d/internal/database/database.go` | 缺表查询；已有 `SlowListUsers()`，执行 `SELECT SLEEP(?)` | 可复用真实 SQL 慢查询，与 C 的应用内等待形成对照 |
| `service-a/cmd/main.go` | `gin.Logger()` / 标准 `log`；不手动初始化 TracerProvider | 增加结构化日志、指标；不得恢复第二个 tracing SDK |
| `otel/collector.yaml` | 只有 traces pipeline；`decision_wait: 10s`、batch 2s；错误/慢请求优先保留、普通成功约 1% | 添加日志采集；保留原 trace 策略；证据查询采用有界重试 |
| `docker-compose.yml` | Jaeger 1.76.0、Collector 0.123.0、Traefik v3.4；MySQL/Redis 外置；挂载本地 `config.yaml` | 默认本地实验环境补全依赖和配置，兼顾原有外部配置 |
| 四个 Go Dockerfile | `otel go build`；运行镜像 distroless nonroot；Agent 当前从 latest 下载 | 保留构建方式；处理写日志目录权限；探活不能假设有 curl / shell；固定经验证的 Agent 版本 |
| `scripts/analyze-jaeger-trace.py` | 获取 Jaeger 并清洗 JSON，本身不是 LLM 服务 | 抽取为可复用模块，保留命令行入口 |
| `.agents/skills/jaeger-trace-rootcause/` | 在 Codex/兼容会话中读取清洗后的 trace 进行推理 | 保留旧 Skill；新增三信号 Skill；与 UI 使用同一证据契约 |
| `web/nginx.conf` | 直接访问 18083 时，只配置了 `/service-a/` 代理 | 新 `/aiops/` 路由需要同时覆盖 Traefik 和此 Nginx 入口 |

### 2.1 必须先修的分析语义

现有 `clean_exception_logs()` 会保留含 `event` 的 span log；`span_is_error()` 最后通过 `bool(logs)` 判错。这样普通 `AddEvent("ordinary successful request")` 也可能变成错误证据。

改为区分普通事件与异常事件。错误判定以 span error status、明确的 `error=true`、适当的 HTTP 5xx、实际 `exception.*` 字段等为依据。普通 event 不能单独判错；HTTP 200 也不能自动抹去子 span 的错误。

慢请求不能机械地选择持续时间最大的 span：入口 span 包含子调用时间，往往天然最长。应输出调用树、关键阻塞分支及可计算的 exclusive time；并行子区间求并集后再扣除，不能简单求和。缺少父 span、时钟偏差或跨 trace 引用时标注限制，不制造精确因果。

### 2.2 保留采样的真实语义

当前 Demo 与“生产统一低比例 head sampling”不是同一个配置。第一版继续使用仓库实际配置：入口/应用记录，Collector 再做尾部采样。不为本次改造重建生产采样架构。

“策略应保留错误”不等于“错误 trace 必然已入库”。导出失败、尚未到达、迟到 span、重启等仍可能导致缺失。`trace_sampled=true` 只表示 span context 的采样标志，不代表 Jaeger 已持久化。

## 3. 第一版架构：明确只选一条主实现路线

```text
                       ┌─ 操作员看到的真实故障 / Ground Truth
浏览器 UI ─ /aiops ─ aiops-api
                       ├─ RunManager：实验、流量、状态、取消、历史
                       ├─ EvidenceCollector：只读查询三种后端
                       ├─ Analyzer：只接收净化后的 EvidenceBundle
                       └─ Evaluator：诊断冻结后，才读取 Ground Truth 对照
                              │
                 经过 Traefik 的固定实验业务入口
                              ▼
                  service-a → service-b
                                ├─ service-c → Redis
                                └─ service-d → MySQL

traces：现有 Agent / 手动业务 span → Collector → Jaeger
logs：  Go JSON 日志 + Traefik access log → Collector filelog → Loki
metrics：Go Prometheus client /metrics ← Prometheus scrape
```

### 3.1 组件选择

| 能力 | 第一版方案 | 不采用的方案 |
|---|---|---|
| Trace | 复用现有 Jaeger / Collector / Agent | 替换 Tempo、再建一个 tracing SDK |
| Logs | `slog` JSON，Collector `filelog`，Loki 原生 OTLP HTTP 接收 | 为本地 Demo 增加完整 ELK、Docker socket 采集权限 |
| Metrics | `prometheus/client_golang`，四服务内部 `/metrics`，Prometheus 直接抓取 | 从采样后的 trace 推导全量错误率、重复计数两套 HTTP 指标 |
| 实验与证据 API | 一个轻量 Python FastAPI 服务 `aiops-api` | 多个微服务、Celery、额外队列集群 |
| 状态持久化 | SQLite；单 API worker；有界任务队列 | 把业务 MySQL 当成实验控制平台数据库 |
| AI | 新三信号 Skill + 可配置 HTTP LLM adapter | 把固定 if/else 输出伪装成 AI |
| UI | 扩展现有 HTML/JS；可拆静态 JS/CSS | 新建一套前端工程 |
| Grafana | 非第一版阻塞项，后续可加 | 为展示三类证据强制再装一层 UI |

这里不强求三个信号都由同一 SDK、同一协议上报。Metrics 走 Prometheus pull，是为了降低与现有自动 tracing SDK 的集成风险；项目定位仍然是三信号关联与 AIOps 实验。以后切换 OTel metrics exporter 时保持数据契约不变，不同时累计两套指标。

第一版不依赖 `personal-mcp` 才能运行。后续可把 Jaeger/Loki/Prometheus 读取实现替换成已有 MCP adapter；替换的是数据访问层，不是实验、诊断和评分契约。

## 4. 实验模型与真实答案隔离

### 4.1 三个 ID，不得混用

| 字段 | 含义 | 放在哪里 |
|---|---|---|
| `run_id` / 日志字段 `experiment_id` | 一次实验，可能包含多条请求 | 控制表、日志、可选 span attribute；不是 metric label |
| `request_id` | 实验中的某一次请求，上下游保留同一个值 | 请求头、响应头、日志；不是 metric label |
| `trace_id` | 本次请求的分布式 trace 标识，可能未被保留 | span、日志、UI；不是 metric label |

`run_id` 与随机 seed、故障类别无编码关系。使用随机 UUID 等不透明 ID，不使用 `mysql-error-001`。

### 4.2 不允许“看答案诊断”

划分五种独立类型，禁止拿一个包含全部字段的大对象在各模块之间传递：

```text
ExperimentSpec     # 场景、seed、故障参数，仅实验执行模块使用
GroundTruth        # 预期 + 实际注入确认，仅 UI/评测模块使用
IncidentContext    # run_id、请求 ID、trace_id、时间范围、入口症状
EvidenceBundle     # 只读后端返回的净化证据 + 可用性，不含标准答案
DiagnosisResult    # 模型独立输出，冻结后才交给 Evaluator
```

Analyzer 的唯一输入为 EvidenceBundle 与通用诊断指令。禁止输入 `scenario_id`、`scenario_name`、`seed`、`expected_*`、注入控制头、场景目录、完整实验执行响应或 UI 的答案区域。日志、span 和 metric label 中也不得出现标准答案字段。

真实数据库错误、连接地址、SQL、stack trace、普通业务操作名是合法运行证据，不能为“防泄露”把它们一并删除；需要排除的是人为附加的测试答案和注入说明。

### 4.3 旧接口保留，新评测使用中性路径

直接从 `/chain/mysql/error` 抽样只能算教学随机化，路径本身已经强烈提示答案。

因此第一版新增固定实验路径，例如：

```text
Traefik：POST /service-a/exercise
A：POST /exercise → B：POST /exercise
B：并行调用 C：POST /exercise 与 D：POST /exercise
```

同一组件在健康/故障模式下使用相同路由与普通业务 span 名。内部复用现有数据库、Redis 和 fanout 基础逻辑，不直接调用其他 Gin handler，也不构造假的遥测数据。

为了让 baseline 与故障阶段有可比性，正常情况下也走相同业务拓扑。A/B 的提前失败可以让后续分支不执行，这正是实际故障的影响，不应伪造下游 span。

### 4.4 请求级注入，不修改全局状态

RunManager 生成有期限、HMAC 签名的 `X-Demo-Fault-Token`，内容限于运行 ID、目标服务、允许的动作、有限参数与过期时间。四个服务校验后只对当前请求生效。

约束：

- 注入必须由 `DEMO_FAULTS_ENABLED` 开启；失效/篡改 token 拒绝执行。
- 不允许任意 URL、SQL、shell、文件路径作为输入；delay、RPS、请求数都有限制。
- 出站 HTTP 使用原 request context 传播 TraceContext，并显式携带 request_id、experiment_id 和内部 fault token。
- token 不能写进日志、baggage、span attribute、错误文本或模型输入；关闭相关 HTTP header 自动捕获。
- 并发分支不共写 Gin context 或未加锁 map；结果使用独立返回值收集。
- 不 stop/restart 容器、不 DROP 表、不 FLUSHDB、不切换共享 Redis client、不改变全局 DSN。
- 应用等待使用可响应 `ctx.Done()` 的 timer；HTTP/SQL/Redis 操作使用有界超时。

控制接口只允许服务端预定义目标。RunManager 在 Compose 内访问 `http://traefik/service-a/exercise` 时，要显式设置符合现有路由的 `Host: localhost`，否则当前 Host 规则可能不会匹配。不要绕过 Traefik 直接打 A 来伪装完整入口链路。

### 4.5 真实注入确认

GroundTruth 必须同时保存“打算注入什么”和“实际是否生效”。不能仅凭 random 选中了某个枚举就认定实验有效。

新 `/exercise` 调用可用独立保留的内部响应头回传执行 receipt：目标服务、动作是否执行、原生错误码/延迟是否符合预期。逐级收集该 receipt，不能拼入业务错误字符串、日志或 span。receipt 不进入 EvidenceBundle，且不作为模型根因证据。

例如选中缺表，但实际发生 MySQL 连不上，应标记 `injection_failed` / `environment_unhealthy`，而不是拿“缺表”当答案给模型扣分。现有 client 会把部分响应体拼到 error 中，新控制元数据尤其不能藏在该响应体内。

这种隔离是第一版的模块与输入边界，不是同进程内的强安全沙箱。外部 Codex 若能读取整个源码/场景目录，该运行只能算辅助演示，不可宣称严格黑盒盲测。严格评分使用只收到 EvidenceBundle、没有仓库/控制接口工具的模型 adapter。

## 5. 故障目录：7 类故障 + 1 类健康对照

全部复用或扩展现有安全操作。每个模板记录 `scenario_id`、版本、执行动作、注入确认条件、预期根服务/组件/类型/影响、清理动作、最长运行限制。

| 场景 | 实际行为 | 正确定位目标 | 入口表现 |
|---|---|---|---|
| `app_error_a` | A 当前请求直接返回应用错误 | A / application / application_error | 500 |
| `app_error_b` | A 调 B，B 返回应用错误 | B / application / application_error；A/网关为传播 | 500 |
| `mysql_missing_table_d` | D 查询不存在的表，C 正常 | D / mysql / table_not_found；原生 MySQL 1146 为证据 | 500 |
| `redis_connect_refused_c` | C 用独立 client 访问 `127.0.0.1:1`，D 正常 | C / redis_client / connection_refused；不能断言正常 Redis 实例宕机 | 500 |
| `application_delay_c` | C 在调用 Redis 前应用等待 4 秒，随后真实 SET/GET | C / application / application_delay；不等于 Redis 服务端慢 | 200、慢 |
| `mysql_slow_query_d` | D 执行已有 `SELECT SLEEP(4)`，随后正常查询，C 正常 | D / mysql / slow_query；不据此断言 CPU/锁/磁盘异常 | 200、慢 |
| `redis_degraded_c` | C 连接失败，B 走既定降级，D 正常 | C / redis_client / connection_refused；业务影响 degraded | 200、含错误与降级证据 |
| `healthy` | 相同拓扑正常访问 C/D | none / none / none；不能因普通 span event 误报 | 200 |

初版默认每次只有一个根因；fanout、传播和降级是观察难点，不是多根因。健康场景不进“Random Error”默认池，但必须有独立按钮，并进入评测批次。

现有 RabbitMQ、`/full/error` 等按钮继续可用；因存在外部依赖或多故障，不进入第一版自动评分池。

---
## 6. Logs：采集真正能与请求关联的应用日志

### 6.1 最小日志契约

统一采用 `slog` JSON。下面是字段示意，不是声称已经采集到的运行结果：

```json
{
  "timestamp": "<UTC RFC3339Nano>",
  "level": "ERROR",
  "message": "database query failed",
  "service_name": "service-d",
  "trace_id": "<32 hex chars>",
  "span_id": "<16 hex chars>",
  "trace_sampled": true,
  "request_id": "<opaque request ID>",
  "experiment_id": "<opaque run ID>",
  "http_method": "POST",
  "http_route": "/exercise",
  "http_status_code": 500,
  "duration_ms": 0,
  "dependency": "mysql",
  "operation": "query",
  "error_type": "mysql_error",
  "error_code": "1146",
  "error_message": "<actual driver error after redaction>"
}
```

`duration_ms` 必须由实际计时替换，不能固定为示例值。请求日志、依赖错误日志、降级日志使用自然业务描述，不写 `expected_root_cause` 或“当前随机选中了某故障”。

四服务都记录 request completion，关键依赖记录成功/失败或耗时，B 额外记录实际执行的 fallback。启动日志允许没有 trace_id，不能捏造；ctx 有有效 ID 时即记录，不按采样标志丢日志。

中间件顺序必须保证 recovery 后能记录最终状态，且异常请求只统计一次。明确启动日志、HTTP 日志、依赖操作日志的 logger 所有者；不要保留 Gin 访问日志与新 JSON completion log 的重复采集路径。

### 6.2 文件采集与 Loki

选择 **JSON 文件 → Collector filelog → OTLP HTTP → Loki**，stdout 只用于 `docker logs` 排障，不再被另一条管道重复入库。

- 每服务独立日志 volume；应用写自身目录，Collector 只读挂载。
- distroless nonroot 镜像中预创建并正确设置日志目录所有者；必须验证 fresh volume 与已有 volume 两种情况。不能用永久 root 运行应用或 `chmod 777` 掩盖问题。
- 文件轮转建议上限 10 MiB × 3；用经过测试的轮转实现，明确采集哪些滚动文件，验证重启/轮转不会大规模重复或漏采。
- Collector 使用持久化 `file_storage` 保存读取位置；首次启动可读取已有文件，重启使用 offset。
- Traefik 原有 `logs/traefik/access.log` 单独只读挂载给 Collector，不要求 Docker socket 或 Linux 宿主容器日志目录。
- 只采业务服务与必要网关请求；排除 `/aiops/*` 控制流量及实验控制日志、答案、AI 生成报告，防止回流后被当成证据。

Collector 将 JSON `service_name` 映射为 resource `service.name`，后者在 Loki 查询中为 `service_name`。将 `level`、request_id、experiment_id、http_route、真实错误等保留为 structured metadata；设置 LogRecord 的时间、severity、TraceId、SpanId；Body 为可读 message。

**trace_id / span_id / request_id / experiment_id 不得做 Loki 索引 label。** 索引先只保留服务名及少量环境/信号来源字段。具体配置按所固定的 Collector/Loki 版本验证，不机械复制最新文档的全部语法。

Loki 使用支持 structured metadata 的配置与 schema，启用 `allow_structured_metadata`，Collector 使用 `otlphttp` exporter 的 `/otlp` base endpoint；不要使用已经不需要的旧式 Loki exporter。此路径依据 Loki 官方 OTLP 接入文档 [S1]。

在本约定下，示意 LogQL 是 structured metadata 过滤，而不是假定 Body 仍是完整 JSON：

```logql
{service_name=~"service-a|service-b|service-c|service-d"} | trace_id="<trace_id>"
```

Collector 实际转换后的字段名必须通过一条真实日志回读确认，再写入查询模板测试。Traefik 的 `TraceId`、`RequestPath`、`DownstreamStatus`、`Duration` 需要单独规范化；网关路径与剥离前缀后的应用路由不能不加区分地合并。

## 7. Metrics：测量全量请求，不依赖 trace 是否被采样

### 7.1 指标清单

每个 Go 服务暴露内部 `/metrics`。使用独立且只注册一次的 registry；Prometheus scrape 配置附加静态 `service_name` target label。应用指标不再额外写同名冲突 label。

| 指标 | 类型 | 低基数 label | 用途 |
|---|---|---|---|
| `demo_http_requests_total` | Counter | `http_route,method,status_class` | 每服务请求量、5xx 比例 |
| `demo_http_request_duration_seconds` | Histogram | `http_route,method` | 每服务耗时分布 |
| `demo_http_inflight_requests` | Gauge | `http_route` | 并发与等待积压 |
| `demo_dependency_calls_total` | Counter | `dependency,operation,outcome` | MySQL/Redis 调用成功/失败 |
| `demo_dependency_duration_seconds` | Histogram | `dependency,operation` | 真正依赖调用的耗时 |
| `demo_business_fallback_total` | Counter | `dependency` | B 的实际降级次数 |
| Go/process 标准指标 | 标准 collector 类型 | 只保留标准有限维度 | 内存、CPU 时间、goroutine 辅助观察 |

`http_route` 用 Gin 路由模板，未知路由归一为固定 `unmatched`；排除 `/health`、`/ready`、`/metrics` 对业务统计的污染。dependency/operation/outcome 使用固定枚举，不用完整 SQL、Redis key、错误全文或动态路径。

不允许把 run_id、request_id、trace_id、scenario_id、seed、用户 ID 放入 Prometheus label。高基数 ID 的关联通过日志/trace 完成，指标依靠 **服务 + 路由/组件 + 时间窗口** 关联；这不是单请求精确 join。[S2]

HTTP middleware 负责 HTTP 指标，C/D 的实际依赖操作负责依赖指标。避免 handler wrapper、driver hook、自动 Agent 同时增加同一 Counter。`application_delay_c` 的 4 秒等待计入 HTTP/应用 span，不计入后面的 Redis SET/GET 耗时，否则会再次制造“Redis 本身慢”的错误证据。

### 7.2 查询与单位

默认 scrape interval 5s，查询 step 5s。HTTP/dependency histogram bucket 必须覆盖 3s、4s、5s 及客户端超时附近，不能直接沿用全部小于 1 秒的分桶。

示例查询，执行器需要固定模板并校验服务/路由参数：

```promql
sum by (service_name) (
  rate(demo_http_requests_total{http_route="/exercise"}[1m])
)
```

```promql
100 *
sum by (service_name) (
  rate(demo_http_requests_total{http_route="/exercise",status_class="5xx"}[1m])
)
/
sum by (service_name) (
  rate(demo_http_requests_total{http_route="/exercise"}[1m])
)
```

```promql
histogram_quantile(0.95,
  sum by (service_name, le) (
    rate(demo_http_request_duration_seconds_bucket{http_route="/exercise"}[1m])
  )
)
```

```promql
sum by (service_name, dependency) (
  rate(demo_dependency_calls_total{outcome="error"}[1m])
)
```

有限且已知的 counter label 组合预初始化为 0；无流量、无 scrape、序列不存在、后端失败必须分别处理。分母为 0 时错误率显示不可计算，不以 0% 替代未知。

`rate`/`increase` 是采样后的统计估计，`increase()` 有外推行为，不能要求“点击 1 次，查询值必须精确等于 1”。若需要某次实验的精确请求数，用请求记录或完整的 completion logs；Prometheus 负责趋势与影响范围。[S3]

统一时间单位：Jaeger startTime/duration 常为微秒，Traefik Duration 为纳秒，Prometheus 时间为秒，Loki 查询边界使用纳秒字符串。API 输出 ISO UTC + 明确单位；前端不要用 JavaScript Number 直接存储纳秒整数。

### 7.3 单次点击与持续实验

两个模式都实现：

**single：** 立即发起一个随机故障请求。适合验证链路和错误日志；照样查询指标，但很可能缺少基线或统计样本。报告应说明“指标不足以判断趋势”，而不是编造 CPU、错误率或 P95 上升。

**episode：** 有界的正常流量预热 → 故障窗口 → 恢复观察。建议默认配置：

```yaml
baseline_seconds: 60
incident_seconds: 60
recovery_seconds: 30
target_rps: 2
fault_ratio: 0.5
max_concurrency: 8
max_total_requests: 1000
```

这是实验参数，不是性能承诺。施加硬限制：持续阶段上限、总请求数、RPS、并发和最长 delay。调度使用限速与背压，不积累无限待发请求；记录 actual_rps、skipped_requests，不能把目标速率当成实测速率。

第一版全局最多一个 active episode，避免窗口指标被其他实验污染；运行时 UI 阻止冲突实验。服务若还有手工流量，必须标注指标仅是窗口相关证据，不能归因到单个 run。

基线、故障、恢复三个时间范围单独保存。查询 baseline 时以 baseline 结束时间求值，不能在实验结束后取“最近一分钟”冒充故障前基线。滚动 rate/P95 在阶段交界处会混合样本，图表与摘要明确窗口口径。

P95 的展示至少要求基本请求样本量和连续 scrape；阈值可先设为 20 个完成请求并在配置中声明，低于阈值标注低样本可靠性，不视为生产统计标准。

## 8. 实验与分析状态机

实验与诊断分开，不能因为模型失败就把真实业务执行标成失败。

```text
experiment:
queued → preparing → baseline(episode) → running
       → recovery(episode) → completed
       ↘ environment_unhealthy / injection_failed / cancelled / interrupted

analysis:
not_started → collecting → ready → analyzing → completed
                      ↘ awaiting_external
                      ↘ failed
```

每类信号同时维护独立状态：

```text
pending | available | partial | unavailable |
not_found_after_deadline | insufficient_samples
```

状态带 `reason`、查询次数、起止时间、数据时间范围、是否截断。健康/异常属于观测内容，不能混在“数据是否可用”的枚举里。

Trace 可采用 1s、2s、3s、5s、5s……的退避重试，总预算默认最多 45s，从业务请求完成后计算。复查时允许已存在 trace 的迟到 span 更新；连续两次 span 集合稳定只能作为尽力判断，不是完整性证明。

Loki、Prometheus 并行查询并有各自超时预算，不串行盲等 45s×3。达到期限后允许用部分证据诊断；但明确告诉模型缺失了什么。不要把 Jaeger 尚未入库直接写成“被采样丢弃”，也不能因 Prometheus 超时诊断业务宕机。

任务采用单 FastAPI worker + 有界 asyncio 任务/队列 + SQLite，不引入 Celery。保存 pending/active 任务；进程重启后把未完成任务标为 interrupted，不静默重放故障或重复调用收费模型。取消实验必须停止发新请求、取消可取消的请求，并给运行状态落盘。

## 9. API 契约

对外前缀统一为 `/aiops/api/v1`；可以由 Traefik strip `/aiops`，后端实际使用 `/api/v1`。下表按浏览器访问路径定义。

| Method / Path | 职责 | 关键返回 |
|---|---|---|
| `GET /health`、`GET /ready`（后端内部） | 存活/准备状态 | 各依赖状态；无模型凭据不影响基础健康 |
| `POST /aiops/api/v1/runs/random` | 创建随机故障实验 | 202，run_id、poll_url；不把业务 500 当控制 API 失败 |
| `POST /aiops/api/v1/runs/healthy` | 创建健康对照 | 同上 |
| `GET /aiops/api/v1/runs/{id}` | 进度与安全摘要 | 状态、时间、业务状态、代表 trace_id、信号可用性 |
| `GET /aiops/api/v1/runs/{id}/requests` | 分页展示请求记录 | request_id、trace_id、status、duration；不返回控制 token |
| `POST /aiops/api/v1/runs/{id}/cancel` | 取消实验 | 显式结果；重复取消幂等 |
| `GET /aiops/api/v1/runs/{id}/evidence` | 读取净化、冻结的证据包 | EvidenceBundle / bundle hash |
| `POST /aiops/api/v1/runs/{id}/analyses` | 发起诊断或生成外部 Skill 输入 | 202，analysis_id；provider 缺失转 awaiting_external |
| `GET /aiops/api/v1/analyses/{id}` | 获取模型输出与状态 | DiagnosisResult；不合格输出单独标记 |
| `POST /aiops/api/v1/analyses/{id}/result` | 外部 Skill 提交结构化结果 | 校验 bundle hash/schema；标记 external 来源 |
| `GET /aiops/api/v1/runs/{id}/ground-truth` | UI 读取实际注入结果 | 仅操作员权限；盲测模式需结果冻结后 |
| `GET /aiops/api/v1/analyses/{id}/evaluation` | 读取确定性对照结果 | matched/partial/mismatch/unscorable 和各字段结果 |
| `GET /aiops/api/v1/runs` | 分页历史 | 状态、模型、核心匹配结果、证据质量 |

创建请求只接受允许的模式和有界参数。固定 seed 可用于重放实验选择与计划中的注入序列；不承诺相同 seed 产生相同真实耗时/trace_id。

```json
{
  "mode": "single",
  "visibility": "learning",
  "seed": 42,
  "auto_analyze": true
}
```

默认未传 seed 时服务端生成并仅存在 GroundTruth/实验控制记录中。API 支持 idempotency key，防止浏览器重复提交创建两个故障。

**两个 trace 不可混淆：** 浏览器创建实验的控制请求可能有自己的 TraceId。UI 必须显示真实业务执行返回的 `representative_trace_id`，不能用 `/runs/random` 自身的响应 TraceId 冒充故障链路。

控制 API 的 HTTP 202/200 代表任务操作成功；业务的 500、200-degraded、slow 在 `business_result` 内独立显示。

## 10. EvidenceCollector：先把证据查准，再交给模型

### 10.1 固定采集流程

1. 从实际请求记录取得时间范围、业务状态、有效 trace_id；不读取 GroundTruth 来决定查询哪个服务。
2. 按 trace_id 查询 Jaeger，复用并修正现有 cleaner；验证响应确实对应目标 ID。
3. 从 trace 中取得服务集合、错误候选、实际调用树；trace 缺失时先查日志，再在已知四服务范围内降级查询。
4. Loki 优先按 trace_id 精确查；episode 可补 experiment_id 范围查询。再有限地查询相关服务在故障前后的上下文，明确哪些日志精确关联、哪些仅时间相关。
5. Prometheus 按观测到的服务、固定业务路由和时间窗口查询，不按“随机选中的故障类型”选择证据。
6. 整理三类原始证据、健康分支、缺失信号、查询与采样限制，输出规范化 EvidenceBundle。
7. 固定 snapshot，计算 SHA-256；诊断、评分、重放均引用此 bundle hash。

episode 可以先按实际错误/慢请求/降级日志聚合，选最多 3 条代表 trace，允许配置上限 5。优先用有观测依据的候选，并验证 Jaeger 中确实存在；找不到就换有限个候选，不能无限循环。不要用真实答案决定“哪条是最有代表性的根因”。

不建设常驻“扫描所有日志并不断检查 Jaeger”的服务。本版只在实验/诊断触发后执行有界查询；这也方便未来对接真实 incident。

### 10.2 EvidenceBundle 结构

使用 JSON Schema/Pydantic 明确定义，禁止额外未知字段通过。以下展示结构，方括号内为占位说明：

```json
{
  "schema_version": "1.0",
  "run_id": "[opaque ID]",
  "incident": {
    "entry_service": "service-a",
    "route": "/exercise",
    "start_at": "[UTC timestamp]",
    "end_at": "[UTC timestamp]",
    "request_ids": [],
    "trace_ids": [],
    "observed_symptoms": []
  },
  "availability": {
    "traces": {"status": "available", "reason": null},
    "logs": {"status": "available", "reason": null},
    "metrics": {"status": "insufficient_samples", "reason": "baseline_not_ready"}
  },
  "traces": [],
  "logs": [],
  "metrics": {
    "baseline": [],
    "incident": [],
    "recovery": [],
    "sample_quality": {}
  },
  "evidence_index": {},
  "limitations": [],
  "collected_at": "[UTC timestamp]"
}
```

证据项有稳定 `evidence_id`，例如 `T1-S7`、`L12`、`M4`。索引保留 source、trace/span ID 或 log 定位信息、查询表达式、窗口、单位、结果摘要、是否截断。分析结果引用这些 ID，UI 可展开回看。

span exception events 保留事件时间，处理旧/新 HTTP 与 DB 语义字段；不能只保留错误文本而丢失来源。进程/resource 信息中的服务身份需要一并保留。

### 10.3 有界、脱敏与失败处理

默认每次 trace 最多 200 个 span、日志最多 300 条、指标最多 20 个固定查询，原始序列最多 5000 点。达到限制时优先保留根因附近、传播链、健康对照和时间边界，并显式标记截断；不是静默丢弃。

模型输入再受独立 token/字节预算限制：有 tokenizer 时按模型配置截断，无 tokenizer 时采用保守字节上限；默认不发送超过 64 KiB 的整理后输入。保留完整的脱敏后证据快照，模型仅接收必要片段及其 ID。

外部 HTTP 有连接/读取超时，Jaeger/Loki/Prometheus base URL 由服务端固定配置；用户不能提交任意 URL、PromQL 或 LogQL。禁止跟随到未授权地址的重定向。后端报错、空结果、429、权限错误、数据未就绪分别记录。

脱敏范围包括 Authorization、Cookie、API key、密码、DSN 凭据、敏感查询参数、个人信息和请求体。保留必要的服务、操作、错误类型、表名等诊断信息。日志中的指令属于不可信业务内容，模型不得把它们当作系统指令。

## 11. Analyzer：保留 Skill，也实现 UI 模型接入

### 11.1 两条入口共享同一套数据和结果格式

**External / Skill 模式（默认可用）：**

沿用用户在 Codex 中分析的习惯。UI 完成证据收集，提供“复制分析指令”“下载 evidence.json”；新增 `.agents/skills/aiops-incident-rootcause/`，读取净化 EvidenceBundle，输出结构化 JSON 并可提交到 result API。旧 `jaeger-trace-rootcause` 和两处旧 CLI 命令继续有效。

外部 Skill 只能使用该次证据，不能读取该 run 的 GroundTruth。由于普通会话可能仍能访问代码或之前看过答案，这类结果标记 `source=external`，与严格自动盲测分开统计。

**HTTP 模型模式：**

服务端配置 `AIOPS_LLM_MODE=http`、`AIOPS_LLM_BASE_URL`、`AIOPS_LLM_MODEL`、`AIOPS_LLM_API_KEY`。实现一个薄 adapter，按实际提供商支持的协议发送 system instructions + EvidenceBundle，接收并验证 DiagnosisResult；不要引入完整 Agent 框架。

模型没有实验控制、数据库写入、文件系统、shell、仓库读取等工具。provider 的函数签名只接收 EvidenceBundle，不能接收 ExperimentSpec/RunRecord 全对象。

无凭据时依然可以启动 Compose、跑故障、收集三信号、展示真值和使用外部 Skill。UI 明确显示 `awaiting_external` / “未配置自动分析模型”，不能假装已有 AI 结论。fixture provider 只供测试，页面和结果必须标识模拟，不计入真实模型评测。

### 11.2 诊断流程

先摘要实际症状；沿调用树区分入口传播和最深可解释失败；用日志检查原生错误、上下文和降级；用指标观察故障窗口、健康基线、恢复以及影响范围；输出最有依据的原因、反证、缺失信息和只读排查建议。

这里的“最深”不是机械排序法。并行健康分支、调用先后、异常是否被处理、网关自己的路由错误等都要考虑。若证据只支持“客户端连接被拒绝”，不能升级成“Redis 全局宕机”。

三类信号各司其职：trace 解释请求路径与传播，logs 解释具体异常与业务语义，metrics 说明窗口级趋势与影响。某类指标没有明显异常，或完全缺失，都是应如实报告的结果；不能为了凑齐三信号证据而强行引用。

### 11.3 DiagnosisResult

下面是结构示例，不是本次已运行的诊断：

```json
{
  "schema_version": "1.0",
  "evidence_bundle_hash": "[SHA-256]",
  "status": "diagnosed",
  "summary": "service-d 的数据库查询发生表不存在错误，失败向 service-b、service-a 和网关传播。",
  "root_service": "service-d",
  "root_component": "mysql",
  "cause_code": "table_not_found",
  "impact_class": "failed",
  "confidence": "high",
  "root_evidence_ids": ["T1-S7", "L12"],
  "propagation": ["service-d", "service-b", "service-a", "traefik"],
  "supporting_findings": [],
  "counter_evidence": [],
  "limitations": [],
  "hypotheses": [],
  "recommended_checks": []
}
```

`status` 为 diagnosed / inconclusive / no_anomaly；`impact_class` 为 failed / slow / degraded / none / unknown。`root_component` 至少包含 application、mysql、redis_client、http_client、none、unknown；cause_code 使用通用故障枚举，不使用某次实验 scenario_id。

健康对照输出 no_anomaly、none，不能臆造原因。部分证据可输出 inconclusive / unknown；置信度是模型的定性判断，不是经校准的准确概率。

解析失败最多一次有界格式修复；仍不合格则 analysis failed，保存脱敏错误，不把未验证文本直接当成功结果。验证 evidence ID 是否存在、hash 是否匹配、字段是否允许，页面用安全文本渲染。

保存模型标识、参数、prompt 版本/hash、证据 hash、schema 版本、模型耗时与可获得的 token 用量；不知道用量时写 null，不估造账单。

## 12. Evaluator：独立对答案，而不是让 AI 自己给自己打分

### 12.1 确定性评分

诊断冻结后，Evaluator 才读取 GroundTruth。第一版比较四个核心字段：

| 维度 | 比较内容 |
|---|---|
| 根服务 | root_service 是否一致 |
| 根组件 | mysql / redis_client / application 等是否一致 |
| 原因类型 | table_not_found / connection_refused / application_delay 等是否一致 |
| 业务影响 | failed / slow / degraded / none 是否一致 |

通用同义词可通过版本化 alias 表规范化，但不要用字符串包含“mysql”就判为根因命中，也不要要求中文描述逐字一致。

附加校验：证据 ID 是否真实存在、引用是否来自本次快照、是否声称使用了不存在的信号。**引用存在率不等于证据在语义上真的支持结论。** 第一版把结构性校验与人工审阅字段分开，不声称已实现完整的自动事实验证。

最终状态：

- `matched`：四个核心维度匹配，结构/schema/引用检查通过。
- `partial`：部分定位正确，或原因只能确认到较粗粒度。
- `mismatch`：模型给出了与真值相冲突的定位。
- `unscorable`：注入失败、环境不健康、证据严重损坏、模型未输出有效结果等；记录具体原因。

matched 表示此受控案例的结构化定位匹配，不意味着已证明其自然语言解释完整可靠。

### 12.2 分开计算系统成功率与诊断准确率

统计总发起次数、有效注入次数、证据就绪率、模型完成率、四维核心匹配率、健康误报率、各场景 macro 结果、平均诊断耗时和拒答/不确定比例。

证据不足而拒答不计成“已准确诊断”，但也不与胡乱诊断混淆。报告两个分母：端到端成功 / 总实验数；核心匹配 / 可评分有效模型结果。列出所有排除项，不能通过删除失败样本美化结果。

### 12.3 对照实验

实现同一 EvidenceBundle 的三个视图：trace-only、trace+logs、trace+logs+metrics。每次独立模型上下文、相同模型与提示词主版本、相同快照；不能把上一轮答案带入下一轮。

这能回答“加 metrics/logs 是否有用”，但不保证每个故障都更准。诸如缺表错误，一条 SQL span 就可能足够；metrics 主要帮助判断影响范围、持续时间和恢复。应用等待 vs SQL 慢查询、成功降级、部分证据缺失等更适合观察联合证据的价值。

先用每类故障若干次加健康对照做试运行，报告样本量；禁止写“达到生产级准确率”。固定 seed 只帮助选择重现，不等于独立的大规模泛化评测。

## 13. UI：一眼看清真实故障、AI 判断和证据

原页面继续可用，新增“随机故障实验”区域。建议布局：

```text
[Random Error] [健康对照] [单次 / 持续实验] [教学 / 盲测]

实验状态      正在故障窗口 / 正在收集 / 等待外部分析 / 已完成
实际请求      HTTP 500 · 业务 TraceId · 请求数量 · 实际耗时
信号状态      Trace 已获取 / Logs 已获取 / Metrics 基线不足

实际注入（操作员区）    AI 独立分析              对照结果
故障、目标、确认状态     根服务/原因/置信度        各维度一致/不一致

证据： [调用链] [日志] [指标与时间窗口] [完整报告]
历史： 实验 ID / 模型 / 核心命中 / 证据缺失 / 耗时
```

教学模式满足“界面直接返回调用了哪个 error”的需求；该答案由 ground-truth API 单独返回，不混入模型请求。盲测模式在模型结果冻结后再揭晓答案。

UI 提供代表 TraceId 的复制与 Jaeger 链接、证据 ID 展开、日志来源、指标窗口与样本数。没有数据时展示明确原因，不用空折线暗示指标正常。

支持取消、错误重试、重新载入页面恢复任务进度。重新分析必须生成 analysis revision，绑定同一或新证据 hash；盲测不能先揭晓答案再无标记覆盖首次结果。

使用有界 polling 即可，不把 WebSocket/SSE 设为必要依赖。旧 trace 结果展示与新实验展示分开，防止异步响应覆盖当前选中的 run。

## 14. Compose、配置与启动体验

目标：fresh clone 在具备镜像/依赖下载能力的机器上，尽量以 `docker compose up --build -d` 启动全部基础实验能力。真正的 UI 自动 AI 分析仍需要有效模型连接；没有模型连接走 Skill 模式。

默认新增 MySQL、Redis、Loki、Prometheus、aiops-api。RabbitMQ 仍是可选，不阻塞随机实验池。Grafana 后置。

新增各服务提交到仓库的 `config.compose.yaml`，默认指向 Compose 内 mysql/redis/service-*。保留 `config.example.yaml` 的外部依赖用法及用户本地 `config.yaml`，通过显式配置路径环境变量/override 选择，不能覆盖用户已有文件。

基础设施配置：

- MySQL 创建 demo 数据库与非 root 业务用户；检查 A/B/D 并发 AutoMigrate/seed，保证迁移与 seed 幂等/有序，避免竞争导致 fresh start 不稳定。
- 合理配置 dependency healthcheck + `depends_on: condition: service_healthy`，并保留应用连接超时/重试。Compose “进程已启动”不等于依赖可用。[S5]
- Go distroless 镜像增加服务二进制自己的 healthcheck 子命令或等价无 shell 探针；不要添加不存在的 curl/wget 检查。
- aiops-api 的存活不依赖 LLM key；ready/状态接口准确区分观测后端、业务依赖和模型是否配置。
- 日志、Collector offset、Loki、Prometheus、SQLite 使用各自有限存储/volume；运行数据加入 .gitignore。
- 默认各管理/控制端口仅绑定 127.0.0.1；容器间查询用服务名。不能把容器内 localhost 当作其他服务地址。
- 同时修改 Traefik `/aiops` 路由、`web/nginx.conf` 同源代理，并验证 18086 与 18083 两种 UI 访问。
- 避免循环 depends_on：aiops-api 可以先启动并报告依赖未就绪，业务实验另做 preflight；不能要求 Traefik 与 aiops-api 相互 healthy 才启动。
- 固定新增镜像和依赖的已验证版本；保留现有组件版本为起点，必要升级单独说明。Agent 从 latest 下载改为明确版本/校验和，四个服务保持一致；不猜测一个未验证的版本号写进锁文件。
- 基础环境支持 amd64/arm64；记录实际验证的平台，不声称未测试平台已通过。

文档写清应用日志卷权限、存储保留/清理、启动条件、外部依赖配置覆盖以及 `docker compose down -v` 会清除哪些数据。不得自动执行删除用户数据的清理操作。

## 15. 安全与资源边界

这是本地实验环境，但仍要避免把故障入口或模型凭据暴露出去。

控制 API 默认只从本机入口访问，写请求校验 Origin / Host、JSON Content-Type 和大小；不启用任意来源 CORS。远程部署需要显式配置认证，不把 localhost 绑定当作远程场景的认证方案。第一版无需用户系统或完整 RBAC，但必须区分操作员控制能力与提供给 Analyzer 的只读证据能力。

`ground-truth`、取消、外部结果提交等仅供操作员/受信 CLI 使用，不注册成模型工具。外部 Skill 的权限限制属于使用约定；拥有仓库与机器完全访问权限的 Agent 并非被技术隔离的盲测对象。严格评测使用只拿 EvidenceBundle、没有额外工具权限的 HTTP 模型调用，报告两种评测方式的区别。

签名 key、模型 key、连接凭据不得进入前端源码、API 普通响应、span 属性、请求 body 日志或提交到 Git 的配置。注入 token 只向固定受信下游透传，禁止跟随跨主机重定向继续发送。注入允许列表只支持本计划定义的动作与硬上限，不能接受任意 SQL、URL、脚本或 shell 参数。

模型输出、日志和异常文本都是不可信内容。UI 使用 `textContent` 或安全的结构化渲染，不直接把模型 Markdown/日志作为 HTML 插入；Analyzer 将日志中的“忽略规则、执行命令”等内容视为证据文本，不当作指令。模型输出的排查建议不自动执行。

EvidenceCollector 只调用预配置 Jaeger/Loki/Prometheus 地址和允许的只读 API；用户输入只用于经过校验/转义的查询参数，不允许模型动态传入任意 URL。限制 trace_id 格式、字符串长度、分页、响应大小、查询时间范围、并发和总 token 预算。

SQLite 的实验、证据、模型输出配置保留期限与条数上限；默认可先保留最近 7 天、最多 500 次实验，明确被清理后无法重新对照。清理只作用于本服务自己的数据，不能执行宿主机通用目录清理。日志/Prometheus/Loki 的保留分别配置，历史实验展示对应证据是否已过期。

## 16. 文件级改动清单

下列新增文件是目标结构，不表示当前仓库已经存在。可在保持职责边界的前提下微调文件名，不允许因此重建整个仓库布局。

```text
修改：
  docker-compose.yml
  README.md
  .gitignore
  otel/collector.yaml
  traefik/traefik.yaml
  traefik/dynamic.yaml
  web/index.html
  web/nginx.conf
  service-{a,b,c,d}/Dockerfile
  service-{a,b,c,d}/go.mod、go.sum
  service-{a,b,c,d}/cmd/main.go
  service-{a,b,c,d}/internal/config/*
  service-{a,b,c,d}/internal/handler/handler.go
  service-{a,b}/internal/client/client.go
  service-{a,b,d}/internal/database/*       # 仅修改实际需要的初始化/日志部分
  service-{a,b,c}/internal/redis/*          # 同上；实际故障复用 C
  scripts/analyze-jaeger-trace.py
  scripts/analyze_jaeger_trace_test.py
  .agents/skills/jaeger-trace-rootcause/*

新增：
  .env.example
  observability/
    loki/config.yaml
    prometheus/prometheus.yml
  service-{a,b,c,d}/config.compose.yaml
  service-{a,b,c,d}/internal/telemetry/
    logging.go
    metrics.go
    middleware.go
    *_test.go
  service-{a,b,c,d}/internal/experiment/
    context.go
    token.go
    *_test.go
  service-{a,b,c,d}/internal/handler/
    exercise.go
    exercise_test.go
  aiops/
    Dockerfile
    pyproject.toml                       # 包含依赖版本与测试依赖
    src/demo_aiops/
      api.py                            # HTTP 契约，不负责提示词拼接
      models.py                         # 分离实验、答案、证据、诊断类型
      storage.py                        # SQLite、迁移、状态与快照
      runs.py                           # 选择、调度、取消、实际注入确认
      analysis.py                       # 只接收 EvidenceBundle
      evaluate.py                       # 冻结后比较标准化字段
      providers.py                      # external / http，测试替身单独标记
      collectors/
        jaeger.py
        loki.py
        prometheus.py
        bundle.py
      prompts/diagnose.md
    schemas/
      evidence-bundle.schema.json
      diagnosis-result.schema.json
    tests/
      fixtures/
      test_*.py
  scripts/
    collect-incident.py                  # 获取净化证据；支持本地 CLI
    evaluate-aiops.py                    # 冻结结果对照/汇总与消融
    smoke-aiops.sh                       # 真实 Compose 冒烟测试
  .agents/skills/aiops-incident-rootcause/
    SKILL.md
    scripts/                            # 薄封装，复用证据实现
  docs/
    plans/2026-09-19-go-otel-aiops-upgrade-plan.md
    observations/aiops-validation.md    # 实现后填写实际验证结果
```

目前四服务各有独立 go.mod 和 Docker build context。第一版保留它们：小型 telemetry/token 包可以受控复制，并用相同契约和跨服务测试约束；不要为了共享几十行代码先迁移整个 Go workspace。若确需共享包，必须同步更新 Docker build context、COPY、Go module 解析和四服务独立测试，并说明收益。

Python 的 trace 清洗逻辑只维护一个真实实现；原脚本、原 Skill、新 EvidenceCollector 复用它。旧 `--trace-id` / `--fixture-file` 等 CLI 行为保持兼容，旧脚本不能因为新 FastAPI 服务的引入而被迫依赖完整 Web 框架。

## 17. Codex 分阶段任务与退出条件

所有阶段都是本次目标的一部分；顺序用于减少返工，不代表做完第一阶段就停止。每阶段先完成可运行的最小路径，再补测试，不先铺满全部空目录。

### P0：锁定基线与纠正分析语义

- [ ] 读取 AGENTS.md、实际 HEAD、现有工作区改动；记录与本计划基线的差异。
- [ ] 检查四个 Go 模块、原 Python 测试、Compose 配置和本地可用验证能力。
- [ ] 修正普通 span event 被判错；异常事件保留时间；正确处理缺失父 span 与非 CHILD_OF 引用。
- [ ] 新增普通成功事件、真实异常、HTTP 200 子调用失败、fanout 慢分支测试。
- [ ] 建立 EvidenceBundle / DiagnosisResult 契约与故障类别枚举，写出关键类型隔离测试。
- [ ] 将 Agent 版本固定方案与当前版本验证结果记录下来，不盲目升级整个技术栈。

**退出条件：** 原 CLI/Skill 可继续使用；普通事件不再导致误报；新增结果契约能够被测试解析。基础故障诊断不是靠预读场景目录实现。

### P1：本地环境与三类信号贯通

- [ ] 补全 MySQL / Redis 默认 Compose 配置与四服务 config.compose.yaml，保留原外部配置模式。
- [ ] 实现日志目录权限、结构化应用日志、trace/request/run 上下文和轮转。
- [ ] 增加 Collector filelog / file_storage / Loki OTLP；应用与网关日志分别规范化。
- [ ] 四服务增加 HTTP、真实依赖操作、fallback、Go/process 指标，Prometheus 5 秒抓取。
- [ ] 验证原自动埋点、原 trace ID 返回与尾部采样未被替换/重复初始化。
- [ ] 增加启动探针、连接重试、依赖就绪检查；验证 fresh volume 与已有 volume。

**退出条件：** 通过旧的已知错误按钮触发真实请求，可以从 Jaeger、Loki、Prometheus 分别读取真实数据。日志 trace_id 与业务 trace 一致；指标没有单请求 ID label；无模型连接也能启动。

### P2：随机故障与独立真实答案

- [ ] 实现 aiops-api 基本 API、SQLite、单 worker 有界队列和 RunManager。
- [ ] 四服务实现中性 `/exercise` 路径，复用实际 MySQL / Redis 操作与 fanout。
- [ ] 实现 7 类故障 + 健康对照；错误仅作用于当前请求，默认不碰共享正常连接配置。
- [ ] 实现有界签名 token、显式上下文透传、实际注入确认、预期和实际结果区分。
- [ ] RunManager 经 Traefik 调业务路径，设置匹配现有路由的 Host；明确控制 trace 与业务 trace。
- [ ] 随机选择在服务端，seed 支持选择复现；种子与答案不放入普通遥测和 Analyzer 输入。
- [ ] 未知动作、过期/伪造 token、环境故障、未发生预期效果均有明确处理。

**退出条件：** 每个场景都能单独运行并确认实际效果；随机按钮不是“随机返回一段错误字符串”。健康请求与下一次实验不受上次故障污染；前端可看答案但 Analyzer 输入看不到。

### P3：三信号证据采集与可用性处理

- [ ] 将现有 Jaeger 清洗器抽成共享纯逻辑，补可靠的时间单位转换和引用关系。
- [ ] 实现 Loki 精确 trace/request/run 过滤与明确标记的时间相关日志；使用真实转换后的字段名。
- [ ] 实现固定 PromQL 查询模板、阶段窗口、无样本/无数据/查询失败区分。
- [ ] 实现并行查询、有界退避、结果截断、单次与多请求实验的代表 trace 选择。
- [ ] EvidenceBundle 附证据 ID、来源查询、窗口、单位、hash、限制与采集状态。
- [ ] 实现 CLI 导出，并断言整个模型输入不含 oracle、seed、注入 token、标准答案等字段。

**退出条件：** 新 API/CLI 能输出真实、净化、可重放的证据快照。任一后端不可用时仍能返回明确的 partial 结果，而不是无限等待或假装业务出故障。

### P4：AI、对照评分与 UI 闭环

- [ ] 新增三信号 Skill，支持获取 EvidenceBundle、输出 DiagnosisResult、提交并冻结结果。
- [ ] 实现可配置 HTTP 模型 provider、超时、有限重试、结构化结果验证、调用统计。
- [ ] 缺少模型配置时使用 awaiting_external；测试替身明确标记 fixture，不进入真实准确率。
- [ ] Evaluator 独立比较根服务、组件、错误类型、影响形态，校验引用并展示差异。
- [ ] UI 增加随机/健康按钮、教学/盲测、证据状态、诊断、揭晓、对照和历史。
- [ ] 原 trace UI 仍可用；18086 / 18083 两种入口代理均覆盖新 API。
- [ ] 验证模型输出冻结、盲测揭晓门槛、重复提交和重新分析 revision 行为。

**退出条件：** 至少通过一条“真实请求 → 三后端证据 → 真实模型或外部 Skill 独立分析 → UI 对照”的链路。未配置真实模型时，软件链路测试可通过，但实际模型诊断验收必须标为未运行。

### P5：持续实验与联合信号评测

- [ ] 实现 baseline / incident / recovery 的有界 episode，记录实际流量及阶段窗口。
- [ ] 实现停止/取消、全局 episode 互斥、重启 interrupted、不自动重放或重复付费。
- [ ] 单次请求明确提示指标样本不足；持续实验可比较请求量、错误率、耗时、fallback 等实际观测。
- [ ] 同一快照支持 trace-only / trace+logs / 三信号三组独立分析。
- [ ] 汇总总实验数、有效注入、证据就绪、模型完成、匹配率、健康误报、各类结果和排除项。
- [ ] 增加关键集成/端到端测试，完成脚本和 README，记录实际测试平台、命令、结果与未验证项。

**退出条件：** 用户能运行一次持续随机故障，查看故障前/中/后的证据，并独立对照 AI 结论；能区分“没采到数据”“模型没完成”“判断错”“判断对”。评测不宣称未测得的效果。

## 18. 验收矩阵与 Definition of Done

### 18.1 功能与数据真实性

| 验收点 | 应观察到的结果 | 不合格的实现 |
|---|---|---|
| fresh clone | 默认 Compose 能启动业务、存储和实验 API；无需先手工创建被忽略的 config.yaml | 仅在开发者现有 MySQL / Redis 上能运行 |
| 原场景回归 | 原 Error、Chain MySQL、fanout、slow、degrade 和 TraceId 展示保持可用 | 为新 UI 删除旧接口或把 Agent 改成普通 go build |
| 缺表故障 | D 真实 SQL 失败，日志/span 显示错误；上游失败是传播 | AI 只根据 `/mysql/error` 路径或隐藏标准答案匹配 |
| Redis 连接被拒绝 | C 对实验错误地址调用失败；正常 Redis 不必故障 | 报告“整个 Redis 实例已宕机”，没有相应证据 |
| 应用内等待 | C 应用/HTTP 路径慢，后面的真实 Redis 操作不必慢 | 把等待塞入 Redis 依赖 duration，再断言 Redis 慢 |
| SQL 慢查询 | D 的实际 `SELECT SLEEP` span 与依赖耗时支持 SQL 阻塞 | 只新增一条名为 slow_sql 的日志，没有执行 SQL |
| HTTP 200 降级 | C 错误、D 健康、B fallback、入口成功；结论为降级 | 因入口 200 宣称一切健康，或宣称全链路不可用 |
| 健康对照 | 普通 event 不判错；足够证据时输出 no_anomaly | 为提高故障命中率，始终输出一类故障 |
| 上下文 | 业务 TraceId / request_id 在真实下游 span/log 可关联 | UI 显示的是创建实验控制 API 的 TraceId |
| Metrics | 原始 registry Counter 递增可精确测试；窗口查询按统计值解释 | 用 `increase()==1` 作为单次点击必须满足的条件 |
| 注入确认 | intended 与 applied/effect 区分；失败注入 unscorable | 只要抽中了缺表就把任何 500 都算作缺表已执行 |

### 18.2 稳健性、采样与防答案泄漏

必须覆盖以下测试组：

- **日志/trace 清洗：** 普通 event、真实 exception、异常时间、HTTP 5xx、200 降级、父 span 缺失、并行区间重叠、跨 trace 引用、不同单位转换、无效 JSON、限量截断。
- **采集失败：** Jaeger 先无数据后可见；过期/不完整 trace；Loki 不可用；Prometheus 无 scrape/无样本/后端超时。应返回正确证据状态，不编造替代数据。
- **采样：** 健康 trace 可能按策略不保留，此时不能让健康对照永远阻塞。用于验证实际健康 trace 清洗的 fixture 与真实随机采样实验分别记录；不靠把健康场景标 ERROR 保留它。
- **日志持久化：** nonroot 可写、fresh volume、轮转后可采、Collector 重启 offset 生效、控制日志不进证据流；避免 stdout/file 双采造成重复。
- **指标：** middleware 只计一次，panic/recovery 后完成指标正确；未知路由归一化；无 request/run/scenario label；应用等待与真实依赖时间分开；没有数据不等于 0。
- **注入安全：** 过期/伪造 token 拒绝、参数上下限、固定下游、不跟随跨主机重定向泄漏 token、并发请求之间无共享故障污染、取消后不再发送新请求。
- **答案隔离：** 捕获完整 HTTP provider 请求，扫描禁止字段和值；扫描参与诊断的 span 属性、日志、响应错误包装和 metric labels。教学 UI 中的答案不得出现在 provider payload；未知真实运行错误仍可作为证据。
- **结果可信：** schema 错误、引用不存在、超时、provider 未配置、重复提交、过早揭晓、重新分析 revision、fixture 混入评测均有测试。自然语言解释正确性不由“JSON 解析成功”替代。
- **任务持久化：** API 重启，未完成实验标 interrupted，不再次发故障；收费模型调用不自动重放；一个 episode 活跃时不允许启动冲突实验。

输入隔离测试之外，还应人工检查至少一个模型请求的完整净化内容。黑名单扫描不能替代类型分层与人工核对。

### 18.3 建议交付的验证命令

下面新脚本/路径是改造完成后的目标命令，不表示它们已在当前仓库存在或已执行：

```bash
# 四个 Go 服务仍分别是独立模块
for service in service-a service-b service-c service-d; do
  (cd "$service" && go test ./...) || exit 1
done

# 兼容现有 Python 测试命名
python3 -m unittest discover -s scripts -p '*_test.py'

# 按 aiops/pyproject.toml 安装测试环境后执行
python3 -m pytest aiops/tests

docker compose config --quiet
docker compose up --build -d

# 新脚本应完成依赖就绪、实际请求、三信号回读、UI/API 冒烟；有界等待
bash scripts/smoke-aiops.sh

# 新 CLI 应支持：采集净化证据、冻结模型结果之后再比较
python3 scripts/collect-incident.py --run-id RUN_ID --output incident.json
python3 scripts/evaluate-aiops.py --analysis-id ANALYSIS_ID
```

脚本不得默认开启高并发、执行删除卷、覆盖本地配置或调用收费模型。真实 LLM 验证使用显式参数/环境开关，报告实际模型标识、提示词版本、EvidenceBundle hash、样本数和结果。

### 18.4 完成标准

“完成改造”至少意味着：源码与配置齐全；原功能回归；三类数据真实可查询；随机故障真实执行；实际答案与诊断隔离；Skill 和 HTTP provider 两条工作流均有实现；单次/持续实验可操作；UI 显示结构化诊断与独立对照；测试及 README 可供新用户复现。

软件工程测试与真实模型效果是两项不同的验收。没有模型 key 时不得伪造“模型通过”；环境无法运行 Docker 时不得声称已完成三后端真实联调。交付报告逐项列出已验证、未验证、失败及原因，保留未完成任务的明确位置。

不得残留用固定场景答案生成分析结果的业务分支、假 metrics 趋势、TODO 按钮或只在 console.log 中返回的评测结果。故障注入模块的预期答案表是合法测试控制；Analyzer 读取该表生成答案是不合法的实现。

## 19. 非目标、后续扩展与最终交接

本轮不做真实生产故障自动发现、告警降噪、多租户、自动修复、Kubernetes chaos、复杂网络故障平台、跨进程强安全沙箱、大规模模型 benchmark 或自建大模型。RabbitMQ、新数据库、Grafana 大屏、服务端基础设施 exporter 都可后置，不影响当前闭环成立。

这也意味着：本轮即使定位到客户端连接错误或某个 SQL 阻塞，也不能声称已经证明 Redis/MySQL 宿主机 CPU、磁盘或网络是根因。没有采集对应信号的结论要保持在已观察到的层级。

之后接生产 AIOps 时，可复用本次 EvidenceCollector、诊断 schema、Skill、证据引用和评测器。把“已知随机实验”入口替换为“聚合错误接口/告警 → 找到存在的代表 trace → 收集同窗口日志与指标”，再逐步补异常检测与知识。现在不需要新增一个永久消费所有 access log、轮询每个 TraceId 的后台消费者。

可直接交给 Codex 的提示词：

```text
请读取 docs/plans/2026-09-19-go-otel-aiops-upgrade-plan.md，
基于实际工作区按第 17 节 P0–P5 直接实施，完成代码、配置、测试与 README。
先检查 AGENTS.md、Git 状态和现有实现，保留用户未提交改动，不重复实现已经存在的能力。
保留四个服务、LoongSuite 自动 tracing、现有 UI/接口与原 Jaeger Skill。
实现真实 logs/traces/metrics、随机故障、独立诊断、真实答案对照和持续实验。
Analyzer 不得读取故障目录、注入参数或真实答案；不得用 fixture 或规则答案伪装模型诊断。
无模型 key 时仍完成软件闭环，保留外部 Skill 工作流，并明确标记真实模型测试未运行。
每阶段执行能运行的验证，最终汇报变更文件、命令与结果、启动方式、未验证项和限制。
不要只再输出一份计划，不要引入本计划明确排除的重型基础设施。
```

## 20. 审查依据与官方参考

### 20.1 仓库源码，固定到本次审查 commit

以下链接只用于说明基线来源，不代表需要在 Analyzer 中读取代码或场景定义。

| 依据 | 位置 |
|---|---|
| 审查基线 | [commit 00129e3](https://github.com/luozijian1990/go-otel-demo/commit/00129e3519347cba70d30c385b0362f472cd5214) |
| 当前使用方式与 Skill 流程 | [README.md](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/README.md) |
| 已有组件与外部配置挂载 | [docker-compose.yml](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/docker-compose.yml) |
| Trace pipeline 与采样规则 | [otel/collector.yaml](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/otel/collector.yaml) |
| A 初始化、日志和 SDK 所有权 | [service-a/cmd/main.go](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/service-a/cmd/main.go) |
| A 旧接口、事件与 TraceId | [service-a/internal/handler/handler.go](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/service-a/internal/handler/handler.go) |
| B fanout 和成功降级 | [service-b/internal/handler/handler.go](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/service-b/internal/handler/handler.go) |
| C Redis 错误地址与应用内等待 | [service-c/internal/redis/redis.go](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/service-c/internal/redis/redis.go) |
| D 缺表、SQL SLEEP 与初始化 | [service-d/internal/database/database.go](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/service-d/internal/database/database.go) |
| 上下文传播及错误 body 包装 | [service-a/internal/client/client.go](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/service-a/internal/client/client.go) |
| Agent 下载、自动编译、nonroot | [service-a/Dockerfile](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/service-a/Dockerfile) |
| 现有 trace 清洗器 | [scripts/analyze-jaeger-trace.py](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/scripts/analyze-jaeger-trace.py) |
| 现有 AI Skill | [jaeger-trace-rootcause/SKILL.md](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/.agents/skills/jaeger-trace-rootcause/SKILL.md) |
| 现有 UI | [web/index.html](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/web/index.html) |
| 入口 Host 与路由约束 | [traefik/dynamic.yaml](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/traefik/dynamic.yaml) |
| 直接访问 UI 的代理 | [web/nginx.conf](https://github.com/luozijian1990/go-otel-demo/blob/00129e3519347cba70d30c385b0362f472cd5214/web/nginx.conf) |

### 20.2 技术方案核对，官方文档

访问核对日期：2026-09-19。latest 文档可能随版本变化；实施时以实际固定的版本为准，并验证配置。本文并未对新增组件组合执行运行时兼容性测试。

| 编号 | 官方参考 | 用途 |
|---|---|---|
| S1 | [Loki：OpenTelemetry 日志接入][S1] | 原生 OTLP、structured metadata、字段规范化与索引 label |
| S2 | [Prometheus：Instrumentation best practices][S2] | 指标基数、已知序列初始化和埋点边界 |
| S3 | [Prometheus：Query functions][S3] | rate / increase / histogram_quantile 的统计语义 |
| S4 | [Prometheus：HTTP API][S4] | query_range、时间参数、查询结果和错误处理 |
| S5 | [Docker Compose：启动顺序][S5] | service_healthy、启动与就绪的区别 |
| S6 | [Prometheus：Go 应用接入][S6] | Go client、metrics handler 与抓取 |
| S7 | [Loki：HTTP API][S7] | 日志查询 API、时间和结果结构 |

[S1]: https://grafana.com/docs/loki/latest/send-data/otel/
[S2]: https://prometheus.io/docs/practices/instrumentation/
[S3]: https://prometheus.io/docs/prometheus/latest/querying/functions/
[S4]: https://prometheus.io/docs/prometheus/latest/querying/api/
[S5]: https://docs.docker.com/compose/how-tos/startup-order/
[S6]: https://prometheus.io/docs/guides/go-application/
[S7]: https://grafana.com/docs/loki/latest/reference/loki-http-api/
