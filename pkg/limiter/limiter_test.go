package limiter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestPreDeductRateLimit(t *testing.T) {
	lim := newTestLimiter(t, 2, time.Second, 1000)
	now := time.UnixMicro(1_000_000)
	lim.now = func() time.Time { return now }

	assertPreDeduct(t, lim, "alice", 10, true, nil)
	assertPreDeduct(t, lim, "alice", 10, true, nil)
	assertPreDeduct(t, lim, "alice", 10, false, ErrRateLimitExceeded)
	assertPreDeduct(t, lim, "bob", 10, true, nil)
}

func TestPreDeductInsufficientBalance(t *testing.T) {
	lim := newTestLimiter(t, 100, time.Second, 15)
	assertPreDeduct(t, lim, "alice", 10, true, nil)
	assertPreDeduct(t, lim, "alice", 10, false, ErrInsufficientBalance)
}

func TestSettleQuotaRefundAndCharge(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	lim, err := New(rdb, Config{Limit: 100, Window: time.Second, DefaultBalance: 100})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if ok, err := lim.PreDeduct(ctx, "alice", 40); err != nil || !ok {
		t.Fatalf("prededuct: ok=%v err=%v", ok, err)
	}

	if err := lim.SettleQuota(ctx, "alice", 40, 12); err != nil {
		t.Fatal(err)
	}
	got, err := rdb.Get(ctx, balanceKey("alice")).Int64()
	if err != nil {
		t.Fatal(err)
	}
	if got != 88 {
		t.Fatalf("after refund balance = %d, want 88", got)
	}

	if err := lim.SettleQuota(ctx, "alice", 10, 18); err != nil {
		t.Fatal(err)
	}
	got, err = rdb.Get(ctx, balanceKey("alice")).Int64()
	if err != nil {
		t.Fatal(err)
	}
	if got != 80 {
		t.Fatalf("after extra charge balance = %d, want 80", got)
	}
}

func TestPreDeductSharedAcrossClients(t *testing.T) {
	server := miniredis.RunT(t)
	left, err := New(redis.NewClient(&redis.Options{Addr: server.Addr()}), Config{
		Limit: 10, Window: time.Second, DefaultBalance: 10_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := New(redis.NewClient(&redis.Options{Addr: server.Addr()}), Config{
		Limit: 10, Window: time.Second, DefaultBalance: 10_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.UnixMicro(1_700_000_000_000_000)
	left.now = func() time.Time { return fixed }
	right.now = func() time.Time { return fixed }

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		lim := left
		if i%2 == 1 {
			lim = right
		}
		go func(l *Limiter) {
			defer wg.Done()
			ok, err := l.PreDeduct(context.Background(), "shared", 1)
			if err != nil && !errors.Is(err, ErrRateLimitExceeded) {
				t.Errorf("prededuct: %v", err)
				return
			}
			if ok {
				allowed.Add(1)
			}
		}(lim)
	}
	wg.Wait()
	if got := allowed.Load(); got != 10 {
		t.Fatalf("allowed = %d, want 10", got)
	}
}

func newTestLimiter(t *testing.T, limit int64, window time.Duration, balance int64) *Limiter {
	t.Helper()
	server := miniredis.RunT(t)
	lim, err := New(redis.NewClient(&redis.Options{Addr: server.Addr()}), Config{
		Limit: limit, Window: window, DefaultBalance: balance,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lim
}

func assertPreDeduct(t *testing.T, lim *Limiter, user string, tokens int64, wantOK bool, wantErr error) {
	t.Helper()
	ok, err := lim.PreDeduct(context.Background(), user, tokens)
	if wantErr != nil {
		if !errors.Is(err, wantErr) {
			t.Fatalf("PreDeduct(%s) err = %v, want %v", user, err, wantErr)
		}
	} else if err != nil {
		t.Fatalf("PreDeduct(%s) unexpected err %v", user, err)
	}
	if ok != wantOK {
		t.Fatalf("PreDeduct(%s) ok = %v, want %v", user, ok, wantOK)
	}
}
