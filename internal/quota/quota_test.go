package quota

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestMemoryReserve(t *testing.T) {
	accountant := NewMemory(2)
	assertReserve(t, accountant, "user:1", true, 1)
	assertReserve(t, accountant, "user:1", true, 0)
	assertReserve(t, accountant, "user:1", false, 0)
	assertReserve(t, accountant, "user:2", true, 1)
}

func TestRedisReserveSharedAcrossClients(t *testing.T) {
	server := miniredis.RunT(t)
	left := NewRedis(redis.NewClient(&redis.Options{Addr: server.Addr()}), 10)
	right := NewRedis(redis.NewClient(&redis.Options{Addr: server.Addr()}), 10)

	var reserved atomic.Int64
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		accountant := left
		if i%2 == 1 {
			accountant = right
		}
		go func(a *Redis) {
			defer wg.Done()
			reservation, err := a.Reserve(context.Background(), "user:shared", 1)
			if err != nil {
				t.Errorf("reserve: %v", err)
				return
			}
			if reservation.Allowed {
				reserved.Add(1)
			}
		}(accountant)
	}
	wg.Wait()

	if got := reserved.Load(); got != 10 {
		t.Fatalf("reserved = %d, want 10", got)
	}
}

func assertReserve(t *testing.T, accountant Accountant, account string, want bool, wantRemaining int64) {
	t.Helper()
	reservation, err := accountant.Reserve(context.Background(), account, 1)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.Allowed != want || reservation.Remaining != wantRemaining {
		t.Fatalf("reserve(%q) = %+v, want allowed=%v remaining=%d", account, reservation, want, wantRemaining)
	}
}
