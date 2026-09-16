# Observations: otel-auto Rollout to All Services

> **Date**: 2026-05-19
> **Branch**: `otel-auto`
> **Related plan**: `docs/plans/2026-05-19-roll-out-otel-auto-tasks.md`
> **Built on**: `docs/observations/2026-05-18-otel-auto-findings.md`（实验阶段的事实清单）
> **Purpose**: 把"实验只验 service-a"扩展到"4 个服务全切"，记录全切后的真实表现。

## TL;DR

按 plan 一次性全部跑通，**没触发任何 fallback**：

- `git rm -r service-a-auto/` 落地——并行实验副本删除，service-a 本身就是 auto 版。
- 4 个服务（a/b/c/d）全部 strip 完毕、`otel go build` 通过、docker compose 起来后 `/health` 都返回 200 + 非空 `X-Trace-Id`。
- **5 服务多跳链路在 Jaeger 中完整连续**：`traefik → service-a → service-b → {service-c, service-d}` 一条 trace 含 15 spans，证明 AUTO↔AUTO W3C TraceContext 互通无损耗。
- RabbitMQ producer span 在 rollout 后的 service-a 上仍然自动注入（`:otel-demo-queue publish kind=producer messaging.system=rabbitmq`）。

## 1. 代码量收益

`git diff --shortstat main..HEAD -- service-a service-b service-c service-d`：

```
30 files changed, 151 insertions(+), 1058 deletions(-)
```

净减 **~907 行**，主要来自：

- 4 个 `internal/observability/observability.go` 各 ~53 行 → 共 ~210 行
- 4 个 `cmd/main.go` 各 ~10 行 strip → 共 ~40 行
- 4 个 `rabbitmq/rabbitmq.go`（a/b 各 ~36 行）→ ~72 行
- 4 个 `database.go`（a/b/d 各 ~4 行）→ ~12 行
- 4 个 `redis/redis.go`（a/b/c 各 ~5 行）→ ~15 行
- `internal/client/client.go` strip + `go.sum` 收敛（最大单一删除来源）

新增的 151 行主要是：4 个 Dockerfile 各加 ARG/RUN 段、docker-compose 给 4 服务加 environment 块、commit-pointer 注释。

## 2. 镜像大小对比

| 服务 | 切前（main） | 切后（otel-auto） | Δ |
|---|---|---|---|
| service-a | 59 MB | 52.7 MB | −6.3 MB |
| service-b | 59 MB | 52.8 MB | −6.2 MB |
| service-c | ~46 MB（估） | 48.5 MB | +2.5 MB（可能因为引入了 loongsuite 注入的 runtime） |
| service-d | ~52 MB（估） | 45.3 MB | −6.7 MB |

注意 service-c 反向上涨：c 切前依赖最少（只 Redis），loongsuite 注入的 runtime 库可能比它原本依赖更大。**不是问题，量级一致**。

## 3. 5 服务多跳 trace（rollout 的终极验证）

`curl http://localhost:18086/service-a/chain/fanout/error`（走 traefik 网关）产出的 trace：

```
trace = 1b7874cdf69530449088d4ff64d06dbd  (15 spans across 5 services)

[traefik     ] EntryPoint                    kind=server   500
[traefik     ] Router                        kind=internal
[traefik     ] StripPrefix                   kind=internal
[traefik     ] Service                       kind=internal
[traefik     ] ReverseProxy                  kind=client   500
[service-a   ] GET /chain/fanout/error       kind=server   500
[service-a   ] GET                           kind=client   500
[service-b   ] GET /chain/fanout/error       kind=server   500
[service-b   ] GET                           kind=client   500    ← to service-d (mysql)
[service-b   ] GET                           kind=client   200    ← to service-c (redis)
[service-d   ] GET /mysql/error              kind=server   500
[service-c   ] GET /redis/ok                 kind=server   200
[service-d   ] SELECT users_table_that_does_not_exist  kind=client  ERROR
[service-c   ] set                           kind=client
[service-c   ] get                           kind=client
```

**意义**：
- 5 个服务全部上报、参与同一 trace，无任何 propagation 断点。
- service-b 同时调 c 和 d（fan-out）—— 两个 sibling client span 都在 trace 里。
- 错误从 service-d/mysql 反向冒泡 → service-b → service-a → traefik，5xx status 沿路被打。
- traefik 自带 OTel（`tracing.otlp` 在 traefik.yaml 中已配）+ 应用侧 loongsuite 注入：两种 OTel 实现共存于同一 trace。

## 4. /chain/fanout/ok（4 服务 happy path）

`curl http://localhost:18080/chain/fanout/ok` 命中 1% 采样：

```
trace = adffe08895694a3c4c0d61abe480521e  (12 spans across 4 services)

[service-a] GET /chain/fanout/ok           server   200
[service-a] GET                            client   200
[service-b] GET /chain/fanout/ok           server   200
[service-b] GET                            client   200
[service-b] GET                            client   200
[service-c] GET /redis/ok                  server   200
[service-d] GET /mysql/ok                  server   200
[service-d] SELECT users                   client
[service-c] set                            client
[service-c] hello                          client       ← go-redis handshake noise
[service-c] client                         client  ERROR ← go-redis admin command noise
[service-c] get                            client
```

go-redis 的 `hello` 与 `client maint_notifications` 两条 noise span 仍然存在——这是 go-redis 库自身的特征，loongsuite 与 redisotel 都会产，**与 rollout 无关**。tail sampling 不会被它误判（它们不带 ERROR semconv 给 trace root）。

## 5. RabbitMQ producer span（验证仍然自动注入）

```
trace = 82ce65af185bcc32fae9234137db4e81  (2 spans)

[service-a] GET /rabbitmq/ok                kind=server
[service-a] :otel-demo-queue publish        kind=producer  messaging.system=rabbitmq
```

与实验里 service-a-auto 见到的形状完全一致。RabbitMQ 的零代码自动注入在 rollout 后**仍然成立**。

## 6. 没触发的 fallback

- **task 2.5.5（plan B revert）**：mode B 在 4 个服务上都成立，无需回退。
- **task 3.3（install.sh fallback）**：GitHub releases 直接下载 `otel-linux-${TARGETARCH}` 4 次全部成功，无需切换到 install.sh。
- **service-c 的合成 slow span**：按 plan 默认保留——`tracer.Start("redis synthetic slow operation")` 仍在 redis.go 里，与 `go.opentelemetry.io/otel` import 共存，归类为 L3 业务行为不在 strip 范围内。

## 7. 一处小坑（执行中遇到）

`docker compose down` 失败那次提示 "Network ... Resource is still in use"——不是 rollout 引入的问题，是上次跑 service-a-auto 实验时其他容器还挂在 network 上。`docker compose up` 后自动重建 network，未影响后续流程。记下来以备未来 cold start 时知道这是常态。

## 8. 当前栈对比

| 维度 | main 分支 | otel-auto 分支 |
|---|---|---|
| 应用代码侧 OTel 行数（4 服务合计） | ~1000+ 行 | ~30 行（仅 L3 业务消费 + service-c 合成 slow span） |
| go.mod 直接依赖（service-a 为例） | 15 项 | 8 项 |
| Build 命令 | `go build` | `otel go build` |
| SDK 配置来源 | YAML（observability.Init 读 config） | env vars（docker-compose 注入） |
| Jaeger 中行为 | 5xx/error/slow 必留、normal 1% 抽样 | **完全一致**（tail sampling 配置零改动） |
| W3C TraceContext | 通过 otelgin/otelhttp 显式管 | 通过 loongsuite 自动管 |
| 多服务交叉 | 4 服务能连成一条 trace | 4 服务能连成一条 trace（+ traefik 5 服务） |

**`git diff main..otel-auto` 现在就是这套对比的完整证据**——任何人 clone 仓库切两条分支就能看完整迁移。

## 9. vibe-replay PPT 影响

按 plan 9.2 决议，**不改 PPT**：

- P1–P20 引用 `service-a/internal/...` 的手动代码——这些代码在 main 分支上仍然完整存在，PPT 的代码片段截图依然有出处。
- P21 extension page 现在更有底气——整个 otel-auto 分支就是它的活样本，跑 demo 时打开 Jaeger 看到 4 服务一条 trace，就是它最强的证据。
- P22 / P23（recap + closing）不受影响。

## 10. follow-up（不在本次范围）

- README.md 可以更新一段"两条分支用法"：main 看 manual demo、otel-auto 看 agent demo。本次没改 README，避免在 demo 实际跑稳前对外宣传新路径。
- 如果要把 otel-auto 合到 main：需要先讨论"main 是否仍作为 manual 参考"。当前模型是 main = manual，otel-auto = auto；合并会让 main 变成 auto，manual 历史只能用 tag 保存。
- service-c 切前依赖最少（只 Redis），切后镜像反而大了 2.5MB——值得未来观察 loongsuite 注入的 runtime 占用情况。

---

## 附：rollout 期间的提交栈

```
docker-compose + 4 Dockerfiles + remove service-a-auto/    (f786087)
service-d: strip manual OTel instrumentation in place      (36f3880)
service-c: strip manual OTel instrumentation; preserve...  (696f0ad)
service-b: strip manual OTel instrumentation in place      (dd4faf0)
service-a: strip manual OTel instrumentation in place      (a802f5d)
Plan: roll out otel-auto instrumentation to service-a/b/c/d (59ed3b7)
```

7 个 commit 完成 sections 1–8（不含本文档）；按 section 9.1 本 observations 文档作为最终收尾。
