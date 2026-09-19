# AIOps 改造验证记录

日期：2026-09-19。代码基线 `00129e3519347cba70d30c385b0362f472cd5214`，与计划基线相同。没有发现适用的 AGENTS.md。开始时仅计划文件未跟踪；未修改该文件，保留四份本地 config.yaml，没有提交或推送。

## 实现与验证边界

| 阶段 | 已实现与已运行验证 | 限制 |
| --- | --- | --- |
| P0 | 单一标准库 trace cleaner，两处旧 CLI 兼容；普通事件、异常时间、200 子调用失败、缺父、FOLLOWS_FROM/跨 trace、并行区间并集测试；Pydantic/schema 与 Analyzer 导入隔离测试 | exclusive time 仍是缺失 span/时钟偏差下的估计 |
| P1 | JSON 文件 → filelog → Loki OTLP；Prometheus 直接抓四服务；Agent v1.10.0，实际 CLI `1.10.0_01fcc67`；四服务和 API 镜像构建；Collector 0.123.0 validate；真实三信号回读 | 默认新依赖镜像组合拉取受阻，实际运行版本见下表 |
| P2 | 中性 `/exercise`、HMAC 请求级控制、七故障+健康、真实 receipt、Traefik Host、幂等创建；八场景真实操作全部确认；签名、过期、动作/目标不匹配、取消和禁止跨主机重定向测试 | 本机受信控制面，同进程模块隔离不是安全沙箱 |
| P3 | 精确 trace/request 日志，episode 分阶段有界补充；固定并行 PromQL、缺失状态、索引/hash、模型预算；真实缺表快照人工检查 | 时窗指标可能含其他流量；迟到 spans 的完整性不能证明 |
| P4 | HTTP provider 协议/格式修复/完整 payload 扫描；外部 Skill；结果冻结/重复提交拒绝/盲测揭晓门槛/revision；两入口真实浏览器测试 | **真实模型测试未运行，无模型 key；没有诊断准确率结论** |
| P5 | 默认持续实验 300 次实验请求+1 次预检；baseline/incident/recovery 真实窗口，互斥/停止/重启中断；同快照三种分析视图；来源、场景、视图、排除项统计 | 未执行真实模型三组消融；外部会话也未提交真实诊断，保持 awaiting_external |

## 平台、镜像与网络限制

宿主 macOS arm64，Docker Desktop 4.83.0，Docker Engine 29.6.2，容器 linux/arm64。宿主新 API 测试使用隔离 Python 3.13.12；API 镜像使用 Python 3.12.14 的已缓存不可变 digest。旧脚本还通过了系统 Python 3.9 测试，不依赖 FastAPI。

| 组件 | 正式 Compose 配置 | 本次实际运行验证 |
| --- | --- | --- |
| Jaeger / Collector / Traefik | 1.76.0 / 0.123.0 / v3.4 | 同左 |
| MySQL | mysql:8.4.4 | 隔离新卷上的 Ubuntu MySQL 8.0.27 |
| Redis | redis:7.4.2-alpine | 缓存 Redis 6.2.24 |
| Prometheus | v3.2.1 | 缓存 v3.5.0 |
| Loki | grafana/loki:3.4.2 | 官方 v3.4.2 linux-arm64 发布二进制，revision 4fa045d3，临时 Alpine 容器 |

Docker Hub 多次 `context deadline exceeded`。因此没有声称默认 MySQL 8.4.4 / Redis 7.4.2 / Prometheus 3.2.1 的 fresh clone 联调通过，也没有验证 amd64。四服务的 **LoongSuite 自动编译新镜像** 已实际运行，未用普通 Go 二进制替代。Loki 采用官方发布包验证真实后端，不是模拟服务。

本次临时覆盖在 `/private/tmp/aiops-runtime-override.yml`，临时 Loki 构建上下文在 `/private/tmp/aiops-loki.uWuXtF`。运行中的 MySQL 只使用 `go-otel-demo_aiops-validation-mysql` 新卷。正式 Compose 仍保留计划指定版本；网络恢复后按 README 的默认命令重新验收。当前运行栈可直接打开 `http://localhost:18086/ui` 或 `http://localhost:18083`。

为保留原内存 trace，没有重建既有 Jaeger 容器；其旧端口绑定仍为所有网卡。提交配置已改为仅 127.0.0.1:16686，OTLP 只走容器网络；下次重建才应用新绑定，并会清空 Jaeger 内存 trace。

自动审批曾拒绝使用现有 config.yaml 启动 A/B，理由是可能触及外部共享数据库迁移/seed。该动作没有执行；后续已切换到提交的 config.compose.yaml 和本项目隔离数据库完成测试，不再需要该外部权限。早期 C/D 曾用原配置启动检查，随后也切换为隔离配置；本地配置文件未覆盖。

## 命令与结果

```bash
# 全部四模块通过，另执行全部 -race 测试通过。
for service in service-a service-b service-c service-d; do
  (cd "$service" && GOCACHE=/private/tmp/go-otel-demo-go-cache go test -race ./...) || exit 1
done
python3 -m unittest discover -s scripts -p '*_test.py' # 4 passed, including resolved Compose startup invariants
/private/tmp/aiops-venv/bin/python -m pytest aiops/tests -q # 13 passed
docker compose config --quiet                       # passed
docker compose run --rm --no-deps otel-collector validate --config=/etc/otelcol-contrib/config.yaml # passed
docker compose build service-a service-b service-c service-d aiops-api # passed
PYTHONUNBUFFERED=1 bash scripts/smoke-aiops.sh --all-scenarios # passed twice
node --check web/aiops.js                            # passed
git diff --check                                    # passed
```

Go 重定向测试需要本机临时监听端口，沙箱内 bind 被拒，随后在获准的本机测试环境运行成功。Python 测试有一条依赖 Starlette/anyio 的弃用警告，不影响结果。测试 HTTP adapter 使用明确的 `fixture-protocol-test` 替身，没有创建真实模型评测样本。

## 最终八场景回执

以下均为真实请求，valid=true；故障的 traces/logs 可用，Prometheus 有实际序列但单次请求标记 insufficient_samples。

| 场景 | run ID |
| --- | --- |
| healthy | 2d1f9882-0540-4338-99b4-df28640e3ab8 |
| app_error_a | 6d55c524-ad83-486d-8bb0-8bf1ac75f0f7 |
| app_error_b | acf81c73-7482-4144-98ce-6f5ff4237e06 |
| mysql_missing_table_d | 37cbc499-ce6f-4ba8-81b2-b9f5be66c4bf |
| redis_connect_refused_c | 87f7b852-01ea-47e2-b658-c04342c8b9cc |
| application_delay_c | 7b3cfe62-f450-4f99-b0c8-7258cd252e50 |
| mysql_slow_query_d | 92503ac1-b9cd-40ba-be13-1d0a3b954012 |
| redis_degraded_c | 37e1e72c-272c-49f6-b4e7-00652c9b9352 |

健康 trace 没有查到时最终状态为 not_found_after_deadline，未宣称一定被采样丢弃；健康实验照常完成。没有把健康 span 人为标 ERROR。

缺表快照 hash：`d40835b34279d2b09469987dae8ac22c6ac5fd98fc8df73d5c6dfee414b1cd68`。包含同一实际请求的 9 条日志：D 原生 1146、B/A 传播、C 正常分支与各服务 completion。完整模型视图人工检查与扫描无 scenario_id/expected_/ground_truth/token/receipt/seed；原生错误、SQL 与服务身份保留。采集阶段曾错误地在预检日志出现后提前结束，已修为 trace/request 精确查询及入口 completion 条件，并加回归测试。旧快照保持不可变，没有偷偷覆盖历史证据。

默认持续实验 `62093377-cdf6-4073-aa61-3fdb9d274273`：300 条实验请求、1 条预检、0 skipped；三个窗口均保存，三信号返回。日志达到 300 条上限明确标 truncated。`8bbb1c1c-3112-4c61-892d-d583ee364d31` 为 awaiting_external，无模型结果。此快照早于精确/分阶段日志修正，不能据此声称其日志覆盖故障窗口完整；最终短持续实验另行复核。

最终版本短持续实验 `b593d039-f332-42d3-abfa-19d007cf3b4a`：20.0006 / 20.0046 / 10.0035 秒，100 条实验请求+1 条预检，实测 1.9974 RPS；metrics available，logs available（分阶段限量明确 truncated），traces partial（有代表 trace 未找到，未掩盖）。快照 hash `e238b0d371d78972c60a623830fc20f4961b7f3a7a6082b345fff9598636f0c0`。分析 `f33ebe25-19a0-4db8-b5b4-6016bfa8048f` 为 awaiting_external；未冻结时 ground-truth 返回 HTTP 409，未为了通过测试提交规则答案。

该快照回读日志分类：精确 trace 27 条、baseline 50 条、incident 补充 41 条、recovery 50 条，共 168 条，其中 ERROR 24 条，验证最终采集确实覆盖故障与健康阶段。

## 存储、浏览器与修复记录

- fresh/既有日志卷目录均通过 init 容器校正为 65532:65532、0750；应用保持 nonroot。Collector offset 卷最初报 permission denied，已按镜像实际 UID 10001 修复，并补 group_add 65532 只读日志权限。
- lumberjack 轮转测试通过：活动/备份文件均保持 0640，Collector 组可读。未做大量真实轮转、崩溃/丢盘压力测试。
- 固定缺表 TraceId 在 Collector 重启前后均为 **9 条、9 个独立时间戳**，持久 offset 未重复导入该请求。未证明任意崩溃窗口都恰好一次。
- Collector 0.123.0 不接受最初的动态 TraceID OTTL 写法，改为 filelog 原生 trace parser；真实 Loki metadata 确认包含 trace_id/span_id/request_id/experiment_id，索引仅 service_name。
- 老网关文件包含五月历史日志，首次读取遭 Loki 超龄拒绝；网关改为首次从末尾采新增业务请求，应用文件仍从 beginning+持久 offset 读取。未删除历史日志。
- 18083 在 API 容器重建后暴露 Nginx DNS 缓存旧 IP 问题，改为 Docker resolver 5 秒有效的变量上游；两个入口重新通过。
- 最终编排审计发现 B/C/D 漏加 Compose healthcheck（此前逐个 --no-deps 启动未暴露），已补齐三服务探针与 MySQL/Redis/迁移先后依赖；没有把 `compose config` 语法通过当成启动时依赖满足的证明。
- Playwright 验证旧 Chain MySQL Error 的 500/TraceId、新实验历史、持续状态、awaiting_external；1200px 与 390px 均无水平溢出。截图保存在被忽略的 `output/playwright/`。
- 原 Chain Degrade OK 补充同一 fallback counter/logger，保持 HTTP 200 与原响应格式；随机实验和旧入口共用真实降级指标。
- 最终六个旧接口回归：`/error` 500、`/chain/mysql/error` 500、`/chain/fanout/ok` 200、`/chain/fanout/error` 500、`/chain/slow/redis` 200（4.01 秒）、`/chain/degrade/ok` 200；均返回业务 TraceId。最后一次降级后 B 的 `demo_fallback_total{dependency="redis",reason="unavailable"}` 为 1。
- Agent 自动 metrics exporter 曾向仅有 traces 的 OTLP 接收端报 MetricsService 错误；Compose 显式设置 OTEL_METRICS_EXPORTER=none、OTEL_LOGS_EXPORTER=none，业务指标只走 Prometheus，日志只走文件。

## 尚未验证和适用限制

- **真实 HTTP 模型推理、外部 Skill 真实诊断、真实三组消融效果均未运行。** 不能用协议/schema/fixture 测试代替模型验收。
- 默认新依赖镜像组合的 fresh clone、amd64、长时间磁盘保留与轮转压力、异常退出时日志完整性未验证。
- 快照 schema/reference 验证不等于事实语义验证；matched 只表示四维对照。建议仍需人工核查，系统不执行修复建议。
- 服务内故障仅用于本地教学；没有生产认证、租户隔离、共享后端保护策略或基础设施 exporter。不能从客户端错误推断宿主 CPU、磁盘、锁或整个 Redis 集群故障。
- 自动 tracing 的 slog 注入会在原始 JSON 中再附加同值 trace_id/span_id；Loki 解码后为单一同值字段。没有 stdout/file 双采造成的重复记录，但未调整 Agent 内部日志注入行为。

## 变更入口

业务：四服务的 cmd/main.go、internal/handler、internal/database/redis/client、internal/exercise、internal/telemetry、各自测试、go.mod/go.sum、Dockerfile/config.compose.yaml。

控制与诊断：aiops/app/{contracts,trace_cleaner,scenarios,run_manager,storage,evidence,analyzer,evaluator,main}.py；aiops/schemas、tests、Dockerfile、requirements.txt、pyproject.toml。

配置与使用：docker-compose.yml、otel/collector.yaml、observability/、Traefik/Nginx、web/aiops.js/css/index.html、四个 CLI/冒烟/初始化脚本、三信号 Skill、两处兼容旧 CLI、README、.env.example 与忽略规则。
