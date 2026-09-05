package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"cloud-gateway-lab/internal/quota"
	"cloud-gateway-lab/internal/ratelimit"
)

type Config struct {
	Routes  map[string]string
	Logger  *slog.Logger
	Limiter ratelimit.Limiter
	Quota   quota.Accountant
	Ready   func(context.Context) error
}

type Gateway struct {
	routes        map[string]*httputil.ReverseProxy
	limiter       ratelimit.Limiter
	quota         quota.Accountant
	ready         func(context.Context) error
	requests      atomic.Uint64
	errors        atomic.Uint64
	limited       atomic.Uint64
	quotaRejected atomic.Uint64
	logger        *slog.Logger
}

func New(cfg Config) (*Gateway, error) {
	if len(cfg.Routes) == 0 {
		return nil, errors.New("at least one route is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	g := &Gateway{
		routes:  make(map[string]*httputil.ReverseProxy, len(cfg.Routes)),
		limiter: cfg.Limiter,
		quota:   cfg.Quota,
		ready:   cfg.Ready,
		logger:  cfg.Logger,
	}
	for name, rawURL := range cfg.Routes {
		target, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("parse route %q: %w", name, err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.FlushInterval = 50 * time.Millisecond
		proxy.Transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          200,
			MaxIdleConnsPerHost:   100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   2 * time.Second,
			ResponseHeaderTimeout: 3 * time.Second,
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			g.errors.Add(1)
			g.logger.Error("upstream request failed", "path", r.URL.Path, "error", err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		}
		g.routes[name] = proxy
	}
	return g, nil
}

func ParseRoutes(value string) (map[string]string, error) {
	routes := make(map[string]string)
	for entry := range strings.SplitSeq(value, ",") {
		name, rawURL, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok || name == "" || rawURL == "" {
			return nil, fmt.Errorf("invalid route %q, expected name=url", entry)
		}
		routes[name] = rawURL
	}
	return routes, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	case "/readyz":
		if g.ready != nil {
			if err := g.ready(r.Context()); err != nil {
				g.logger.Error("readiness check failed", "error", err)
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	case "/metrics":
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(w,
			"gateway_requests_total %d\ngateway_upstream_errors_total %d\ngateway_rate_limited_total %d\ngateway_quota_rejected_total %d\n",
			g.requests.Load(), g.errors.Load(), g.limited.Load(), g.quotaRejected.Load(),
		)
		return
	}

	g.requests.Add(1)
	identity := clientKey(r)
	route := routeName(r.URL.Path)

	if g.limiter != nil {
		decision, err := g.limiter.Allow(r.Context(), route+":"+identity)
		if err != nil {
			g.logger.Error("rate limiter failed", "identity", identity, "error", err)
			http.Error(w, "limiter unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", decision.Limit))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining(decision.Limit, decision.Count)))
		if !decision.Allowed {
			g.limited.Add(1)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
	}

	if g.quota != nil {
		reservation, err := g.quota.Reserve(r.Context(), identity, 1)
		if err != nil {
			g.logger.Error("quota reserve failed", "identity", identity, "error", err)
			http.Error(w, "quota unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-Quota-Remaining", fmt.Sprintf("%d", reservation.Remaining))
		if !reservation.Allowed {
			g.quotaRejected.Add(1)
			http.Error(w, "quota exceeded", http.StatusForbidden)
			return
		}
	}

	proxy, ok := g.routes[route]
	if !ok {
		http.Error(w, "route not found", http.StatusNotFound)
		return
	}
	proxy.ServeHTTP(w, r)
}

func clientKey(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get("X-User-ID")); id != "" {
		return "user:" + id
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		return "ip:" + strings.TrimSpace(ip)
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "ip:" + r.RemoteAddr
	}
	return "ip:" + ip
}

func routeName(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" {
		return ""
	}
	return parts[1]
}

func remaining(limit, count int64) int64 {
	if count >= limit {
		return 0
	}
	return limit - count
}
