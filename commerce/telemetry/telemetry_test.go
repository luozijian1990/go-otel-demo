package telemetry

import (
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"gopkg.in/natefinch/lumberjack.v2"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoveryCountsOnceAndLabelsAreBounded(t *testing.T) {
	Init("test-service")
	r := gin.New()
	r.Use(Middleware(), gin.CustomRecovery(func(c *gin.Context, _ any) { c.AbortWithStatus(500) }))
	r.GET("/panic", func(c *gin.Context) { panic("test") })
	req := httptest.NewRequest("GET", "/panic", nil)
	req.Header.Set("X-Request-Id", "unique-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range families {
		for _, m := range family.Metric {
			for _, label := range m.Label {
				if label.GetValue() == "unique-secret" {
					t.Fatal("unbounded request label")
				}
			}
			if family.GetName() == "demo_http_requests_total" {
				for _, label := range m.Label {
					if label.GetName() == "http_route" && label.GetValue() == "/panic" {
						found = true
						if m.Counter.GetValue() != 1 {
							t.Fatal("request counted more than once")
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("missing completion metric")
	}
}

func TestRotationKeepsReadablePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	writer := &lumberjack.Logger{Filename: path, MaxSize: 1, MaxBackups: 3}
	chunk := strings.Repeat("x", 600000)
	if _, err := writer.Write([]byte(chunk)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(chunk)); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "app*.log"))
	if err != nil || len(files) != 2 {
		t.Fatalf("expected active and rotated file: %v", files)
	}
	for _, name := range files {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0640 {
			t.Fatalf("collector group cannot read mode %v", info.Mode())
		}
	}
}

func TestFallbackDoesNotDeclareSuccessBeforeResult(t *testing.T) {
	var output strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(old)
	Fallback(context.Background())
	if strings.Contains(output.String(), "served") || strings.Contains(output.String(), "succeeded") {
		t.Fatal(output.String())
	}
	if !strings.Contains(output.String(), "fallback_attempted") {
		t.Fatal(output.String())
	}
}

type rejection struct{}

func (rejection) Error() string          { return "insufficient stock" }
func (rejection) BusinessRejected() bool { return true }
func counterValue(c prometheus.Counter) float64 {
	var m dto.Metric
	c.Write(&m)
	return m.Counter.GetValue()
}
func TestRejectedDependencyAndFallbackCounters(t *testing.T) {
	ctx := context.Background()
	service := Service
	rejected := calls.WithLabelValues(service, "mysql", "reserve", "rejected")
	technical := calls.WithLabelValues(service, "mysql", "reserve", "error")
	beforeRejected, beforeError := counterValue(rejected), counterValue(technical)
	Observe(ctx, "mysql", "reserve", time.Now(), fmt.Errorf("wrapped: %w", rejection{}))
	if counterValue(rejected) != beforeRejected+1 || counterValue(technical) != beforeError {
		t.Fatal("business rejection counted as SQL failure")
	}
	Observe(ctx, "mysql", "reserve", time.Now(), errors.New("Error 1146 table unavailable"))
	if counterValue(technical) != beforeError+1 {
		t.Fatal("native SQL failure erased")
	}
	attempt := fallbacks.WithLabelValues(service, "redis", "unavailable")
	success := fallbackResults.WithLabelValues(service, "redis", "unavailable", "success")
	failure := fallbackResults.WithLabelValues(service, "redis", "unavailable", "failure")
	ba, bs, bf := counterValue(attempt), counterValue(success), counterValue(failure)
	Fallback(ctx)
	FallbackResult(ctx, errors.New("SQL unavailable"))
	Fallback(ctx)
	FallbackResult(ctx, nil)
	if counterValue(attempt) != ba+2 || counterValue(success) != bs+1 || counterValue(failure) != bf+1 {
		t.Fatal("fallback counter semantics")
	}
}
