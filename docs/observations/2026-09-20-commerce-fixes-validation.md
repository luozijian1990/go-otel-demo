# Commerce Incident Lab 修复验证记录

执行日期：2026-09-20（UTC）。基线实际 HEAD：`8f8c87109fa162acf53fcb5511962c0a19f76846`，比 handoff 核对基线更新。开始时 `git status --short` 仅有 `?? docs/plans/`；未覆盖、提交或暂存该目录。未发现适用 AGENTS.md。以当前 README、Compose、Dockerfile、四个业务入口为准，不恢复历史 AI 平台。

## 交付状态

| 项目 | 状态 | 证据与边界 |
| --- | --- | --- |
| H01 错误契约 | 已实现并验证 | HTTP 契约红→绿；真实缺表矩阵保留原生 1146；业务拒绝/技术错误计数分离，坏响应/截断响应保守 unknown |
| H02 网关查询 | 已实现并验证 | Python 查询回归；14 个实测用例均关联到业务与 Traefik 日志；metrics 拒绝网关 |
| H03 UI 所有权 | 已实现并验证 | 6 个可控异步测试；两个真实浏览器入口的复制、下载和历史切换通过 |
| H04 预占幂等 | 已实现并验证 | 真实 MySQL 8.4.4 复现 confirmed→held，修复后 held/confirmed/released、并发、冲突和回滚测试通过 |
| H05 恢复/补偿 | 已实现但部分验证受限 | 真实提交后 504、重建 App、历史未知后明确未应用、补偿/状态写入失败等已通过；最终新增响应破坏组合回归受环境超时影响，见命令记录 |
| H06 fallback | 已实现并验证 | 提前 served 的失败测试已修复；attempt/result 计数测试、实际 Redis refusal 回源成功和真实 SQL 缺表回源失败测试通过 |
| H07 固定矩阵 | 已实现但部分验证受限 | 首轮默认镜像 10 故障 + 4 健康全部通过，目录覆盖断言通过；最后状态日志细化后未完成全矩阵重跑 |
| H08 默认环境 | 已实现但部分验证受限 | 新配置/新卷/独立 bind mount/default ports/linux-arm64 首轮构建运行通过；最终工作树 Agent 重建中止，amd64 与最终重启回归未验证 |

**不能把首轮通过解释为最终工作树所有层级全部通过。** 最后实现细化包括：独立 GORM 更新对象避免回滚后内存谎报状态、准确记录持久化/补偿结果和业务拒绝级别、恢复遇到可信状态/幂等冲突转入 reconciliation_required。第一次状态对象修复已成功重建，最后日志和恢复冲突细化未完成 Agent 重建。

## 改动与安全边界

- `commerce/errors.go`、`app.go`：有限 code/category/outcome、Unwrap、可信服务/操作/字段组合验证、有界响应读取和成功业务响应核验。保留字符串 error；不回显原始下游响应。技术原因留在原生日志/trace；不新增 TracerProvider。
- `store.go`、`routes.go`：事务内返回真实 reservation；下单恢复复用持久化金额；支付先写 payment_unknown；历史未知不因后次 not_applied 释放；取消先写 cancel_pending；2 秒有界收尾保留关联信息；不吞状态写入/补偿错误。
- 下单预占成功但最终状态写失败，选择保留 creating/reservation_unknown 供显式同键恢复，不进行可能丢响应的释放补偿。这样不会让已释放库存继续成为可支付订单。
- `telemetry/`：业务拒绝 outcome=rejected；fallback attempt 与 success/failure 终态分离，正常 miss 不算故障降级。
- `scripts/observability/query.py` 与四个 Skill/reference：按信号服务目录、Traefik AND 关联限制、fallback 模板。
- `web/commerce.js`：选择版本、poll 序号、读取消、历史序号、await 后所有权校验、快照清空、复制/下载 ID 核验。
- 新增错误/真实 MySQL/遥测/矩阵目录/DOM 测试；新增隔离准备、统一回归、矩阵和真实浏览器脚本；原随机 smoke 保留并参数化私有 env 与双入口。

未修改私有 `.env`、`service-*/config.yaml`；未访问外部数据库；未执行 DROP/TRUNCATE/FLUSHDB、down -v、prune、提交或推送。测试数据与卷保留。

## 红→绿与失败记录

| 复现命令/测试 | 修复前实际现象 | 后续结果 |
| --- | --- | --- |
| `go test -run 'TestErrorContract\|TestWrappedRejection' ./...` | 可信库存不足被转 500；无 code；原始 HTML 回显 | 修复后通过 |
| `go test ./telemetry -run TestFallback` | 回源前输出 fallback served | 修复后通过 |
| `python3 -m unittest discover -s scripts -p observability_query_test.py` | 默认 selector 无 Traefik；metrics 接受网关 | 9 项通过 |
| `node --test web/commerce.test.cjs` | 切换后按钮未禁用；旧答案显示到新现场 | 最终 6 项通过 |
| `COMMERCE_ISOLATED_MYSQL=1 go test -run 'TestReservationPersistence\|TestCommittedResponseLossRecovery' -v .` | confirmed 返回 held；已提交预占响应丢失后写 failed | 修复后真实 MySQL + race 通过 |
| `... go test -run TestPriorUnknownNeverCompensates .` | 库存 18→20、payment_unknown→payment_failed | 修复后通过 |
| `... go test -run TestFailedStateWriteDoesNotClaimPersistence .` | SQL 更新后事务回滚，内存仍宣称 paid | 独立更新对象后通过；同值更新 RowsAffected=0 导致 404 的回归失败也已修复 |

第一次普通沙箱 Go HTTP 测试因回环监听被拒绝；允许本地测试监听后正常执行。这不是业务失败。

真实 MySQL 回归曾多轮通过（commerce 约 1–6 秒，含 race），涵盖补偿失败、确认失败、首次未应用补偿、未知后冲突、pending 写失败阻止副作用、取消丢响应、请求取消后有界收尾、真实 SQL 回源失败。最新扩展将“已提交后丢响应”细化为 504、坏 JSON、截断 body、直接断连四种组合；扩展后的最终数据库回归遇到环境超时，不能把之前通过移植给这些新增组合。

最后并行构建时，真实 MySQL 出现 `invalid connection` / `i/o timeout`，原来毫秒级操作升到数秒。一次测试用 SIGQUIT 截止并保存堆栈，另一次 `-timeout 90s` 在 92.538 秒失败；停止构建后的重试仍在 91.139 秒超时。宿主机同时存在高 CPU 负载；这说明验证环境受限，不能据此证明业务回归，也不能抹去失败。最终 Agent 构建超过 10 分钟停留在四个 `otel go build` 步骤，本轮主动中断，退出码 130。没有改镜像版本来规避。

## 实际验证命令

本机默认 Go launcher 为 1.22.2；在 commerce module 内 `GOTOOLCHAIN=auto` 使用 **Go 1.25.0**。所有本机 Go 命令使用 `GOCACHE=/private/tmp/go-otel-demo-go-cache`；`GO_BIN=/opt/homebrew/bin/go`。

```bash
GO_BIN=/opt/homebrew/bin/go GIN_MODE=release bash scripts/test-commerce.sh unit
GO_BIN=/opt/homebrew/bin/go GIN_MODE=release bash scripts/test-commerce.sh integration
# 独立重试并设置硬截止（commerce 目录）
COMMERCE_ISOLATED_MYSQL=1 GIN_MODE=release GOCACHE=/private/tmp/go-otel-demo-go-cache \
  /opt/homebrew/bin/go test -race -timeout 90s ./...
python3 -m unittest discover -s scripts -p '*_test.py'
node --check web/commerce.js
node --check scripts/browser-commerce.js
node --test web/commerce.test.cjs
git diff --check
```

- 单元/静态入口：包含 commerce race、四个薄入口编译（没有服务独立用例）、Python 14 项、DOM（统一入口当时 5 项，最后启动竞态修复后单独重跑 6 项）、JS 语法、Compose config。
- 真实集成入口只在 `COMMERCE_ISOLATED_MYSQL=1` 时连接固定 `127.0.0.1:23306/commerce_fixes_test`；普通 unit 显式取消该变量，不偷偷跑数据库。
- SQL 回归使用真实 MySQL；响应破坏使用测试专用 HTTP 代理，先执行真实库存/支付 handler 再破坏结果。部分商品 HTTP 响应使用 fake；状态写失败用测试私有 GORM callback；不声称这些是 Agent 端到端测试。
- DOM 异步测试使用测试替身的 fetch/timer/DOM；真实浏览器另测，不用 `node --check` 冒充 UI 验证。

## 独立默认 Compose 与真实遥测

隔离目录：`/private/tmp/commerce-fixes-mh27oa05`；project：`commerce-fixes-6e64d81d`。由当前工作区副本生成新 `.env`，清除外部依赖覆盖，使用全新 project 命名卷和该副本内独立 `logs/traefik`。默认端口零改动、无 override，未停止其他项目腾端口。

独立 MySQL 边界测试容器：`commerce-fixes-mysql-20260920`，专用 23306，不连接 Compose 私有/外部 DSN。

```bash
python3 scripts/prepare-isolated-commerce.py
# 在生成的隔离目录：
./isolated-compose.sh pull --ignore-buildable
./isolated-compose.sh build
./isolated-compose.sh up -d --wait --wait-timeout 120
# 从工作区：
python3 scripts/smoke-commerce-matrix.py \
  --isolation /private/tmp/commerce-fixes-mh27oa05/isolation.json \
  --report /private/tmp/commerce-fixes-mh27oa05/matrix.json
```

首轮矩阵 UTC **03:24:27–03:27:11**，约 164 秒：F01–F10、四类 healthy 均 pass；两 UI HTTP/API 入口通过；四服务 Prometheus scrape up=1。每个用例记录业务状态/库存前后值、TraceId、request ID、注入回执核对布尔值；故障有真实 Jaeger spans，所有用例有相同 TraceId 的 Traefik + 业务 Loki 日志。正常 trace 不强制保留。原生 SQL 1146、Redis refused、fallback 成功证据均实际查询。另用已保存真实 traces 核对四个 slow_sql 有 SLEEP(3)，application_delay 无 SLEEP(3)。

摘要见 [matrix-first.json](commerce-fixes-2026-09-20/matrix-first.json)。原始查询证据（含有界清洗结果）保存在隔离目录 `matrix.json`。这是软件回归，真实 AI 诊断 **not_run**。

macOS 26.6.2 / arm64；Docker 29.6.2 linux/arm64；Compose v5.3.1；Node v22.23.2；Python 3.9.10。实际 tag/digest 见 [images.txt](commerce-fixes-2026-09-20/images.txt)。MySQL 8.4.4、Redis 7.4.2-alpine、Prometheus v3.2.1、Loki 3.4.2、Collector 0.123.0、Jaeger 1.76.0、Traefik v3.4 均未换版本。**amd64 构建/运行 not_run**，没有跨架构成功声明。

## 真实浏览器

```bash
playwright-cli --session commerce-fixes open http://localhost:18086/ui/ --headed
playwright-cli --session commerce-fixes run-code "$(cat scripts/browser-commerce.js)"
playwright-cli --session commerce-fixes console error
```

Chromium/Chrome **153.0.8010.48**：两个入口实际启动健康请求，逐一读取 clipboard 和下载 JSON，核对内容 ID、文件名 ID 与所选现场一致；切换两条真实历史后再核对复制内容。最终 browser console **0 errors / 0 warnings**。首轮恰逢重建产生 /demo/runs 502 并超时，稳定后重跑通过；首轮失败没有作为成功计算。

真实下载文件分别为 `incident-4acbef2ea51e4b5d72536640d2f3ba3f.json` 和 `incident-dcdf9bd6caff8d065fb90992a72a0de0.json`，只含业务上下文。最终 JS 已同步到隔离副本 bind mount 后重跑通过，含启动 POST 返回期间切换历史的所有权修复（该乱序由第 6 个 DOM 用例固定复现）。脚本没有 mock 后端。详细运行记录在隔离目录 `browser-last.log`，可控乱序证据在 `web/commerce.test.cjs`。

## 复跑与剩余限制

现有隔离容器/数据保留。最终 commerce/web/scripts/Skills 源码已经同步到该副本，但同步源码不会自动更新运行镜像。其运行镜像不是最后日志细化后的源码；不要直接把再次访问现有容器称为最终版本验证。最终冲突状态用例所在的最后一次回归（integration-latest.log）仍出现真实 MySQL i/o timeout，91.015 秒触发硬截止，退出码 1，未通过。资源恢复后，推荐重新运行 `prepare-isolated-commerce.py` 生成最终源码的全新环境；先检查端口占用，必要时对自己创建的旧隔离项目执行不带 `-v` 的 down，或使用明确记录的端口 override。绝不清理用户现有项目或卷。

需要补验：最终 Agent 重建及全矩阵、扩展响应破坏 MySQL 回归、真实进程重启后的恢复、amd64。此前重建 App 的数据库恢复用例通过，不等价于最终容器重启验收。最后矩阵脚本新增逐用例指标窗口及更强 SQL span 断言，前者尚未重新跑完整矩阵。

业务仍为单实例订单编排；数据库持续不可用时不能保证收尾落库成功；reconciliation_required 保留为人工核对状态，无自动对账 worker；Jaeger 和网页历史仍为内存。最终源码未提交、未推送。原始日志保留在隔离目录：`build.log`、`startup.log`、`rebuild-final.log`、`regression.log`、`integration-final.log`、`integration-after-build-stop.log`、`integration-latest.log`、`unit-final.log`、`browser-last.log`、`matrix.json`、`source-sha256.json`。

记录收尾 UTC：2026-09-20T03:51:40.118982+00:00。最终单元入口退出码 0；最终 DOM 6/6、真实浏览器 2/2；最终数据库回归退出码 1；最后 Agent 重建退出码 130。
