package types

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Message struct {
	Role    string
	Content string
}

type ChatRequest struct {
	Model       string
	Messages    []Message
	Temperature float32
	MaxTokens   int
	Stream      bool
}

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type ChatResponse struct {
	ID      string
	Model   string
	Content string
	Usage   Usage
}

type apiRequest struct {
	Model       string       `json:"model"`
	Messages    []apiMessage `json:"messages"`
	Temperature *float32     `json:"temperature"`
	MaxTokens   int          `json:"max_tokens"`
	Stream      bool         `json:"stream"`
}

type apiMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func ParseChatRequest(body []byte) (*ChatRequest, error) {
	var raw apiRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	if strings.TrimSpace(raw.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if len(raw.Messages) == 0 {
		return nil, fmt.Errorf("messages is required")
	}

	req := &ChatRequest{
		Model:     strings.TrimSpace(raw.Model),
		MaxTokens: raw.MaxTokens,
		Stream:    raw.Stream,
	}
	if raw.Temperature != nil {
		req.Temperature = *raw.Temperature
	}
	for i, msg := range raw.Messages {
		role := strings.TrimSpace(msg.Role)
		content := ContentToString(msg.Content)
		if role == "" {
			return nil, fmt.Errorf("messages[%d].role is required", i)
		}
		req.Messages = append(req.Messages, Message{Role: role, Content: content})
	}
	return req, nil
}

func ContentToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []map[string]any
	if json.Unmarshal(raw, &parts) != nil {
		return strings.Trim(string(raw), `"`)
	}
	var b strings.Builder
	for _, part := range parts {
		if text, ok := part["text"].(string); ok {
			b.WriteString(text)
		}
	}
	return b.String()
}

func PromptText(messages []Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func EncodeChatCompletion(resp *ChatResponse) []byte {
	if resp.ID == "" {
		resp.ID = "chatcmpl-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	payload := map[string]any{
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   resp.Model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": resp.Content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int64{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		},
	}
	body, _ := json.Marshal(payload)
	return body
}

func EncodeAPIError(message, typ, code string) []byte {
	err := map[string]any{
		"message": message,
		"type":    typ,
	}
	if code != "" {
		err["code"] = code
	}
	body, _ := json.Marshal(map[string]any{"error": err})
	return body
}
