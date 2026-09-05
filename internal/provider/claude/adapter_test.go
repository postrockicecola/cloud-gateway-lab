package claude

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/provider"
	"cloud-gateway-lab/internal/types"
)

func TestChatMapsMessagesAPI(t *testing.T) {
	var gotPath, gotKey, gotVersion, gotSystem string
	var gotMax int
	var gotMessages []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		body, _ := io.ReadAll(r.Body)
		var payload messageRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		gotSystem = payload.System
		gotMax = payload.MaxTokens
		gotMessages = payload.Messages
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","content":[{"type":"text","text":"Bonjour"}],"usage":{"input_tokens":4,"output_tokens":2}}`)
	}))
	t.Cleanup(srv.Close)

	a := New(endpoint.Endpoint{
		BaseURL:   srv.URL,
		APIKey:    "claude-key",
		ModelName: "claude-sonnet-4-5",
	})
	resp, err := a.Chat(context.Background(), &types.ChatRequest{
		Model: "claude-sonnet",
		Messages: []types.Message{
			{Role: "system", Content: "Be brief"},
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Bonjour" || resp.Usage.TotalTokens != 6 {
		t.Fatalf("%+v", resp)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotKey != "claude-key" || gotVersion != endpoint.DefaultAnthropicAPIVersion {
		t.Fatalf("key=%s version=%s", gotKey, gotVersion)
	}
	if gotSystem != "Be brief" {
		t.Fatalf("system = %q", gotSystem)
	}
	if gotMax != 1024 {
		t.Fatalf("max_tokens = %d", gotMax)
	}
	if len(gotMessages) != 1 || gotMessages[0]["role"] != "user" {
		t.Fatalf("messages = %+v", gotMessages)
	}
}

func TestChatStreamRewritesToOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":1}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	a := New(endpoint.Endpoint{BaseURL: srv.URL})
	rec := httptest.NewRecorder()
	usage, err := a.ChatStream(context.Background(), &types.ChatRequest{
		Model: "claude-sonnet", Messages: []types.Message{{Role: "user", Content: "Hi"}}, Stream: true,
	}, rec)
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"Hi"`) || !strings.Contains(body, "[DONE]") {
		t.Fatalf("client stream = %s", body)
	}
	if !strings.Contains(body, "event:") {
		// rewritten to OpenAI data: lines only
	}
	if strings.Contains(body, "content_block_delta") {
		t.Fatalf("leaked anthropic event to client: %s", body)
	}
	if usage.PromptTokens != 3 || usage.CompletionTokens != 1 || usage.TotalTokens != 4 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestRejectsSystemOnly(t *testing.T) {
	a := New(endpoint.Endpoint{BaseURL: "http://example.invalid"})
	_, err := a.Chat(context.Background(), &types.ChatRequest{
		Model:    "claude-sonnet",
		Messages: []types.Message{{Role: "system", Content: "only"}},
	})
	var pe *provider.Error
	if err == nil || !errorsAs(err, &pe) {
		t.Fatalf("err = %v", err)
	}
}

func errorsAs(err error, target **provider.Error) bool {
	e, ok := err.(*provider.Error)
	if !ok {
		return false
	}
	*target = e
	return true
}
