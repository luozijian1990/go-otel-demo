package commerce

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignedRequestBoundary(t *testing.T) {
	t.Setenv("DEMO_FAULT_SECRET", strings.Repeat("s", 32))
	t.Setenv("DEMO_FAULTS_ENABLED", "true")
	good := control{Run: "opaque", Target: "payment-service", Action: "missing_table", Exp: time.Now().Unix() + 20}
	if _, err := verify(sign(good), "opaque"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []control{{"other", "payment-service", "missing_table", good.Exp}, {"opaque", "payment-service", "cache_refused", good.Exp}, {"opaque", "payment-service", "shell", good.Exp}, {"opaque", "payment-service", "missing_table", time.Now().Unix() - 1}, {"opaque", "payment-service", "missing_table", time.Now().Unix() + 50}} {
		if _, err := verify(sign(bad), "opaque"); err == nil {
			t.Fatal("invalid control accepted")
		}
	}
	if _, err := verify(sign(good)+"0", "opaque"); err == nil {
		t.Fatal("tampered token accepted")
	}
	t.Setenv("DEMO_FAULTS_ENABLED", "false")
	if _, err := verify(sign(good), "opaque"); err == nil {
		t.Fatal("disabled fault accepted")
	}
	if _, err := verify("", ""); err != nil {
		t.Fatal("healthy request blocked")
	}
}
func TestRedirectDoesNotForwardControls(t *testing.T) {
	var forwarded atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	_, _, err := demoHTTP(context.Background(), source.URL, "POST", "/orders", nil, "opaque", "req", "private-control")
	if err != nil {
		t.Fatal(err)
	}
	if forwarded.Load() {
		t.Fatal("followed redirect")
	}
}
func TestControlCSRFAndUnknownBusiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	d := newDemo()
	r := gin.New()
	d.register(r)
	for _, tc := range []struct {
		origin, body string
		status       int
	}{{"https://evil.invalid", `{}`, 403}, {"", `{"business":"shell","mode":"healthy","traffic":"single"}`, 400}, {"", `{"business":"order","mode":"healthy","traffic":"forever"}`, 400}} {
		req := httptest.NewRequest("POST", "http://localhost/demo/runs", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("status %d expected %d", w.Code, tc.status)
		}
	}
}
func TestWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if wait(ctx, 3*time.Second) == nil {
		t.Fatal("wait ignored cancel")
	}
}
