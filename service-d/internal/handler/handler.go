package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go-otel-demo/service-d/internal/config"
	"go-otel-demo/service-d/internal/database"

	"github.com/gin-gonic/gin"
	"go-otel-demo/service-d/internal/exercise"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Handler struct {
	cfg *config.Config
	db  *database.Store
}

func New(cfg *config.Config, db *database.Store) *Handler {
	return &Handler{cfg: cfg, db: db}
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
	router.GET("/mysql/ok", h.MySQLOK)
	router.GET("/mysql/error", h.MySQLError)
	router.GET("/mysql/slow", h.MySQLSlow)
}

func (h *Handler) registerExercise(router *gin.Engine) {
	exercise.Register(router, h.cfg.Server.Name, nil, func(ctx context.Context, action string) error {
		if action == "missing_table" {
			return h.db.QueryBrokenTable(ctx)
		}
		if action == "slow_query" {
			_, err := h.db.SlowListUsers(ctx, 4*time.Second)
			return err
		}
		_, err := h.db.ListUsers(ctx)
		return err
	})
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": h.cfg.Server.Name,
	})
}

func (h *Handler) MySQLOK(c *gin.Context) {
	users, err := h.db.ListUsers(c.Request.Context())
	if err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"users":   users,
	})
}

func (h *Handler) MySQLError(c *gin.Context) {
	if err := h.db.QueryBrokenTable(c.Request.Context()); err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	h.fail(c, http.StatusInternalServerError, fmt.Errorf("mysql error endpoint did not fail"))
}

func (h *Handler) MySQLSlow(c *gin.Context) {
	delay := 4 * time.Second
	users, err := h.db.SlowListUsers(c.Request.Context(), delay)
	if err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"delay":   delay.String(),
		"users":   users,
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
