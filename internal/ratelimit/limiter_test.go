package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestMemoryLimiterRejectsAfterLimit(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	limiter := NewMemory(2, time.Second)
	limiter.now = func() time.Time { return now }

	assertAllow(t, limiter, "user:1", true, 1)
	assertAllow(t, limiter, "user:1", true, 2)
	assertAllow(t, limiter, "user:1", false, 2)
	assertAllow(t, limiter, "user:2", true, 1)
}

func TestMemoryLimiterSlides(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	limiter := NewMemory(1, time.Second)
	limiter.now = func() time.Time { return now }

	assertAllow(t, limiter, "user:1", true, 1)
	assertAllow(t, limiter, "user:1", false, 1)

	now = now.Add(time.Second + time.Millisecond)
	assertAllow(t, limiter, "user:1", true, 1)
}

func TestMemoryLimiterDoesNotShareState(t *testing.T) {
	left := NewMemory(1, time.Second)
	right := NewMemory(1, time.Second)
	assertAllow(t, left, "user:1", true, 1)
	assertAllow(t, right, "user:1", true, 1)
}

func TestRedisLimiterSharedAcrossClients(t *testing.T) {
	server := miniredis.RunT(t)
	limit := 10
	left := NewRedis(redis.NewClient(&redis.Options{Addr: server.Addr()}), limit, time.Second)
	right := NewRedis(redis.NewClient(&redis.Options{Addr: server.Addr()}), limit, time.Second)
	fixed := time.UnixMilli(1_700_000_000_000)
	left.now = func() time.Time { return fixed }
	right.now = func() time.Time { return fixed }

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		limiter := left
		if i%2 == 1 {
			limiter = right
		}
		go func(l *Redis) {
			defer wg.Done()
			decision, err := l.Allow(context.Background(), "user:shared")
			if err != nil {
				t.Errorf("allow: %v", err)
				return
			}
			if decision.Allowed {
				allowed.Add(1)
			}
		}(limiter)
	}
	wg.Wait()

	if got := allowed.Load(); got != int64(limit) {
		t.Fatalf("allowed = %d, want %d", got, limit)
	}
}

func TestRedisLimiterSlides(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.UnixMilli(1_000_000)
	limiter := NewRedis(redis.NewClient(&redis.Options{Addr: server.Addr()}), 1, time.Second)
	limiter.now = func() time.Time { return now }

	assertAllow(t, limiter, "user:1", true, 1)
	assertAllow(t, limiter, "user:1", false, 1)

	now = now.Add(time.Second + time.Millisecond)
	assertAllow(t, limiter, "user:1", true, 1)
}

func assertAllow(t *testing.T, limiter Limiter, key string, want bool, wantCount int64) {
	t.Helper()
	decision, err := limiter.Allow(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed != want || decision.Count != wantCount {
		t.Fatalf("allow(%q) = %+v, want allowed=%v count=%d", key, decision, want, wantCount)
	}
}
