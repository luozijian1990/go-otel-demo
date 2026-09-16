package redisstore

import (
	"context"
	"fmt"
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

func (c *Client) SetGet(ctx context.Context, key, value string) (string, error) {
	if err := c.client.Set(ctx, key, value, time.Minute).Err(); err != nil {
		return "", fmt.Errorf("redis set: %w", err)
	}
	got, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return "", fmt.Errorf("redis get: %w", err)
	}
	return got, nil
}

func (c *Client) BrokenOperation(ctx context.Context) error {
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

func (c *Client) Close() error {
	return c.client.Close()
}
