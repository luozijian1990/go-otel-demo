package commerce

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"strings"
	"time"
)

type jaegerTag struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}
type jaegerRef struct {
	Type    string `json:"refType"`
	TraceID string `json:"traceID"`
	SpanID  string `json:"spanID"`
}
type jaegerSpan struct {
	ID         string      `json:"spanID"`
	Process    string      `json:"processID"`
	Operation  string      `json:"operationName"`
	Start      int64       `json:"startTime"`
	Duration   int64       `json:"duration"`
	References []jaegerRef `json:"references"`
	Tags       []jaegerTag `json:"tags"`
	Logs       []struct {
		Timestamp int64       `json:"timestamp"`
		Fields    []jaegerTag `json:"fields"`
	} `json:"logs"`
}
type jaegerTrace struct {
	ID        string       `json:"traceID"`
	Spans     []jaegerSpan `json:"spans"`
	Processes map[string]struct {
		Service string `json:"serviceName"`
	} `json:"processes"`
}
type viewSpan struct {
	ID         string   `json:"span_id"`
	Parent     string   `json:"parent_span_id"`
	Service    string   `json:"service"`
	Operation  string   `json:"operation"`
	Start      int64    `json:"start_us"`
	Duration   float64  `json:"duration_ms"`
	Error      bool     `json:"is_error"`
	Driver     bool     `json:"driver"`
	Status     any      `json:"http_status,omitempty"`
	Statement  string   `json:"statement,omitempty"`
	Exceptions []string `json:"exceptions"`
}

func normalizeTrace(t jaegerTrace) ([]viewSpan, bool) {
	out := make([]viewSpan, 0, len(t.Spans))
	truncated := len(t.Spans) > 300
	for i, s := range t.Spans {
		if i >= 300 {
			break
		}
		v := viewSpan{ID: s.ID, Service: t.Processes[s.Process].Service, Operation: s.Operation, Start: s.Start, Duration: float64(s.Duration) / 1000, Exceptions: []string{}}
		for _, r := range s.References {
			if r.Type == "CHILD_OF" && (r.TraceID == "" || strings.TrimLeft(r.TraceID, "0") == strings.TrimLeft(t.ID, "0")) {
				v.Parent = r.SpanID
				break
			}
		}
		for _, tag := range s.Tags {
			switch tag.Key {
			case "error":
				v.Error = v.Error || tag.Value == true
			case "otel.status_code":
				v.Error = v.Error || fmt.Sprint(tag.Value) == "ERROR"
			case "http.status_code", "http.response.status_code":
				v.Status = tag.Value
				if n, ok := tag.Value.(float64); ok && n >= 500 {
					v.Error = true
				}
			case "db.query.text", "db.statement":
				v.Statement = fmt.Sprint(tag.Value)
			case "demo.traffic_driver":
				v.Driver = tag.Value == true
			}
		}
		for _, event := range s.Logs {
			for _, field := range event.Fields {
				if field.Key == "exception.message" {
					v.Error = true
					message := fmt.Sprint(field.Value)
					if len(message) > 2000 {
						message = message[:2000]
					}
					v.Exceptions = append(v.Exceptions, message)
				}
			}
		}
		out = append(out, v)
	}
	return out, truncated
}

func (d *Demo) traceView(c *gin.Context) {
	d.mu.Lock()
	run, ok := d.runs[c.Param("id")]
	traceID := ""
	if ok {
		for _, r := range run.Requests {
			if r.Phase == "incident" && r.TraceID != "" {
				traceID = r.TraceID
				break
			}
		}
	}
	d.mu.Unlock()
	if !ok {
		c.JSON(404, gin.H{"error": "run not found"})
		return
	}
	if traceID == "" {
		c.JSON(200, gin.H{"status": "pending", "reason": "waiting for business TraceId"})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), "GET", env("JAEGER_QUERY_URL", "http://jaeger:16686")+"/api/traces/"+traceID, nil)
	if err != nil {
		c.JSON(200, gin.H{"status": "unavailable", "trace_id": traceID})
		return
	}
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		c.JSON(200, gin.H{"status": "unavailable", "trace_id": traceID, "reason": "Jaeger query failed"})
		return
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		c.JSON(200, gin.H{"status": "pending", "trace_id": traceID})
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4_000_001))
	if err != nil || len(data) > 4_000_000 || response.StatusCode != 200 {
		c.JSON(200, gin.H{"status": "unavailable", "trace_id": traceID})
		return
	}
	var raw struct {
		Data []jaegerTrace `json:"data"`
	}
	if json.Unmarshal(data, &raw) != nil {
		c.JSON(200, gin.H{"status": "unavailable", "trace_id": traceID})
		return
	}
	if len(raw.Data) == 0 {
		c.JSON(200, gin.H{"status": "pending", "trace_id": traceID})
		return
	}
	if strings.TrimLeft(raw.Data[0].ID, "0") != strings.TrimLeft(traceID, "0") {
		c.JSON(200, gin.H{"status": "unavailable", "reason": "trace ID mismatch"})
		return
	}
	spans, truncated := normalizeTrace(raw.Data[0])
	c.JSON(200, gin.H{"status": "available", "trace_id": traceID, "spans": spans, "truncated": truncated, "queried_at": time.Now().UTC(), "limitations": []string{"真实 span 父子关系；缺失父节点单独显示。未执行的服务不补画。", "错误标记表示观测异常，不自动等同根因；父 span 耗时包含子调用。", "尾部采样和导出可能延迟或丢失；查到 span 不保证链路完整。"}})
}
