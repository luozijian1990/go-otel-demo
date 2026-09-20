# 商城演示应用关系（CMDB reference）

版本：2026-09-19 / business-v1。范围：本机 Docker Compose。这里描述正常设计，不表示实时健康。依据业务路由与数据模型维护；应用升级时同步核对。诊断时无需读取源码或故障目录。

## 应用目录

| 应用 / service.name | 职责 | HTTP 入口 | 存储 |
| --- | --- | --- | --- |
| product-service | 商品资料和价格；缓存不可用时回源 | GET /products/:sku | Redis 商品缓存；MySQL commerce_products |
| inventory-service | 库存查询、预占、释放和确认 | GET /inventory/:sku；POST /reservations；POST /reservations/:id/release 或 confirm | MySQL commerce_stock、commerce_reservations |
| order-service | 下单、支付编排、订单状态 | POST /orders；GET /orders/:id；POST /orders/:id/pay 或 cancel | MySQL commerce_orders |
| payment-service | 本地支付记录与幂等检查，不连接真实支付机构 | POST /payments | MySQL commerce_payments |

四服务共享一个本地 MySQL 实例，各自拥有不同表，不直接读写其他服务的表。Redis 只承担商品缓存，不是库存的权威数据源。Traefik 是浏览器和演示业务流量入口。

## 正常调用关系

- 商品浏览：Traefik → product-service → Redis；未命中或缓存故障时 → MySQL。
- 库存查询：Traefik → inventory-service → MySQL。
- 提交订单：Traefik → order-service → product-service（确认价格）→ inventory-service（预占库存）→ 订单状态 awaiting_payment。
- 确认支付：Traefik → order-service → payment-service（本地支付记录）→ inventory-service（确认预占）→ 订单状态 paid。
- 支付明确失败：订单服务调用库存释放，订单变为 payment_failed；补偿失败变为 reconciliation_required。
- 支付网络结果未知：订单变为 payment_unknown，保留预占；相同订单可重试幂等支付，不能仅凭超时释放库存。
- 取消待支付订单：订单 → 库存释放 → cancelled。
- 下单演示流量成功后会另发无故障的取消请求清理预占。支付演示会先创建待支付订单；准备请求和主要业务请求有不同 TraceId。

调用路径表示设计顺序，不代表每次都执行完毕。上游提前失败时不能要求下游一定有 span；缓存命中时没有 MySQL span 是正常情况。

## 业务一致性

SKU 示例为 SKU-001，金额单位为分。库存以事务锁保护，重复预占不重复扣减，释放仅针对 held 状态且幂等。支付按 order_id 幂等，相同订单不同金额拒绝。订单编排在单实例内串行保护；这是教学模型，不是生产分布式事务保证。基础设施在关键提交时中断仍可能需要对账。

## 观测关联

- Jaeger service 名与表格相同。TraceId 来自实际业务请求返回的 X-Trace-Id；不要用创建演示控制请求的 trace。
- 演示流量由嵌入订单进程的 driver 发出；每次以独立 trace 的 business.request client span 开始，带 demo.traffic_driver=true，后续进入真实 Traefik 和业务服务。该 driver span 不是订单业务故障证据，也不是创建演示的控制请求。
- Loki 索引：service_name；trace_id、span_id、request_id、experiment_id、order_id 是 metadata。experiment_id 是一次本地演示的随机 ID，不表示故障类别。
- Prometheus 标签：service_name、http_route、method、status_class、dependency、operation、outcome；没有单请求或订单 ID。
- HTTP 与真实 SQL/Redis 耗时分开记录。fallback attempt counter 表示 Redis 技术故障后的降级尝试。
- 入口业务超时约 10 秒；服务间 HTTP 客户端上限 12 秒，并受入站 context 限制。超时后查询是否有迟到/缺失 span，不把最长父 span 直接判为根因。
- Collector 尾部采样：错误与超过 3 秒请求优先保留，普通成功约 1%；导出和持久化仍可能失败。
- 只采集业务应用日志与网关业务路径，不把控制接口、真实答案或 AI 报告采进证据。
- Redis 缓存故障可能仍返回 HTTP 200；订单/支付 HTTP 500 也可能只是下游传播。

## 本地查询端点

Jaeger http://localhost:16686；Loki http://localhost:13100；Prometheus http://localhost:19090。容器内部使用 jaeger:16686、loki:3100、prometheus:9090。这些是观测读取地址，不是 CMDB 实时探活结果。

## Recovery and gateway contract (2026-09-20)

- creating / reservation_unknown can be retried with identical order ID, SKU and quantity, using the persisted amount. Reservation retries return persisted held/confirmed; confirmed must not regress to awaiting_payment.
- payment_unknown is persisted before payment. Only a first attempt proven not_applied with no historical uncertainty can release inventory. Unknown followed by not_applied/conflict keeps inventory. Confirm failure after payment requires reconciliation_required.
- Cancellation persists cancel_pending before release and supports explicit retry. paid/payment_unknown/reconciliation_required reject cancellation. state_persisted=false never claims the requested new state was saved.
- Jaeger/Loki support service=traefik; business Prometheus templates cover four services only. Gateway may lack request_id/experiment_id; filters remain AND. Query gateway separately using a business TraceId. Time correlation alone is not an exact join.
- demo_fallback_total counts Redis technical-failure fallback attempts; demo_fallback_results_total distinguishes success/failure. Normal cache misses are excluded. Success means an alternative result was obtained and entered the response path, not client acknowledgement.
