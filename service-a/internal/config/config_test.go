package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsYAMLConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`server:
  name: test-service
  port: 8080
otel:
  service_name: test-service
  endpoint: otel-collector:4317
  insecure: true
peer:
  service_b_url: http://service-b:8080
  service_a_url: http://service-a:8080
  service_c_url: http://service-c:8080
  service_d_url: http://service-d:8080
mysql:
  dsn: root:root@tcp(host.docker.internal:3306)/otel_demo?charset=utf8mb4&parseTime=True&loc=Local
redis:
  addr: host.docker.internal:6379
  password: ""
  db: 0
rabbitmq:
  url: amqp://guest:guest@host.docker.internal:5672/
  queue: otel-demo-queue
  exchange: ""
  routing_key: otel-demo-queue
  enabled: true
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.Name != "test-service" {
		t.Fatalf("server name = %q", cfg.Server.Name)
	}
	if cfg.OTel.Endpoint != "otel-collector:4317" {
		t.Fatalf("otel endpoint = %q", cfg.OTel.Endpoint)
	}
	if cfg.RabbitMQ.RoutingKey != "otel-demo-queue" {
		t.Fatalf("rabbitmq routing key = %q", cfg.RabbitMQ.RoutingKey)
	}
	if cfg.Peer.ServiceCURL != "http://service-c:8080" {
		t.Fatalf("peer service c url = %q", cfg.Peer.ServiceCURL)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "config.example.yaml")
	if _, err := Load(path); err != nil {
		t.Fatalf("example config is invalid: %v", err)
	}
}
