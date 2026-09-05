package azure

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/provider"
	"cloud-gateway-lab/internal/provider/openaicompat"
	"cloud-gateway-lab/internal/types"
)

// Adapter talks to Azure OpenAI. The JSON body is OpenAI-compatible;
// the URL and api-key header are not.
type Adapter struct {
	baseURL    string
	apiKey     string
	deployment string
	apiVersion string
	client     *http.Client
}

func New(ep endpoint.Endpoint) *Adapter {
	return &Adapter{
		baseURL:    strings.TrimRight(ep.BaseURL, "/"),
		apiKey:     ep.APIKey,
		deployment: ep.ModelName,
		apiVersion: ep.AzureAPIVersion(),
		client:     provider.NewHTTPClient(ep.Timeout),
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
		return nil, openaicompat.HTTPError(resp, body)
	}
	id, content, usage, err := openaicompat.ParseCompletion(body)
	if err != nil {
		return nil, &provider.Error{Message: "decode provider response: " + err.Error()}
	}
	return &types.ChatResponse{ID: id, Model: req.Model, Content: content, Usage: usage}, nil
}

func (a *Adapter) ChatStream(ctx context.Context, req *types.ChatRequest, w http.ResponseWriter) (*types.Usage, error) {
	resp, err := a.do(ctx, req, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, openaicompat.HTTPError(resp, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	return openaicompat.CopyStream(ctx, resp.Body, w)
}

func (a *Adapter) do(ctx context.Context, req *types.ChatRequest, stream bool) (*http.Response, error) {
	// Azure binds the model to the deployment in the path; omit body.model.
	body, err := openaicompat.MarshalRequest("", req, stream)
	if err != nil {
		return nil, &provider.Error{Message: err.Error()}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.chatURL(req.Model), bytes.NewReader(body))
	if err != nil {
		return nil, &provider.Error{Message: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		httpReq.Header.Set("api-key", a.apiKey)
	}
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, &provider.Error{Message: err.Error()}
	}
	return resp, nil
}

func (a *Adapter) chatURL(requested string) string {
	deploy := a.deployment
	if deploy == "" {
		deploy = requested
	}
	root := a.baseURL
	if !strings.Contains(root, "/openai") {
		root += "/openai"
	}
	u, err := url.Parse(root + "/deployments/" + url.PathEscape(deploy) + "/chat/completions")
	if err != nil {
		return root + "/deployments/" + deploy + "/chat/completions?api-version=" + a.apiVersion
	}
	q := u.Query()
	q.Set("api-version", a.apiVersion)
	u.RawQuery = q.Encode()
	return u.String()
}

func Register(reg *provider.Registry) {
	reg.Register("azure", func(ep endpoint.Endpoint) provider.ModelProvider { return New(ep) })
}
