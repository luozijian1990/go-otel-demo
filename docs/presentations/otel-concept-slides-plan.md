# OTel 概念扫盲幻灯片 · Plan

> 状态:**待评审**。本文件只定方案,确认后再用 `frontend-slides` 生成 HTML。

## 1. 目标与边界

- **目标**:把"OpenTelemetry 是什么"讲明白,并带同事**读懂一份真实的 Collector 配置**。
- **定位**:概念扫盲 + Collector 配置入门(偏运维友好)。比"纯扫盲"重,因为受众含运维,Collector 配置是他们真正要上手的部分。
- **受众**:公司内部同事,没接触过 OTel 的 Go 开发 / 运维。
- **形式**:交互式 HTML 幻灯片,视觉风格对齐已有的 `go-otel-demo-vibe-replay.html`。
- **在本场范围**:Collector 四组件、用仓库真实 `otel/collector.yaml` 走查 receiver→processor→exporter 管道、tail_sampling 采样策略、agent vs gateway 部署形态。
- **不在本场范围**(留给下一场 demo):现场点 demo-ui→翻 Jaeger 的 live 操作、trace 分析脚本、手动埋点、Collector 全量调参 / connector / extension。

## 2. 设计原则

1. **概念优先,但不悬空**:每个概念钉一张图或一个真实例子,纯文字讲"什么是 span"听完就忘。
2. **笔记是原料,不照搬**:`otel-learn-1.md` / `otel-learn-2.md` 提供概念解释和口径,我重组进"为什么→是什么→怎么用"的叙事。
3. **画面统一用 Go demo**:笔记是 Node + Zipkin + Console,demo 是 Go + Jaeger + Traefik。**概念用笔记的讲法,画面统一用本仓库的 Go demo**,避免两套技术栈混淆同事。唯一例外见 slide 4。
4. **Collector 用仓库真实配置当教材**:不抄 file2 的零散通用 yaml,改用本仓库 `otel/collector.yaml`(真在跑、注释全)走查 receiver→processor→exporter 怎么串成管道。深度到"读懂这份配置",全量调参 / connector / extension 留第二场。

## 3. 素材盘点(两份笔记 × demo 的覆盖情况)

| 概念 | 笔记里有没有 | demo 锚点 | 处理 |
|---|---|---|---|
| 遥测 / 三支柱(traces·metrics·logs) | ✅ file1 开头有定义 | — | 补"监控 vs 可观测性"对比 |
| 痛点开场(多服务定位难) | ❌ | ✅ 4 服务 + `/chain/*` 链路 | **我写** |
| Trace / Span = 一棵树 | ✅ file2"一次请求 6 个 span" | ✅ `/chain/mysql/error` Jaeger 树 | 锚定截图 |
| Span 内部结构 | ✅✅ **file1 的 console span JSON 最强素材** | demo 同款字段 | 直接用 |
| Context Propagation / `traceparent` | ⚠️ 只有多服务例子,没讲机制 | ✅ Traefik access log 里的 TraceId | **机制我补** |
| OTel 是什么(规范+SDK+工具,不是后端) | ✅ file1"标准化遥测 / 观测后端"段 | ✅ 对上 demo 架构 | 提炼成"啊哈页" |
| Exporter / OTLP | ✅ file1 OTLP 段 | ✅ Go 服务 `otel.endpoint` | 提炼成架构桥页 |
| Collector 四组件 + 部署形态 | ✅✅ file2 四组件 + agent/gateway | ✅ demo 的 otel-collector | 直接用 |
| **Collector 真实配置走查** | ⚠️ file2 是通用示例 | ✅✅ **仓库 `otel/collector.yaml`** | **用仓库 config,不用 file2** |
| **采样(tail_sampling 5 条策略)** | ⚠️ file2 只提 probabilistic | ✅✅ 仓库 config:5xx/error/slow/1% | **用仓库 config** |
| 自动埋点 + service.name | ✅ file1/file2 + `unknown_service` 故事 | ✅ demo 刚做完 auto 迁移 | 直接用 |

## 4. 幻灯片大纲(逐页)

> 共 **12 页**。三幕:概念(1–6)→ 数据流与 Collector 配置(7–10)→ 接入与收尾(11–12)。Collector 实战占 8/9/10 三页,架构桥 7 引入。

**第一幕 · 概念(1–6)**

1. **开场痛点** — "4 个服务,一个请求又慢又报错,你怎么查?" 传统:开 4 个终端 grep 日志拼时间戳。
   - 来源:我写,锚定 demo service-a→b→c/d 拓扑。
2. **遥测与可观测性** — 遥测 = 系统发出的行为数据(traces / metrics / logs);监控告诉你"出事了",可观测性让你追问"为什么"。本场聚焦 **Trace**。
   - 来源:file1 遥测定义 + 我补监控对比。
3. **Trace 与 Span** — 一次请求 = 一棵 span 树,每个 span 是一段工作。
   - 来源:file2 概念 + demo `/chain/mysql/error` Jaeger 树截图。
4. **Span 里装什么** — name / traceId / parentId / 起止时间 / duration / attributes / status / events。
   - 来源:**直接用 file1 的 console span JSON**(本场唯一保留的笔记原生画面,够通用)。标注"demo 里 Go span 字段一样"。
5. **凭什么 4 个服务能拼成一条链** — Context Propagation:traceId 顺 HTTP header(`traceparent`)往下游传。
   - 来源:**机制我补**,用 file2 多服务例子引子,锚定 Traefik access log 的 TraceId。
6. **🎯 OTel 到底是什么** — 核心扫盲页:OTel = 规范 + SDK + 工具集,厂商中立,标准化产出遥测;**它不是后端**,Jaeger / Zipkin / Prometheus 才是后端。
   - 来源:file1"标准化遥测 / 选择观测后端"段。全场"啊哈时刻"。

**第二幕 · 数据流与 Collector 配置(7–10)**

7. **数据怎么流出去(架构桥)** — SDK 用 Exporter 按 **OTLP** 协议把数据发出去;一张全景图:App(SDK)→ OTLP → Collector → 后端。
   - 来源:file1 OTLP/Exporter 段,锚定 demo 的 `otel.endpoint: otel-collector:4317`。
8. **Collector 是什么** — 厂商无关的"数据中转站";四组件 receiver / processor / exporter / connector(+ extension / service);两种部署:agent(sidecar,随应用)vs gateway(独立网关)。为什么要它:统一管理、与应用解耦。
   - 来源:file2 四组件 + agent/gateway,锚定 demo 的 otel-collector。
9. **走查真实配置:三段怎么串成管道** — 用仓库 `otel/collector.yaml`:`receivers.otlp`(grpc/http 入口)→ `exporters`(otlp/jaeger + debug)→ `service.pipelines.traces` 把 receiver/processor/exporter 串起来。讲清"配了不等于启用,要在 pipeline 里引用"。
   - 来源:**仓库 config 直接走查**(真实、注释全)。
10. **Processor 重点:采样** — `tail_sampling` 5 条策略:5xx / span ERROR / 慢请求(>3s)100% 保留,正常请求 1%;尾采样 = 收齐整条 trace 再决策;**为什么 tail_sampling 必须排在 batch 前**。
    - 来源:仓库 config 的 processors 段。采样概念在这页讲透。

**第三幕 · 接入与收尾(11–12)**

11. **怎么接入:自动埋点** — auto-instrumentation 近乎零代码改动就有 trace;配 `service.name` 区分服务(`unknown_service` → 命名的故事)。
    - 来源:file1/file2 自动埋点 + service.name 故事 + demo 刚做完的 auto 迁移。
12. **收尾 + 预告** — 一图流心智模型(请求 → span 树 → SDK → OTLP → Collector(采样)→ Jaeger);预告"下一场真跑 demo:live 翻 trace、根因定位、手动埋点"。
    - 来源:我写。

## 5. 待你拍板的决策点

1. ~~页数 8 还是 9~~ **已定:12 页,Collector 扩成 8/9/10 三页,采样并入本场。**
2. **配图:真截图 vs 示意图**:slide 3/4 用 demo 真实 Jaeger 截图最有说服力,但要先把 demo 跑起来造流量截图(我可帮你 `docker compose up` + `trace-demo.sh`)。不想折腾就先用干净示意图占位,真图后补。
3. **slide 4 的 span JSON**:打算保留笔记里那段 Node console 输出当"真实 span"。若要全程零 Node 痕迹,换成 demo Go span 等价字段(需跑一次抓取)。
4. **(新)slide 9/10 配置走查**:直接把仓库 `otel/collector.yaml` 的关键片段贴进幻灯片走查 —— 这是真配置、注释全。确认用它即可,无需额外造素材。

## 6. 视觉风格

- 复用 `go-otel-demo-vibe-replay.html` 的配色 / 字体 / 卡片排版,两份教学物料观感一致。
- 代码 / yaml 片段需语法高亮 + 行级强调(走查配置时高亮当前讲到的块)。
- 键盘左右翻页 + 进度指示,单文件 HTML,可直接发给同事本地打开。

## 7. 下一步

1. 你 review 本 plan,在第 5 节决策点 2/3 上拍板(决策 1/4 已定,可直接确认)。
2. 确认后我用 `frontend-slides` 生成 HTML;若选"真截图",生成前先帮你把 demo 跑起来抓图。
3. 产出 `otel-concept-slides.html`(命名可改),与 vibe-replay 并列放在 `docs/presentations/`。
