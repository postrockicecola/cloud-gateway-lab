package azure

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/types"
)

func TestChatUsesDeploymentPathAndAPIKey(t *testing.T) {
	var gotPath, gotQuery, gotKey, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("api-version")
		gotKey = r.Header.Get("api-key")
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if _, ok := payload["model"]; ok {
			gotModel = payload["model"].(string)
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("azure should not send Bearer, got %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"cmpl-az","choices":[{"message":{"content":"from-azure"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	}))
	t.Cleanup(srv.Close)

	a := New(endpoint.Endpoint{
		BaseURL:    srv.URL,
		APIKey:     "azure-key",
		ModelName:  "gpt-4o-deploy",
		APIVersion: "2024-06-01",
	})
	resp, err := a.Chat(context.Background(), &types.ChatRequest{
		Model:    "gpt-5",
		Messages: []types.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "from-azure" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("%+v", resp)
	}
	if gotPath != "/openai/deployments/gpt-4o-deploy/chat/completions" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotQuery != "2024-06-01" {
		t.Fatalf("api-version = %s", gotQuery)
	}
	if gotKey != "azure-key" {
		t.Fatalf("api-key = %s", gotKey)
	}
	if gotModel != "" {
		t.Fatalf("body should omit model, got %q", gotModel)
	}
}

func TestChatURLWhenBaseAlreadyHasOpenAI(t *testing.T) {
	a := New(endpoint.Endpoint{
		BaseURL:   "https://res.openai.azure.com/openai",
		ModelName: "dep",
	})
	u := a.chatURL("gpt-5")
	if !strings.Contains(u, "/openai/deployments/dep/chat/completions") {
		t.Fatalf("url = %s", u)
	}
	if strings.Count(u, "/openai") != 1 {
		t.Fatalf("doubled openai path: %s", u)
	}
}
