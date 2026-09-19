package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
	"gopkg.in/natefinch/lumberjack.v2"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

var Service = os.Getenv("OTEL_SERVICE_NAME")
var requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "demo_http_requests_total", Help: "Completed business requests"}, []string{"service_name", "http_route", "method", "status_class"})
var duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "demo_http_request_duration_seconds", Help: "Business request latency", Buckets: []float64{.005, .01, .05, .1, .5, 1, 2, 3, 4, 5, 8, 12}}, []string{"service_name", "http_route", "method"})
var calls = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "demo_dependency_calls_total", Help: "Actual dependency operations"}, []string{"service_name", "dependency", "operation", "outcome"})
var depDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "demo_dependency_duration_seconds", Help: "Actual dependency duration", Buckets: []float64{.005, .01, .05, .1, .5, 1, 2, 3, 4, 5, 8, 12}}, []string{"service_name", "dependency", "operation"})
var fallbacks = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "demo_fallback_total", Help: "Executed fallbacks"}, []string{"service_name", "dependency", "reason"})
var inflight = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "demo_http_inflight_requests", Help: "Active business requests"}, []string{"service_name"})

type contextKey string

func Init(service string) {
	Service = service
	var out io.Writer = os.Stdout
	if path := os.Getenv("DEMO_LOG_FILE"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
		if err != nil {
			panic(err)
		}
		f.Close()
		out = io.MultiWriter(os.Stdout, &lumberjack.Logger{Filename: path, MaxSize: 10, MaxBackups: 3, MaxAge: 7})
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			a.Key = "timestamp"
		}
		if a.Key == slog.MessageKey {
			a.Key = "message"
		}
		return a
	}})).With("service_name", service))
	prometheus.MustRegister(requests, duration, calls, depDuration, fallbacks, inflight)
	for _, status := range []string{"2xx", "4xx", "5xx"} {
		requests.WithLabelValues(service, "/exercise", "POST", status).Add(0)
	}
	for _, dep := range []string{"mysql", "redis"} {
		for _, op := range []string{"query", "set_get", "get"} {
			for _, outcome := range []string{"ok", "error"} {
				calls.WithLabelValues(service, dep, op, outcome).Add(0)
			}
		}
	}
	fallbacks.WithLabelValues(service, "redis", "unavailable").Add(0)
}
func Log(ctx context.Context, level slog.Level, msg string, attrs ...any) {
	sc := trace.SpanContextFromContext(ctx)
	base := []any{"request_id", ctx.Value(contextKey("request_id")), "experiment_id", ctx.Value(contextKey("experiment_id"))}
	if sc.IsValid() {
		base = append(base, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String(), "trace_sampled", sc.IsSampled())
	}
	// IDs are owned by this file pipeline; avoid automatic slog instrumentation appending duplicate keys.
	slog.Log(context.Background(), level, msg, append(base, attrs...)...)
}
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		route := c.FullPath()
		if route == "/metrics" || route == "/health" || route == "/ready" {
			c.Next()
			return
		}
		if route == "" {
			route = "unmatched"
		}
		rid := c.GetHeader("X-Request-Id")
		if rid == "" || len(rid) > 128 {
			var id [16]byte
			_, _ = rand.Read(id[:])
			rid = hex.EncodeToString(id[:])
		}
		c.Request.Header.Set("X-Request-Id", rid)
		c.Header("X-Request-Id", rid)
		ctx := context.WithValue(c.Request.Context(), contextKey("request_id"), rid)
		ctx = context.WithValue(ctx, contextKey("experiment_id"), c.GetHeader("X-Experiment-Id"))
		c.Request = c.Request.WithContext(ctx)
		start := time.Now()
		inflight.WithLabelValues(Service).Inc()
		c.Next()
		inflight.WithLabelValues(Service).Dec()
		elapsed := time.Since(start).Seconds()
		status := c.Writer.Status()
		method := c.Request.Method
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			method = "OTHER"
		}
		requests.WithLabelValues(Service, route, method, fmt.Sprintf("%dxx", status/100)).Inc()
		duration.WithLabelValues(Service, route, method).Observe(elapsed)
		Log(ctx, slog.LevelInfo, "request completed", "http_route", route, "http_method", c.Request.Method, "http_status_code", status, "duration_ms", elapsed*1000)
	}
}
func Propagate(ctx context.Context, headers http.Header) {
	for field, header := range map[string]string{"request_id": "X-Request-Id", "experiment_id": "X-Experiment-Id"} {
		if value, ok := ctx.Value(contextKey(field)).(string); ok && value != "" {
			headers.Set(header, value)
		}
	}
}

func Observe(ctx context.Context, dep, op string, start time.Time, err error) {
	outcome := "ok"
	level := slog.LevelInfo
	attrs := []any{"dependency", dep, "operation", op, "duration_ms", float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		outcome = "error"
		level = slog.LevelError
		attrs = append(attrs, "error_message", err.Error())
	}
	calls.WithLabelValues(Service, dep, op, outcome).Inc()
	depDuration.WithLabelValues(Service, dep, op).Observe(time.Since(start).Seconds())
	Log(ctx, level, "dependency operation", attrs...)
}
func Fallback(ctx context.Context) {
	fallbacks.WithLabelValues(Service, "redis", "unavailable").Inc()
	Log(ctx, slog.LevelWarn, "fallback served", "dependency", "redis")
}
func Register(r *gin.Engine, ready func(context.Context) error) {
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		c.Status(http.StatusOK)
	})
}
