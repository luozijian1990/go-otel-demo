# Go OpenTelemetry Local Demo · Vibe Coding 教学拆解方案

> ⚠️ 本拆解为面向教学的重构路径，非真实开发时序。用于讲清"这个项目可以怎样一步步 vibe 出来"，不代表当初真实的提交顺序。

## 一、项目实现了什么

**一句话**：在本地一键起一整套"生产级可观测性最小骨架"——4 个 Go 服务 + 网关 + Collector + Jaeger + UI，让你能点一个按钮就触发一条横跨 HTTP/MySQL/Redis/RabbitMQ/多跳调用的真实 trace，并能用 TraceId 在 Jaeger 里精准还原"哪一跳挂了 / 哪一跳慢了"。

**核心机制**（最不显然、最值钱的设计）：**Collector 的 tail sampling 策略**。让 demo 真正像生产：5xx、span ERROR、>3s 慢请求 100% 保留，其余正常请求只留 1%。这一条把整个项目从"trace 玩具"升级成"可拿来讲故障排障的演练台"——它解释了为什么你点 `/ok` 在 Jaeger 里常常查不到，但点 `/error` 一定查得到。配合**全链路把 TraceId 回吐给用户**（响应 header `X-Trace-Id` + 错误 JSON 体 + Traefik 访问日志），完成了"用户看见 5xx → 拿 TraceId → 一条命令分析根因"的闭环。

**技术栈**：Go (Gin) · GORM · go-redis · amqp091 · OpenTelemetry SDK (otelgin/otelhttp/otelgorm/redisotel) · OTLP gRPC · OpenTelemetry Collector Contrib · Jaeger all-in-one · Traefik v3 · Docker Compose · 一个零依赖的 Python 3 脚本做 trace 清洗与可选 LLM 根因分析。

**对外暴露**：4 个 Go 服务共暴露约 50 个 HTTP 路由，按"层次"分四档——
- 基础态：`/ok`、`/error`、`/slow`
- 单依赖：`/mysql/*`、`/redis/*`、`/rabbitmq/*`
- 跨服务：`/call/*`、`/full/*`（一次请求覆盖所有依赖 + peer）
- 多跳链路：`/chain/redis/ok`、`/chain/mysql/error`、`/chain/fanout/{ok,error}`、`/chain/slow/redis`、`/chain/degrade/ok`（A→B→{C,D} 的拓扑）
- 入口：Traefik `localhost:18086/service-{a,b,c,d}/*`；Demo UI `/ui`；Jaeger UI `:16686`

## 二、拆解概览

- **建议步数**：7 步（含 Step 0 原型）
- **理由**：项目实际有 5 个清晰的能力跳跃（单服务+OTel → 依赖埋点 → 跨服务传播 → 尾部采样 → 网关 → 多跳链路 → 闭环 UI/分析）。压到 5 步会把"尾部采样"塞进"加 Collector"里讲，听众只会觉得"配了个 yaml"，但**它恰恰是这个 demo 最值钱的设计**，必须独立一步。扩到 9 步会把 MySQL/Redis/RabbitMQ 拆成各自一步，节奏变碎、决策点重复。7 步刚好每步都能演示一个"看，trace 形状变了"的瞬间。
- **方法论映射**：镜像项目 `docs/plans/` 中已使用的 SDD 流程（每个增量产出 `proposal.md` → `design.md` → `tasks.md`），并在 Step 0 单独引入 Phase 0 原型探索（最薄一条线）。讲解时每步开头点一下"如果你来做，先写 proposal、再设计、再拆任务、最后用 TDD/手工验收落地"，让流程感和代码同步。
- **预计讲解时长**：约 **22–30 分钟**（不含问答与现场写码；含 3 处现场演示）
- **预计幻灯片页数**：约 **22 页** → 交接 `/frontend-slides` 时篇幅选 **Medium**
- **视觉风格**：**可观测性控制台风**（深色 + 等宽字体 + Jaeger/Grafana 配色），具体规范见第六节

## 三、分步详情

### Step 0 · Phase 0 原型——证明 SDK→Collector→Jaeger 链路真的通
- **交付什么（可演示）**：一个最小 Gin 服务 `/ok`，配合 `docker-compose.yml` 起 Jaeger all-in-one + Collector，浏览器打开 Jaeger UI 能看到一条带 `service-a` 名字的 trace。**演示**：curl `/ok` → 刷新 Jaeger Search → 看到那条 span。
- **涉及模块/文件**：`docker-compose.yml`、`otel/collector.yaml`（最初版只有 OTLP receiver + OTLP exporter to Jaeger，没有 tail sampling）、`service-a/internal/observability/observability.go`、`service-a/cmd/main.go`。
- **方法论阶段**：Phase 0 原型探索——这一步**专门验证全链路最不确定的事**：版本兼容（OTel SDK / Collector / Jaeger 的 OTLP 端口与协议），而不是写功能。
- **决策点**：
  - **OTLP gRPC 还是 HTTP？** 选 gRPC（4317）。Why：Collector 默认两个都开，但 Go 侧 gRPC exporter 是 OTel 官方推荐，连接复用 + 自动重试，HTTP exporter 主要是给浏览器/移动端用的。换 HTTP 不会错，只是没必要。
  - **Sampler 用 `ParentBased(AlwaysSample)`**：因为这是 demo，要让每个请求都产 span 进入 Collector，**真正的采样决策放到 Collector 端做**（铺垫 Step 3 的尾部采样）。如果在 SDK 端就采样，Collector 拿不到完整 trace，tail sampling 就成了无源之水。
- **讲解词预估**：约 700 字

### Step 1 · 依赖埋点——MySQL/Redis/RabbitMQ 各产一根 span
- **交付什么（可演示）**：在 `service-a` 里把 GORM、go-redis、amqp091 接上 otel instrumentation，新增 `/mysql/ok`、`/redis/ok`、`/rabbitmq/ok` 和各自的 `/error` 变种。**演示**：curl `/full/ok`（其实这一步还没有 /full，但可以一次性 curl 三个 /ok 路由），在 Jaeger 里看到一条 trace 下挂着 SQL、Redis SET/GET、RabbitMQ publish 三种子 span。
- **涉及模块/文件**：`service-a/internal/database/`、`service-a/internal/redis/`、`service-a/internal/rabbitmq/`、`service-a/internal/handler/handler.go` 的前半段、`service-a/config.yaml`。
- **方法论阶段**：第 1 个 SDD 增量的主体——写 design 决定"每个依赖一个 internal package，构造函数返回 *Client，handler 注入"。
- **决策点**：
  - **GORM 用 `otelgorm` 插件、Redis 用 `redisotel.InstrumentTracing`、RabbitMQ 手动创建 producer span 并 inject context**。Why：前两个有官方 instrumentation 直接 hook 进框架钩子；amqp091 没有官方 otel 包，必须手动 `StartSpan` + `propagation.TextMapPropagator.Inject` 到 message headers。这一段最容易写错——consumer 侧 extract 不到 traceparent 就断链。**反问听众**：如果你只接 otelgin、不手动给 RabbitMQ 注入 context，consumer 那一侧的 span 会发生什么？
  - **错误路径单独造一个 `BrokenOperation`** 而不是复用正常路径加 fault injection。Why：保持 happy/error 路径在 trace 里形状对称，方便对比；同时为 Step 3 的尾部采样准备"必然产 5xx 的弹药"。
- **讲解词预估**：约 1100 字

### Step 2 · 跨服务传播——`service-b` 与 W3C TraceContext
- **交付什么（可演示）**：复制 `service-a` 结构生成 `service-b`，用 `otelhttp.NewTransport` 包装 HTTP client，新增 `/call/ok`、`/call/error`、`/call/slow`、`/full/{ok,error}`。**演示**：curl `service-a:18080/call/ok?depth=2`，在 Jaeger 看到一条 trace 横跨 A→B→A→B 四段。
- **涉及模块/文件**：`service-b/` 全套（与 a 镜像）、`service-a/internal/client/peer.go`、`service-{a,b}/internal/handler/handler.go` 的 Call/Full 部分。
- **方法论阶段**：第 1 个 SDD 增量收尾。
- **决策点**：
  - **propagator 用 `NewCompositeTextMapPropagator(TraceContext{}, Baggage{})`**。Why：W3C TraceContext 是事实标准，所有现代 SDK 默认支持；加 Baggage 是为了将来传业务标签（如 tenant_id）。**反问**：如果一边用 B3、一边用 W3C，会发生什么？（答：跨服务断链，每次 hop 都开新 trace。）
  - **两个服务共用一份 internal 包结构而不抽公共 lib**。Why：教学场景下"对称的重复"比"巧妙的抽象"更易讲——你看 a 和 b 几乎一样，只是 config 不同。真生产里才考虑抽 `pkg/otelboot`。
- **讲解词预估**：约 900 字

### Step 3 · 尾部采样——把 demo 拉到生产形状（**全片高潮**）
- **交付什么（可演示）**：升级 `otel/collector.yaml`，把 `tail_sampling` processor 插到 `batch` 前面，配 5 条策略：`http.status_code 5xx`（兼容旧 semconv）、`http.response.status_code 5xx`（新 semconv）、`status_code=ERROR`、`latency>3000ms`、其余 `probabilistic 1%`。**演示**：跑一段循环 `curl /ok`（产 100 条正常 trace）+ 1 条 `curl /error`，在 Jaeger 里搜：错误那条一定在，正常那 100 条只会看到 ~1 条。
- **涉及模块/文件**：仅 `otel/collector.yaml`（约 30 行 yaml 改动撬动整个 demo 的语义）。
- **方法论阶段**：可独立成一个小 SDD 增量，proposal 一句话写完。
- **决策点**：
  - **新旧 semconv 都要写一条策略**。Why：OTel HTTP 语义约定从 `http.status_code` 演进到 `http.response.status_code`，不同 instrumentation 版本输出的 key 不一样，少写一条就漏 5xx。**反问**：怎么发现自己漏了？答案是"线上 5xx 告警的 trace 在 Jaeger 找不到"。这是真坑。
  - **processor 顺序必须 tail_sampling 在 batch 前**。Why：batch 会打散 trace 的 span 边界，破坏尾部采样需要的"等 trace 完整"语义。配错了的现象是采样率正常、但保留的 trace 残缺。
  - **`decision_wait=10s`、`num_traces=50000`**：在演示和生产之间折衷。生产里这两个数要根据 P99 trace duration 和 QPS 调，否则要么内存爆要么晚到 span 被丢。
- **讲解词预估**：约 1300 字

### Step 4 · 网关接入——Traefik 当入口 + access log 索引 TraceId
- **交付什么（可演示）**：加 Traefik v3，把所有服务藏到 `localhost:18086/service-{a,b,c,d}/*` 后面，配 JSON access log（含 `OtelTraceID`）。**演示**：curl 一次 `localhost:18086/service-a/error`，去 `logs/traefik/access.log` 用 `jq` 抓出 TraceId，再用这个 TraceId 直接去 Jaeger 搜。
- **涉及模块/文件**：`traefik/traefik.yaml`、`traefik/dynamic.yaml`、`docker-compose.yml`（把端口收敛、加 `depends_on`）、`logs/traefik/`。
- **方法论阶段**：`docs/plans/2026-05-15-add-traefik-*` 三件套。
- **决策点**：
  - **网关也开 OTel 并把自己也作为 trace 起点**。Why：很多团队把网关当黑盒，故障时才发现"用户到网关那段没有 trace"。Traefik v3 内置 OTel，开一行 `tracing` 配置就行；不开就只能从应用 span 反推入口。
  - **StripPrefix 中间件让应用代码不感知前缀**。Why：保持 service-a 内部继续看到 `/ok` 而不是 `/service-a/ok`，避免路由表污染。**反问**：如果不 strip，会怎样？（答：要么应用路由全改，要么 404 调一晚上。）
- **讲解词预估**：约 800 字

### Step 5 · 多跳链路——`service-c`/`service-d` + fanout/degrade 场景
- **交付什么（可演示）**：新增 `service-c`（只接 Redis）、`service-d`（只接 MySQL）；在 `service-a` 加 6 个 `/chain/*` 入口，由 `service-b` 编排扇出到 c/d。**演示**：curl `/chain/fanout/error`，在 Jaeger 里看到一条树状 trace，A→B→{C,D}，其中某一支标红、另一支正常。再 curl `/chain/degrade/ok`，trace 里有 ERROR span 但顶层是 200——典型的"部分降级"trace 形状。
- **涉及模块/文件**：`service-c/*`、`service-d/*`、`service-{a,b}/internal/handler/handler.go` 的 `Chain*`、`service-b/internal/client/`。
- **方法论阶段**：`docs/plans/2026-05-18-add-chain-services-*`。
- **决策点**：
  - **c 只承担 Redis、d 只承担 MySQL，职责单一**。Why：演示时观众一眼能看出"哪种 trace 形状对应什么依赖故障"，混在一起就丢了对比性。
  - **降级场景顶层返回 200 但子 span 标红**。Why：模拟"业务可用 + 局部失败"的真实生产场景，并且**故意制造一个"5xx 策略接不住、需要 ERROR status 策略才能保留"的 trace**——这一条把 Step 3 的 `status_code=ERROR` 策略的存在意义讲透了。
- **讲解词预估**：约 1000 字

### Step 6 · 闭环——Demo UI + Python trace 清洗脚本
- **交付什么（可演示）**：静态 HTML + nginx 暴露 `/ui`，按钮触发各场景；响应里展示 HTTP 状态码、耗时、TraceId、自动生成 Jaeger 跳转链接。再加 `scripts/analyze-jaeger-trace.py`：从 Traefik access log 找最近一条错误/慢请求，拉 Jaeger API，清洗成 LLM-friendly 的简化 JSON（去掉无用 tag、保留 `http.*`/`db.*`/`redis.*` 等关键字段）。**演示**：在 UI 点 `/chain/mysql/error`，拿到 TraceId，跑 `python3 scripts/analyze-jaeger-trace.py --format markdown`，看到一段人类可读的故障摘要。
- **涉及模块/文件**：`web/index.html`、`web/nginx.conf`、`scripts/analyze-jaeger-trace.py`、`scripts/analyze_jaeger_trace_test.py`、`scripts/trace-demo.sh`。
- **方法论阶段**：`docs/plans/2026-05-18-add-demo-ui-*` + 工具脚本演进。
- **决策点**：
  - **TraceId 同时写 response header 和错误体 JSON**。Why：header 适合机器抓（Traefik access log、前端 fetch），JSON 体适合人类（错误页直接展示）。两条路都给，故障定位时永远能拿到。
  - **Python 脚本零第三方依赖（只用 urllib + argparse）**。Why：让脚本能在任何 SRE 跳板机直接 `python3` 跑，不要求 `pip install`——这是 SRE 工具的基本素养。**反问**：为什么不用 `requests` 写得更顺手？（答：装包就是部署成本，跳板机往往没网。）
  - **脚本输出 markdown 模式**：为下一步喂给 LLM 做根因分析铺路（项目里隐含的下一站）。
- **讲解词预估**：约 1100 字

## 四、逐页大纲（交接 /frontend-slides 用）

> 此段为"内容已就绪"包，确认后直接交给 /frontend-slides，用途选 Teaching-Tutorial，篇幅 Medium（约 22 页），内容就绪度 All content ready。标题页备注务必保留"教学重构路径，非真实开发时序"。

- **P1 标题页**：`从零 vibe 出一套 Go OpenTelemetry 排障演练台` / 副标题：`7 步，把 SDK→Collector→Jaeger 的链路讲成一个生产故事` / 备注："教学重构路径，非真实开发时序"
- **P2 项目是什么**：一句话定位 + 核心机制（尾部采样 + TraceId 闭环）+ 技术栈卡片
- **P3 路线图**：7 步全景一页看完（Step 0 原型 → Step 6 闭环），每步一行标题 + 一行交付物
- **P4 Step 0 — Phase 0 原型**：要点 / 决策点（OTLP gRPC、SDK 端 AlwaysSample 的伏笔）
- **P5 Step 0 演示页 🎬**：curl /ok → Jaeger UI 看到 span 的截图/动图
- **P6 Step 1 — 依赖埋点**：MySQL/Redis/RabbitMQ 三种 instrumentation 的对比表
- **P7 Step 1 代码页**：handler + 三个 internal package 的骨架 + RabbitMQ 手动 inject context 的关键 5 行
- **P8 Step 2 — 跨服务传播**：W3C TraceContext 一图说清楚 + propagator 配置
- **P9 Step 2 代码页**：`otelhttp.NewTransport` 包 client + `otelgin.Middleware` 接 server 的对称结构
- **P10 Step 3 — 尾部采样（高潮页 1）**：为什么"采样在 Collector，不在 SDK"
- **P11 Step 3 代码页**：5 条 policy 的 yaml + processor 顺序图
- **P12 Step 3 演示页 🎬**：100 条 /ok + 1 条 /error，Jaeger 搜索结果对比
- **P13 Step 4 — Traefik 网关**：网关接入前/后的 trace 形状对比
- **P14 Step 4 代码页**：traefik.yaml 的 tracing 块 + dynamic.yaml 的 StripPrefix
- **P15 Step 5 — 多跳链路**：A→B→{C,D} 拓扑图 + 6 个 /chain/* 场景的"trace 形状家族"
- **P16 Step 5 代码页**：service-c/service-d 的"最小下游服务"骨架
- **P17 Step 5 演示页 🎬**：/chain/degrade/ok 在 Jaeger 里"200 顶层 + ERROR 子 span"的截图
- **P18 Step 6 — 闭环**：从用户点按钮到 SRE 拿 TraceId 跑分析脚本的完整链路图
- **P19 Step 6 代码页**：response header + JSON body 双写 TraceId 的 5 行 + Python 脚本的 tag 白名单
- **P20 全貌回顾**：7 步串成一条线——"每一步都让 trace 多长出一点点"
- **P21 你可以怎么用**：复用到自家 Go 项目的 3 个抓手（OTel 接入清单 / Collector 策略模板 / TraceId 回吐套路）
- **P22 收尾**：钩子句——`下一站：让 LLM 自动读这份清洗后的 trace 给出根因结论`

代码页/演示页统计：3 处现场演示（P5、P12、P17），6 个代码页（P7/P9/P11/P14/P16/P19），其余为讲解页。这个比例适合 22–30 分钟的密度。

## 五、估时假设与调节

- **假设**：
  - 无 Q&A，问答另算。
  - 仅 P5/P12/P17 三处现场演示，其余截图/动图。
  - 中文技术演讲节奏 240 字/分钟，平均每页 30s 切换冗余，演示页额外加 60s。
  - 总字数估算约 6900 字 → ~29 分钟纯讲解；加 22×0.5 = 11 分钟切换 + 3×1 = 3 分钟演示 → 不太对，重算：6900/240 ≈ 28.8 但其中已含演示页的解说，所以保守 22–30 分钟区间合理。
- **想更短（压到 15 分钟）**：砍 Step 4（Traefik）整步、合并 Step 1+2 为"单服务依赖 + 复制成两服务"，砍掉 P21 收尾延展；保留 Step 3 和 Step 6 这两个"高潮和闭环"页不能动。
- **想更长（拉到 45 分钟）**：在 Step 1 加一个 RabbitMQ consumer 侧的真实 demo（要起一个 worker，最贵但最有戏）；在 Step 3 加现场改 policy + 重启 Collector 看 Jaeger 立刻变化的演示（约 5 分钟）；P22 后接一段 LLM 根因分析的现场跑 demo（约 5 分钟）。

## 六、视觉风格规范（交接 /frontend-slides 用）

**总基调**：可观测性控制台风。让幻灯片本身看起来就像 Jaeger / Grafana 截下来的一帧——观众视觉上立刻进入"看监控"的状态，和内容主题同频。

- **色板**（直接抄进 CSS variables）
  - `--bg: #0d1117`（GitHub Dark / Grafana Panel 同色系深底）
  - `--bg-elevated: #161b22`（卡片、代码块底）
  - `--border: #30363d`
  - `--text: #e6edf3`
  - `--text-muted: #8b949e`
  - `--accent: #58a6ff`（链接、关键词、step 编号 —— 像 Jaeger 的 span 蓝）
  - `--accent-2: #a371f7`（次强调，用于"决策点"标签）
  - `--warn: #f78166`（慢请求、降级，对应 tail sampling 里的 latency policy）
  - `--err: #ff7b72`（5xx、ERROR span、红色高亮）
  - `--ok: #3fb950`（成功 trace、通过的检查）

- **字体**
  - 正文中文：`"PingFang SC", "Noto Sans SC", system-ui`
  - 等宽（代码、TraceId、路由、span 名）：`"JetBrains Mono", "Fira Code", ui-monospace`
  - 标题略加粗（600–700），不要 100/200 极细——深色背景上看不清。

- **页面骨架**（每页通用元素）
  - 顶栏左侧三个圆点（红/黄/绿，像 macOS 窗口控件），中部小字 `Step N / 7 · <步骤名>`，右侧时间戳样式的 `vibe-replay · go-otel-demo`。
  - 主区一行细 accent 色横线分隔标题与内容（模仿 Grafana panel header）。
  - 底部一条 1px `--border` 横线 + 右下角页码 `· P05 ·`。

- **关键元素表现力**
  - **代码块**：左侧 4px `--accent` 竖条 + 右上角小标签（如 `yaml` / `go` / `bash`），背景 `--bg-elevated`，行号 `--text-muted`。
  - **trace 拓扑图**：用 SVG，节点为圆角矩形带 1px 描边，链路用 `--accent` 实线，错误支用 `--err` 虚线，慢支用 `--warn`。鼓励直接用方框 + 箭头，不要塞图标，保持"工程图"质感。
  - **采样可视化**（P12）：100 个小方格，1 个 `--accent` 高亮（normal kept）+ 1 个 `--err`（error kept），其余 `--border` 灰底——一眼看出 "1% vs 100%"。
  - **决策点标签**：行首一个 `▸ 决策` 紫色 chip（`--accent-2` 背景 + 白字），紧跟问题——让"决策点"在密集页面里一眼可识别。
  - **演示页（P5/P12/P17）**：左上角红色 `● REC` 标签 + "现场演示"字样，提醒讲者切到 demo 环境。

- **动效**（克制原则）
  - 进出页用 8px 上滑 + 透明度淡入，120ms，缓动 `ease-out`。不要左右翻飞。
  - trace 拓扑图首次出现时按"A → B → C/D"顺序逐节点点亮，每节点 200ms。
  - 采样方格用 stagger 出现（每个 8ms），制造"trace 流过 Collector"的感觉。
  - **禁止**：parallax、3D 翻转、视差滚动、装饰性粒子。这是技术演讲不是产品发布。

- **不要做的事**（避免常见 AI 美学翻车）
  - 不用 emoji 当装饰图标（与控制台风冲突）；演示页的 🎬 在标题里用一次即可，正文不撒。
  - 不用渐变背景，深色纯色更专业。
  - 不放抽象插画/3D 几何体。所有视觉元素都要"有信息含义"（span、policy、metric）。

---

## 一点观察（不属于方案，仅供你参考）

`docs/plans/` 里有 `2026-05-16-add-grafana-observability-*` 三件套，但 `docker-compose.yml` 里并没有 Grafana 服务，也没有 metrics 相关代码。这意味着那一档要么被弃了、要么是另一条没合进来的分支。要不要把它纳入教学拆解里？我倾向**不纳入**——纳入会让"7 步纯 trace 故事"被打断，metrics 是另一个独立话题，单独做一节课更合适。等你确认。
