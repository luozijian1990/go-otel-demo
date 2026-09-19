package client

import (
	"context"
	"fmt"
	"go-otel-demo/service-b/internal/telemetry"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go-otel-demo/service-b/internal/config"
)

type PeerClient struct {
	peerURL     string
	serviceBURL string
	serviceCURL string
	serviceDURL string
	client      *http.Client
}

func NewPeerClient(cfg config.Config) (*PeerClient, error) {
	var base string
	switch cfg.Server.Name {
	case "service-a":
		base = cfg.Peer.ServiceBURL
	case "service-b":
		base = cfg.Peer.ServiceAURL
	default:
		return nil, fmt.Errorf("unsupported server.name %q", cfg.Server.Name)
	}
	if strings.TrimSpace(base) == "" {
		return nil, fmt.Errorf("peer base url is required for %s", cfg.Server.Name)
	}
	return &PeerClient{
		peerURL:     strings.TrimRight(base, "/"),
		serviceBURL: strings.TrimRight(cfg.Peer.ServiceBURL, "/"),
		serviceCURL: strings.TrimRight(cfg.Peer.ServiceCURL, "/"),
		serviceDURL: strings.TrimRight(cfg.Peer.ServiceDURL, "/"),
		client: &http.Client{
			Timeout: 10 * time.Second,
			// Transport intentionally omitted: under loongsuite-go-agent the
			// agent auto-instruments net/http RoundTrip and is expected to
			// inject traceparent into outbound requests.
		},
	}, nil
}

func (c *PeerClient) CallPeer(ctx context.Context, path string, depth int) (int, string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u, err := url.Parse(c.peerURL + path)
	if err != nil {
		return 0, "", fmt.Errorf("parse peer url: %w", err)
	}
	q := u.Query()
	q.Set("depth", fmt.Sprintf("%d", depth))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, "", fmt.Errorf("create peer request: %w", err)
	}
	telemetry.Propagate(ctx, req.Header)
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("call peer %s: %w", path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("read peer response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, string(body), fmt.Errorf("peer %s returned status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.StatusCode, string(body), nil
}

func (c *PeerClient) CallServiceB(ctx context.Context, path string) (int, string, error) {
	return c.call(ctx, c.serviceBURL, path)
}

func (c *PeerClient) CallServiceC(ctx context.Context, path string) (int, string, error) {
	return c.call(ctx, c.serviceCURL, path)
}

func (c *PeerClient) CallServiceD(ctx context.Context, path string) (int, string, error) {
	return c.call(ctx, c.serviceDURL, path)
}

func (c *PeerClient) call(ctx context.Context, baseURL, path string) (int, string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return 0, "", fmt.Errorf("downstream base url is empty")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + path)
	if err != nil {
		return 0, "", fmt.Errorf("parse downstream url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, "", fmt.Errorf("create downstream request: %w", err)
	}
	telemetry.Propagate(ctx, req.Header)
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("call downstream %s: %w", path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("read downstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, string(body), fmt.Errorf("downstream %s returned status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.StatusCode, string(body), nil
}
