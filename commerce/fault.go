package commerce

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

const faultHeader = "X-Demo-Fault-Token"
const receiptHeader = "X-Demo-Receipt"

type control struct {
	Run    string `json:"run"`
	Target string `json:"target"`
	Action string `json:"action"`
	Exp    int64  `json:"exp"`
}
type receipt struct {
	Service string `json:"service"`
	Action  string `json:"action"`
	Applied bool   `json:"applied"`
	Effect  bool   `json:"effect"`
}

func allowed(c control) bool {
	if c.Target != "order-service" && c.Target != "payment-service" && c.Target != "inventory-service" && c.Target != "product-service" {
		return false
	}
	switch c.Action {
	case "missing_table", "slow_sql", "application_delay":
		return true
	case "cache_refused":
		return c.Target == "product-service"
	}
	return false
}
func sign(c control) string {
	b, _ := json.Marshal(c)
	p := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, []byte(os.Getenv("DEMO_FAULT_SECRET")))
	m.Write([]byte(p))
	return p + "." + hex.EncodeToString(m.Sum(nil))
}
func verify(token, run string) (control, error) {
	var c control
	if token == "" {
		return c, nil
	}
	if os.Getenv("DEMO_FAULTS_ENABLED") != "true" || len(os.Getenv("DEMO_FAULT_SECRET")) < 32 || len(token) > 1024 {
		return c, errors.New("invalid demo control")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c, errors.New("invalid demo control")
	}
	m := hmac.New(sha256.New, []byte(os.Getenv("DEMO_FAULT_SECRET")))
	m.Write([]byte(parts[0]))
	sig, err := hex.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, m.Sum(nil)) {
		return c, errors.New("invalid demo control")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, errors.New("invalid demo control")
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || c.Run != run || run == "" || c.Exp <= time.Now().Unix() || c.Exp > time.Now().Unix()+30 || !allowed(c) {
		return control{}, errors.New("invalid demo control")
	}
	return c, nil
}
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var peerHTTP = &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
