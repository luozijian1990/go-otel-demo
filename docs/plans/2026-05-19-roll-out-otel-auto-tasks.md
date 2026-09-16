# Tasks: Roll Out otel-auto Instrumentation to All Services

> **Context**: 2026-05-18 experiment (`docs/plans/2026-05-18-add-otel-auto-instrumentation-*.md` + `docs/observations/2026-05-18-otel-auto-findings.md`) proved that `loongsuite-go-agent` can replace manual instrumentation end-to-end on a single service (`service-a-auto`). This plan commits the `otel-auto` branch to that path: **all 4 application services migrate in place; `service-a-auto/` is removed as redundant**.
>
> **Branch model**: `main` stays as the manual instrumentation reference; `otel-auto` becomes the all-auto rollout. `git diff main..otel-auto` is the comparison artifact.
>
> **Mode**: B for all 4 services (no `observability.Init`, no `internal/observability/` package; `OTEL_*` env vars in docker-compose do the SDK bootstrap). Consistent with the experiment's proven path.
>
> **Out of scope**: PPT (`go-otel-demo-vibe-replay.html`) does not change — its Step 1/2 code references still match `main`'s manual code, which is the intended teaching content. README does not change in this plan (separate concern if at all).

**Goal**: After this plan, every Go service on `otel-auto` builds with `otel go build`, contains zero application-side OTel埋点 code (only L3 business consumption survives — `TraceIDHeaderMiddleware`, handler-side `RecordError`, `SpanFromContext`), and produces equivalent or better traces in Jaeger versus `main`.

---

## 1. Strip service-a in place

> 与 section 2 + 2.5 of the original experiment plan结构相同，照搬应用到 service-a。

- [x] 1.1 `cmd/main.go`:
  - 删 `otelgin` import + middleware 调用。
  - 删 `observability` import + `observability.Init(...)` 块 + `defer shutdownOTel(...)` + `shutdownOTel` helper。
  - **保留** `handler.TraceIDHeaderMiddleware()`。
  - **保留** `rootCtx`（`database.New(rootCtx, ...)` 还在用）。

- [x] 1.2 `internal/database/database.go`: 删 `gorm.io/plugin/opentelemetry/tracing` import + `db.Use(tracing.NewPlugin())` 块。

- [x] 1.3 `internal/redis/redis.go`: 删 `redisotel` import + 两处 `redisotel.InstrumentTracing(...)` 调用。

- [x] 1.4 `internal/rabbitmq/rabbitmq.go`: 删手动 `tracer.Start` / `SetAttributes` / `RecordError` / `propagator.Inject` / traceparent 写 headers / `recordSpanError` helper / 相关 imports。**保留** `ch.PublishWithContext(ctx, ...)` 并把 `Publishing.Headers` 留空（loongsuite 接管）。**保留** `New(cfg, serviceName)` 签名以免改 cmd/main.go。

- [x] 1.5 `internal/client/client.go`: 删 `otelhttp` import + `Transport: otelhttp.NewTransport(...)`。**不要** 删 `case "service-a"` 路由分支。

- [x] 1.6 删除 `service-a/internal/observability/` 整个目录。

- [x] 1.7 `cd service-a && go mod tidy`。

- [x] 1.8 `go build ./cmd` 验证。

---

## 2. Strip service-b in place

> service-b 是 service-a 的镜像，多 6 个 chain 编排端点（peer 调用 service-c/d）；strip 点位完全对称。

- [x] 2.1 `cmd/main.go`: 同 1.1（删 otelgin / observability 块）。
- [x] 2.2 `internal/database/database.go`: 同 1.2。
- [x] 2.3 `internal/redis/redis.go`: 同 1.3。
- [x] 2.4 `internal/rabbitmq/rabbitmq.go`: 同 1.4。
- [x] 2.5 `internal/client/client.go`: 同 1.5（注意：service-b 的 client 既调 service-a 也调 service-c/d，分支结构不一样但 otelhttp 剥离方式相同）。
- [x] 2.6 删除 `service-b/internal/observability/`。
- [x] 2.7 `cd service-b && go mod tidy`。
- [x] 2.8 `go build ./cmd` 验证。

---

## 3. Strip service-c in place

> service-c 比 a/b 简单（只有 Redis），但有**一处隐藏的手写 span** 要点明。

- [x] 3.1 `cmd/main.go`: 删 `otelgin` import + middleware 调用；删 `observability.Init` 块 + `shutdownOTel` helper（service-c 是不是有 rootCtx 给别处用要 grep 确认）。

- [x] 3.2 `internal/redis/redis.go`:
  - 删 `redisotel` import + 两处 `redisotel.InstrumentTracing(...)`。
  - **决策**：`tracer.Start(ctx, "redis synthetic slow operation")` 这一处手写 span 要不要保留？
    - **保留**（推荐）：这不是 instrumentation，是**业务侧合成 span**（人为给 `/redis/slow` 加个可观察的 5s 标记）。归属 **L3 业务消费**，与 `TraceIDHeaderMiddleware` 同类，按 design Decision 3 应保留。
    - **删除**：strip 得更彻底，但 `/chain/slow/redis` 在 Jaeger 里看到的 trace 会少一条 5s 的内部 span，影响 demo 演示效果。
    - **本计划默认保留**——但要同时**保留对应的 `otel`/`trace`/`codes` imports**，并在 commit message 里点明这是 L3 业务 span，不是 L1 库埋点。

- [x] 3.3 删除 `service-c/internal/observability/`。

- [x] 3.4 `cd service-c && go mod tidy`。**关键**：tidy 完后确认 `go.opentelemetry.io/otel`、`otel/trace`、`otel/codes` 仍在 direct deps（被 handler.go 和 redis.go 的 L3 业务 span 引用）。

- [x] 3.5 `go build ./cmd` 验证。

---

## 4. Strip service-d in place

> service-d 最简单——只有 MySQL，没有 redis / rabbitmq / peer client。

- [x] 4.1 `cmd/main.go`: 删 otelgin + observability 块（结构同 1.1，少几个 init）。
- [x] 4.2 `internal/database/database.go`: 同 1.2。
- [x] 4.3 删除 `service-d/internal/observability/`。
- [x] 4.4 `cd service-d && go mod tidy`。
- [x] 4.5 `go build ./cmd` 验证。

---

## 5. Dockerfile 改造（4 个服务一致改）

> 每个服务的 Dockerfile 都按 service-a-auto 已验证的样板改造。

- [x] 5.1 `service-a/Dockerfile`: 加 `ARG TARGETARCH` + curl 下 `otel-linux-${TARGETARCH}` + `otel version` 预检；`go build` → `otel go build`。

- [x] 5.2 `service-b/Dockerfile`: 同 5.1。

- [x] 5.3 `service-c/Dockerfile`: 同 5.1。

- [x] 5.4 `service-d/Dockerfile`: 同 5.1。

> 实际操作可以直接 `cp service-a-auto/Dockerfile service-a/Dockerfile`（或者用 sed 批改），4 个 Dockerfile 长得一样。

---

## 6. docker-compose.yml 调整

- [x] 6.1 给 `service-a` / `service-b` / `service-c` / `service-d` 4 个 block 各加 `environment:` 段，6 个 OTEL_* env vars（service_name 各自命名即可，其他 5 个完全相同）：
  ```yaml
  environment:
    - OTEL_SERVICE_NAME=service-{a,b,c,d}
    - OTEL_EXPORTER_OTLP_PROTOCOL=grpc
    - OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
    - OTEL_EXPORTER_OTLP_INSECURE=true
    - OTEL_TRACES_SAMPLER=parentbased_always_on
    - OTEL_PROPAGATORS=tracecontext,baggage
  ```

- [x] 6.2 **删除** `service-a-auto` 整个 block（service-a 现在自己就是 auto 版，副本无信息量）。释放端口 18087。

- [x] 6.3 `docker compose config` 解析无错。

---

## 7. 删除 service-a-auto/ 目录

- [x] 7.1 `git rm -r service-a-auto/`。
  - service-a-auto 完成了它的历史使命：作为隔离实验证明 mode B 可行。现在 service-a 本身已是 mode B，并行副本是冗余的（且会让人误以为 demo 提供 manual vs auto 两套样本，但其实 manual 在 main 分支）。
  - 实验阶段的 SDD 文档 (`2026-05-18-add-otel-auto-instrumentation-*.md`) 和 observations 文档**保留**——它们记录了"为什么这条路径安全"，是 rollout 的依据。

---

## 8. 端到端验证

- [x] 8.1 `docker compose up --build -d`（带 `--build` 强制重新走 `otel go build`）。

- [x] 8.2 4 个服务的 `/health` 都返回 200：
  - `curl localhost:18080/health` → `service-a`
  - `curl localhost:18081/health` → `service-b`
  - `curl localhost:18084/health` → `service-c`
  - `curl localhost:18085/health` → `service-d`

- [x] 8.3 4 个服务的 `/health` 响应都带非空 `X-Trace-Id`——证明每个服务的 L2 SDK bootstrap 都成功（与 5.3.b 同样的 floor check）。

- [x] 8.4 跑一条**跨 4 服务的多跳链路**作为完整验证：
  ```bash
  curl http://localhost:10080/service-a/chain/fanout/ok
  ```
  - 等 15s 过 tail sampling decision_wait。
  - 去 Jaeger 找 service-a 的 `/chain/fanout/ok` trace，确认看到 `service-a → service-b → {service-c, service-d}` 完整 4 服务 span 树。
  - 这一步是渐进迁移可行性的**最终证据**——只不过现在不是 manual↔auto 混跑，而是 auto↔auto 互通。

- [x] 8.5 跑一条带 RabbitMQ 的请求验证 producer span 自动注入：
  ```bash
  for i in $(seq 1 100); do curl -sS -o /dev/null http://localhost:18080/rabbitmq/ok; done
  sleep 15
  ```
  - 在 Jaeger 搜 service-a 的 `/rabbitmq/ok` trace（1% 采样下打 100 次约期望 1 条命中），确认 `kind=producer` span 存在。

- [x] 8.6 跑一条 `/error` 端点验证 tail sampling 5xx 策略仍然有效：
  ```bash
  curl http://localhost:18080/error
  ```
  - 这条 trace 必须**立刻**能在 Jaeger 搜到（10s decision_wait 后），证明 `keep-http-5xx-new-semconv` 策略对 auto 产的 span 正常工作。

---

## 9. 收尾

- [x] 9.1 写一条 commit message 收尾（或新增一份 `docs/observations/2026-05-19-otel-auto-rollout.md`）记录：
  - 4 个服务 strip 后**总共删了多少行**手动埋点（用 `git diff main..HEAD -- 'service-*'` 算）。
  - 镜像总大小变化（4 个服务镜像 sum）。
  - 8.4 那条多跳 trace 的 span 数量与 service 名列表（证据存档）。

- [x] 9.2 **不修改 vibe-replay PPT**——P21 extension page 现在的叙事在新状态下更有底气（整个分支就是它的活样本）；P1–P20 引用 main 上仍存在的 manual 代码，叙事不破。

- [x] 9.3 容器留运行 / `docker compose down` 全停，用户自决。

---

## 注意与回退

- **如果 service-c 的合成 slow span 决定删而不是保留**（task 3.2 的另一选项），`/redis/slow` 和 `/chain/slow/redis` 在 Jaeger 里不会看到那个 5s 的内部 span，但 trace 整体仍能被 tail sampling 的 latency 策略捞到（因为 server span duration > 3000ms）。**功能不会坏，只是视觉差**。决定权留给执行时。

- **如果某个服务 strip 后 build 失败**（理论上不会，因为 service-a-auto 已经全跑通），就把那个服务 revert 到 main 状态，进 observations 文档作为 finding，rollout 不再追求"100% 服务覆盖"——保住已迁好的服务，剩下的当 follow-up。

- **如果 8.4 的多跳 trace 在 Jaeger 里断裂**（即看不到 4 服务连续），就检查每个服务 docker-compose 的 OTEL_PROPAGATORS env var 是不是都设了 `tracecontext`。这是 W3C 互通的硬要求；漏写一个就断链。
