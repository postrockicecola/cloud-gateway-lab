package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"cloud-gateway-lab/pkg/limiter"
	"cloud-gateway-lab/pkg/prefixcache"
	"cloud-gateway-lab/pkg/scheduler"
)

func TestProxyStreamsSSEAndSettlesRefund(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	handler, rdb, lim := newTestProxy(t, upstream.URL)
	ctx := context.Background()
	if err := rdb.Set(ctx, "llm:bal:alice", 1000, 0).Err(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"llama3","stream":true,"messages":[{"role":"user","content":"Hi"}]}`,
	))
	req.Header.Set("X-User-ID", "alice")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Hi") {
		t.Fatalf("body = %s", rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	var bal int64
	var err error
	for time.Now().Before(deadline) {
		bal, err = rdb.Get(ctx, "llm:bal:alice").Int64()
		if err == nil && bal == 997 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("balance = %d, want 997 (1000 - actual 3); limiter=%T", bal, lim)
}

func TestProxyRateLimited(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, _, _ := newTestProxy(t, upstream.URL)
	body := `{"messages":[{"role":"user","content":"Hi"}]}`
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("X-User-ID", "bob")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if i < 2 && rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", i, rec.Code)
		}
		if i == 2 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("request %d status = %d, want 429", i, rec.Code)
		}
	}
}

func TestProxyPrefixCacheHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Prefix-Cache-Hit") != "true" {
			t.Errorf("upstream missing prefix-cache header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()

	handler, _, _ := newTestProxy(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"messages":[{"role":"system","content":"You are a helpful assistant"},{"role":"user","content":"Hi there"}]}`,
	))
	req.Header.Set("X-User-ID", "carol")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("X-Prefix-Cache-Hit") != "true" {
		t.Fatalf("client missing prefix-cache header, status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func newTestProxy(t *testing.T, upstreamURL string) (http.Handler, *redis.Client, *limiter.Limiter) {
	t.Helper()
	target, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	lim, err := limiter.New(rdb, limiter.Config{
		Limit: 2, Window: time.Second, DefaultBalance: 100_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	sched, err := scheduler.New(4, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sched.Stop)
	idx := prefixcache.New(8, 0.2)
	idx.Insert("You are a helpful assistant")
	handler, err := New(Config{
		Upstream:          target,
		Limiter:           lim,
		Scheduler:         sched,
		Prefix:            idx,
		Logger:            nil,
		ShortTextTokens:   128,
		CompletionReserve: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, rdb, lim
}
