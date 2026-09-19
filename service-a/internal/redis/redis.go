package redisstore

import (
	"context"
	"fmt"
	"go-otel-demo/service-a/internal/telemetry"
	"time"

	"go-otel-demo/service-a/internal/config"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	client *redis.Client
}

func New(cfg config.RedisConfig) (*Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:            cfg.Addr,
		Password:        cfg.Password,
		DB:              cfg.DB,
		DisableIdentity: true,
	})
	return &Client{client: client}, nil
}

func (c *Client) SetGet(ctx context.Context, key, value string) (_ string, resultErr error) {
	start := time.Now()
	defer func() { telemetry.Observe(ctx, "redis", "set_get", start, resultErr) }()
	if err := c.client.Set(ctx, key, value, time.Minute).Err(); err != nil {
		return "", fmt.Errorf("redis set: %w", err)
	}
	got, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return "", fmt.Errorf("redis get: %w", err)
	}
	return got, nil
}

func (c *Client) BrokenOperation(ctx context.Context) (resultErr error) {
	start := time.Now()
	defer func() { telemetry.Observe(ctx, "redis", "get", start, resultErr) }()
	bad := redis.NewClient(&redis.Options{
		Addr:            "127.0.0.1:1",
		DialTimeout:     50 * time.Millisecond,
		ReadTimeout:     50 * time.Millisecond,
		DisableIdentity: true,
	})
	defer func() {
		_ = bad.Close()
	}()

	err := bad.Get(ctx, "otel-demo-broken-key").Err()
	if err == nil {
		return fmt.Errorf("expected redis broken operation to fail")
	}
	return fmt.Errorf("redis broken operation: %w", err)
}

func (c *Client) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }

func (c *Client) Close() error {
	return c.client.Close()
}
