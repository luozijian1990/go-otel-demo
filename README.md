# Commerce Incident Lab

一个基于四个 Go 业务服务的故障排障 Demo：在 UI 触发商品、库存、订单或支付异常，用三个观测 Skill 查询真实 Jaeger / Loki / Prometheus，再由综合 Skill 结合应用关系 reference 形成诊断。

**当前运行架构不再包含 aiops-api、模型 HTTP adapter、自动评分或 SQLite 实验平台。** 分析发生在你的 AI 会话中，无需给网页配置模型 key。

## 快速开始

```bash
bash scripts/init-demo-env.sh
docker compose config --quiet
docker compose up --build -d --wait
```

打开 [业务故障实验室](http://localhost:18086/ui)，或 [Nginx 直接入口](http://localhost:18083)。

首次构建需要访问镜像仓库、GitHub 和 Go 模块代理。四服务都使用 LoongSuite `v1.10.0` 的 `otel go build`；没有初始化第二个 tracing SDK。默认本地 MySQL/Redis 随 Compose 启动；数据库表由各自业务服务创建，演示商品为 `SKU-001`，金额单位为分。

历史本地 `service-*/config.yaml` 文件原样保留，**新业务入口不读取它们**。如需明确连接另一套依赖，在私有 `.env` 中设置 `BUSINESS_MYSQL_DSN`、`BUSINESS_REDIS_ADDR`、`BUSINESS_REDIS_PASSWORD`，并确认目标允许创建演示表；不要对生产数据库运行。

## 使用流程

1. 选择商品浏览、库存操作、提交订单或确认支付，点击“模拟异常”；每类也有正常对照。
2. 点击“随机模拟一次故障”会随机选择业务和故障；也可选某类业务。默认只发一条主业务调用，不再跑正常预检或持续流量。
3. “本次调用链”直接读取这次 TraceId 的 Jaeger spans，展示实际父子关系、服务、耗时、HTTP 状态及异常。尾部采样未完成时显示采集中，最多重试 45 秒；没有执行的下游不会补画。支付建单准备、下单成功清理有独立 TraceId，不混入主链路。
4. 查看真实状态、请求 ID 和业务 TraceId，点击“复制排障上下文”。
5. 在支持本地 Skills 的 AI 会话中粘贴，使用 `$aiops-incident-rootcause`。
6. 综合 Skill 读取应用关系 reference，按需使用 Jaeger、Loki、Prometheus 三个 Skill，输出原因、传播、影响、证据与不确定性。
7. 分析完成后，手动“揭晓本次故障”核对。真实注入控制和回执不进入复制内容或 Skill 查询结果。

“下单异常”不等于“订单服务就是根因”：下游商品或库存同样可能影响下单。单次点击适合 trace/log 定位，但不能据此宣称指标趋势。后端保留显式 window 参数用于兼容旧测试，页面不再默认启动窗口。

网页不会自动调用模型或展示预制 AI 答案。综合 Skill 不读取故障目录或答案接口，也不执行自动修复。当前开发会话具备源码背景，因此开发中的诊断示例不是严格黑盒盲测。

已完成 [两个独立 subagent 测试](docs/observations/single-call-subagent-validation.md)：一个只拿 TraceId，另一个只拿“订单调用库存功能故障”的描述；两者使用全新上下文，报告保存后才读取答案对照。第二个自行发现了同一故障 TraceId。它们是同一次故障的两种输入方式验证，不是准确率 benchmark。

## 四个业务服务

| 业务服务 | 实际职责与调用 | 源码入口 |
| --- | --- | --- |
| product-service | 商品资料、价格；Redis 缓存，缺失或失败时 MySQL 回源 | service-c/cmd |
| inventory-service | 库存查询、事务预占、释放、确认 | service-d/cmd |
| order-service | 商品价格确认 → 库存预占 → 订单；支付编排与补偿 | service-a/cmd |
| payment-service | 本地支付记录，按订单幂等；不连接真实支付渠道 | service-b/cmd |

共享业务实现位于 [commerce/](commerce/)，包括路由、数据模型、事务和遥测。四个独立 Go module 是薄进程入口，通过 replace 引用共享模块；根 [Dockerfile](Dockerfile) 使用仓库根目录构建上下文。保留物理 service-a/b/c/d 目录是为了避免破坏本地配置和迁移历史，运行时服务身份已经是业务名称。

每个服务只读写自己的 `commerce_*` 表。库存事务避免重复扣减/释放，支付按 order_id 幂等。订单收到明确支付失败后释放库存；网络结果未知时保留库存并记录 payment_unknown，允许幂等重试。补偿失败标 reconciliation_required。单实例编排有串行保护，**不声称实现了生产分布式事务或完整对账系统**。

| API | 用途 |
| --- | --- |
| GET /products/SKU-001 | 商品读取 |
| GET /inventory/SKU-001 | 库存读取 |
| POST /orders | JSON：id、sku、quantity |
| GET /orders/:id | 订单状态 |
| POST /orders/:id/pay | 本地支付与库存确认 |
| POST /orders/:id/cancel | 取消待支付订单并释放库存 |
| POST /reservations | 库存预占：order_id、sku、quantity |
| POST /reservations/:id/release、confirm | 释放或确认预占 |
| POST /payments | 本地支付记录：order_id、amount_cents |

## 四个 Skill

| Skill | 工作内容 |
| --- | --- |
| [jaeger-trace-rootcause](.agents/skills/jaeger-trace-rootcause/SKILL.md) | 根据 TraceId 或服务/时间查询真实调用链，分析传播、慢分支与缺失证据 |
| [loki-incident-logs](.agents/skills/loki-incident-logs/SKILL.md) | 查询具体错误、业务状态、缓存回源、库存补偿 |
| [prometheus-incident-metrics](.agents/skills/prometheus-incident-metrics/SKILL.md) | 比较请求量、错误率、延迟、依赖失败与 fallback |
| [aiops-incident-rootcause](.agents/skills/aiops-incident-rootcause/SKILL.md) | 串联前三者，结合应用关系形成有证据的综合判断 |

综合 Skill 的 [application-map.md](.agents/skills/aiops-incident-rootcause/references/application-map.md) 模拟 CMDB：职责、正常调用关系、存储、状态语义、遥测标签和查询端点。它不包含场景到答案的映射，不能替代实时证据。

三个查询脚本只依赖 Python 标准库，统一返回查询、UTC 窗口、证据 ID、来源、状态和限制：

```bash
python3 .agents/skills/jaeger-trace-rootcause/scripts/query.py --trace-id TRACE_ID --wait 20
python3 .agents/skills/loki-incident-logs/scripts/query.py --trace-id TRACE_ID --start START_UTC --end END_UTC
python3 .agents/skills/prometheus-incident-metrics/scripts/query.py --service order-service --start START_UTC --end END_UTC
```

无 TraceId 时，Jaeger 可按 `--service` 与时间检索，Loki 支持 `--request-id`、`--run-id` 或服务/时间关联。默认回看 300 秒，时间范围最多 1 小时；查询不跟随重定向、限制返回量，并脱敏凭据/控制字段。Loki 长窗口截断时要缩小范围或按代表 TraceId 查询，不能把最早的基线日志当故障证据。

旧 Jaeger CLI 仍可用：`python3 scripts/analyze-jaeger-trace.py --trace-id TRACE_ID`。两处兼容入口复用 `scripts/observability/trace_cleaner.py`。

## 真实三信号

- **Traces**：LoongSuite 自动埋点 → Collector 尾部采样 → Jaeger。错误及超过 3 秒 trace 优先保留，普通成功约 1%。普通事件不判错；exclusive time 合并并行子区间。请求返回 sampled 标志不代表一定已入库。
- **Logs**：slog JSON 文件 → Collector filelog → Loki 原生 OTLP。只采文件，stdout 用于排障。仅 service_name 作索引，trace/request/run/order ID 留在 metadata。
- **Metrics**：Prometheus 每 5 秒抓取各服务 /metrics。HTTP、实际依赖操作、fallback、inflight、Go/process 指标；不把任何单请求 ID 作为标签。应用等待与 SQL/Redis 操作分别计时。
- 演示 driver 嵌入订单进程，每次业务请求独立起 trace，经真实 Traefik 转发；driver span 标记 `demo.traffic_driver=true`，不是订单业务根因。不会把创建演示控制请求的 TraceId 复制给诊断。

| 观测入口 | 地址 |
| --- | --- |
| Jaeger | http://localhost:16686 |
| Loki | http://localhost:13100/ready |
| Prometheus | http://localhost:19090 |
| Traefik | http://localhost:18082/dashboard/ |

## 控制面与数据边界

轻量控制面位于订单进程的独立 demo 模块，`POST /demo/runs` 接受 business、traffic（single/window）、mode（fault/healthy）。故障选择在服务端，只有一个活动演示，最多运行 120 秒；顺序发送、最大约 1 请求/秒，不积压队列。停止会取消后续请求。页面历史只保留进程内最近 100 次，重启不重放；这是有意移除实验平台后的简化。

每次注入使用短期 HMAC token，只透传到固定业务下游。禁止跨主机重定向、任意 SQL/URL/shell；无全局 DSN/client 修改，不 DROP 表或 FLUSHDB。实际执行通过内部 receipt 确认，单独答案接口只供操作员核对。控制请求/答案不计入业务日志指标，不作为 Skill 证据。

默认端口只绑定本机，控制写操作校验 Host、Origin、JSON。没有生产认证或租户隔离；远程部署必须另加认证。只允许在演示数据上运行。

日志目录由短期 init 容器设置权限：应用 UID/GID 65532、0750；Collector UID 10001，加入日志只读组；应用和 Collector 均非 root。应用日志轮转 10 MiB、3 个备份；Collector offset 持久化。Loki 7 天；Prometheus 7 天/512 MiB。Jaeger 仍为内存存储，重建会丢 trace。普通 compose down 保留卷；down -v 会删除该项目卷，不要用于保留历史的场景。

## 验证

```bash
(cd commerce && go test -race ./...)
for service in service-a service-b service-c service-d; do
  (cd "$service" && go test ./...) || exit 1
done
python3 -m unittest discover -s scripts -p '*_test.py'
python3 scripts/smoke-commerce.py --controls
docker compose config --quiet
```

smoke-commerce 使用真实业务接口验证幂等、支付失败与库存补偿、四类随机故障、独立 TraceId 和两入口代理，不调用模型；--controls 从私有 .env 读取本地签名 key，仅用于操作员测试，不是诊断 Skill。

实际验证结果和环境差异见 [business-demo-validation.md](docs/observations/business-demo-validation.md)。

## 从旧版本迁移

旧独立 aiops-api 已从 Compose、UI、路由和当前 Skills 工作流移除，旧容器已停止并保留。自动审批拒绝大批量删除旧源码，因此 `aiops/`、旧 `service-*/internal`、旧服务 Dockerfile/config 示例和旧评测脚本暂作为历史源码保留；它们不被新进程入口注册或根 Dockerfile 打包。不要用旧 smoke-aiops/evaluate-aiops 测试当前业务版本。

改造前完整源码备份：`/private/tmp/commerce-migration.80HSSf/before-business-refactor.tar.gz`，这是本机迁移备份，不是仓库运行依赖。用户原有 config.yaml、计划文档、存储卷未删除。当前运行入口以本 README 和根 docker-compose.yml 为准；旧架构图/历史验证记录描述旧版本。
