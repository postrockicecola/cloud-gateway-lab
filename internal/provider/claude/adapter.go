package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/provider"
	"cloud-gateway-lab/internal/provider/openaicompat"
	"cloud-gateway-lab/internal/types"
)

// Adapter talks to the Anthropic Messages API and converts both
// request and response back to the gateway's internal Chat types.
// Streaming is rewritten to OpenAI-compatible SSE so clients stay unaware.
type Adapter struct {
	baseURL   string
	apiKey    string
	modelName string
	version   string
	client    *http.Client
}

func New(ep endpoint.Endpoint) *Adapter {
	base := strings.TrimRight(ep.BaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return &Adapter{
		baseURL:   base,
		apiKey:    ep.APIKey,
		modelName: ep.ModelName,
		version:   ep.AnthropicVersion(),
		client:    provider.NewHTTPClient(ep.Timeout),
	}
}

func (a *Adapter) Chat(ctx context.Context, req *types.ChatRequest) (*types.ChatResponse, error) {
	resp, err := a.do(ctx, req, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, &provider.Error{Message: "read provider response: " + err.Error()}
	}
	if resp.StatusCode >= 400 {
		return nil, httpError(resp, body)
	}
	var parsed messageResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, &provider.Error{Message: "decode provider response: " + err.Error()}
	}
	return &types.ChatResponse{
		ID:      parsed.ID,
		Model:   req.Model,
		Content: parsed.Text(),
		Usage:   parsed.Usage.asUsage(),
	}, nil
}

func (a *Adapter) ChatStream(ctx context.Context, req *types.ChatRequest, w http.ResponseWriter) (*types.Usage, error) {
	resp, err := a.do(ctx, req, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, httpError(resp, body)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	reader := bufio.NewReaderSize(resp.Body, 32*1024)
	var generated strings.Builder
	var usage types.Usage
	event := ""
	dst := w

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			switch {
			case bytes.HasPrefix(trimmed, []byte("event:")):
				event = string(bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("event:"))))
			case bytes.HasPrefix(trimmed, []byte("data:")):
				payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
				text, u := parseEvent(event, payload)
				if u.PromptTokens > 0 {
					usage.PromptTokens = u.PromptTokens
				}
				if u.CompletionTokens > 0 {
					usage.CompletionTokens = u.CompletionTokens
				}
				if text != "" {
					generated.WriteString(text)
					if dst != nil {
						if werr := openaicompat.WriteOpenAIDelta(dst, text); werr != nil {
							dst = nil
						} else if flusher != nil {
							flusher.Flush()
						}
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF && err != io.ErrClosedPipe {
				return openaicompat.UsageOrEstimate(usage, generated.String()), &provider.Error{Message: "read stream: " + err.Error()}
			}
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	out := openaicompat.UsageOrEstimate(usage, generated.String())
	if dst != nil {
		_ = openaicompat.WriteOpenAIUsage(dst, *out)
		_ = openaicompat.WriteOpenAIDone(dst)
		if flusher != nil {
			flusher.Flush()
		}
	}
	return out, nil
}

func (a *Adapter) do(ctx context.Context, req *types.ChatRequest, stream bool) (*http.Response, error) {
	payload, err := marshalMessage(a.upstreamModel(req.Model), req, stream)
	if err != nil {
		return nil, &provider.Error{Message: err.Error()}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, &provider.Error{Message: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		httpReq.Header.Set("x-api-key", a.apiKey)
	}
	httpReq.Header.Set("anthropic-version", a.version)
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, &provider.Error{Message: err.Error()}
	}
	return resp, nil
}

func (a *Adapter) upstreamModel(requested string) string {
	if a.modelName != "" {
		return a.modelName
	}
	return requested
}

type messageRequest struct {
	Model       string              `json:"model"`
	MaxTokens   int                 `json:"max_tokens"`
	System      string              `json:"system,omitempty"`
	Messages    []map[string]string `json:"messages"`
	Temperature float32             `json:"temperature,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
}

func marshalMessage(model string, req *types.ChatRequest, stream bool) ([]byte, error) {
	payload := messageRequest{
		Model:       model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      stream,
	}
	if payload.MaxTokens < 1 {
		payload.MaxTokens = 1024
	}
	var system []string
	for _, msg := range req.Messages {
		if strings.EqualFold(msg.Role, "system") {
			if msg.Content != "" {
				system = append(system, msg.Content)
			}
			continue
		}
		role := msg.Role
		if !strings.EqualFold(role, "assistant") {
			role = "user"
		}
		payload.Messages = append(payload.Messages, map[string]string{
			"role":    role,
			"content": msg.Content,
		})
	}
	payload.System = strings.Join(system, "\n\n")
	if len(payload.Messages) == 0 {
		return nil, &provider.Error{Message: "claude requires at least one non-system message"}
	}
	return json.Marshal(payload)
}

type tokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func (u tokenUsage) asUsage() types.Usage {
	return types.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.InputTokens + u.OutputTokens,
	}
}

type messageResponse struct {
	ID      string `json:"id"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage tokenUsage `json:"usage"`
}

func (r messageResponse) Text() string {
	var b strings.Builder
	for _, block := range r.Content {
		b.WriteString(block.Text)
	}
	return b.String()
}

type streamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
	Message struct {
		Usage struct {
			InputTokens int64 `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage struct {
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func parseEvent(event string, payload []byte) (text string, usage types.Usage) {
	var ev streamEvent
	if json.Unmarshal(payload, &ev) != nil {
		return "", types.Usage{}
	}
	if event == "" {
		event = ev.Type
	}
	switch event {
	case "content_block_delta":
		return ev.Delta.Text, types.Usage{}
	case "message_start":
		return "", types.Usage{PromptTokens: ev.Message.Usage.InputTokens}
	case "message_delta":
		return "", types.Usage{CompletionTokens: ev.Usage.OutputTokens}
	default:
		return "", types.Usage{}
	}
}

func httpError(resp *http.Response, body []byte) error {
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
	return &provider.Error{StatusCode: resp.StatusCode, Message: msg}
}

func Register(reg *provider.Registry) {
	reg.Register("claude", func(ep endpoint.Endpoint) provider.ModelProvider { return New(ep) })
	reg.Register("anthropic", func(ep endpoint.Endpoint) provider.ModelProvider { return New(ep) })
}
