package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go-otel-demo/service-b/internal/client"
	"go-otel-demo/service-b/internal/config"
	"go-otel-demo/service-b/internal/database"
	"go-otel-demo/service-b/internal/rabbitmq"
	redisstore "go-otel-demo/service-b/internal/redis"

	"github.com/gin-gonic/gin"
	"go-otel-demo/service-b/internal/exercise"
	"go-otel-demo/service-b/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Handler struct {
	cfg    *config.Config
	db     *database.Store
	redis  *redisstore.Client
	rabbit *rabbitmq.Publisher
	peer   *client.PeerClient
}

func New(cfg *config.Config, db *database.Store, redis *redisstore.Client, rabbit *rabbitmq.Publisher, peer *client.PeerClient) *Handler {
	return &Handler{
		cfg:    cfg,
		db:     db,
		redis:  redis,
		rabbit: rabbit,
		peer:   peer,
	}
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
	router.GET("/ok", h.OK)
	router.GET("/error", h.Error)
	router.GET("/slow", h.Slow)
	router.GET("/mysql/ok", h.MySQLOK)
	router.GET("/mysql/error", h.MySQLError)
	router.GET("/redis/ok", h.RedisOK)
	router.GET("/redis/error", h.RedisError)
	router.GET("/rabbitmq/ok", h.RabbitMQOK)
	router.GET("/rabbitmq/error", h.RabbitMQError)
	router.GET("/call/ok", h.CallOK)
	router.GET("/call/error", h.CallError)
	router.GET("/call/slow", h.CallSlow)
	router.GET("/full/ok", h.FullOK)
	router.GET("/full/error", h.FullError)
	router.GET("/chain/redis/ok", h.ChainRedisOK)
	router.GET("/chain/mysql/error", h.ChainMySQLError)
	router.GET("/chain/fanout/ok", h.ChainFanoutOK)
	router.GET("/chain/fanout/error", h.ChainFanoutError)
	router.GET("/chain/slow/redis", h.ChainSlowRedis)
	router.GET("/chain/degrade/ok", h.ChainDegradeOK)
}

func (h *Handler) registerExercise(router *gin.Engine) {
	exercise.Register(router, h.cfg.Server.Name, []string{h.cfg.Peer.ServiceCURL, h.cfg.Peer.ServiceDURL}, nil)
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": h.cfg.Server.Name,
	})
}

func (h *Handler) OK(c *gin.Context) {
	span := trace.SpanFromContext(c.Request.Context())
	span.SetAttributes(attribute.String("demo.result", "ok"))
	span.AddEvent("ordinary successful request")
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"status":  "ok",
	})
}

func (h *Handler) Error(c *gin.Context) {
	err := fmt.Errorf("intentional 5xx error from %s", h.cfg.Server.Name)
	h.fail(c, http.StatusInternalServerError, err)
}

func (h *Handler) Slow(c *gin.Context) {
	time.Sleep(5 * time.Second)
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"status":  "slow-ok",
		"delay":   "5s",
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

func (h *Handler) RedisOK(c *gin.Context) {
	key := fmt.Sprintf("otel-demo:%s:%d", h.cfg.Server.Name, time.Now().UnixNano())
	value := "hello-redis"
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

func (h *Handler) RabbitMQOK(c *gin.Context) {
	body := fmt.Sprintf("hello from %s at %s", h.cfg.Server.Name, time.Now().Format(time.RFC3339Nano))
	if err := h.rabbit.Publish(c.Request.Context(), body); err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"status":  "published",
	})
}

func (h *Handler) RabbitMQError(c *gin.Context) {
	if err := h.rabbit.PublishBroken(c.Request.Context()); err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	h.fail(c, http.StatusInternalServerError, fmt.Errorf("rabbitmq error endpoint did not fail"))
}

func (h *Handler) CallOK(c *gin.Context) {
	depth := parseDepth(c)
	if depth <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"service": h.cfg.Server.Name,
			"depth":   depth,
			"status":  "ok",
		})
		return
	}

	status, body, err := h.peer.CallPeer(c.Request.Context(), "/ok", depth-1)
	if err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service":     h.cfg.Server.Name,
		"peer_status": status,
		"peer_body":   body,
	})
}

func (h *Handler) CallError(c *gin.Context) {
	depth := parseDepth(c)
	_, body, err := h.peer.CallPeer(c.Request.Context(), "/error", depth-1)
	if err != nil {
		h.recordError(c, err)
		body := gin.H{
			"service":   h.cfg.Server.Name,
			"error":     err.Error(),
			"peer_body": body,
		}
		h.addTraceID(body, c.Request.Context())
		c.JSON(http.StatusInternalServerError, body)
		return
	}
	h.fail(c, http.StatusInternalServerError, fmt.Errorf("peer /error did not fail"))
}

func (h *Handler) CallSlow(c *gin.Context) {
	depth := parseDepth(c)
	status, body, err := h.peer.CallPeer(c.Request.Context(), "/slow", depth-1)
	if err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service":     h.cfg.Server.Name,
		"peer_status": status,
		"peer_body":   body,
	})
}

func (h *Handler) FullOK(c *gin.Context) {
	ctx := c.Request.Context()
	result := gin.H{"service": h.cfg.Server.Name, "status": "ok"}

	users, err := h.db.ListUsers(ctx)
	if err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	result["users"] = users

	key := fmt.Sprintf("otel-demo:full:%s:%d", h.cfg.Server.Name, time.Now().UnixNano())
	value, err := h.redis.SetGet(ctx, key, "full-ok")
	if err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	result["redis"] = gin.H{"key": key, "value": value}

	if err := h.rabbit.Publish(ctx, fmt.Sprintf("full ok from %s", h.cfg.Server.Name)); err != nil {
		trace.SpanFromContext(ctx).AddEvent("rabbitmq publish failed but full ok continues", trace.WithAttributes(attribute.String("error", err.Error())))
		result["rabbitmq_error"] = err.Error()
	} else {
		result["rabbitmq"] = "published"
	}

	status, body, err := h.peer.CallPeer(ctx, "/ok", 0)
	if err != nil {
		h.fail(c, http.StatusInternalServerError, err)
		return
	}
	result["peer"] = gin.H{"status": status, "body": body}

	c.JSON(http.StatusOK, result)
}

func (h *Handler) FullError(c *gin.Context) {
	ctx := c.Request.Context()
	var errors []string

	if err := h.db.QueryBrokenTable(ctx); err != nil {
		errors = append(errors, err.Error())
		h.recordCurrentSpanError(ctx, err)
	}
	if err := h.redis.BrokenOperation(ctx); err != nil {
		errors = append(errors, err.Error())
		h.recordCurrentSpanError(ctx, err)
	}
	if err := h.rabbit.PublishBroken(ctx); err != nil {
		errors = append(errors, err.Error())
		h.recordCurrentSpanError(ctx, err)
	}
	if _, body, err := h.peer.CallPeer(ctx, "/error", 0); err != nil {
		errors = append(errors, err.Error())
		if body != "" {
			errors = append(errors, "peer_body="+body)
		}
		h.recordCurrentSpanError(ctx, err)
	}
	if len(errors) == 0 {
		err := fmt.Errorf("full error endpoint forced failure")
		errors = append(errors, err.Error())
		h.recordCurrentSpanError(ctx, err)
	}

	body := gin.H{
		"service": h.cfg.Server.Name,
		"error":   "full error flow failed intentionally",
		"errors":  errors,
	}
	h.addTraceID(body, ctx)
	c.JSON(http.StatusInternalServerError, body)
}

func (h *Handler) ChainRedisOK(c *gin.Context) {
	status, body, err := h.peer.CallServiceC(c.Request.Context(), "/redis/ok")
	if err != nil {
		h.recordError(c, err)
		body := gin.H{
			"service": h.cfg.Server.Name,
			"error":   err.Error(),
			"redis":   branchResultBody(status, body, err),
		}
		h.addTraceID(body, c.Request.Context())
		c.JSON(statusOrDefault(status), body)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"status":  "chain-redis-ok",
		"redis":   branchResultBody(status, body, nil),
	})
}

func (h *Handler) ChainMySQLError(c *gin.Context) {
	status, body, err := h.peer.CallServiceD(c.Request.Context(), "/mysql/error")
	if err != nil {
		h.recordError(c, err)
		body := gin.H{
			"service": h.cfg.Server.Name,
			"error":   err.Error(),
			"mysql":   branchResultBody(status, body, err),
		}
		h.addTraceID(body, c.Request.Context())
		c.JSON(http.StatusInternalServerError, body)
		return
	}
	h.fail(c, http.StatusInternalServerError, fmt.Errorf("service-d /mysql/error did not fail"))
}

func (h *Handler) ChainFanoutOK(c *gin.Context) {
	redisResult, mysqlResult := h.callRedisAndMySQL(c.Request.Context(), "/redis/ok", "/mysql/ok")
	if redisResult.err != nil || mysqlResult.err != nil {
		err := fmt.Errorf("fanout ok branch failed")
		h.recordError(c, err)
		body := gin.H{
			"service": h.cfg.Server.Name,
			"error":   err.Error(),
			"redis":   redisResult.asJSON(),
			"mysql":   mysqlResult.asJSON(),
		}
		h.addTraceID(body, c.Request.Context())
		c.JSON(http.StatusInternalServerError, body)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"status":  "chain-fanout-ok",
		"redis":   redisResult.asJSON(),
		"mysql":   mysqlResult.asJSON(),
	})
}

func (h *Handler) ChainFanoutError(c *gin.Context) {
	redisResult, mysqlResult := h.callRedisAndMySQL(c.Request.Context(), "/redis/ok", "/mysql/error")
	if mysqlResult.err != nil {
		h.recordError(c, mysqlResult.err)
		body := gin.H{
			"service": h.cfg.Server.Name,
			"error":   mysqlResult.err.Error(),
			"redis":   redisResult.asJSON(),
			"mysql":   mysqlResult.asJSON(),
		}
		h.addTraceID(body, c.Request.Context())
		c.JSON(http.StatusInternalServerError, body)
		return
	}
	h.fail(c, http.StatusInternalServerError, fmt.Errorf("fanout error endpoint did not fail"))
}

func (h *Handler) ChainSlowRedis(c *gin.Context) {
	redisResult, mysqlResult := h.callRedisAndMySQL(c.Request.Context(), "/redis/slow", "/mysql/ok")
	if redisResult.err != nil || mysqlResult.err != nil {
		err := firstError(redisResult.err, mysqlResult.err)
		h.recordError(c, err)
		body := gin.H{
			"service": h.cfg.Server.Name,
			"error":   err.Error(),
			"redis":   redisResult.asJSON(),
			"mysql":   mysqlResult.asJSON(),
		}
		h.addTraceID(body, c.Request.Context())
		c.JSON(http.StatusInternalServerError, body)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service": h.cfg.Server.Name,
		"status":  "chain-slow-redis",
		"redis":   redisResult.asJSON(),
		"mysql":   mysqlResult.asJSON(),
	})
}

func (h *Handler) ChainDegradeOK(c *gin.Context) {
	redisResult, mysqlResult := h.callRedisAndMySQL(c.Request.Context(), "/redis/error", "/mysql/ok")
	result := gin.H{
		"service": h.cfg.Server.Name,
		"status":  "chain-degrade-ok",
		"redis":   redisResult.asJSON(),
		"mysql":   mysqlResult.asJSON(),
	}
	if redisResult.err != nil {
		h.recordCurrentSpanError(c.Request.Context(), redisResult.err)
		trace.SpanFromContext(c.Request.Context()).AddEvent("redis branch degraded", trace.WithAttributes(attribute.String("error", redisResult.err.Error())))
		result["redis_error"] = redisResult.err.Error()
	}
	if mysqlResult.err != nil {
		h.recordError(c, mysqlResult.err)
		h.addTraceID(result, c.Request.Context())
		c.JSON(http.StatusInternalServerError, result)
		return
	}
	if redisResult.err != nil {
		telemetry.Fallback(c.Request.Context())
	}
	c.JSON(http.StatusOK, result)
}

type downstreamResult struct {
	name     string
	status   int
	body     string
	err      error
	duration time.Duration
}

func (h *Handler) callRedisAndMySQL(ctx context.Context, redisPath, mysqlPath string) (downstreamResult, downstreamResult) {
	var wg sync.WaitGroup
	redisResult := downstreamResult{name: "redis"}
	mysqlResult := downstreamResult{name: "mysql"}

	wg.Add(2)
	go func() {
		defer wg.Done()
		start := time.Now()
		redisResult.status, redisResult.body, redisResult.err = h.peer.CallServiceC(ctx, redisPath)
		redisResult.duration = time.Since(start)
	}()
	go func() {
		defer wg.Done()
		start := time.Now()
		mysqlResult.status, mysqlResult.body, mysqlResult.err = h.peer.CallServiceD(ctx, mysqlPath)
		mysqlResult.duration = time.Since(start)
	}()
	wg.Wait()

	return redisResult, mysqlResult
}

func (r downstreamResult) asJSON() gin.H {
	body := branchResultBody(r.status, r.body, r.err)
	body["name"] = r.name
	body["duration_ms"] = float64(r.duration.Microseconds()) / 1000
	return body
}

func branchResultBody(status int, body string, err error) gin.H {
	result := gin.H{
		"status": status,
	}
	if body != "" {
		result["body"] = decodeBody(body)
	}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}

func decodeBody(body string) any {
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err == nil {
		return raw
	}
	return body
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("unknown downstream error")
}

func statusOrDefault(status int) int {
	if status == 0 {
		return http.StatusInternalServerError
	}
	return status
}

func (h *Handler) fail(c *gin.Context, status int, err error) {
	h.recordError(c, err)
	body := gin.H{
		"service": h.cfg.Server.Name,
		"error":   err.Error(),
	}
	if traceID := traceIDFromContext(c.Request.Context()); traceID != "" {
		body["trace_id"] = traceID
	}
	c.JSON(status, body)
}

func (h *Handler) addTraceID(body gin.H, ctx context.Context) {
	if traceID := traceIDFromContext(ctx); traceID != "" {
		body["trace_id"] = traceID
	}
}

func (h *Handler) recordError(c *gin.Context, err error) {
	h.recordCurrentSpanError(c.Request.Context(), err)
}

func (h *Handler) recordCurrentSpanError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func parseDepth(c *gin.Context) int {
	raw := c.DefaultQuery("depth", "1")
	depth, err := strconv.Atoi(raw)
	if err != nil {
		return 1
	}
	return depth
}

func traceIDFromContext(ctx context.Context) string {
	spanContext := trace.SpanFromContext(ctx).SpanContext()
	if !spanContext.IsValid() || !spanContext.TraceID().IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}
