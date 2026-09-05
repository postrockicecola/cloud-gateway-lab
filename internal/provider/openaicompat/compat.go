// Package openaicompat holds the OpenAI chat/completions wire format.
// OpenAI and Azure share this body; they differ only in URL and auth.
package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"cloud-gateway-lab/internal/provider"
	"cloud-gateway-lab/internal/types"
)

type completionRequest struct {
	Model       string              `json:"model,omitempty"`
	Messages    []map[string]string `json:"messages"`
	Temperature float32             `json:"temperature,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
}

type completionResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Text string `json:"text"`
	} `json:"choices"`
	Usage types.Usage `json:"usage"`
}

func (r completionResponse) Content() string {
	var b strings.Builder
	for _, c := range r.Choices {
		b.WriteString(c.Message.Content)
		b.WriteString(c.Text)
	}
	return b.String()
}

func MarshalRequest(model string, req *types.ChatRequest, stream bool) ([]byte, error) {
	payload := completionRequest{
		Model:       model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      stream,
	}
	for _, msg := range req.Messages {
		payload.Messages = append(payload.Messages, map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}
	return json.Marshal(payload)
}

func ParseCompletion(body []byte) (id, content string, usage types.Usage, err error) {
	var parsed completionResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", types.Usage{}, err
	}
	return parsed.ID, parsed.Content(), parsed.Usage, nil
}

func HTTPError(resp *http.Response, body []byte) error {
	msg := strings.TrimSpace(string(body))
	var wrapped struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &wrapped) == nil && wrapped.Error.Message != "" {
		msg = wrapped.Error.Message
	}
	if msg == "" {
		msg = resp.Status
	}
	err := &provider.Error{StatusCode: resp.StatusCode, Message: msg}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if d, perr := time.ParseDuration(ra + "s"); perr == nil {
			err.RetryAfter = d
		}
	}
	return err
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
	Usage types.Usage `json:"usage"`
}

func ParseSSELine(line []byte, generated *strings.Builder, usage *types.Usage) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return
	}
	payload, ok := bytes.CutPrefix(trimmed, []byte("data:"))
	if !ok {
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
	if chunk.Usage.TotalTokens > 0 || chunk.Usage.CompletionTokens > 0 {
		*usage = chunk.Usage
	}
}

func UsageOrEstimate(usage types.Usage, generated string) *types.Usage {
	if usage.TotalTokens > 0 {
		return &usage
	}
	if usage.CompletionTokens == 0 && generated != "" {
		usage.CompletionTokens = int64((len(generated) + 3) / 4)
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return &usage
}

func CopyStream(ctx context.Context, src io.Reader, w http.ResponseWriter) (*types.Usage, error) {
	w.Header().Set("Cache-Control", "no-cache")
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	reader := bufio.NewReaderSize(src, 32*1024)
	var generated strings.Builder
	var usage types.Usage
	dst := w

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if dst != nil {
				if _, werr := dst.Write(line); werr != nil {
					dst = nil
				} else if flusher != nil {
					flusher.Flush()
				}
			}
			ParseSSELine(line, &generated, &usage)
		}
		if err != nil {
			if err != io.EOF && err != io.ErrClosedPipe {
				return UsageOrEstimate(usage, generated.String()), &provider.Error{Message: "read stream: " + err.Error()}
			}
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	return UsageOrEstimate(usage, generated.String()), nil
}

func WriteOpenAIDelta(w http.ResponseWriter, text string) error {
	if text == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"choices": []map[string]any{
			{"delta": map[string]string{"content": text}},
		},
	})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "data: "); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n\n")
	return err
}

func WriteOpenAIUsage(w http.ResponseWriter, usage types.Usage) error {
	payload, err := json.Marshal(map[string]any{"usage": usage})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "data: "); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n\n")
	return err
}

func WriteOpenAIDone(w http.ResponseWriter) error {
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	return err
}
