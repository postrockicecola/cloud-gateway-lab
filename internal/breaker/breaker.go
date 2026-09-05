package breaker

import (
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Open:
		return "OPEN"
	case HalfOpen:
		return "HALF_OPEN"
	default:
		return "CLOSED"
	}
}

type Config struct {
	FailureThreshold int
	Cooldown         time.Duration
}

func (c Config) withDefaults() Config {
	if c.FailureThreshold < 1 {
		c.FailureThreshold = 5
	}
	if c.Cooldown <= 0 {
		c.Cooldown = 10 * time.Second
	}
	return c
}

type Breaker struct {
	cfg Config

	mu               sync.Mutex
	state            State
	failures         int
	openedAt         time.Time
	halfOpenInFlight bool
}

func New(cfg Config) *Breaker {
	return &Breaker{cfg: cfg.withDefaults()}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpenLocked(time.Now())
	return b.state
}

func (b *Breaker) Eligible() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.maybeHalfOpenLocked(now)
	switch b.state {
	case Closed:
		return true
	case HalfOpen:
		return !b.halfOpenInFlight
	default:
		return false
	}
}

func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.maybeHalfOpenLocked(now)
	switch b.state {
	case Closed:
		return true
	case HalfOpen:
		if b.halfOpenInFlight {
			return false
		}
		b.halfOpenInFlight = true
		return true
	default:
		return false
	}
}

func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.state = Closed
	b.halfOpenInFlight = false
}

func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == HalfOpen {
		b.state = Open
		b.openedAt = time.Now()
		b.halfOpenInFlight = false
		return
	}
	b.failures++
	if b.failures >= b.cfg.FailureThreshold {
		b.state = Open
		b.openedAt = time.Now()
	}
}

// Release frees a half-open probe without counting success or failure.
func (b *Breaker) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.halfOpenInFlight = false
}

func (b *Breaker) maybeHalfOpenLocked(now time.Time) {
	if b.state == Open && now.Sub(b.openedAt) >= b.cfg.Cooldown {
		b.state = HalfOpen
		b.halfOpenInFlight = false
	}
}
