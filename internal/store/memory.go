package store

import (
	"context"
	"sync"

	"cloud-gateway-lab/internal/auth"
	"cloud-gateway-lab/internal/endpoint"
)

type Memory struct {
	keys      *auth.Memory
	mu        sync.RWMutex
	endpoints []endpoint.Endpoint
}

func NewMemory(keys *auth.Memory, endpoints []endpoint.Endpoint) *Memory {
	if keys == nil {
		keys = auth.NewMemory()
	}
	return &Memory{keys: keys, endpoints: append([]endpoint.Endpoint(nil), endpoints...)}
}

func (m *Memory) LookupKey(ctx context.Context, keyHash string) (auth.Record, error) {
	rec, err := m.keys.LookupKey(ctx, keyHash)
	if err != nil {
		return auth.Record{}, ErrNotFound
	}
	return rec, nil
}

func (m *Memory) ListEndpoints(_ context.Context) ([]endpoint.Endpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]endpoint.Endpoint(nil), m.endpoints...), nil
}

func (m *Memory) ReplaceEndpoints(endpoints []endpoint.Endpoint) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.endpoints = append([]endpoint.Endpoint(nil), endpoints...)
}
