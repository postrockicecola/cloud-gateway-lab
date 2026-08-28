package gateway

import (
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
)

type Gateway struct {
	routes   map[string]*httputil.ReverseProxy
	requests atomic.Uint64
	errors   atomic.Uint64
	logger   *slog.Logger
}

func New(routes map[string]string, logger *slog.Logger) (*Gateway, error) {
	if len(routes) == 0 {
		return nil, errors.New("at least one route is required")
	}

	g := &Gateway{
		routes: make(map[string]*httputil.ReverseProxy, len(routes)),
		logger: logger,
	}
	for name, rawURL := range routes {
		target, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("parse route %q: %w", name, err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
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
	case "/healthz", "/readyz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	case "/metrics":
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(w,
			"gateway_requests_total %d\ngateway_upstream_errors_total %d\n",
			g.requests.Load(), g.errors.Load(),
		)
		return
	}

	g.requests.Add(1)
	route := routeName(r.URL.Path)
	proxy, ok := g.routes[route]
	if !ok {
		http.Error(w, "route not found", http.StatusNotFound)
		return
	}
	proxy.ServeHTTP(w, r)
}

func routeName(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" {
		return ""
	}
	return parts[1]
}
