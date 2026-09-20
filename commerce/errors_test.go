package commerce

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		status, want int
		code         string
	}{
		{"stock", `{"service":"inventory-service","error":"insufficient stock","code":"insufficient_stock","category":"business_rejection","operation":"reserve","operation_outcome":"not_applied"}`, 409, 409, "insufficient_stock"},
		{"known404", `{"service":"inventory-service","error":"not found","code":"resource_not_found","category":"business_rejection","operation":"reserve","operation_outcome":"not_applied"}`, 404, 404, "resource_not_found"},
		{"wrong_operation", `{"service":"inventory-service","error":"no stock","code":"insufficient_stock","category":"business_rejection","operation":"payment","operation_outcome":"not_applied"}`, 409, 500, "dependency_failure"},
		{"raw404", `<html>private upstream</html>`, 404, 500, "dependency_failure"},
		{"invalid", `{"error":"bad","code":"insufficient_stock","category":"internal_failure","operation":"reserve","operation_outcome":"not_applied"}`, 409, 500, "dependency_failure"},
		{"broken", `{"state":`, 200, 500, "invalid_dependency_response"},
		{"large", strings.Repeat("x", 16001), 200, 500, "invalid_dependency_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer s.Close()
			a := &App{role: "order-service", peers: map[string]string{"inventory-service": s.URL}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/orders", nil)
			var out Reservation
			err := a.call(c, "inventory-service", "POST", "/reservations", Reservation{OrderID: "x", SKU: "s", Quantity: 1}, &out)
			if err == nil {
				t.Fatal("invalid response accepted")
			}
			a.fail(c, err)
			var b map[string]any
			json.Unmarshal(w.Body.Bytes(), &b)
			if w.Code != tc.want || b["code"] != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if _, ok := b["error"].(string); !ok {
				t.Fatal("error must remain string")
			}
			if strings.Contains(w.Body.String(), "private upstream") {
				t.Fatal("raw body leaked")
			}
		})
	}
}
func TestWrappedRejection(t *testing.T) {
	err := fmt.Errorf("reserve: %w", errNoStock)
	if !errors.Is(err, errNoStock) {
		t.Fatal("unwrap lost")
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/reservations", nil)
	(&App{role: "inventory-service"}).fail(c, err)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"insufficient_stock"`) {
		t.Fatal(w.Body.String())
	}
}

func TestWriteResponseReadFailureIsUnknown(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte(`{"state":"paid"}`))
	}))
	defer s.Close()
	a := &App{peers: map[string]string{"payment-service": s.URL}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/orders/o/pay", nil)
	var p Payment
	err := a.call(c, "payment-service", "POST", "/payments", Payment{OrderID: "o", Amount: 100}, &p)
	var e *OperationError
	if !errors.As(err, &e) || e.Outcome != "unknown" || e.Code != "invalid_dependency_response" {
		t.Fatal(err)
	}
}
func TestErrorCarriesExistingCorrelation(t *testing.T) {
	tid, _ := trace.TraceIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	sid, _ := trace.SpanIDFromHex("bbbbbbbbbbbbbbbb")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/reservations", nil).WithContext(ctx)
	c.Request.Header.Set("X-Request-Id", "correlation-test")
	(&App{role: "inventory-service"}).fail(c, errNoStock)
	if !strings.Contains(w.Body.String(), tid.String()) || !strings.Contains(w.Body.String(), "correlation-test") {
		t.Fatal(w.Body)
	}
}
