package endpoint

import (
	"testing"
	"time"

	"cloud-gateway-lab/internal/breaker"
)

func TestWRRDistribution(t *testing.T) {
	pool, err := NewPool([]Endpoint{
		{ID: "a", Model: "gpt-5", BaseURL: "http://a", Weight: 5},
		{ID: "b", Model: "gpt-5", BaseURL: "http://b", Weight: 3},
		{ID: "c", Model: "gpt-5", BaseURL: "http://c", Weight: 2},
	}, breaker.Config{})
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	for range 100 {
		ep, err := pool.Pick("gpt-5", nil)
		if err != nil {
			t.Fatal(err)
		}
		counts[ep.ID]++
	}
	if counts["a"] < counts["b"] || counts["b"] < counts["c"] {
		t.Fatalf("distribution = %v", counts)
	}
	if counts["a"] < 40 || counts["c"] < 10 {
		t.Fatalf("distribution = %v", counts)
	}
}

func TestPickSkipsUnhealthyAndOpen(t *testing.T) {
	pool, err := NewPool([]Endpoint{
		{ID: "a", Model: "gpt-5", BaseURL: "http://a", Weight: 1},
		{ID: "b", Model: "gpt-5", BaseURL: "http://b", Weight: 1},
	}, breaker.Config{FailureThreshold: 1, Cooldown: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	pool.SetHealth("a", Unhealthy)
	ep, err := pool.Pick("gpt-5", nil)
	if err != nil || ep.ID != "b" {
		t.Fatalf("ep=%+v err=%v", ep, err)
	}

	pool.SetHealth("a", Healthy)
	pool.Report("b", Result{Retryable: true, StatusCode: 502})
	ep, err = pool.Pick("gpt-5", nil)
	if err != nil || ep.ID != "a" {
		t.Fatalf("ep=%+v err=%v", ep, err)
	}
}

func TestParseYAML(t *testing.T) {
	eps, err := ParseYAML([]byte(`
models:
  gpt-5:
    - id: local
      provider: openai
      base_url: http://localhost:11434/v1
      weight: 2
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].ID != "local" || eps[0].Model != "gpt-5" {
		t.Fatalf("%+v", eps)
	}
}
