package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cloud-gateway-lab/internal/quota"
	"cloud-gateway-lab/internal/ratelimit"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGatewayRoutesRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/42" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("user-42"))
	}))
	t.Cleanup(upstream.Close)

	g, err := New(Config{Routes: map[string]string{"users": upstream.URL}, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/users/42", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder.Body.String() != "user-42" {
		t.Fatalf("body = %q, want %q", recorder.Body.String(), "user-42")
	}
}

func TestGatewayUnknownRoute(t *testing.T) {
	g, err := New(Config{Routes: map[string]string{"users": "http://users"}, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/products", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestGatewayRateLimited(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	g, err := New(Config{
		Routes:  map[string]string{"users": upstream.URL},
		Logger:  testLogger(),
		Limiter: ratelimit.NewMemory(1, time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	req.Header.Set("X-User-ID", "alice")

	first := httptest.NewRecorder()
	g.ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
	}

	second := httptest.NewRecorder()
	g.ServeHTTP(second, req.Clone(context.Background()))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
	if second.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("remaining = %q", second.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestGatewayQuotaExceeded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	g, err := New(Config{
		Routes: map[string]string{"users": upstream.URL},
		Logger: testLogger(),
		Quota:  quota.NewMemory(1),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	req.Header.Set("X-User-ID", "bob")

	first := httptest.NewRecorder()
	g.ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
	}

	second := httptest.NewRecorder()
	g.ServeHTTP(second, req.Clone(context.Background()))
	if second.Code != http.StatusForbidden {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusForbidden)
	}
}

func TestGatewayReadyz(t *testing.T) {
	g, err := New(Config{
		Routes: map[string]string{"users": "http://users"},
		Logger: testLogger(),
		Ready:  func(context.Context) error { return errors.New("redis down") },
	})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestParseRoutes(t *testing.T) {
	routes, err := ParseRoutes("users=http://users:8080,products=http://products:8080")
	if err != nil {
		t.Fatal(err)
	}
	if routes["users"] != "http://users:8080" {
		t.Fatalf("users route = %q", routes["users"])
	}
}

func TestClientKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/users/1", nil)
	req.Header.Set("X-User-ID", "dev-7")
	if got := clientKey(req); got != "user:dev-7" {
		t.Fatalf("clientKey = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/users/1", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := clientKey(req); got != "ip:203.0.113.9" {
		t.Fatalf("clientKey = %q", got)
	}
}
