package proxy

import (
	"strings"
	"testing"
)

func TestParseSSELineAccumulatesContent(t *testing.T) {
	var generated strings.Builder
	var usage streamUsage
	parseSSELine([]byte(`data: {"choices":[{"delta":{"content":"Hello"}}]}`), &generated, &usage)
	parseSSELine([]byte(`data: {"choices":[{"delta":{"content":" world"}}]}`), &generated, &usage)
	parseSSELine([]byte(`data: [DONE]`), &generated, &usage)
	if generated.String() != "Hello world" {
		t.Fatalf("generated = %q", generated.String())
	}
}

func TestParseSSELineReadsUsage(t *testing.T) {
	var generated strings.Builder
	var usage streamUsage
	parseSSELine([]byte(`data: {"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`), &generated, &usage)
	if usage.TotalTokens != 13 || usage.PromptTokens != 9 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestPromptFromChatBody(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"You are a helpful assistant"},{"role":"user","content":"Hi"}]}`)
	got := promptFromChatBody(body)
	if !strings.Contains(got, "You are a helpful assistant") || !strings.Contains(got, "Hi") {
		t.Fatalf("prompt = %q", got)
	}
}
