package commerce

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActualTraceNormalization(t *testing.T) {
	var raw jaegerTrace
	err := json.Unmarshal([]byte(`{"traceID":"abc","processes":{"p":{"serviceName":"order-service"}},"spans":[{"spanID":"root","processID":"p","operationName":"POST /orders","startTime":1000,"duration":6000,"tags":[{"key":"http.response.status_code","value":200}],"logs":[{"fields":[{"key":"event","value":"ordinary successful request"}]}]},{"spanID":"child","processID":"p","operationName":"SELECT inventory","startTime":2000,"duration":2000,"references":[{"refType":"CHILD_OF","spanID":"root","traceID":"abc"}],"tags":[{"key":"db.query.text","value":"SELECT inventory"},{"key":"http.request.header.x-demo-fault-token","value":"secret"}],"logs":[{"fields":[{"key":"exception.message","value":"Error 1146"}]}]},{"spanID":"link","processID":"p","references":[{"refType":"FOLLOWS_FROM","spanID":"root"}]}]}`), &raw)
	if err != nil {
		t.Fatal(err)
	}
	spans, truncated := normalizeTrace(raw)
	if truncated || len(spans) != 3 {
		t.Fatal("fake or missing spans")
	}
	if spans[0].Error || !spans[1].Error {
		t.Fatal("incorrect error classification")
	}
	if spans[1].Parent != "root" || spans[2].Parent != "" {
		t.Fatal("incorrect relationships")
	}
	if spans[1].Duration != 2 {
		t.Fatal("unit conversion")
	}
	b, _ := json.Marshal(spans)
	if strings.Contains(string(b), "secret") {
		t.Fatal("control header leaked into trace view")
	}
	var result any
	if json.Unmarshal(b, &result) != nil {
		t.Fatal("invalid output")
	}
}
