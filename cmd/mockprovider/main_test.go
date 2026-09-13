package main

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatCompletion(t *testing.T) {
	p := provider{name: "mock-a", apiKey: "secret"}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"mock-model","messages":[{"role":"user","content":"hello"}]}`,
	))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	p.chat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "response from mock-a") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"instance"`) || !strings.Contains(rec.Body.String(), `"node"`) {
		t.Fatalf("missing instance/node fields: %s", rec.Body.String())
	}
}

func TestChatStream(t *testing.T) {
	p := provider{name: "mock-b"}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"mock-model","messages":[{"role":"user","content":"hello"}],"stream":true}`,
	))
	rec := httptest.NewRecorder()

	p.chat(rec, req)

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q", got)
	}
	scanner := bufio.NewScanner(rec.Body)
	var done bool
	for scanner.Scan() {
		if scanner.Text() == "data: [DONE]" {
			done = true
		}
	}
	if !done {
		t.Fatalf("stream does not contain DONE: %s", rec.Body.String())
	}
}

func TestConfiguredFailure(t *testing.T) {
	p := provider{name: "mock-a", failStatus: http.StatusServiceUnavailable}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"mock-model","messages":[{"role":"user","content":"hello"}]}`,
	))
	rec := httptest.NewRecorder()

	p.chat(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}
