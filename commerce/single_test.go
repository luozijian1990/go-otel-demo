package commerce

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleSendsOnePrimaryRequestWithoutPreflight(t *testing.T) {
	var count atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.URL.Path != "/products/SKU-001" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("X-Trace-Id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		w.Write([]byte(`{"sku":"SKU-001"}`))
	}))
	defer server.Close()
	t.Setenv("BUSINESS_GATEWAY", server.URL)
	d := newDemo()
	d.runs["r"] = &demoRun{ID: "r", Start: time.Now(), Windows: map[string][]time.Time{}}
	d.answers["r"] = &answer{}
	d.done = make(chan struct{})
	d.execute(context.Background(), "r", demoSpec{Business: "product", Traffic: "single", Mode: "healthy"}, control{})
	if count.Load() != 1 || len(d.runs["r"].Requests) != 1 || d.runs["r"].Requests[0].Phase != "incident" {
		t.Fatal("single mode emitted extra business requests")
	}
}
