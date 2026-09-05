package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cloud-gateway-lab/internal/breaker"
	"cloud-gateway-lab/internal/endpoint"
)

func TestCheckMarksUnhealthyAndHealthy(t *testing.T) {
	var healthy bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	pool, err := endpoint.NewPool([]endpoint.Endpoint{
		{ID: "local", Model: "gpt-5", BaseURL: srv.URL, Weight: 1},
	}, breaker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	c := New(pool, time.Hour, time.Second, nil)
	c.Check(context.Background(), mustGet(t, pool, "local"))
	if _, err := pool.Pick("gpt-5", nil); err != nil {
		t.Fatalf("single failure should not evict, err=%v", err)
	}
	c.Check(context.Background(), mustGet(t, pool, "local"))
	if _, err := pool.Pick("gpt-5", nil); err != endpoint.ErrNoEndpoint {
		t.Fatalf("expected no endpoint, err=%v", err)
	}

	healthy = true
	c.Check(context.Background(), mustGet(t, pool, "local"))
	ep, err := pool.Pick("gpt-5", nil)
	if err != nil || ep.ID != "local" {
		t.Fatalf("ep=%+v err=%v", ep, err)
	}
}

func mustGet(t *testing.T, pool *endpoint.Pool, id string) endpoint.Endpoint {
	t.Helper()
	ep, ok := pool.Get(id)
	if !ok {
		t.Fatalf("missing %s", id)
	}
	return ep
}
