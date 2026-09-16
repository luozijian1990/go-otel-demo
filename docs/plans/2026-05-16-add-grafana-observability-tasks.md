# Tasks: Add Grafana Observability Stack

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Design:** @docs/plans/2026-05-16-add-grafana-observability-design.md

**Goal:** 增加可选 Grafana/Loki/Tempo/Alloy 观测栈，让 Traefik access log 中的 `TraceId` 能在 Grafana Explore 中跳转到 Tempo trace。

---

## 1. Overlay Compose

- [ ] 1.1 创建 `docker-compose.grafana.yml`，新增 `grafana`、`tempo`、`loki`、`alloy` 服务，禁止把这些服务合并进主 `docker-compose.yml`。
- [ ] 1.2 在 `docker-compose.grafana.yml` 中配置 Grafana 暴露 `3000:3000`，挂载 `grafana/provisioning`。
- [ ] 1.3 在 `docker-compose.grafana.yml` 中配置 Tempo 暴露 `3200:3200`，并在 compose 网络内提供 `tempo:4317` 给 Collector OTLP exporter 使用。
- [ ] 1.4 在 `docker-compose.grafana.yml` 中配置 Loki 暴露 `3100:3100`，供 Grafana 和 Alloy 使用。
- [ ] 1.5 在 `docker-compose.grafana.yml` 中配置 Alloy 挂载 `./logs/traefik:/logs/traefik:ro` 和 `./alloy/config.alloy:/etc/alloy/config.alloy:ro`，禁止使用 Promtail。

## 2. Tempo 配置

- [ ] 2.1 创建 `tempo/tempo.yaml`，启用 OTLP gRPC receiver 监听 `0.0.0.0:4317`。
- [ ] 2.2 在 `tempo/tempo.yaml` 中配置本地文件存储或本地临时存储，满足 Demo 查询即可，不做生产持久化。
- [ ] 2.3 确保 Tempo HTTP/API 监听 `0.0.0.0:3200`，供 Grafana datasource 访问。

## 3. Loki 配置

- [ ] 3.1 创建 `loki/loki.yaml`，配置单节点本地 Demo Loki。
- [ ] 3.2 Loki 配置只满足本地日志查询，不做生产级持久化、鉴权、多租户或 retention 策略扩展。

## 4. Alloy 日志采集

- [ ] 4.1 创建 `alloy/config.alloy`，通过 `local.file_match` 匹配 `/logs/traefik/access.log`。
- [ ] 4.2 在 `alloy/config.alloy` 中配置低基数 label：`job="traefik-access"`，禁止把 `TraceId` 配成 label。
- [ ] 4.3 在 `alloy/config.alloy` 中配置 `loki.source.file` 读取文件并转发到 `loki.write`。
- [ ] 4.4 在 `alloy/config.alloy` 中配置 `loki.write` 写入 `http://loki:3100/loki/api/v1/push`。

## 5. Collector 导出 Tempo

- [ ] 5.1 修改 `otel-collector-config.yaml`，新增 `otlp/tempo` exporter，endpoint 为 `tempo:4317`，TLS insecure。
- [ ] 5.2 修改 traces pipeline exporters，保留 `otlp/jaeger`，新增 `otlp/tempo`，禁止移除 Jaeger。
- [ ] 5.3 在注释中说明 Jaeger 与 Tempo 同时接收采样后的 trace。

## 6. Grafana Provisioning

- [ ] 6.1 创建 `grafana/provisioning/datasources/datasources.yaml`，配置 Tempo datasource，uid 固定为 `tempo`，url 为 `http://tempo:3200`。
- [ ] 6.2 在同一文件中配置 Loki datasource，uid 固定为 `loki`，url 为 `http://loki:3100`。
- [ ] 6.3 在 Loki datasource 的 `jsonData.derivedFields` 中配置 `TraceId` 匹配正则，从 Traefik JSON access log 中提取 `"TraceId":"<traceid>"`。
- [ ] 6.4 `derivedFields` 指向 `datasourceUid: tempo`，url 使用 trace id 原始值，确保点击 `TraceId` 可跳转 Tempo。

## 7. README

- [ ] 7.1 更新 `README.md`，说明增强观测栈启动命令：`docker compose -f docker-compose.yml -f docker-compose.grafana.yml up --build`。
- [ ] 7.2 更新 `README.md`，说明 Grafana 地址 `http://localhost:3000`。
- [ ] 7.3 更新 `README.md`，说明 Loki 查询 5xx：`{job="traefik-access"} | json | DownstreamStatus >= 500`。
- [ ] 7.4 更新 `README.md`，说明 Loki 查询慢请求：`{job="traefik-access"} | json | Duration > 3000000000`。
- [ ] 7.5 更新 `README.md`，说明从日志 `TraceId` 点击跳转 Tempo 的排障流程。
- [ ] 7.6 更新 `README.md`，说明普通成功请求可能因 tail sampling 在 Tempo 中查不到，错误和慢请求是主要验证对象。

## 8. 验证

- [ ] 8.1 执行 `docker compose -f docker-compose.yml -f docker-compose.grafana.yml config`，确认 overlay compose 可解析。
- [ ] 8.2 执行 `docker compose -f docker-compose.yml -f docker-compose.grafana.yml up --build`，确认 Grafana、Tempo、Loki、Alloy、Collector 启动。
- [ ] 8.3 通过 Traefik 请求 `/service-a/error`，确认 `logs/traefik/access.log` 出现 500 日志并包含 `TraceId`。
- [ ] 8.4 在 Grafana Explore 使用 Loki 查询 5xx 日志，确认能看到 Traefik access log。
- [ ] 8.5 点击日志中的 `TraceId`，确认跳转到 Tempo trace，并能看到 Traefik 与 service-a spans。

---

## Decision→Task 映射检查

- Decision 1：使用 overlay compose，不修改主 compose → Task 1.1、7.1、8.1 ✓
- Decision 2：使用 Alloy 采集 Traefik access log → Task 1.5、4.1、4.3、4.4 ✓
- Decision 3：Collector 同时导出 Jaeger 和 Tempo → Task 5.1、5.2、5.3 ✓
- Decision 4：用 Grafana datasource derived fields 做 TraceId 跳转 → Task 6.1、6.2、6.3、6.4 ✓
- Decision 5：不把 TraceId 做成 Loki label → Task 4.2 ✓
