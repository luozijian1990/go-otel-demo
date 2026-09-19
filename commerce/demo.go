package commerce

// Local demonstration controls. Query Skills never read this module or answer endpoint.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type demoSpec struct {
	Business string `json:"business"`
	Traffic  string `json:"traffic"`
	Mode     string `json:"mode"`
}
type observedRequest struct {
	RequestID  string    `json:"request_id"`
	TraceID    string    `json:"trace_id"`
	Phase      string    `json:"phase"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	At         time.Time `json:"at"`
	Error      string    `json:"error,omitempty"`
}
type demoRun struct {
	ID       string                 `json:"id"`
	Business string                 `json:"business"`
	Traffic  string                 `json:"traffic"`
	State    string                 `json:"state"`
	Start    time.Time              `json:"start_at"`
	End      time.Time              `json:"end_at"`
	Requests []observedRequest      `json:"requests"`
	Windows  map[string][]time.Time `json:"windows"`
	Notice   string                 `json:"notice"`
}
type answer struct {
	Target    string    `json:"target"`
	Action    string    `json:"action"`
	Receipts  []receipt `json:"receipts"`
	Confirmed bool      `json:"confirmed"`
}
type Demo struct {
	mu      sync.Mutex
	runs    map[string]*demoRun
	answers map[string]*answer
	active  string
	cancel  context.CancelFunc
	done    chan struct{}
}

func newDemo() *Demo { return &Demo{runs: map[string]*demoRun{}, answers: map[string]*answer{}} }
func (d *Demo) close() {
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

var choices = map[string][]control{
	"product":   {{Target: "product-service", Action: "cache_refused"}, {Target: "product-service", Action: "slow_sql"}},
	"inventory": {{Target: "inventory-service", Action: "missing_table"}, {Target: "inventory-service", Action: "slow_sql"}},
	"order":     {{Target: "order-service", Action: "missing_table"}, {Target: "inventory-service", Action: "missing_table"}, {Target: "product-service", Action: "slow_sql"}},
	"payment":   {{Target: "payment-service", Action: "missing_table"}, {Target: "payment-service", Action: "slow_sql"}, {Target: "order-service", Action: "application_delay"}},
}

func (d *Demo) register(r *gin.Engine) {
	r.GET("/demo/runs/:id/trace", d.traceView)
	r.POST("/demo/runs", func(c *gin.Context) {
		if !operatorRequest(c) {
			return
		}
		var spec demoSpec
		if !bind(c, &spec) {
			return
		}
		if spec.Business == "random" {
			names := []string{"product", "inventory", "order", "payment"}
			spec.Business = names[rand.IntN(len(names))]
		}
		if spec.Traffic == "" {
			spec.Traffic = "single"
		}
		if _, ok := choices[spec.Business]; !ok {
			c.Status(400)
			return
		}
		if spec.Traffic != "single" && spec.Traffic != "window" {
			c.Status(400)
			return
		}
		if spec.Mode != "fault" && spec.Mode != "healthy" {
			c.Status(400)
			return
		}
		if spec.Mode == "fault" && (os.Getenv("DEMO_FAULTS_ENABLED") != "true" || len(os.Getenv("DEMO_FAULT_SECRET")) < 32) {
			c.JSON(503, gin.H{"error": "fault controls not configured"})
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.active != "" {
			c.JSON(409, gin.H{"error": "another demo is running"})
			return
		}
		if len(d.runs) >= 100 {
			var oldest string
			for id, run := range d.runs {
				if oldest == "" || run.Start.Before(d.runs[oldest].Start) {
					oldest = id
				}
			}
			delete(d.runs, oldest)
			delete(d.answers, oldest)
		}
		id := newID()
		ctl := control{}
		if spec.Mode == "fault" {
			pool := choices[spec.Business]
			ctl = pool[rand.IntN(len(pool))]
		}
		ctl.Run = id
		d.runs[id] = &demoRun{ID: id, Business: spec.Business, Traffic: spec.Traffic, State: "preparing", Start: time.Now().UTC(), Requests: []observedRequest{}, Windows: map[string][]time.Time{}, Notice: "单次请求不足以判断指标趋势；窗口指标可能含其他手工流量。历史仅保留本进程最近100次，重启不重放。"}
		d.answers[id] = &answer{Target: ctl.Target, Action: ctl.Action, Receipts: []receipt{}}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		d.cancel = cancel
		d.active = id
		d.done = make(chan struct{})
		go d.execute(ctx, id, spec, ctl)
		c.JSON(202, gin.H{"id": id})
	})
	r.GET("/demo/runs", func(c *gin.Context) { d.mu.Lock(); defer d.mu.Unlock(); c.JSON(200, d.runs) })
	r.GET("/demo/runs/:id", func(c *gin.Context) {
		d.mu.Lock()
		defer d.mu.Unlock()
		run, ok := d.runs[c.Param("id")]
		if !ok {
			c.Status(404)
			return
		}
		c.JSON(200, run)
	})
	r.POST("/demo/runs/:id/stop", func(c *gin.Context) {
		if !operatorRequest(c) {
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.active == c.Param("id") && d.cancel != nil {
			d.cancel()
		}
		c.JSON(200, gin.H{"status": "stop requested"})
	})
	r.GET("/demo/runs/:id/answer", func(c *gin.Context) {
		d.mu.Lock()
		defer d.mu.Unlock()
		id := c.Param("id")
		run, ok := d.runs[id]
		if !ok {
			c.Status(404)
			return
		}
		if run.State != "completed" && run.State != "cancelled" && run.State != "environment_unhealthy" {
			c.JSON(409, gin.H{"error": "wait for completion"})
			return
		}
		c.JSON(200, d.answers[id])
	})
}
func operatorRequest(c *gin.Context) bool {
	host := strings.Split(c.Request.Host, ":")[0]
	if host != "localhost" && host != "127.0.0.1" {
		c.AbortWithStatus(403)
		return false
	}
	if origin := c.GetHeader("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != c.Request.Host {
			c.AbortWithStatus(403)
			return false
		}
	}
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		c.AbortWithStatus(415)
		return false
	}
	return true
}
func (d *Demo) execute(ctx context.Context, id string, spec demoSpec, ctl control) {
	defer func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		r := d.runs[id]
		if ctx.Err() != nil {
			r.State = "cancelled"
		} else if r.State != "environment_unhealthy" {
			r.State = "completed"
		}
		r.End = time.Now().UTC()
		a := d.answers[id]
		a.Confirmed = ctl.Action == ""
		if len(a.Receipts) > 0 {
			a.Confirmed = true
			for _, receipt := range a.Receipts {
				a.Confirmed = a.Confirmed && receipt.Effect && receipt.Applied && receipt.Service == ctl.Target && receipt.Action == ctl.Action
			}
		}
		if r.State != "completed" {
			a.Confirmed = false
		}
		for _, request := range r.Requests {
			if (ctl.Action == "" || request.Phase != "incident") && (request.Status == 0 || request.Status >= 400) {
				a.Confirmed = false
			}
		}
		d.active = ""
		if d.cancel != nil {
			d.cancel()
		}
		d.cancel = nil
		close(d.done)
		d.done = nil
	}()
	if spec.Traffic == "single" {
		start := time.Now().UTC()
		d.mu.Lock()
		d.runs[id].State = "incident"
		d.mu.Unlock()
		row, rec, _ := d.request(ctx, id, spec.Business, "incident", ctl)
		d.mu.Lock()
		d.runs[id].Requests = append(d.runs[id].Requests, row)
		d.runs[id].Windows["incident"] = []time.Time{start, time.Now().UTC()}
		if ctl.Action != "" {
			d.answers[id].Receipts = append(d.answers[id].Receipts, rec)
		}
		d.mu.Unlock()
		return
	}
	phases := []struct {
		name    string
		seconds int
	}{{"incident", 1}}
	if spec.Traffic == "window" {
		phases = []struct {
			name    string
			seconds int
		}{{"baseline", 15}, {"incident", 20}, {"recovery", 10}}
	}
	// Actual topology preflight; any failure stops injection.
	pre, _, err := d.request(ctx, id, spec.Business, "preflight", control{})
	d.mu.Lock()
	d.runs[id].Requests = append(d.runs[id].Requests, pre)
	d.mu.Unlock()
	if err != nil || pre.Status >= 400 {
		d.mu.Lock()
		d.runs[id].State = "environment_unhealthy"
		d.mu.Unlock()
		return
	}
	for _, phase := range phases {
		start := time.Now().UTC()
		deadline := start.Add(time.Duration(phase.seconds) * time.Second)
		d.mu.Lock()
		d.runs[id].State = phase.name
		d.mu.Unlock()
		for time.Now().Before(deadline) {
			if ctx.Err() != nil {
				return
			}
			active := control{}
			if phase.name == "incident" {
				active = ctl
			}
			row, rec, _ := d.request(ctx, id, spec.Business, phase.name, active)
			d.mu.Lock()
			d.runs[id].Requests = append(d.runs[id].Requests, row)
			if active.Action != "" {
				d.answers[id].Receipts = append(d.answers[id].Receipts, rec)
			}
			d.mu.Unlock()
			if spec.Traffic == "single" {
				break
			}
			if err := wait(ctx, time.Until(minTime(deadline, row.At.Add(time.Second)))); err != nil {
				return
			}
		}
		d.mu.Lock()
		d.runs[id].Windows[phase.name] = []time.Time{start, time.Now().UTC()}
		d.mu.Unlock()
	}
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func (d *Demo) request(ctx context.Context, run, business, phase string, ctl control) (observedRequest, receipt, error) {
	gateway := env("BUSINESS_GATEWAY", "http://traefik")
	rid := newID()
	orderID := newID()
	method, path := "GET", "/products/SKU-001"
	var body any
	if business == "inventory" {
		path = "/inventory/SKU-001"
	}
	if business == "order" {
		method, path, body = "POST", "/orders", map[string]any{"id": orderID, "sku": "SKU-001", "quantity": 1}
	}
	if business == "payment" {
		r, _, err := demoHTTP(ctx, gateway, "POST", "/orders", map[string]any{"id": orderID, "sku": "SKU-001", "quantity": 1}, run, newID(), "")
		if err != nil || r.Status >= 400 {
			r.Phase = phase
			r.Error = "order preparation failed"
			return r, receipt{}, fmt.Errorf("order preparation failed")
		}
		method, path = "POST", "/orders/"+orderID+"/pay"
	}
	token := ""
	if ctl.Action != "" {
		ctl.Exp = time.Now().Unix() + 25
		token = sign(ctl)
	}
	row, rec, err := demoHTTP(ctx, gateway, method, path, body, run, rid, token)
	row.Phase = phase
	if business == "order" && row.Status < 400 && row.Status > 0 {
		_, _, _ = demoHTTP(ctx, gateway, "POST", "/orders/"+orderID+"/cancel", nil, run, newID(), "")
	}
	return row, rec, err
}
func demoHTTP(ctx context.Context, base, method, path string, body any, run, rid, token string) (observedRequest, receipt, error) {
	// A fresh root separates each business request from the control request and prior traffic.
	// Reuse the agent-owned provider; never initialize a second SDK.
	ctx, span := otel.Tracer("commerce-demo-driver").Start(ctx, "business.request", trace.WithNewRoot(), trace.WithSpanKind(trace.SpanKindClient))
	span.SetAttributes(attribute.Bool("demo.traffic_driver", true))
	defer span.End()
	row := observedRequest{RequestID: rid, At: time.Now().UTC()}
	var rec receipt
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(b))
	if err != nil {
		return row, rec, err
	}
	req.Host = "localhost"
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", rid)
	req.Header.Set("X-Experiment-Id", run)
	if token != "" {
		req.Header.Set(faultHeader, token)
	}
	res, err := peerHTTP.Do(req)
	row.DurationMS = time.Since(row.At).Milliseconds()
	if err != nil {
		row.Error = "business request did not complete"
		return row, rec, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 16384))
	row.Status = res.StatusCode
	row.TraceID = res.Header.Get("X-Trace-Id")
	value := res.Header.Get(receiptHeader)
	if value != "" {
		if data, e := base64.RawURLEncoding.DecodeString(value); e == nil {
			_ = json.Unmarshal(data, &rec)
		}
	}
	return row, rec, nil
}
