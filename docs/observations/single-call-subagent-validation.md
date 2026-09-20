# 单次故障链路与两个独立 subagent 验证

> 历史验证记录：以下内容保留当时的环境与结果；其中提及的旧源码、配置、脚本或临时路径可能已移除。当前运行方式以根 README 为准。

日期：2026-09-19。默认 UI 改为一条随机故障主调用，增加 Jaeger 实际 span 调用树。不使用固定拓扑补画未执行服务，不把注入目录当链路数据。

## 实现

- 页面顶部“随机模拟一次故障”由后端选择业务及故障；四个业务卡片也保持单次调用。
- single 模式只发送一条主业务调用，不再预检。支付建单准备和成功下单后的取消属于独立辅助调用，并在 UI/README 说明；它们不混入主 TraceId。
- `/demo/runs/:id/trace` 只读取该 run 真实主请求的 TraceId，再查询固定 Jaeger 后端。仅返回服务、操作、父子引用、耗时、状态、SQL/异常等展示字段，不返回控制头或答案。
- UI 最多等待 45 秒，未查到时说明可能延迟、未保留或后端不可用，并允许重新查询。仅 CHILD_OF 同 trace 引用连为父子；缺父节点单独显示，普通 span event 不自动判错。

## 测试输入隔离

仅启动两个 subagent，均 `fork_turns=none`，没有继承开发对话，也没有共享彼此结果。两者只获准读取四个观测 Skill、CMDB reference、查询 helper 和三个后端；禁止源码、memory、历史报告、故障目录、.env、控制/答案 API。没有启动其他诊断代理。

| 测试 | 提供的业务输入 | 结果 |
| --- | --- | --- |
| diagnose_with_trace | 请分析 TraceId 08f49c66f456d6899bb368aeba75ef20 | 定位库存 MySQL 1146，区分订单/网关传播、商品健康分支 |
| diagnose_from_symptom | 订单调用库存功能故障，帮我分析下问题在哪 | 自行选择近期 5 分钟，先查订单日志，发现同一个 TraceId，再用 trace/log/metrics 交叉验证 |

第二个没有获得 TraceId、时间、日志、故障类型或另一个代理结论。它报告窗口 08:44–08:49 UTC，发现一条符合描述的失败候选，无截断；没有把 CMDB 当答案表。

## 先报告、后对照

先保存 [TraceId 报告](subagent-trace-diagnosis.md) 与 [业务描述报告](subagent-symptom-diagnosis.md)，随后主代理才读取实际注入回执：

```json
{"target":"inventory-service","action":"missing_table","receipts":[{"service":"inventory-service","action":"missing_table","applied":true,"effect":true}],"confirmed":true}
```

对应 run_id：40c0c11a8611e306d30aab9d9c9f0ec2；request_id：41ec2ba675c39163ec9ffefec8a001f0；UTC 时间：08:47:23.630593–08:47:23.665328。

核对结果：两者根服务、组件、直接原因和业务影响均符合实际回执与原生证据；第二个独立发现的 TraceId 与真实请求完全相同。两者都识别商品缓存未命中已被处理，不把 redis: nil 当本次订单失败根因；也没有根据单次请求或短时 100% 错误率宣称持续大面积故障。

这是同一次故障的两种输入方式测试，**不是两个独立故障样本的准确率 benchmark**。代理具备工具/文件能力，隔离通过新上下文和明确访问约定实现，没有宣称 OS 强沙箱。完整调用记录在当前会话，文档保留最终诊断和查询重试说明。

## 软件与 UI 验证

- commerce `go test -race ./...` 通过；增加“single 只发一条请求，无预检”测试和 trace 规范化测试（普通 event、子错误、关系、单位与控制字段排除）。
- Python 10 项测试、Compose 配置检查、JS 语法与 git diff --check 通过。
- 新 order-service 镜像用 LoongSuite 自动埋点重建并启动，其余业务服务和观测后端保持运行。
- 真实浏览器选择该现场：页面主请求数为 1，Jaeger API 返回 22 spans，UI 实际渲染 22 个节点、8 个异常标记；标记不等同根因判断。
- 截图：output/playwright/single-trace-chain.png。截图来自真实数据，不是 mock。
- 两个诊断结束后实际点击顶部随机按钮：初始请求数 0、链路节点 0，页面显示等待；完成后主请求数 1，TraceId 7a3594b5a16c3be390ffd1c4165bebec，实际渲染 9 spans。390px 视口 scrollWidth=390，无水平溢出。

保留原本使用缓存依赖版本的环境限制；默认新依赖版本 fresh clone 和 amd64 未在本轮验证。源码尚未提交。
