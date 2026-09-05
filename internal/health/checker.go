package health

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"cloud-gateway-lab/internal/endpoint"
)

type Checker struct {
	pool     *endpoint.Pool
	interval time.Duration
	timeout  time.Duration
	fails    int
	client   *http.Client
	logger   *slog.Logger
	stop     chan struct{}
	done     chan struct{}
	streak   map[string]int
}

func New(pool *endpoint.Pool, interval, timeout time.Duration, logger *slog.Logger) *Checker {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Checker{
		pool:     pool,
		interval: interval,
		timeout:  timeout,
		fails:    2,
		client:   &http.Client{Timeout: timeout},
		logger:   logger,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		streak:   make(map[string]int),
	}
}

func (c *Checker) Start() {
	go func() {
		defer close(c.done)
		c.CheckAll(context.Background())
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				c.CheckAll(context.Background())
			}
		}
	}()
}

func (c *Checker) Stop() {
	select {
	case <-c.stop:
		return
	default:
		close(c.stop)
	}
	<-c.done
}

func (c *Checker) CheckAll(ctx context.Context) {
	for _, ep := range c.pool.All() {
		c.Check(ctx, ep)
	}
}

func (c *Checker) Check(ctx context.Context, ep endpoint.Endpoint) {
	probeCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := strings.TrimRight(ep.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		c.mark(ep.ID, false, "health check build failed", "error", err)
		return
	}
	if ep.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ep.APIKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		c.mark(ep.ID, false, "health check failed", "error", err)
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		c.mark(ep.ID, false, "health check status", "status", resp.StatusCode)
		return
	}
	c.mark(ep.ID, true, "")
}

func (c *Checker) mark(id string, ok bool, msg string, args ...any) {
	if ok {
		c.streak[id] = 0
		c.pool.SetHealth(id, endpoint.Healthy)
		return
	}
	c.streak[id]++
	if c.streak[id] >= c.fails {
		c.pool.SetHealth(id, endpoint.Unhealthy)
	}
	if msg != "" {
		c.logger.Warn(msg, append([]any{"endpoint", id, "failures", c.streak[id]}, args...)...)
	}
}
