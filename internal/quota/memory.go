package quota

import (
	"context"
	"fmt"
	"sync"
)

// Memory is a process-local balance. Two gateway pods can each deduct the
// last token and overspend compared with Redis pre-authorization.
type Memory struct {
	defaultBalance int64
	mu             sync.Mutex
	balances       map[string]int64
}

func NewMemory(defaultBalance int64) *Memory {
	return &Memory{
		defaultBalance: defaultBalance,
		balances:       make(map[string]int64),
	}
}

func (m *Memory) Reserve(_ context.Context, account string, cost int64) (Reservation, error) {
	if cost < 1 {
		return Reservation{}, fmt.Errorf("cost must be positive")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	current, ok := m.balances[account]
	if !ok {
		current = m.defaultBalance
	}
	if current < cost {
		return Reservation{Allowed: false, Remaining: current}, nil
	}
	remaining := current - cost
	m.balances[account] = remaining
	return Reservation{Allowed: true, Remaining: remaining}, nil
}
