package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Memory is a process-local sliding window. Two gateway pods each have
// their own map, so they can over-admit compared with the configured limit.
type Memory struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	windows map[string][]int64
}

func NewMemory(limit int, window time.Duration) *Memory {
	return &Memory{
		limit:   limit,
		window:  window,
		now:     time.Now,
		windows: make(map[string][]int64),
	}
}

func (m *Memory) Allow(_ context.Context, key string) (Decision, error) {
	now := m.now().UnixMilli()
	cutoff := now - windowMillis(m.window)

	m.mu.Lock()
	defer m.mu.Unlock()

	stamps := m.windows[key]
	kept := stamps[:0]
	for _, ts := range stamps {
		if ts > cutoff {
			kept = append(kept, ts)
		}
	}

	limit := int64(m.limit)
	count := int64(len(kept))
	if count >= limit {
		if len(kept) == 0 {
			delete(m.windows, key)
		} else {
			m.windows[key] = kept
		}
		return Decision{Allowed: false, Count: count, Limit: limit}, nil
	}

	kept = append(kept, now)
	m.windows[key] = kept
	return Decision{Allowed: true, Count: count + 1, Limit: limit}, nil
}
