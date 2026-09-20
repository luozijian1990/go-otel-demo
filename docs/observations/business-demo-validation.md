# 业务故障 + 四 Skill 改造验证

> 历史验证记录：以下内容保留当时的环境与结果；其中提及的旧源码、配置、脚本或临时路径可能已移除。当前运行方式以根 README 为准。

日期：2026-09-19。目标从独立 AIOps 评测平台调整为“真实业务故障 + Jaeger/Loki/Prometheus 查询 Skill + CMDB reference 综合排障”。未提交或推送代码，用户原计划和本地 config.yaml 保留。

## 已交付

- 四个业务运行服务：order-service、payment-service、product-service、inventory-service。原四个 Go module 保留为薄入口，共享 commerce 模块；根 Dockerfile 以整个仓库为构建上下文，仍用 LoongSuite v1.10.0。
- 商品缓存及数据库回源、库存事务预占/释放/确认、订单落库与状态、幂等支付记录和失败补偿。四服务只访问自己拥有的表，不假装调用真实支付机构。
- UI 四类业务异常/正常对照、单次/45 秒窗口、停止、真实请求/TraceId、复制净化现场、人工揭晓与有限内存历史。
- 三个只读查询 Skill，统一证据 ID/查询/UTC 窗口/限制；综合 Skill 通过 application-map.md 模拟 CMDB。无模型 adapter、答案生成器或自动修复。
- aiops-api 不再出现在 Compose、UI、Traefik/Nginx 或当前 Skill 工作流中，其旧容器已停止。

## 文件与迁移边界

主要新增 commerce/{app,store,routes,fault,demo}.go、commerce/telemetry、共享模块 go.mod/go.sum、根 Dockerfile；四服务 cmd/main.go 与依赖更新。新 UI 为 web/index.html、commerce.css/js；配置涉及 Compose、Collector、Prometheus、Traefik、Nginx。

诊断入口是 .agents/skills 下 jaeger-trace-rootcause、loki-incident-logs、prometheus-incident-metrics、aiops-incident-rootcause。查询实现位于 scripts/observability，原 Jaeger CLI 两处入口仍可运行。新增 scripts/init-demo-env.sh、smoke-commerce.py 与 query 测试。

自动审批拒绝了整批删除旧服务源码/测试/配置和旧平台，理由是不可恢复影响范围过大。没有绕过删除限制；改为切换新入口、停用旧运行链路并保留历史源文件。aiops/RETIRED.md 标明旧平台已退役。旧 service-*/internal、旧 Dockerfile/config 示例、旧评测脚本和 web/aiops.* 暂留历史，不被新根 Dockerfile COPY 或当前页面加载。

切换前备份位于 `/private/tmp/commerce-migration.80HSSf/before-business-refactor.tar.gz`，另有工作树 patch。旧五个应用容器保留为 stopped，旧卷未删除，Compose 的 orphan 提示属于预期迁移状态。不要不加检查地 remove-orphans/down -v。

## 实际环境

macOS arm64，Docker 容器 linux/arm64。沿用上一轮已隔离的 MySQL 8.0.27、Redis 6.2.24、Prometheus 3.5.0 缓存镜像及官方 Loki 3.4.2 二进制临时镜像；Jaeger 1.76.0、Collector 0.123.0、Traefik v3.4。临时依赖覆盖文件仍为 `/private/tmp/aiops-runtime-override.yml`，名称是历史遗留，不代表运行 aiops-api。

默认 Compose 配置的新依赖版本仍为 MySQL 8.4.4 / Redis 7.4.2 / Prometheus 3.2.1 / Loki 3.4.2。没有把使用缓存版本的实测当成默认镜像 fresh clone 已通过；amd64 未运行。

业务进程只加载 BUSINESS_MYSQL_DSN / BUSINESS_REDIS_ADDR 等显式环境参数，当前连接本项目隔离数据库，不加载原外部 config.yaml。原 Jaeger 容器未重建，以保留其内存 trace；提交配置的 localhost 端口绑定下次重建才完整应用。

## 已运行验证

- commerce 模块 `go test -race ./...`：签名有效/伪造/过期/动作目标限制、重定向不透传控制、CSRF/未知参数拒绝、等待可取消，通过。
- 四服务独立 `go test ./...`：编译及保留的旧模块测试通过。
- `python3 -m unittest discover -s scripts -p '*_test.py'`：10 项通过，包含旧 Jaeger 兼容、Compose 健康依赖、Loki 精确查询和纳秒、Go RFC3339Nano 在 Python 3.9 上的兼容、无样本语义、脱敏、trace ID 校验、查询层无控制模块依赖。
- 四个新业务镜像真实 `otel go build` 成功，不是普通 Go 二进制替身。
- `docker compose config --quiet` 与固定 Collector 0.123.0 的 validate 通过。
- `python3 scripts/smoke-commerce.py --controls`：真实下单重复请求不重复扣库存；重复支付保持 paid；重复取消只恢复一次库存；支付真实 MySQL 1146 后订单 payment_failed、库存恢复；四业务随机故障均有生效回执和独立业务 TraceId；18086/18083 代理通过。
- 四个 Skill frontmatter 验证通过，三个查询脚本实测访问后端，不依赖 aiops-api。
- Playwright 实际点击“模拟下单异常”，完成 45 秒窗口并展示请求、历史、复制上下文入口和人工揭晓。诊断前没有点击揭晓。
- 最终版本在 18083 入口验证：停止窗口后状态为 cancelled，等待后请求数不再增长；随后单次健康商品对照返回 200，未被之前控制污染。最后四个业务容器均 healthy，旧 aiops-api 和旧四服务不在运行列表。
- 桌面与 390px 移动视口截图已检查；移动页 scrollWidth=390，无水平溢出。18083 同样显示四业务卡片；页面排障上下文中不存在 target/action/receipts/confirmed 字段。截图位于 output/playwright/commerce-desktop.png 与 commerce-mobile.png（忽略目录）。

## 真实综合排障示例

[business-incident-example.md](business-incident-example.md) 是本次 AI 会话依据综合 Skill 形成的诊断；[business-incident-evidence.json](business-incident-evidence.json) 保存对应三个后端净化回读。

run_id `fa02f34e0ff8c8788170f9befc52d732`：正常 15 次 201、故障窗口 20 次 500、恢复 10 次 201，另有 1 次预检。代表 trace `3aef1819fb5839f5b8bbb90777993605` 查询到 20 spans，Loki 有 11 条精确日志，Prometheus 7 个查询均有实际数据。

先保存报告：库存服务查询不存在的表导致 MySQL 1146，订单/网关为传播；商品和订单自身数据库操作提供反证。之后才读取答案：target=inventory-service、action=missing_table、confirmed=true，20 条执行回执。报告没有根据揭晓内容倒改结论。

这次是实际 AI 会话 + 三后端证据的诊断演示，不是 fixture 或规则答案；本会话参与开发、具备源码背景，**不称为技术隔离盲测或准确率评测**。没有调用额外收费模型 API。

## 实测中修正的问题

- 自动埋点会传播协程上下文，最初预检/故障请求共用控制 trace。改为每条 driver 业务请求用 agent 所属 provider 创建 WithNewRoot span，再经过 Traefik；实测每条请求 TraceId 不同。driver 标记为 demo.traffic_driver，不作为业务根因证据。
- 幂等查找中“新订单/预占尚不存在”是正常状态，改用有界 Find/RowsAffected，避免将普通首次创建误记为依赖错误。
- 保留真实 SQL/Redis 错误、订单状态和补偿日志，控制 token/receipt 不写入业务日志，复制现场不含目标/动作/答案。
- 45 秒窗口短于指标 1 分钟滚动口径；示例明确服务级分母包含正常取消/释放请求，没有把窗口错误率冒充该批订单的精确失败比例。
- 指标预初始化改为按各服务真实路由注册，避免商品/库存服务出现无实际接口的 /orders 零值系列；旧历史 TSDB 样本会按保留策略自然过期。

## 未验证与限制

默认新依赖版本 fresh clone、amd64、长期压力与崩溃恢复尚未验证。演示历史有意改为进程内最近 100 条，重启会丢控制记录但不重放故障；遥测后端和业务数据库按各自保留策略运行。

业务状态机是教学实现，串行订单编排、单实例控制器不是生产事务保证。数据库在关键提交点中断可能需要人工对账。短窗口/单次请求不提供可靠 P95 统计；健康 trace 可能不保留。CMDB reference 仅描述正常拓扑，不能当当前健康或根因答案。
