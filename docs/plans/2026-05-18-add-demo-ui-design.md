# Design: Add Demo UI

## Context

当前 demo 已经支持 service-a/service-b、Traefik、OpenTelemetry Collector、Jaeger、MySQL、Redis、RabbitMQ 等本地链路验证。用户目前主要通过 curl 或脚本触发请求，再从响应、日志或 Jaeger 中定位 TraceId。

下一步需要一个轻量 HTML 操作台：点击按钮模拟请求，错误时直接返回并展示 TraceId，便于复制给 `$jaeger-trace-rootcause` 或跳转到 Jaeger。

## Goals

- 提供一个 Ant Design 风格的单页 HTML 操作台。
- 页面通过 Traefik 同源调用 demo API，避免 CORS。
- 点击按钮后展示请求路径、HTTP 状态、耗时、响应 JSON。
- 错误响应 JSON 必须包含 `trace_id`。
- 所有响应尽量通过 `X-Trace-Id` header 暴露当前 trace id。
- 页面可以基于 TraceId 生成 Jaeger trace 链接。
- 保持现有 docker compose 一键启动体验。

## Non-Goals

- 不引入 React、Vite、npm 构建链。
- 不真正接入 Ant Design React 组件库。
- 不做 Grafana、Loki、Tempo、日志检索或 AI 自动分析。
- 不做登录、权限、用户系统。
- 不持久化请求历史。
- 不新增 MySQL/Redis/RabbitMQ 容器。
- 不在本次实现 service-c/service-d 复杂链路。
- 不改变现有接口的状态码和业务语义。

## Decisions

### Decision 1: 使用静态 HTML，而不是 React 应用

选择：新增 `web/index.html`，用原生 HTML/CSS/JavaScript 实现操作台。

理由：这个页面只承担 demo 请求触发和结果展示，不需要组件状态管理、路由、构建产物或前端依赖。静态 HTML 可以降低启动和维护成本，也符合“本地 demo 可直接运行”的目标。

拒绝方案：React + Ant Design。它能得到真实 Ant Design 组件，但会引入 npm install、构建、镜像缓存和依赖版本问题，对当前 demo 的核心目标收益不高。

### Decision 2: 采用 Ant Design 风格，不引入 Ant Design runtime

选择：用本地 CSS 模拟 Ant Design 的按钮、卡片、Tag、Alert、Result、Descriptions/Table 等视觉语言。

理由：Ant Design 本身是 React 组件库。纯 HTML 页面引入 antd runtime 不自然，使用 CDN 又会让 demo 依赖公网。用本地 CSS 保持视觉风格，同时保持离线可运行。

拒绝方案：从 CDN 加载 Ant Design CSS/JS。公网依赖会影响本地 demo 稳定性。

### Decision 3: UI 通过 Traefik `/ui` 同源暴露

选择：新增 `demo-ui` nginx 容器，并通过 Traefik 暴露为 `http://localhost:10080/ui`。

理由：页面和 API 都在 `localhost:10080` 下，前端可以直接 fetch `/service-a/...`，不需要为 Go 服务加 CORS。Traefik 仍然是入口，符合后续从网关拿 TraceId 的生产心智。

拒绝方案：把 UI 映射到 `localhost:18083` 后直接请求 `localhost:10080`。这会触发浏览器跨域，需要额外 CORS 配置，增加非核心复杂度。

### Decision 4: Go 服务统一暴露 `X-Trace-Id`，错误 JSON 增加 `trace_id`

选择：在 service-a/service-b 的 HTTP 层统一设置 `X-Trace-Id` header；错误返回体通过现有失败响应逻辑增加 `trace_id`。

理由：UI 需要稳定拿到 TraceId。header 对成功和失败都适用；错误 JSON 中显式返回 `trace_id` 更适合人工复制和 AI 分析。

拒绝方案：只从 Traefik access log 查 TraceId。这个方式不符合当前想要的点击式 demo，也不贴近生产中“日志平台已经给出 TraceId 后再查 Jaeger”的流程。

### Decision 5: 第一阶段 UI 只覆盖现有接口，复杂链路按钮预留

选择：先覆盖 `/ok`、`/error`、`/slow`、MySQL、Redis、RabbitMQ、`/call/*`、`/full/*`。页面结构预留“复杂链路”分组，但不启用不存在的 endpoint。

理由：先把“点击请求 -> 返回 TraceId -> 打开 Jaeger”闭环做稳。service-c/service-d 会引入新的服务、配置、Dockerfile 和链路编排，应该单独计划。

拒绝方案：本次同时实现复杂链路和 UI。范围过大，容易把 UI 验证和后端链路设计耦合在一起。

## Risks / Trade-offs

### Risk 1: 静态 HTML 的 Ant Design 风格不等于真实 Ant Design 组件

场景：用户期望看到完整 antd 行为，例如表单校验、Message、Modal 动画。

缓解：明确本次目标是“Ant Design 风格”，不是“Ant Design React 应用”。页面只做请求触发和结果展示，不做复杂交互。

### Risk 2: `X-Trace-Id` header 写入时机不正确

场景：中间件在 otelgin 创建 span 之前执行，导致 header 里拿不到有效 trace id。

缓解：TraceId header 逻辑必须放在 otelgin middleware 之后，或在 handler 失败响应/成功响应时从当前 request context 获取 trace id。

### Risk 3: 通过 `/ui` 暴露 nginx 时路径重写可能导致静态资源 404

场景：访问 `/ui`、`/ui/`、刷新页面时 nginx 路径不一致。

缓解：Traefik 配置 strip prefix；nginx 配置对 `/` 回落到 `index.html`。页面使用内联 CSS/JS，不依赖额外静态资源路径。

### Risk 4: 普通成功请求可能在 Jaeger 中查不到

场景：tail sampling 对普通成功请求只保留 1%，UI 给出 TraceId 后 Jaeger 未必能查到。

缓解：README 和 UI 文案说明：错误 span 和慢请求应 100% 保留；普通 `/ok` 可能因为 tail sampling 被丢弃。

