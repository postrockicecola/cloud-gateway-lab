package endpoint

import (
	"net/http"
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
      api_version: 2024-06-01
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].ID != "local" || eps[0].Model != "gpt-5" || eps[0].APIVersion != "2024-06-01" {
		t.Fatalf("%+v", eps)
	}
}

func TestProbeURLAndAuth(t *testing.T) {
	az := Endpoint{Provider: "azure", BaseURL: "https://res.openai.azure.com", APIKey: "k", APIVersion: "2024-06-01"}
	if got := az.ProbeURL(); got != "https://res.openai.azure.com/openai/models?api-version=2024-06-01" {
		t.Fatalf("azure probe = %s", got)
	}
	req, _ := http.NewRequest(http.MethodGet, az.ProbeURL(), nil)
	az.SetAuth(req)
	if req.Header.Get("api-key") != "k" || req.Header.Get("Authorization") != "" {
		t.Fatalf("azure headers = %v", req.Header)
	}

	cl := Endpoint{Provider: "claude", BaseURL: "https://api.anthropic.com", APIKey: "ck"}
	if got := cl.ProbeURL(); got != "https://api.anthropic.com/v1/models" {
		t.Fatalf("claude probe = %s", got)
	}
	req, _ = http.NewRequest(http.MethodGet, cl.ProbeURL(), nil)
	cl.SetAuth(req)
	if req.Header.Get("x-api-key") != "ck" || req.Header.Get("anthropic-version") == "" {
		t.Fatalf("claude headers = %v", req.Header)
	}
}
