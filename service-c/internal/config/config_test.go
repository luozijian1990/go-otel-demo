package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsRedisOnlyYAMLConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`server:
  name: service-c
  port: 8080
otel:
  service_name: service-c
  endpoint: otel-collector:4317
  insecure: true
redis:
  addr: host.docker.internal:6379
  password: ""
  db: 0
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.Name != "service-c" {
		t.Fatalf("server name = %q", cfg.Server.Name)
	}
	if cfg.Redis.Addr != "host.docker.internal:6379" {
		t.Fatalf("redis addr = %q", cfg.Redis.Addr)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "config.example.yaml")
	if _, err := Load(path); err != nil {
		t.Fatalf("example config is invalid: %v", err)
	}
}
