package exercise

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestControlBoundary(t *testing.T) {
	t.Setenv("DEMO_FAULTS_ENABLED", "true")
	t.Setenv("DEMO_FAULT_SECRET", strings.Repeat("s", 32))
	sign := func(v Control) string {
		b, _ := json.Marshal(v)
		p := base64.RawURLEncoding.EncodeToString(b)
		m := hmac.New(sha256.New, []byte(strings.Repeat("s", 32)))
		m.Write([]byte(p))
		return p + "." + hex.EncodeToString(m.Sum(nil))
	}
	good := Control{"opaque", "service-c", "application_delay", time.Now().Unix() + 20}
	if _, err := Verify(sign(good), "opaque"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []Control{{"other", "service-c", "application_delay", good.Expires}, {"opaque", "service-c", "shell", good.Expires}, {"opaque", "service-d", "application_delay", good.Expires}, {"opaque", "service-c", "application_delay", time.Now().Unix() - 1}, {"opaque", "service-c", "application_delay", time.Now().Unix() + 100}} {
		if _, err := Verify(sign(v), "opaque"); err == nil {
			t.Fatalf("accepted invalid control: %+v", v)
		}
	}
	if _, err := Verify(sign(good)+"0", "opaque"); err == nil {
		t.Fatal("accepted tampered signature")
	}
	t.Setenv("DEMO_FAULTS_ENABLED", "false")
	if _, err := Verify(sign(good), "opaque"); err == nil {
		t.Fatal("accepted disabled control")
	}
	if v, err := Verify("", ""); err != nil || v.Action != "" {
		t.Fatal("normal request affected")
	}
}
func TestWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if Wait(ctx, 4*time.Second) == nil || time.Since(start) > time.Second {
		t.Fatal("wait did not cancel")
	}
}

func TestClientDoesNotForwardControlAcrossRedirect(t *testing.T) {
	var forwarded atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	headers := http.Header{"X-Demo-Fault-Token": []string{"private-control"}}
	result := call(context.Background(), source.URL, headers)
	if result.status != 307 || forwarded.Load() {
		t.Fatal("control followed redirect")
	}
}
