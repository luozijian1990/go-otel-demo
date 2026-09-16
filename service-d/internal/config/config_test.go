package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsMySQLOnlyYAMLConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`server:
  name: service-d
  port: 8080
otel:
  service_name: service-d
  endpoint: otel-collector:4317
  insecure: true
mysql:
  dsn: root:root@tcp(host.docker.internal:3306)/otel_demo?charset=utf8mb4&parseTime=True&loc=Local
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.Name != "service-d" {
		t.Fatalf("server name = %q", cfg.Server.Name)
	}
	if cfg.MySQL.DSN == "" {
		t.Fatalf("mysql dsn is empty")
	}
}

func TestExampleConfigLoads(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "config.example.yaml")
	if _, err := Load(path); err != nil {
		t.Fatalf("example config is invalid: %v", err)
	}
}
