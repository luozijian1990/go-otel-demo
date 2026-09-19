package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go-otel-demo/service-c/internal/config"
	redisstore "go-otel-demo/service-c/internal/redis"

	"github.com/gin-gonic/gin"
	"go-otel-demo/service-c/internal/exercise"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Handler struct {
	cfg   *config.Config
	redis *redisstore.Client
}

func New(cfg *config.Config, redis *redisstore.Client) *Handler {
	return &Handler{cfg: cfg, redis: redis}
}

func TraceIDHeaderMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if traceID := traceIDFromContext(c.Request.Context()); traceID != "" {
			c.Header("X-Trace-Id", traceID)
		}
		c.Next()
	}
}

func (h *Handler) Register(router *gin.Engine) {
	h.registerExercise(router)
	router.GET("/health", h.Health)
	router.GET("/redis/ok", h.RedisOK)
	router.GET("/redis/error", h.RedisError)
	router.GET("/redis/slow", h.RedisSlow)
}

func (h *Handler) registerExercise(router *gin.Engine) {
	exercise.Register(router, h.cfg.Server.Name, nil, func(ctx context.Context, action string) error {
		if action == "application_delay" {
			if err := exercise.Wait(ctx, 4*time.Second); err != nil {
				return err
			}
		}
		if action == "connect_refused" || action == "degraded" {
			return h.redis.BrokenOperation(ctx)
		}
		_, err := h.redis.SetGet(ctx, fmt.Sprintf("demo:exercise:%d", time.Now().UnixNano()), "value")
		return err
	})
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": h.cfg.Server.Name,
	})
}

func (h *Handler) RedisOK(c *gin.Context) {
	key := fmt.Sprintf("otel-demo:chain:%s:%d", h.cfg.Server.Name, time.Now().UnixNano())
	value := "chain-redis-ok"
	got, err := h.redis.SetGet(c.Request.Context(), key, value)
	if err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"key":     key,
		"value":   got,
	})
}

func (h *Handler) RedisError(c *gin.Context) {
	if err := h.redis.BrokenOperation(c.Request.Context()); err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	h.fail(c, http.StatusInternalServerError, fmt.Errorf("redis error endpoint did not fail"))
}

func (h *Handler) RedisSlow(c *gin.Context) {
	delay := 4 * time.Second
	key := fmt.Sprintf("otel-demo:chain:slow:%s:%d", h.cfg.Server.Name, time.Now().UnixNano())
	got, err := h.redis.SlowOperation(c.Request.Context(), key, "chain-redis-slow", delay)
	if err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"key":     key,
		"value":   got,
		"delay":   delay.String(),
	})
}

func (h *Handler) fail(c *gin.Context, status int, err error) {
	h.recordCurrentSpanError(c.Request.Context(), err)
	body := gin.H{
		"service": h.cfg.Server.Name,
		"error":   err.Error(),
	}
	if traceID := traceIDFromContext(c.Request.Context()); traceID != "" {
		body["trace_id"] = traceID
	}
	c.JSON(status, body)
}

func (h *Handler) recordCurrentSpanError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func traceIDFromContext(ctx context.Context) string {
	spanContext := trace.SpanFromContext(ctx).SpanContext()
	if !spanContext.IsValid() || !spanContext.TraceID().IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}
