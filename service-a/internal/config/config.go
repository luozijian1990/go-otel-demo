package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	OTel     OTelConfig     `yaml:"otel"`
	Peer     PeerConfig     `yaml:"peer"`
	MySQL    MySQLConfig    `yaml:"mysql"`
	Redis    RedisConfig    `yaml:"redis"`
	RabbitMQ RabbitMQConfig `yaml:"rabbitmq"`
}

type ServerConfig struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

type OTelConfig struct {
	ServiceName string `yaml:"service_name"`
	Endpoint    string `yaml:"endpoint"`
	Insecure    bool   `yaml:"insecure"`
}

type PeerConfig struct {
	ServiceBURL string `yaml:"service_b_url"`
	ServiceAURL string `yaml:"service_a_url"`
	ServiceCURL string `yaml:"service_c_url"`
	ServiceDURL string `yaml:"service_d_url"`
}

type MySQLConfig struct {
	DSN string `yaml:"dsn"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type RabbitMQConfig struct {
	URL        string `yaml:"url"`
	Queue      string `yaml:"queue"`
	Exchange   string `yaml:"exchange"`
	RoutingKey string `yaml:"routing_key"`
	Enabled    bool   `yaml:"enabled"`
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = "config.yaml"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c Config) Validate() error {
	if c.Server.Name == "" {
		return fmt.Errorf("server.name is required")
	}
	if c.Server.Port <= 0 {
		return fmt.Errorf("server.port must be positive")
	}
	if c.OTel.ServiceName == "" {
		return fmt.Errorf("otel.service_name is required")
	}
	if c.OTel.Endpoint == "" {
		return fmt.Errorf("otel.endpoint is required")
	}
	if c.MySQL.DSN == "" {
		return fmt.Errorf("mysql.dsn is required")
	}
	if c.Redis.Addr == "" {
		return fmt.Errorf("redis.addr is required")
	}
	if c.RabbitMQ.Enabled {
		if c.RabbitMQ.URL == "" {
			return fmt.Errorf("rabbitmq.url is required when rabbitmq.enabled is true")
		}
		if c.RabbitMQ.Queue == "" {
			return fmt.Errorf("rabbitmq.queue is required when rabbitmq.enabled is true")
		}
		if c.RabbitMQ.RoutingKey == "" {
			return fmt.Errorf("rabbitmq.routing_key is required when rabbitmq.enabled is true")
		}
	}
	return nil
}
