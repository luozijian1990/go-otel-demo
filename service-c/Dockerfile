# Historical recipe only. Active business images use ../Dockerfile with repository-root context.
FROM golang:1.25-bookworm AS builder

ENV GOPROXY=https://goproxy.cn/,direct

# Install loongsuite-go-agent CLI (binary name: otel).
# Documented install methods (per upstream README) are: prebuilt binary
# download, install.sh, or `make install` from source. There is no
# documented `go install` path. We use the prebuilt binary because it
# adds no CDN/script dependency and is unambiguous across architectures.
# TARGETARCH is supplied automatically by Docker buildx (amd64 / arm64).
ARG TARGETARCH
ARG OTEL_AGENT_VERSION=v1.10.0
RUN set -eux \
    && curl -fsSL -o /usr/local/bin/otel \
        "https://github.com/alibaba/loongsuite-go-agent/releases/download/${OTEL_AGENT_VERSION}/otel-linux-${TARGETARCH}" \
    && chmod +x /usr/local/bin/otel \
    && otel version

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
# `otel go build` invokes the standard Go toolchain via -toolexec, performing
# AST rewriting at compile time to inject OpenTelemetry instrumentation into
# this binary's dependencies (gin, gorm, go-redis, amqp091, net/http, ...).
# Application source code does NOT need manual otelgin/otelhttp/etc. imports.
RUN CGO_ENABLED=0 GOOS=linux otel go build -o /out/server ./cmd

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=builder /out/server /app/server
COPY --chown=65532:65532 logdir/ /var/log/demo/

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
