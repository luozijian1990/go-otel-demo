# 综合 Skill 实际排障示例：下单失败

诊断形成于 2026-09-19。使用当前 AI 会话执行综合 Skill：读取应用关系 reference，再查询 Jaeger、Loki、Prometheus。形成以下结论前没有访问本次 answer 接口。开发会话知道项目实现背景，因此这是实际遥测驱动的演示验证，不是技术隔离的黑盒盲测，也不是准确率 benchmark。

## 结论

下单失败的直接原因是 **inventory-service 在处理库存预占时查询了不存在的 MySQL 表**，MySQL 返回 1146 / 42S02。失败从库存服务传播到订单服务，再由网关返回 HTTP 500。

证据不足以断言 MySQL 实例宕机、库存不足或宿主机资源异常；同一请求中订单服务对同一 MySQL 实例的操作成功，商品查询和 Redis 访问也成功。

## 现场与业务影响

- run_id：fa02f34e0ff8c8788170f9befc52d732
- 代表 TraceId：3aef1819fb5839f5b8bbb90777993605
- request_id：97b70428210aa4e5a25912884a325f9d
- 业务入口：POST /orders
- UTC 异常窗口：08:18:05.845731 至 08:18:25.846495。
- 公开现场记录：正常阶段 15 次 HTTP 201；异常阶段 20 次 HTTP 500；恢复阶段 10 次 HTTP 201，另有 1 次正常预检。
- 代表请求耗时约 16ms，属于快速失败。订单曾写入数据库，随后状态更新为 failed；不能概括为“完全没有创建订单记录”。

实际路径：演示 driver → Traefik → order-service → product-service（成功）→ inventory-service（SQL 失败）→ 订单记录 failed → HTTP 500。payment-service 不在这条下单 trace 中。

## 证据

| 来源 | 证据 ID | 观测 |
| --- | --- | --- |
| Jaeger | T-eb67a02ff0b9ddd6 | inventory-service 的 SELECT inventory_service_journal_unavailable 为 ERROR；原生 Error 1146，表不存在 |
| Jaeger | T-675db31cbd85c400 | POST /reservations 返回 500，与该 SQL 异常相连 |
| Jaeger | T-1bd0014feeffd868 | POST /orders 的错误说明下游 inventory-service 返回 500，属于传播 |
| Jaeger | T-864f3a70334fc9b4、T-7221dd63bc0ecf8f | 商品请求 HTTP 200，Redis get 无错误 |
| Jaeger | T-e0d198794a0b2bff、T-7fa1fbb1c1b164e9 | 订单 INSERT 与 UPDATE 成功，反驳共享 MySQL 完全不可访问 |
| Loki | L-ef75da16530dccdd | 同一 TraceId/request_id 的库存依赖日志记录 1146，实际耗时 4.087ms |
| Loki | L-02a1d81846f33e1f | 同一请求的订单状态变为 failed |
| Prometheus | M-2e2948c6006054cb | 库存 MySQL 依赖错误率出现非零值；订单 MySQL 错误率为零 |
| Prometheus | M-dbaef9173aac0f8a | 订单与库存 HTTP 错误率非零，商品服务为零；支付无业务流量时为 NaN |
| Prometheus | M-5de9295cd3fbadc1 | 四服务 scrape up=1，采集目标没有同时失联 |

## 查询与限制

```bash
python3 .agents/skills/jaeger-trace-rootcause/scripts/query.py --trace-id 3aef1819fb5839f5b8bbb90777993605 --wait 5 --start 2026-09-19T08:17:45Z --end 2026-09-19T08:18:45Z
python3 .agents/skills/loki-incident-logs/scripts/query.py --trace-id 3aef1819fb5839f5b8bbb90777993605 --start 2026-09-19T08:17:45Z --end 2026-09-19T08:18:45Z
python3 .agents/skills/prometheus-incident-metrics/scripts/query.py --start 2026-09-19T08:17:45Z --end 2026-09-19T08:19:00Z
```

Jaeger 返回 20 spans；Loki 返回 11 条精确关联日志；Prometheus 的 7 个固定查询有实际数据。完整净化回读保存在同目录 business-incident-evidence.json。

指标使用滚动 1 分钟窗口，服务级分母含正常下单后的取消/释放请求；不能把窗口百分比直接等同于该批下单的 20/20 失败。恢复后滚动错误率仍可能非零，公开请求记录则已恢复 201。时间短、样本小，不把 P95 当可靠根因证据。up=1 只证明 scrape 成功，不证明全部业务正常。

只读下一步：核对库存服务实际查询的表名与正常数据模型、目标 schema 和部署迁移版本；若用于真实环境，再检查发布变更。当前已知证据支持“表不存在”，尚未证明是迁移漏执行、配置错误还是代码引用错误。没有执行任何修复。
