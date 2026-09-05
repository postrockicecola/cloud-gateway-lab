package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSubmitPrefersHighPriority(t *testing.T) {
	s, err := New(1, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	started := make(chan struct{})
	release := make(chan struct{})
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.Submit(ctx, func(context.Context) {
			close(started)
			<-release
		}, PriorityLow); err != nil {
			t.Errorf("blocker: %v", err)
		}
	}()
	<-started

	var mu sync.Mutex
	var order []string

	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := s.Submit(ctx, func(context.Context) {
			mu.Lock()
			order = append(order, "low")
			mu.Unlock()
		}, PriorityLow); err != nil {
			t.Errorf("low: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.Submit(ctx, func(context.Context) {
			mu.Lock()
			order = append(order, "high")
			mu.Unlock()
		}, PriorityHigh); err != nil {
			t.Errorf("high: %v", err)
		}
	}()

	// Both Submit calls block until a worker runs them; give them time
	// to land in the priority channels before freeing the blocker.
	time.Sleep(40 * time.Millisecond)
	close(release)
	wg.Wait()

	if len(order) != 2 || order[0] != "high" || order[1] != "low" {
		t.Fatalf("order = %v, want [high low]", order)
	}
}

func TestSubmitRespectsConcurrency(t *testing.T) {
	s, err := New(2, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	var running atomicInt
	var peak atomicInt
	var wg sync.WaitGroup
	ctx := context.Background()

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Submit(ctx, func(context.Context) {
				n := running.add(1)
				peak.max(n)
				time.Sleep(20 * time.Millisecond)
				running.add(-1)
			}, PriorityLow)
		}()
	}
	wg.Wait()
	if got := peak.load(); got > 2 {
		t.Fatalf("peak concurrency = %d, want <= 2", got)
	}
}

func TestSubmitCanceled(t *testing.T) {
	s, err := New(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	block := make(chan struct{})
	go func() {
		_ = s.Submit(context.Background(), func(context.Context) {
			<-block
		}, PriorityLow)
	}()
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := s.Submit(ctx, func(context.Context) {}, PriorityLow); err == nil {
		t.Fatal("expected context error while worker is busy")
	}
	close(block)
}

type atomicInt struct {
	mu sync.Mutex
	n  int
}

func (a *atomicInt) add(d int) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n += d
	return a.n
}

func (a *atomicInt) max(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n > a.n {
		a.n = n
	}
}

func (a *atomicInt) load() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}
