package aigateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"cloud-gateway-lab/internal/auth"
	"cloud-gateway-lab/internal/breaker"
	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/provider"
	"cloud-gateway-lab/internal/provider/openai"
	"cloud-gateway-lab/internal/retry"
	"cloud-gateway-lab/pkg/limiter"
)

func TestChatCompletionsSuccessAndRequestID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Hello"}}],"usage":{"total_tokens":3}}`)
	}))
	t.Cleanup(upstream.Close)

	g := newTestGateway(t, []endpoint.Endpoint{
		endpoint.Single("local", "gpt-5", upstream.URL, ""),
	}, "sk-alice:alice")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"gpt-5","messages":[{"role":"user","content":"Hi"}]}`,
	))
	req.Header.Set("Authorization", "Bearer sk-alice")
	req.Header.Set("X-Request-ID", "req-test-1")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Request-ID") != "req-test-1" {
		t.Fatalf("request id = %q", rec.Header().Get("X-Request-ID"))
	}
	if !strings.Contains(rec.Body.String(), "Hello") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRejectsMissingAPIKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach upstream")
	}))
	t.Cleanup(upstream.Close)
	g := newTestGateway(t, []endpoint.Endpoint{
		endpoint.Single("local", "gpt-5", upstream.URL, ""),
	}, "sk-alice:alice")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"gpt-5","messages":[{"role":"user","content":"Hi"}]}`,
	))
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestFailoverToHealthyEndpoint(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"down"}}`)
	}))
	t.Cleanup(bad.Close)
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"from-b"}}],"usage":{"total_tokens":1}}`)
	}))
	t.Cleanup(good.Close)

	g := newTestGateway(t, []endpoint.Endpoint{
		{ID: "a", Model: "gpt-5", BaseURL: bad.URL, Weight: 1, Provider: "openai"},
		{ID: "b", Model: "gpt-5", BaseURL: good.URL, Weight: 1, Provider: "openai"},
	}, "sk-alice:alice")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"gpt-5","messages":[{"role":"user","content":"Hi"}]}`,
	))
	req.Header.Set("Authorization", "Bearer sk-alice")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from-b") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestDoesNotRetryBadRequest(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad schema"}}`)
	}))
	t.Cleanup(upstream.Close)

	g := newTestGateway(t, []endpoint.Endpoint{
		{ID: "a", Model: "gpt-5", BaseURL: upstream.URL, Weight: 1, Provider: "openai"},
		{ID: "b", Model: "gpt-5", BaseURL: upstream.URL, Weight: 1, Provider: "openai"},
	}, "sk-alice:alice")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"gpt-5","messages":[{"role":"user","content":"Hi"}]}`,
	))
	req.Header.Set("Authorization", "Bearer sk-alice")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestRateLimited(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"total_tokens":1}}`)
	}))
	t.Cleanup(upstream.Close)

	g := newTestGateway(t, []endpoint.Endpoint{
		endpoint.Single("local", "gpt-5", upstream.URL, ""),
	}, "sk-alice:alice")

	body := `{"model":"gpt-5","messages":[{"role":"user","content":"Hi"}]}`
	var last int
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer sk-alice")
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("last status = %d, want 429", last)
	}
}

func TestMetricsAndHealthz(t *testing.T) {
	g := newTestGateway(t, []endpoint.Endpoint{
		endpoint.Single("local", "gpt-5", "http://127.0.0.1:1", ""),
	}, "sk-alice:alice")

	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "gateway_requests_total") {
		t.Fatalf("metrics = %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "gateway_endpoint_healthy") {
		t.Fatalf("metrics missing endpoint health: %s", rec.Body.String())
	}
}

func newTestGateway(t *testing.T, eps []endpoint.Endpoint, keys string) *Gateway {
	t.Helper()
	pool, err := endpoint.NewPool(eps, breaker.Config{FailureThreshold: 5, Cooldown: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	mem, err := auth.ParseKeyList(keys)
	if err != nil {
		t.Fatal(err)
	}
	reg := provider.NewRegistry()
	openai.Register(reg)

	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	lim, err := limiter.New(rdb, limiter.Config{Limit: 2, Window: time.Second, DefaultBalance: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(Config{
		Auth:    auth.New(mem),
		Limiter: lim,
		Pool:    pool,
		Providers: reg,
		Retry:   retry.Config{MaxAttempts: 3, BaseDelay: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestUnknownModel(t *testing.T) {
	g := newTestGateway(t, []endpoint.Endpoint{
		endpoint.Single("local", "gpt-5", "http://127.0.0.1:1", ""),
	}, "sk-alice:alice")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"missing","messages":[{"role":"user","content":"Hi"}]}`,
	))
	req.Header.Set("Authorization", "Bearer sk-alice")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestReadyz(t *testing.T) {
	g := newTestGateway(t, []endpoint.Endpoint{
		endpoint.Single("local", "gpt-5", "http://127.0.0.1:1", ""),
	}, "sk-alice:alice")
	g.ready = func(context.Context) error { return io.ErrUnexpectedEOF }
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}
