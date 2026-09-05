package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/types"
)

type ModelProvider interface {
	Chat(ctx context.Context, req *types.ChatRequest) (*types.ChatResponse, error)
	ChatStream(ctx context.Context, req *types.ChatRequest, w http.ResponseWriter) (*types.Usage, error)
}

type Error struct {
	StatusCode int
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.StatusCode == 0 {
		return e.Message
	}
	return fmt.Sprintf("provider status %d: %s", e.StatusCode, e.Message)
}

func (e *Error) Retryable() bool {
	if e.StatusCode == 429 || e.StatusCode == 408 {
		return true
	}
	if e.StatusCode >= 500 {
		return true
	}
	return e.StatusCode == 0
}

type FactoryFunc func(endpoint.Endpoint) ModelProvider

type Registry struct {
	mu        sync.Mutex
	factories map[string]FactoryFunc
	cache     map[string]ModelProvider
}

func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]FactoryFunc),
		cache:     make(map[string]ModelProvider),
	}
}

func (r *Registry) Register(name string, fn FactoryFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[strings.ToLower(name)] = fn
}

func (r *Registry) For(ep endpoint.Endpoint) (ModelProvider, error) {
	name := strings.ToLower(strings.TrimSpace(ep.Provider))
	if name == "" {
		name = "openai"
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	key := ep.ID + "|" + ep.BaseURL + "|" + name
	if p, ok := r.cache[key]; ok {
		return p, nil
	}
	fn, ok := r.factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", ep.Provider)
	}
	p := fn(ep)
	r.cache[key] = p
	return p, nil
}
