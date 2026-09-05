package endpoint

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"cloud-gateway-lab/internal/breaker"
)

var ErrNoEndpoint = errors.New("no healthy endpoint")

type Result struct {
	Success    bool
	StatusCode int
	Latency    time.Duration
	Retryable  bool
}

type member struct {
	ep       Endpoint
	health   Health
	breaker  *breaker.Breaker
	current  int
	latency  atomic.Int64
}

type Pool struct {
	breakerCfg breaker.Config

	mu      sync.Mutex
	byModel map[string][]*member
	byID    map[string]*member
}

func NewPool(endpoints []Endpoint, breakerCfg breaker.Config) (*Pool, error) {
	p := &Pool{
		breakerCfg: breakerCfg,
		byModel:    make(map[string][]*member),
		byID:       make(map[string]*member),
	}
	if err := p.Replace(endpoints); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Pool) Replace(endpoints []Endpoint) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	nextModel := make(map[string][]*member)
	nextID := make(map[string]*member)
	for _, ep := range endpoints {
		if err := Validate(ep); err != nil {
			return err
		}
		if ep.Weight == 0 {
			ep.Weight = 1
		}
		if ep.Provider == "" {
			ep.Provider = "openai"
		}
		m := &member{ep: ep, health: Healthy, breaker: breaker.New(p.breakerCfg)}
		if old, ok := p.byID[ep.ID]; ok {
			m.breaker = old.breaker
			m.health = old.health
			m.current = old.current
			m.latency.Store(old.latency.Load())
		}
		nextModel[ep.Model] = append(nextModel[ep.Model], m)
		nextID[ep.ID] = m
	}
	p.byModel = nextModel
	p.byID = nextID
	return nil
}

func (p *Pool) Pick(model string, exclude map[string]struct{}) (Endpoint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	members := p.byModel[model]
	var cands []*member
	for _, m := range members {
		if _, skip := exclude[m.ep.ID]; skip {
			continue
		}
		if m.health != Healthy {
			continue
		}
		if m.ep.Weight <= 0 {
			continue
		}
		if !m.breaker.Eligible() {
			continue
		}
		cands = append(cands, m)
	}
	if len(cands) == 0 {
		return Endpoint{}, ErrNoEndpoint
	}

	chosen := nextWRR(cands)
	if !chosen.breaker.Allow() {
		excludeCopy := exclude
		if excludeCopy == nil {
			excludeCopy = map[string]struct{}{}
		}
		excludeCopy[chosen.ep.ID] = struct{}{}
		// Try once more among remaining candidates without nesting locks.
		var rest []*member
		for _, m := range cands {
			if m.ep.ID == chosen.ep.ID {
				continue
			}
			if m.breaker.Eligible() {
				rest = append(rest, m)
			}
		}
		if len(rest) == 0 {
			return Endpoint{}, ErrNoEndpoint
		}
		chosen = nextWRR(rest)
		if !chosen.breaker.Allow() {
			return Endpoint{}, ErrNoEndpoint
		}
	}
	return chosen.ep, nil
}

func (p *Pool) Report(id string, result Result) {
	p.mu.Lock()
	m := p.byID[id]
	p.mu.Unlock()
	if m == nil {
		return
	}
	if result.Success {
		m.breaker.Success()
		if result.Latency > 0 {
			prev := m.latency.Load()
			if prev == 0 {
				m.latency.Store(result.Latency.Milliseconds())
			} else {
				// Simple EWMA: 0.7 prev + 0.3 next.
				m.latency.Store((prev*7 + result.Latency.Milliseconds()*3) / 10)
			}
		}
		return
	}
	if result.StatusCode == 429 || !result.Retryable {
		m.breaker.Release()
		return
	}
	m.breaker.Failure()
}

func (p *Pool) SetHealth(id string, health Health) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := p.byID[id]; ok {
		m.health = health
	}
}

func (p *Pool) Snapshot() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Status, 0, len(p.byID))
	for _, m := range p.byID {
		out = append(out, Status{
			Endpoint:  m.ep,
			Health:    m.health,
			Breaker:   m.breaker.State(),
			LatencyMS: m.latency.Load(),
		})
	}
	return out
}

func (p *Pool) All() []Endpoint {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Endpoint, 0, len(p.byID))
	for _, m := range p.byID {
		out = append(out, m.ep)
	}
	return out
}

func (p *Pool) Get(id string) (Endpoint, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.byID[id]
	if !ok {
		return Endpoint{}, false
	}
	return m.ep, true
}

type Status struct {
	Endpoint  Endpoint
	Health    Health
	Breaker   breaker.State
	LatencyMS int64
}
