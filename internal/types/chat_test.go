package types

import "testing"

func TestParseChatRequest(t *testing.T) {
	req, err := ParseChatRequest([]byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"Hello"}],
		"temperature":0.7,
		"stream":false
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-5" || req.Temperature != 0.7 || len(req.Messages) != 1 {
		t.Fatalf("%+v", req)
	}
	if req.Messages[0].Content != "Hello" {
		t.Fatalf("content = %q", req.Messages[0].Content)
	}
}

func TestParseChatRequestArrayContent(t *testing.T) {
	req, err := ParseChatRequest([]byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":[{"type":"text","text":"Hi"}]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Messages[0].Content != "Hi" {
		t.Fatalf("content = %q", req.Messages[0].Content)
	}
}

func TestParseChatRequestMissingModel(t *testing.T) {
	_, err := ParseChatRequest([]byte(`{"messages":[{"role":"user","content":"x"}]}`))
	if err == nil {
		t.Fatal("expected error")
	}
}
