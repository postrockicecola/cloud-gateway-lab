package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/provider"
	"cloud-gateway-lab/internal/types"
)

func TestChatSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("auth = %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"cmpl-1","choices":[{"message":{"content":"Hello"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	}))
	defer srv.Close()

	a := New(endpoint.Endpoint{BaseURL: srv.URL, APIKey: "test-key", ModelName: "llama3"})
	resp, err := a.Chat(context.Background(), &types.ChatRequest{
		Model:    "gpt-5",
		Messages: []types.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("%+v", resp)
	}
}

func TestChatMapsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
	}))
	defer srv.Close()

	a := New(endpoint.Endpoint{BaseURL: srv.URL})
	_, err := a.Chat(context.Background(), &types.ChatRequest{Model: "gpt-5", Messages: []types.Message{{Role: "user", Content: "Hi"}}})
	var pe *provider.Error
	if !errorsAs(err, &pe) || pe.StatusCode != 503 || pe.Message != "busy" {
		t.Fatalf("err = %v", err)
	}
}

func TestChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(endpoint.Endpoint{BaseURL: srv.URL})
	rec := httptest.NewRecorder()
	usage, err := a.ChatStream(context.Background(), &types.ChatRequest{
		Model: "gpt-5", Messages: []types.Message{{Role: "user", Content: "Hi"}}, Stream: true,
	}, rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), "Hi") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if usage.TotalTokens != 2 {
		t.Fatalf("usage = %+v", usage)
	}
}

func errorsAs(err error, target **provider.Error) bool {
	if err == nil {
		return false
	}
	e, ok := err.(*provider.Error)
	if !ok {
		return false
	}
	*target = e
	return true
}
