# Tasks: Add OTel Auto Instrumentation Experiment

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Design:** @docs/plans/2026-05-18-add-otel-auto-instrumentation-design.md
> **Branch:** `otel-auto`（已切到该分支后才执行本计划）

**Goal:** 在不动 `service-a` 主代码的前提下，新增 `service-a-auto` 副本，用 loongsuite-go-agent 编译期 AST 重写做埋点；启动后在 Jaeger 中能看到等价的 5 类 span，并产出对比观察文档。

---

## 1. 复制 service-a 为 service-a-auto

- [x] 1.1 用 `cp -R service-a service-a-auto` 复制整个目录到 `service-a-auto/`。

- [x] 1.2 删除复制过来的 `service-a-auto/go.sum`（go.mod tidy 后会重生成），避免哈希错位带来的二次错误。

- [x] 1.3 修改 `service-a-auto/go.mod` 首行模块名：`module go-otel-demo/service-a` → `module go-otel-demo/service-a-auto`。

- [x] 1.4 批量替换 `service-a-auto/` 内所有 `.go` 文件 import 路径：`go-otel-demo/service-a/` → `go-otel-demo/service-a-auto/`。验证：`grep -r "go-otel-demo/service-a/" service-a-auto/` 应为空。

- [x] 1.5 修改 `service-a-auto/config.yaml`：
  - `server.name: service-a` → `server.name: service-a-auto`
  - `otel.service_name: service-a` → `otel.service_name: service-a-auto`
  - 其他字段保持不变。

---

## 2. 剥离手动埋点

> 每条 task 删除前先用 `grep` 确认位置；删完后保留紧邻的导入清理，不删别的无关代码。

- [x] 2.1 修改 `service-a-auto/cmd/main.go`：
  - 删除 import `"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"`。
  - 删除 `router.Use(...)` 链中的 `otelgin.Middleware(cfg.OTel.ServiceName),`。
  - **保留** `handler.TraceIDHeaderMiddleware()`（业务层 trace_id 回吐，loongsuite 不会覆盖）。
  - **保留** `observability.Init(rootCtx, cfg.OTel)` 调用（SDK / OTLP exporter 设置，loongsuite 不替代）。

- [x] 2.2 修改 `service-a-auto/internal/database/database.go`：
  - 删除 import `"gorm.io/plugin/opentelemetry/tracing"`。
  - 删除 `if err := db.Use(tracing.NewPlugin()); err != nil { ... }` 整段（连同错误处理）。

- [x] 2.3 修改 `service-a-auto/internal/redis/redis.go`（先读文件确认实际 instrumentation 调用名）：
  - 删除 redisotel import（`github.com/redis/go-redis/extra/redisotel/v9`）。
  - 删除 `redisotel.InstrumentTracing(rdb)` 调用及其错误处理。

- [x] 2.4 修改 `service-a-auto/internal/rabbitmq/rabbitmq.go`（先读文件，标注每行删除原因）：
  - 删除手动 `tracer.Start(...)` / `span.End()` 相关代码。
  - 删除 `otel.GetTextMapPropagator().Inject(...)` 调用。
  - 删除把 traceparent 写进 `amqp.Table` headers 的代码。
  - **保留** `ch.PublishWithContext(ctx, ...)`：context 还是要传，loongsuite 才能从 ctx 拿到 span 上下文。
  - **关键观察点**：这一处删完后 RabbitMQ 是否还能自动产 producer span + 注入 traceparent 到 headers——是本实验的最大未知。

- [x] 2.5 修改 `service-a-auto/internal/client/client.go`（先读文件）：
  - 删除 `"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"` import。
  - 把 `Transport: otelhttp.NewTransport(http.DefaultTransport)` 改回 `Transport: http.DefaultTransport`（或直接不设 Transport，让其用默认）。

- [x] 2.6 运行 `cd service-a-auto && go mod tidy` 让 go.mod 自洽。**预期**：otelgin、otelhttp、redisotel、gorm tracing plugin 这几个 import 行被 tidy 移到 indirect，或干脆消失（取决于剩余代码是否还引用 OTel 任意子包）。**不要手工删 go.mod**，让 tidy 决定。

- [x] 2.7 验证：在 `service-a-auto/` 本地执行 `go build ./...`（**先不带 otel**），确认普通 build 也能过——证明代码语法/import 正确，再用 otel build 加注入。

---

## 2.5 删除 L2 SDK init（"激进零代码"路径，可回退）

> **目标**：把 `observability.Init` 也剥掉，验证 loongsuite + env vars 能否独立完成 SDK bootstrap。
> 这是设计 Decision 2 的"模式 B"。若在 section 5 验证时 trace 为空，按 task 2.5.5 回退到模式 A。

- [x] 2.5.1 修改 `service-a-auto/cmd/main.go`：
  - 删除 import `"go-otel-demo/service-a-auto/internal/observability"`。
  - 删除整段 `observability.Init(rootCtx, cfg.OTel)` 调用与其错误处理（约 5 行）。
  - 删除 `defer shutdownOTel(otelShutdown)` 行。
  - 删除文件底部的 `shutdownOTel(...)` 帮手函数（约 7 行）。
  - 检查 `rootCtx` 变量：若 `observability.Init` 是唯一使用方，一并删除其声明；否则保留。
  - **保留** `handler.TraceIDHeaderMiddleware()`（L3 业务消费，不在零代码范围）。

- [x] 2.5.2 删除整个 `service-a-auto/internal/observability/` 目录（包含 `observability.go`）。如果模式 B 跑通，这个包以后都不需要了；若回退，从 service-a 复制回来。

- [x] 2.5.3 运行 `cd service-a-auto && go mod tidy` 再次收敛 go.mod：预期会把 `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`、`go.opentelemetry.io/otel/sdk`、`google.golang.org/grpc`、`semconv` 等从直接依赖移到 indirect 或彻底消失（取决于 handler.go 还引用什么）。

- [x] 2.5.4 验证：`go build ./cmd` 仍能通过——证明应用代码不依赖 SDK init 也能编译。

- [ ] 2.5.5 **失败回退方案**（仅在 section 5 验证时 trace 空才执行）：
  - `git checkout HEAD~1 -- service-a-auto/internal/observability/`（或从 service-a 复制回来并改 module path）
  - 在 `cmd/main.go` 中恢复 `observability.Init` 调用与 `defer shutdownOTel`
  - `go mod tidy` + `docker compose build service-a-auto` 重新跑
  - 在 observations 文档中明确写："L2 在 loongsuite-go-agent vX.Y.Z 下需要手动 init，env-only bootstrap 不工作"

---

## 3. Dockerfile 改造

> **方法修正（执行前）**：核实 loongsuite 官方 README，`go install` 路径**没有被官方支持**，
> 文档列出的安装方式是 (1) 预编译二进制 (2) `install.sh` (3) `make install` 源码。
> 改用预编译二进制下载，原因：无脚本依赖、无 CDN 依赖、跨架构清晰。

- [x] 3.1 修改 `service-a-auto/Dockerfile`，在 `WORKDIR /src` 之前安装 `otel` CLI：
  ```dockerfile
  ARG TARGETARCH
  RUN set -eux \
      && curl -fsSL -o /usr/local/bin/otel \
          "https://github.com/alibaba/loongsuite-go-agent/releases/latest/download/otel-linux-${TARGETARCH}" \
      && chmod +x /usr/local/bin/otel \
      && otel version
  ```
  - `TARGETARCH` 由 Docker buildx 自动注入（amd64 / arm64），无需手写。
  - `otel version` 作为预检查：拉不到 / 不可执行 / 不是预期程序，build 立即 fail，不会拖到下一步才报错。

- [x] 3.2 修改 `service-a-auto/Dockerfile` 编译行：
  - `RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd`
  - → `RUN CGO_ENABLED=0 GOOS=linux otel go build -o /out/server ./cmd`

- [ ] 3.3 Fallback（仅在 3.1 失败时启用）：若 GitHub releases URL 在某些网络环境下不可达，把 3.1 的下载段替换为官方 install.sh：
  ```dockerfile
  RUN curl -fsSL https://cdn.jsdelivr.net/gh/alibaba/loongsuite-go-agent@main/install.sh \
      | bash && otel version
  ```
  在 observations 文档里记录最终使用的安装方式与版本号。

---

## 4. docker-compose 接入

- [x] 4.1 修改 `docker-compose.yml`，在 `service-d:` block 之后新增 `service-a-auto:` block：
  - `build: ./service-a-auto`
  - `command: ["-config", "/etc/go-otel-demo/config.yaml"]`
  - `volumes: ./service-a-auto/config.yaml:/etc/go-otel-demo/config.yaml:ro`
  - `ports: ["18087:8080"]`
  - `depends_on: [otel-collector]`
  - **不**加入 traefik 的 `depends_on` 链（Out-of-Scope Decisions）。
  - **新增 `environment:` 段**，给模式 B（env-based SDK bootstrap）所需的全部 OTEL_* env vars：
    ```yaml
    environment:
      - OTEL_SERVICE_NAME=service-a-auto
      - OTEL_EXPORTER_OTLP_PROTOCOL=grpc
      - OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
      - OTEL_EXPORTER_OTLP_INSECURE=true
      - OTEL_TRACES_SAMPLER=parentbased_always_on
      - OTEL_PROPAGATORS=tracecontext,baggage
    ```
    模式 A（保留 observability.Init）下这些 env vars 也无害——`observability.Init` 用 YAML 自己的配置，env vars 被忽略。所以**这段 env 配置在 A/B 两种模式下都正确**。

- [x] 4.2 验证：`docker compose config` 输出无报错；`docker compose ps` 在未启动状态显示 service-a-auto。

---

## 5. 启动与验证

> 这一节涉及网络请求和容器启动，必须在用户本机执行，结果由用户回填到 observations 文档。

- [x] 5.1 单独构建：`docker compose build service-a-auto`。预期：build 日志中能看到 `otel` CLI 的 AST 重写输出；构建时长记录在 observations 文档。

- [x] 5.2 启动：`docker compose up -d service-a-auto`。预期：`docker compose logs service-a-auto` 显示 `service-a-auto listening on :8080`，无 fatal。

- [x] 5.3 健康检查：`curl -s localhost:18087/health` 返回 `{"status":"ok","service":"service-a-auto"}`。

- [x] 5.3.b **L2 floor check（决定 A/B 模式）**：在跑 5.4 之前先 `curl localhost:18087/ok` 一次，等 30s（过 tail sampling decision_wait），去 Jaeger 看是否能搜到 service-a-auto。
  - **能搜到** → 模式 B 成功，零代码原则完整落地，继续 5.4。
  - **搜不到** → loongsuite 在当前版本下没自动 bootstrap SDK；执行 task 2.5.5 回退到模式 A，重新 build/up，再回来跑 5.4。把"L2 floor 存在"写入 observations。

- [x] 5.4 五类 span 验证，每条 curl 后立刻去 Jaeger UI 搜 service `service-a-auto`，截图或记录 span tree：
  - [x] 5.4.a `curl localhost:18087/ok` → HTTP server span
  - [x] 5.4.b `curl localhost:18087/mysql/ok` → server + GORM SQL span
  - [x] 5.4.c `curl localhost:18087/redis/ok` → server + Redis SET/GET span
  - [x] 5.4.d `curl localhost:18087/rabbitmq/ok` → server + **是否有 publish span**？（核心未知）
  - [x] 5.4.e `curl localhost:18087/call/ok?depth=1` → server + HTTP client span + service-a 的 server span 是否同一 trace？（核心未知）

- [x] 5.5 错误路径（验证 tail sampling 是否仍能捞到）：
  - [x] 5.5.a `curl localhost:18087/error` → trace 含 5xx，应被保留
  - [x] 5.5.b `curl localhost:18087/mysql/error` → trace 含 ERROR span，应被保留
  - [x] 5.5.c 检查 Jaeger 中两条 trace 都能查到（>30s 后查，过 decision_wait）。

- [x] 5.6 与 service-a 对比（核心交付）：分别 curl `localhost:18080/ok` 与 `localhost:18087/ok`，在 Jaeger 中并排打开两条 trace，逐项比对：
  - span 名字
  - span.kind
  - 关键 attributes：`http.method` / `http.route` / `http.status_code` / `http.response.status_code` / `url.path` / `server.address`
  - 时间精度
  - resource attributes 中的 `service.name`、`telemetry.sdk.*`

---

## 6. 产出观察文档

- [x] 6.1 创建 `docs/observations/2026-05-18-otel-auto-findings.md`，按下列骨架填写：
  - **环境**：Go 版本、loongsuite-go-agent 版本（取自 `otel version`）、各依赖版本
  - **构建对比**：service-a vs service-a-auto 的镜像体积、构建耗时
  - **代码量对比**：删了多少行手动埋点（用 `git diff --stat main..otel-auto -- service-a-auto`，并注明是"相对于 service-a 副本起点"）
  - **span 形状对比**：5 类 span × {手动, 自动} 的表格，标出差异
  - **semconv 版本观察**：`http.status_code`（旧）还是 `http.response.status_code`（新）还是都有？这直接影响 tail sampling 那两条 policy 中哪条命中
  - **RabbitMQ 验证结论**：loongsuite 是否自动给 amqp091 加 producer span 并写 traceparent
  - **跨服务传播验证结论**：service-a-auto → service-a 的 trace 是否连续
  - **`observability.Init` 能否省略**：留着的话 trace 上报正常；尝试注释掉后 trace 是否消失
  - **遇到的坑**：build 失败、版本不兼容、PATH 冲突等
  - **总结**：是否值得在 vibe-replay 加对照页 / 是否值得迁移主代码 / 还需要验证什么

- [x] 6.2 把 observations 文档 git add + commit 到 `otel-auto` 分支。

---

## 7. 收尾

- [ ] 7.1 实验结束后：用户决定保留 / 丢弃 `otel-auto` 分支。
  - **保留**：merge 到 main 或继续作长期对照分支。
  - **丢弃**：`git checkout main && git branch -D otel-auto`。
  - **后续动作**：根据 observations 决定是否更新 `go-otel-demo-vibe-replay.html` / 主代码迁移（这两条是单独的 follow-up，**不在本计划范围**）。

- [ ] 7.2 跑完前后停一下 `service-a-auto` 容器，避免长期占用端口：`docker compose stop service-a-auto`。
