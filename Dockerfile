FROM golang:1.25-bookworm AS builder
ENV GOPROXY=https://goproxy.cn/,direct
ARG TARGETARCH
ARG OTEL_AGENT_VERSION=v1.10.0
RUN set -eux \
    && curl -fsSL -o /usr/local/bin/otel \
        "https://github.com/alibaba/loongsuite-go-agent/releases/download/${OTEL_AGENT_VERSION}/otel-linux-${TARGETARCH}" \
    && chmod +x /usr/local/bin/otel \
    && otel version
ARG SERVICE_DIR
WORKDIR /src/${SERVICE_DIR}
COPY commerce/go.* /src/commerce/
COPY ${SERVICE_DIR}/go.* ./
RUN go mod download
COPY commerce /src/commerce
COPY ${SERVICE_DIR}/cmd ./cmd
RUN CGO_ENABLED=0 GOOS=linux otel go build -o /out/server ./cmd
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/server /app/server
COPY --chown=65532:65532 service-a/logdir/ /var/log/demo/
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
