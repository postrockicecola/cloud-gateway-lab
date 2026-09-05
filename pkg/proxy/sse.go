package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
)

type streamUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Text    string `json:"text"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Response string      `json:"response"`
	Usage    streamUsage `json:"usage"`
}

func parseSSELine(line []byte, generated *strings.Builder, usage *streamUsage) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return
	}
	payload, ok := bytes.CutPrefix(trimmed, []byte("data:"))
	if !ok {
		// Ollama native /api/chat is NDJSON without the data: prefix.
		payload = trimmed
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}

	var chunk streamChunk
	if json.Unmarshal(payload, &chunk) != nil {
		return
	}
	for _, choice := range chunk.Choices {
		generated.WriteString(choice.Delta.Content)
		generated.WriteString(choice.Text)
		generated.WriteString(choice.Message.Content)
	}
	generated.WriteString(chunk.Message.Content)
	generated.WriteString(chunk.Response)
	if chunk.Usage.TotalTokens > 0 || chunk.Usage.CompletionTokens > 0 {
		*usage = chunk.Usage
	}
}

func promptFromChatBody(body []byte) string {
	var req struct {
		Prompt   string `json:"prompt"`
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	var b strings.Builder
	if req.Prompt != "" {
		b.WriteString(req.Prompt)
	}
	for _, msg := range req.Messages {
		b.WriteString(contentToString(msg.Content))
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func contentToString(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, part := range v {
			m, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := m["text"].(string); ok {
				b.WriteString(text)
			}
		}
		return b.String()
	default:
		return ""
	}
}
