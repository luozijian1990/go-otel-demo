// Package exercise implements request-scoped controls; control metadata never becomes span/log data.
package exercise

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"go-otel-demo/service-a/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

type Control struct {
	Run     string `json:"run"`
	Target  string `json:"target"`
	Action  string `json:"action"`
	Expires int64  `json:"exp"`
}
type Receipt struct {
	Service string `json:"service"`
	Action  string `json:"action"`
	Applied bool   `json:"applied"`
	Effect  bool   `json:"effect"`
}

var actions = map[string]string{"app_error_a": "service-a", "app_error_b": "service-b", "missing_table": "service-d", "connect_refused": "service-c", "application_delay": "service-c", "slow_query": "service-d", "degraded": "service-c"}

func Verify(token, run string) (Control, error) {
	var v Control
	if token == "" {
		return v, nil
	}
	if os.Getenv("DEMO_FAULTS_ENABLED") != "true" || len(os.Getenv("DEMO_FAULT_SECRET")) < 32 {
		return v, errors.New("fault controls disabled")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return v, errors.New("invalid control")
	}
	mac := hmac.New(sha256.New, []byte(os.Getenv("DEMO_FAULT_SECRET")))
	mac.Write([]byte(parts[0]))
	sig, err := hex.DecodeString(parts[1])
	if err != nil || !hmac.Equal(mac.Sum(nil), sig) {
		return v, errors.New("invalid control")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return v, errors.New("invalid control")
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if dec.Decode(&v) != nil || v.Run != run || v.Run == "" || v.Expires < time.Now().Unix() || v.Expires > time.Now().Add(30*time.Second).Unix() || actions[v.Action] != v.Target {
		return Control{}, errors.New("invalid control")
	}
	return v, nil
}
func Wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type result struct {
	status  int
	body    string
	receipt string
	err     error
}

var client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func call(ctx context.Context, url string, headers http.Header) result {
	req, err := http.NewRequestWithContext(ctx, "POST", url+"/exercise", nil)
	if err != nil {
		return result{err: err}
	}
	for _, k := range []string{"X-Request-Id", "X-Experiment-Id", "X-Demo-Fault-Token"} {
		req.Header.Set(k, headers.Get(k))
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	resp, err := client.Do(req)
	if err != nil {
		return result{err: err}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	return result{resp.StatusCode, string(b), resp.Header.Get("X-Demo-Receipt"), err}
}
func Register(r *gin.Engine, service string, peers []string, operation func(context.Context, string) error) {
	r.POST("/exercise", func(c *gin.Context) {
		ctl, err := Verify(c.GetHeader("X-Demo-Fault-Token"), c.GetHeader("X-Experiment-Id"))
		if err != nil {
			c.JSON(403, gin.H{"error": "invalid experiment control"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 9*time.Second)
		defer cancel()
		c.Header("X-Request-Id", c.GetHeader("X-Request-Id"))
		action := ""
		if ctl.Target == service {
			action = ctl.Action
		}
		start := time.Now()
		if action == "app_error_a" || action == "app_error_b" {
			err = errors.New("business operation failed")
		} else if operation != nil {
			err = operation(ctx, action)
		}
		if action != "" {
			effect := err != nil
			switch action {
			case "missing_table":
				effect = err != nil && strings.Contains(err.Error(), "1146")
			case "connect_refused", "degraded":
				effect = err != nil && strings.Contains(err.Error(), "connection refused")
			case "application_delay", "slow_query":
				effect = err == nil && time.Since(start) >= 3900*time.Millisecond
			}
			b, _ := json.Marshal(Receipt{service, action, true, effect})
			c.Header("X-Demo-Receipt", base64.RawURLEncoding.EncodeToString(b))
		}
		if err == nil && len(peers) > 0 {
			ch := make(chan result, len(peers))
			headers := c.Request.Header.Clone()
			for _, url := range peers {
				go func(u string) { ch <- call(ctx, u, headers) }(url)
			}
			for range peers {
				res := <-ch
				if res.receipt != "" {
					c.Header("X-Demo-Receipt", res.receipt)
				}
				if res.err != nil {
					err = res.err
					continue
				}
				if res.status >= 300 {
					if service == "service-b" && ctl.Action == "degraded" && strings.Contains(res.body, "connection refused") {
						telemetry.Fallback(ctx)
						continue
					}
					err = fmt.Errorf("downstream request failed: %s", res.body)
				}
			}
		}
		if err != nil {
			span := trace.SpanFromContext(ctx)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			telemetry.Log(ctx, slog.LevelError, "business request failed", "error_message", err.Error())
			c.JSON(500, gin.H{"error": err.Error(), "service": service})
			return
		}
		trace.SpanFromContext(ctx).AddEvent("business operation completed")
		c.JSON(200, gin.H{"status": "ok", "service": service})
	})
}
