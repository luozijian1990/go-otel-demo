package commerce

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go-otel-demo/commerce/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

type App struct {
	role      string
	db        *gorm.DB
	cache     *redis.Client
	peers     map[string]string
	orderGate chan struct{}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func Run(role string) {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		c := http.Client{Timeout: 2 * time.Second}
		r, e := c.Get("http://127.0.0.1:8080/ready")
		if e != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	telemetry.Init(role)
	var db *gorm.DB
	var err error
	for i := 0; i < 20; i++ {
		db, err = gorm.Open(mysql.Open(env("BUSINESS_MYSQL_DSN", "demo:demo-local-only@tcp(mysql:3306)/otel_demo?parseTime=true&timeout=3s&readTimeout=10s&writeTimeout=5s")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		slog.Error("database initialization failed")
		os.Exit(1)
	}
	a := &App{role: role, db: db, orderGate: make(chan struct{}, 1), peers: map[string]string{"product-service": env("PRODUCT_URL", "http://product-service:8080"), "inventory-service": env("INVENTORY_URL", "http://inventory-service:8080"), "payment-service": env("PAYMENT_URL", "http://payment-service:8080")}}
	a.cache = redis.NewClient(&redis.Options{Addr: env("BUSINESS_REDIS_ADDR", "redis:6379"), Password: os.Getenv("BUSINESS_REDIS_PASSWORD"), DisableIdentity: true, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, MaxRetries: 0})
	defer a.cache.Close()
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if err := a.migrate(); err != nil {
		slog.Error("business schema initialization failed", "error", err.Error())
		os.Exit(1)
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(telemetry.Middleware(), gin.CustomRecovery(func(c *gin.Context, _ any) { c.AbortWithStatus(500) }))
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		sc := trace.SpanContextFromContext(ctx)
		if sc.IsValid() {
			c.Header("X-Trace-Id", sc.TraceID().String())
		}
		c.Next()
	})
	telemetry.Register(r, func(ctx context.Context) error { return sqlDB.PingContext(ctx) })
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"service": role, "status": "ok"}) })
	a.routes(r)
	var demo *Demo
	if role == "order-service" {
		demo = newDemo()
		demo.register(r)
		defer demo.close()
	}
	server := &http.Server{Addr: ":8080", Handler: r, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		slog.Info("business service listening", "service", role)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "error", err.Error())
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	if demo != nil {
		demo.close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
func (a *App) fail(c *gin.Context, err error) {
	e := classify(err, operation(c.Request.URL.Path))
	status := e.Status()
	span := trace.SpanFromContext(c.Request.Context())
	level, message := slog.LevelWarn, "business operation rejected"
	if status >= 500 {
		span.RecordError(err)
		span.SetStatus(codes.Error, e.Code)
		level, message = slog.LevelError, "business operation failed"
	}
	telemetry.Log(c.Request.Context(), level, message, "error_message", err.Error(), "code", e.Code, "operation", e.Operation, "operation_outcome", e.Outcome, "upstream_service", e.Upstream)
	c.JSON(status, gin.H{"service": a.role, "error": e.Code, "code": e.Code, "category": e.Category, "operation": e.Operation, "operation_outcome": e.Outcome, "upstream_service": e.Upstream, "trace_id": span.SpanContext().TraceID().String(), "request_id": c.GetHeader("X-Request-Id"), "order_state": c.GetString("order_state"), "state_persisted": c.GetBool("state_persisted")})
}
func (a *App) fault() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctl, err := verify(c.GetHeader(faultHeader), c.GetHeader("X-Experiment-Id"))
		if err != nil {
			c.AbortWithStatusJSON(403, gin.H{"error": "invalid demo control"})
			return
		}
		if ctl.Target != a.role || ctl.Action == "cache_refused" {
			c.Set("control", ctl)
			c.Next()
			return
		}
		start := time.Now()
		ctx := c.Request.Context()
		switch ctl.Action {
		case "application_delay":
			err = wait(ctx, 3*time.Second)
		case "slow_sql":
			err = a.query(ctx, "query", func(db *gorm.DB) error { var v int; return db.Raw("SELECT SLEEP(3)").Scan(&v).Error })
		case "missing_table":
			err = a.query(ctx, "query", func(db *gorm.DB) error {
				var rows []map[string]any
				return db.Table(strings.ReplaceAll(a.role, "-", "_") + "_journal_unavailable").Find(&rows).Error
			})
		}
		effect := err == nil && time.Since(start) >= 2900*time.Millisecond
		if ctl.Action == "missing_table" {
			effect = err != nil && strings.Contains(err.Error(), "1146")
		}
		setReceipt(c, receipt{a.role, ctl.Action, true, effect})
		if err != nil {
			a.fail(c, &OperationError{Code: "internal_failure", Category: "internal_failure", Operation: operation(c.Request.URL.Path), Outcome: "not_applied", Cause: err})
			c.Abort()
			return
		}
		c.Next()
	}
}
func setReceipt(c *gin.Context, r receipt) {
	b, _ := json.Marshal(r)
	c.Header(receiptHeader, base64.RawURLEncoding.EncodeToString(b))
}

func (a *App) call(c *gin.Context, service, method, path string, body any, out any) error {
	op := operation(path)
	failure := func(code string, cause error) error {
		return &OperationError{Code: code, Category: "dependency_failure", Operation: op, Outcome: "unknown", Upstream: service, Cause: cause}
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return failure("invalid_dependency_response", err)
		}
	}
	base, ok := a.peers[service]
	if !ok {
		return failure("dependency_failure", errors.New("unknown peer"))
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), method, base+path, bytes.NewReader(data))
	if err != nil {
		return failure("dependency_failure", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, h := range []string{faultHeader, "X-Request-Id", "X-Experiment-Id"} {
		req.Header.Set(h, c.GetHeader(h))
	}
	otel.GetTextMapPropagator().Inject(req.Context(), propagation.HeaderCarrier(req.Header))
	res, err := peerHTTP.Do(req)
	if err != nil {
		code := "dependency_failure"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "dependency_timeout"
		}
		return failure(code, err)
	}
	defer res.Body.Close()
	if value := res.Header.Get(receiptHeader); value != "" {
		c.Header(receiptHeader, value)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 16001))
	if err != nil {
		return failure("invalid_dependency_response", err)
	}
	if len(b) > 16000 {
		return failure("invalid_dependency_response", errors.New("peer response exceeds limit"))
	}
	if res.StatusCode >= 300 {
		var wire struct {
			OperationError
			Service string `json:"service"`
			Message string `json:"error"`
		}
		if json.Unmarshal(b, &wire) == nil && wire.Service == service && validContract(&wire.OperationError, res.StatusCode, op) {
			e := wire.OperationError
			e.Upstream = service
			e.Cause = fmt.Errorf("%s: %s", service, e.Code)
			return &e
		}
		return failure("dependency_failure", fmt.Errorf("%s returned HTTP %d without a valid error contract", service, res.StatusCode))
	}
	if err := validateResponse(path, body, b); err != nil {
		return failure("invalid_dependency_response", err)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return failure("invalid_dependency_response", err)
		}
	}
	return nil
}

// Successful HTTP alone is not proof that a write was applied.
func validateResponse(path string, body any, b []byte) error {
	bad := errors.New("invalid peer business response")
	switch operation(path) {
	case "reserve":
		var r Reservation
		if json.Unmarshal(b, &r) != nil {
			return bad
		}
		want, ok := body.(Reservation)
		if !ok || r.OrderID != want.OrderID || r.SKU != want.SKU || r.Quantity != want.Quantity || (r.State != "held" && r.State != "confirmed") {
			return bad
		}
	case "payment":
		var p Payment
		if json.Unmarshal(b, &p) != nil {
			return bad
		}
		want, ok := body.(Payment)
		if !ok || p.OrderID != want.OrderID || p.Amount != want.Amount || p.State != "paid" {
			return bad
		}
	case "release", "confirm":
		var r Reservation
		if json.Unmarshal(b, &r) != nil {
			return bad
		}
		parts := strings.Split(path, "/")
		state := "released"
		if operation(path) == "confirm" {
			state = "confirmed"
		}
		if r.OrderID != parts[2] || r.State != state {
			return bad
		}
	case "product_lookup":
		var p Product
		if json.Unmarshal(b, &p) != nil || p.SKU != strings.TrimPrefix(path, "/products/") || p.Price <= 0 {
			return bad
		}
	default:
		if !json.Valid(b) {
			return bad
		}
	}
	return nil
}
