package store

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"cloud-gateway-lab/internal/auth"
)

type countingStore struct {
	inner *auth.Memory
	hits  atomic.Int64
}

func (c *countingStore) LookupKey(ctx context.Context, keyHash string) (auth.Record, error) {
	c.hits.Add(1)
	return c.inner.LookupKey(ctx, keyHash)
}

func TestCachedKeysHitAndNull(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	mem := auth.NewMemory()
	mem.Put("sk-alice", auth.Record{UserID: "alice", Status: "active"})
	inner := &countingStore{inner: mem}
	cache := NewCachedKeys(inner, rdb, time.Minute, time.Second)

	ctx := context.Background()
	rec, err := cache.LookupKey(ctx, auth.HashKey("sk-alice"))
	if err != nil || rec.UserID != "alice" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
	_, err = cache.LookupKey(ctx, auth.HashKey("sk-alice"))
	if err != nil {
		t.Fatal(err)
	}
	if inner.hits.Load() != 1 {
		t.Fatalf("inner hits = %d, want 1", inner.hits.Load())
	}

	_, err = cache.LookupKey(ctx, auth.HashKey("missing"))
	if err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	_, err = cache.LookupKey(ctx, auth.HashKey("missing"))
	if err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	if inner.hits.Load() != 2 {
		t.Fatalf("null should be cached, hits = %d", inner.hits.Load())
	}
}

func TestCachedKeysTTLJitter(t *testing.T) {
	c := NewCachedKeys(auth.NewMemory(), redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"}), 30*time.Minute, 5*time.Minute)
	ttl := c.jitteredTTL()
	if ttl < 30*time.Minute || ttl > 35*time.Minute {
		t.Fatalf("ttl = %s", ttl)
	}
}
