package rabbitmq

import (
	"context"
	"fmt"
	"net"
	"time"

	"go-otel-demo/service-b/internal/config"

	amqp091 "github.com/rabbitmq/amqp091-go"
)

type Publisher struct {
	cfg config.RabbitMQConfig
}

// New keeps the (cfg, serviceName) signature so cmd/main.go does not need
// to change. serviceName is intentionally unused under loongsuite-go-agent:
// the manual tracer name is gone, and we rely on the agent to auto-instrument
// amqp091 publish calls (see observations doc — RabbitMQ is the key unknown).
func New(cfg config.RabbitMQConfig, serviceName string) *Publisher {
	_ = serviceName
	return &Publisher{cfg: cfg}
}

func (p *Publisher) Publish(ctx context.Context, body string) error {
	return p.publish(ctx, "rabbitmq publish", body, p.cfg.Exchange, p.cfg.RoutingKey, true)
}

// PublishBroken used to construct a manual error span. Under loongsuite-go-agent
// there is no library call here for the agent to auto-instrument, so this
// function produces no rabbitmq-level span; the caller's gin server span will
// still be marked ERROR via handler-side recordError. This is intentional —
// see the observations doc for the comparison vs the manually instrumented
// service-a.
func (p *Publisher) PublishBroken(ctx context.Context) error {
	_ = ctx
	return fmt.Errorf("simulated rabbitmq publish error: invalid exchange")
}

func (p *Publisher) publish(ctx context.Context, spanName, body, exchange, routingKey string, declareQueue bool) error {
	_ = spanName

	if !p.cfg.Enabled {
		return fmt.Errorf("rabbitmq is disabled")
	}

	conn, err := p.dial()
	if err != nil {
		return fmt.Errorf("connect rabbitmq: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open rabbitmq channel: %w", err)
	}
	defer func() {
		_ = ch.Close()
	}()

	if declareQueue {
		if _, err := ch.QueueDeclare(p.cfg.Queue, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare rabbitmq queue: %w", err)
		}
	}

	// Headers intentionally empty: under manual instrumentation we injected
	// traceparent here. The experiment is whether loongsuite-go-agent does
	// the equivalent inject when it auto-instruments PublishWithContext.
	if err := ch.PublishWithContext(ctx, exchange, routingKey, false, false, amqp091.Publishing{
		ContentType: "text/plain",
		Body:        []byte(body),
		Timestamp:   time.Now(),
	}); err != nil {
		return fmt.Errorf("publish rabbitmq message: %w", err)
	}

	return nil
}

func (p *Publisher) dial() (*amqp091.Connection, error) {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	return amqp091.DialConfig(p.cfg.URL, amqp091.Config{
		Heartbeat: 10 * time.Second,
		Locale:    "en_US",
		Dial:      dialer.Dial,
	})
}
