package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/provider"
	"cloud-gateway-lab/internal/types"
)

type Adapter struct {
	baseURL    string
	apiKey     string
	modelName  string
	client *http.Client
}

func New(ep endpoint.Endpoint) *Adapter {
	timeout := ep.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Adapter{
		baseURL:   strings.TrimRight(ep.BaseURL, "/"),
		apiKey:    ep.APIKey,
		modelName: ep.ModelName,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          200,
				MaxIdleConnsPerHost:   32,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: timeout,
			},
		},
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

	var parsed completionResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, &provider.Error{Message: "decode provider response: " + err.Error()}
	}
	return &types.ChatResponse{
		ID:      parsed.ID,
		Model:   req.Model,
		Content: parsed.Content(),
		Usage: types.Usage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		},
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

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	reader := bufio.NewReaderSize(resp.Body, 32*1024)
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
			parseSSELine(line, &generated, &usage)
		}
		if err != nil {
			if err != io.EOF && err != io.ErrClosedPipe {
				return usageOrEstimate(usage, generated.String()), &provider.Error{Message: "read stream: " + err.Error()}
			}
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	return usageOrEstimate(usage, generated.String()), nil
}

func (a *Adapter) do(ctx context.Context, req *types.ChatRequest, stream bool) (*http.Response, error) {
	payload := completionRequest{
		Model:       a.upstreamModel(req.Model),
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
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &provider.Error{Message: err.Error()}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, &provider.Error{Message: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

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

type completionRequest struct {
	Model       string              `json:"model"`
	Messages    []map[string]string `json:"messages"`
	Temperature float32             `json:"temperature,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
}

type completionResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
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

func parseSSELine(line []byte, generated *strings.Builder, usage *types.Usage) {
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

func usageOrEstimate(usage types.Usage, generated string) *types.Usage {
	if usage.TotalTokens > 0 {
		return &usage
	}
	if usage.CompletionTokens == 0 && generated != "" {
		usage.CompletionTokens = int64((len(generated) + 3) / 4)
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return &usage
}

func Register(reg *provider.Registry) {
	reg.Register("openai", func(ep endpoint.Endpoint) provider.ModelProvider { return New(ep) })
	reg.Register("ollama", func(ep endpoint.Endpoint) provider.ModelProvider { return New(ep) })
	reg.Register("azure", func(ep endpoint.Endpoint) provider.ModelProvider { return New(ep) })
}
