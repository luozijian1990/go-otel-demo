# Design: Add Grafana Observability Stack

## Context

当前 Demo 已包含：

- `service-a` / `service-b`：Go Gin 服务，上报 trace 到 `otel-collector`。
- `otel-collector`：执行 tail sampling 并导出到 Jaeger。
- `jaeger`：用于查看 trace。
- `traefik`：统一入口，写 JSON access log 到 `logs/traefik/access.log`，日志中包含 `TraceId`。

后续希望补充 Grafana 体验：在 Grafana 中查询 Traefik access log，看到 `TraceId` 后直接跳转到 Tempo trace。当前阶段只记录任务，是否实现由后续效果验证决定。

## Goals

- 新增一个可选 overlay compose：`docker-compose.grafana.yml`。
- overlay 中新增 Grafana、Tempo、Loki、Alloy。
- Alloy 只采集 Traefik access log：`logs/traefik/access.log`。
- Loki 保存 Traefik access log，Grafana Explore 可查询。
- Tempo 接收 `otel-collector` 导出的 trace。
- Grafana 自动 provision Loki 和 Tempo datasource。
- Loki datasource 配置 derived field，从 JSON 日志中的 `TraceId` 提取 trace id，并跳转到 Tempo。
- README 提供启动命令、LogQL 查询示例和排障流程。

## Non-Goals

- 不替换 Jaeger；Jaeger 和 Tempo 可以同时接收 trace。
- 不把 Go 服务日志接入 Loki。
- 不把 Traefik access log 通过 OTLP logs 发送到 Collector；先用 Alloy 采集文件日志。
- 不使用 Promtail；Promtail 已进入维护/EOL 路线，优先使用 Grafana Alloy。
- 不把 `TraceId` 配成 Loki label，避免高基数字段导致索引膨胀。
- 不做 Grafana dashboard JSON；本阶段核心能力是 Explore 中日志到 trace 的跳转。
- 不做生产级认证、持久化、告警、资源限制或多租户配置。

## Decisions

### Decision 1: 使用 overlay compose，而不是修改主 compose

**选择**：新增 `docker-compose.grafana.yml`，通过 `docker compose -f docker-compose.yml -f docker-compose.grafana.yml up --build` 启动。

**理由**：主 Demo 已能验证 Go OTel、Jaeger、Traefik。Grafana/Loki/Tempo/Alloy 是增强观测栈，资源更重。单独 overlay 可以让用户按需启用，不影响最小 Demo。

### Decision 2: 使用 Alloy 采集 Traefik access log

**选择**：Alloy 读取宿主机挂载进容器的 `/logs/traefik/access.log`，写入 Loki。

**理由**：用户当前只需要手工看 Traefik access log 并在 Grafana 中检索，文件采集最直接。Promtail 已不适合作为新方案首选。

### Decision 3: Collector 同时导出到 Jaeger 和 Tempo

**选择**：在 `otel-collector-config.yaml` 中保留 `otlp/jaeger`，新增 `otlp/tempo`，traces pipeline 同时导出到两个后端。

**理由**：Jaeger 已验证可用，不应为了 Grafana 体验破坏原链路。Tempo 是 Grafana 内 trace 查询后端，两者并行更适合 Demo 对比。

### Decision 4: 用 Grafana datasource derived fields 做 TraceId 跳转

**选择**：在 Loki datasource 的 `jsonData.derivedFields` 中配置 `TraceId` 正则匹配，并将 datasourceUid 指向 Tempo。

**理由**：用户要的是在日志里看到 `TraceId` 后直接点到 trace，这不是 dashboard 的职责，而是 datasource correlation/derived field 的职责。

### Decision 5: 不把 TraceId 做成 Loki label

**选择**：Alloy 给日志打低基数 label，例如 `job=traefik-access`，`TraceId` 保留在 JSON 日志内容中。

**理由**：TraceId 高基数，作为 label 会增加 Loki 索引压力。用 `| json` 解析字段即可满足排障查询。

## Risks / Trade-offs

### Risk 1: 普通请求日志有 TraceId，但 Tempo 查不到

**场景**：Collector tail sampling 丢弃普通成功请求。

**缓解**：README 明确说明该链路主要用于错误和慢请求；普通请求可能查不到 trace。必要时可以临时提高正常请求采样率。

### Risk 2: Grafana derived field 正则不匹配

**场景**：Traefik access log JSON 字段格式变化，导致 `TraceId` 不能自动变成 Tempo 链接。

**缓解**：用实际 `logs/traefik/access.log` 中的 `"TraceId":"..."` 格式写正则，并在验证任务中通过 Grafana provisioning 文件检查和手工 Explore 验证。

### Risk 3: Tempo OTLP 端口与 Collector exporter 配置不一致

**场景**：Tempo 未监听 `4317`，Collector 导出失败。

**缓解**：Tempo 配置显式启用 OTLP gRPC receiver，并在 `docker compose logs otel-collector tempo` 中验证无导出错误。

### Risk 4: Alloy 读取不到 access log

**场景**：volume 路径挂载错误或日志文件尚未生成。

**缓解**：overlay compose 复用 `./logs/traefik:/logs/traefik:ro`，README 提醒先通过 Traefik 打一次请求生成 access log。
