package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Memory maps sha256(api_key) -> user. Keys are hashed at load time.
type Memory struct {
	mu   sync.RWMutex
	keys map[string]Record
}

func NewMemory() *Memory {
	return &Memory{keys: make(map[string]Record)}
}

// ParseKeyList loads "sk-alice:alice,sk-bob:bob" style pairs.
func ParseKeyList(value string) (*Memory, error) {
	m := NewMemory()
	for entry := range strings.SplitSeq(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, user, ok := strings.Cut(entry, ":")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(user) == "" {
			return nil, fmt.Errorf("invalid GATEWAY_API_KEYS entry %q, expected key:user", entry)
		}
		m.Put(strings.TrimSpace(key), Record{
			UserID: strings.TrimSpace(user),
			Name:   strings.TrimSpace(user),
			Status: "active",
		})
	}
	return m, nil
}

func (m *Memory) Put(rawKey string, rec Record) {
	if rec.Status == "" {
		rec.Status = "active"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[HashKey(rawKey)] = rec
}

func (m *Memory) PutHash(keyHash string, rec Record) {
	if rec.Status == "" {
		rec.Status = "active"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[keyHash] = rec
}

func (m *Memory) LookupKey(_ context.Context, keyHash string) (Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.keys[keyHash]
	if !ok {
		return Record{}, ErrInvalid
	}
	return rec, nil
}

func (m *Memory) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.keys)
}
