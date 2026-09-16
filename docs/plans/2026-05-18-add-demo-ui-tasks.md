# Tasks: Add Demo UI

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Design:** @docs/plans/2026-05-18-add-demo-ui-design.md

**Goal:** 增加一个 Ant Design 风格的静态 HTML 操作台，通过 Traefik 同源触发 demo 请求，并在错误响应中展示 TraceId 和 Jaeger 链接。

---

## 1. Go 服务 TraceId 输出

- [x] 1.1 在 `service-a/internal/handler/handler.go` 中补充 TraceId 提取辅助逻辑：从 `trace.SpanFromContext(ctx).SpanContext()` 获取 trace id → 校验有效 → 返回字符串；无有效 trace id 时返回空字符串。

- [x] 1.2 在 `service-a/internal/handler/handler.go` 的错误响应逻辑中增加 `trace_id` 字段：调用 TraceId 辅助方法 → 写入 JSON → 保持原有 `service`、`error`、HTTP 状态码不变。

- [x] 1.3 在 `service-a/internal/handler/handler.go` 中确保成功和失败响应尽量设置 `X-Trace-Id` header：从当前 request context 获取 trace id → trace id 非空时设置 header → 不改变响应 body 语义。

- [x] 1.4 对 `service-b/internal/handler/handler.go` 执行与 service-a 相同的 TraceId 输出改造：保持两个服务行为一致 → 不引入共享包 → 不改变现有接口状态码。

## 2. 静态 UI 文件

- [x] 2.1 创建 `web/index.html`：使用单文件 HTML/CSS/JS → 不引入 React、Vite、npm、外部 CDN → 使用 Ant Design 风格的布局、按钮、卡片、Tag、Alert 和结果面板。

- [x] 2.2 在 `web/index.html` 中定义请求场景配置：基础请求、依赖请求、组合请求三组 → 每个按钮绑定一个 `/service-a/...` 路径 → 预留复杂链路分组但不启用不存在的 endpoint。

- [x] 2.3 在 `web/index.html` 中实现请求执行逻辑：点击按钮 → 记录开始时间 → `fetch(path)` → 读取 HTTP 状态、`X-Trace-Id` header、响应文本/JSON → 计算耗时 → 渲染结果。

- [x] 2.4 在 `web/index.html` 中实现错误 TraceId 展示：优先读取响应 JSON 的 `trace_id` → 其次读取 `X-Trace-Id` header → 有 trace id 时展示复制文本和 Jaeger 链接 → 无 trace id 时展示明确提示。

- [x] 2.5 在 `web/index.html` 中加入 tail sampling 提示：错误和慢请求通常可在 Jaeger 查到 → 普通成功请求可能因 1% 采样被丢弃 → 文案保持简短，不做大段教程。

- [x] 2.6 创建 `web/nginx.conf`：监听 80 → 静态根目录 `/usr/share/nginx/html` → `/` 回落到 `index.html` → 支持 Traefik strip prefix 后访问。

## 3. Docker Compose 与 Traefik

- [x] 3.1 修改 `docker-compose.yml` 增加 `demo-ui` 服务：使用 `nginx:alpine` → 挂载 `./web/index.html` 和 `./web/nginx.conf` → 不依赖 MySQL、Redis、RabbitMQ → 可依赖 traefik 或由 traefik 依赖它，避免路由启动时找不到服务。

- [x] 3.2 修改 `docker-compose.yml` 中 `traefik.depends_on`：加入 `demo-ui` → 保持 service-a/service-b/otel-collector 现有依赖不变。

- [x] 3.3 修改 `traefik/dynamic.yaml` 增加 `/ui` router：匹配 `PathPrefix('/ui')` → 使用 strip prefix middleware → 转发到 `demo-ui:80`。

- [x] 3.4 修改 `traefik/dynamic.yaml` 增加 `demo-ui` service 和 middleware：middleware 只去掉 `/ui` 前缀 → 不影响 `/service-a`、`/service-b` 路由。

## 4. README

- [x] 4.1 修改 `README.md` 增加 UI 访问方式：写明 `http://localhost:10080/ui` → 说明按钮会通过 Traefik 调用 service-a。

- [x] 4.2 修改 `README.md` 增加 TraceId 使用说明：错误响应 JSON 包含 `trace_id` → 页面会生成 Jaeger 链接 → 普通成功请求可能被 tail sampling 丢弃。

- [x] 4.3 修改 `README.md` 增加验证命令：`docker compose up --build` → 访问 UI → 点击 `/error` 或 `/mysql/error` → 使用页面中的 Jaeger 链接查看 trace。

## 5. 验证

- [x] 5.1 执行 `go test ./...` 分别验证 `service-a` 和 `service-b`。

- [x] 5.2 执行 `docker compose config` 验证 compose 和 Traefik 配置引用无明显错误。

- [x] 5.3 执行 `docker compose build service-a service-b` 验证 Go 服务镜像仍可构建。

- [x] 5.4 启动 stack 后访问 `http://localhost:10080/ui`：页面正常加载 → 点击错误请求 → 页面展示 HTTP 500、响应 JSON、TraceId、Jaeger 链接。

- [x] 5.5 点击慢请求场景：页面展示约 5 秒耗时 → 等待 Collector tail sampling decision wait 后 → Jaeger 链接可查看 trace。

## Decision→Task 映射检查

- Decision 1 静态 HTML 而不是 React 应用 → Task 2.1, 2.3 ✓
- Decision 2 Ant Design 风格但不引入 runtime → Task 2.1 ✓
- Decision 3 UI 通过 Traefik `/ui` 同源暴露 → Task 3.1, 3.2, 3.3, 3.4 ✓
- Decision 4 Go 服务统一暴露 `X-Trace-Id`，错误 JSON 增加 `trace_id` → Task 1.1, 1.2, 1.3, 1.4 ✓
- Decision 5 第一阶段只覆盖现有接口，复杂链路按钮预留 → Task 2.2 ✓
- Risk 2 header 写入时机 → Task 1.3 已写入“从当前 request context 获取 trace id”约束 ✓
- Risk 3 `/ui` 路径重写 → Task 2.6, 3.3, 3.4 ✓
- Risk 4 tail sampling 影响普通成功请求 → Task 2.5, 4.2 ✓
