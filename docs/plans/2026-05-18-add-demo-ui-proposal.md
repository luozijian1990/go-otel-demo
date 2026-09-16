# Proposal: Add Demo UI

## Why

为 OpenTelemetry demo 增加一个可点击的本地操作台，让用户不用手写 curl，也能快速触发错误、慢请求和依赖调用，并直接拿到可用于 Jaeger 查询的 TraceId。

## What Changes

- Add `web/index.html`
  - 单文件静态页面，使用 Ant Design 风格布局和组件视觉。
  - 提供请求场景按钮、结果面板、耗时、状态码、响应 JSON、TraceId、Jaeger 链接。

- Add `web/nginx.conf`
  - 用 nginx 托管静态 HTML。
  - 支持 `/ui` 路径下刷新和访问。

- Modify `docker-compose.yml`
  - 增加 `demo-ui` 静态容器。
  - 通过 Traefik 暴露 UI，不要求前端直接跨域访问后端。

- Modify `traefik/dynamic.yaml`
  - 增加 `/ui` 路由到 `demo-ui`。
  - 保持 `/service-a`、`/service-b` 现有路由不变。

- Modify Go service handlers/middleware
  - 对响应统一写入 `X-Trace-Id` header。
  - 错误 JSON 返回体增加 `trace_id` 字段。
  - 不改变现有接口语义和状态码。

- Modify `README.md`
  - 增加 UI 访问地址和使用说明。
  - 说明错误响应里的 TraceId 可以直接用于 Jaeger 查询。

## Capabilities

- **Scenario Trigger UI**
  - 用户打开 `http://localhost:10080/ui` 后，可以点击按钮触发基础请求、错误请求、慢请求和依赖请求。

- **TraceId Visibility**
  - 错误请求返回 JSON 中包含 `trace_id`，页面直接展示该值。
  - 页面同时读取 `X-Trace-Id` header，作为成功请求和错误请求的统一 trace 标识来源。

- **Jaeger Jump**
  - 页面基于 TraceId 生成 `http://localhost:16686/trace/<trace_id>` 链接。

- **Same-Origin Demo Flow**
  - UI 和后端调用都经过 Traefik 的 `localhost:10080`，不引入 CORS 配置。

- **Future Chain Extension**
  - 页面结构预留“复杂链路”分组，后续新增 `service-c/service-d` 后只需要补按钮配置。

## Impact

- **Files**
  - 新增 `web/index.html`
  - 新增 `web/nginx.conf`
  - 修改 `docker-compose.yml`
  - 修改 `traefik/dynamic.yaml`
  - 修改 `service-a/internal/handler/handler.go`
  - 修改 `service-b/internal/handler/handler.go`
  - 修改 `README.md`

- **Database**
  - 无 schema 变更。

- **Cache**
  - 无 Redis key 结构变更。

- **External Dependencies**
  - 不新增 npm、React、Vite、Ant Design runtime。
  - UI 使用本地静态 CSS 模拟 Ant Design 风格，避免依赖公网 CDN。

- **Downstream Services**
  - UI 会调用已有 Traefik 路由 `/service-a/*`。
  - 不改变 service-a/service-b 之间的调用关系。

