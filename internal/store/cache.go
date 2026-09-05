package store

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"cloud-gateway-lab/internal/auth"
)

const nullSentinel = "{null}"

// CachedKeys wraps an API key store with Redis + singleflight.
// Null results are cached briefly; positive TTLs are jittered.
type CachedKeys struct {
	inner      APIKeyStore
	rdb        redis.Cmdable
	ttl        time.Duration
	jitter     time.Duration
	nullTTL    time.Duration
	group      singleflight.Group
	lockWait   time.Duration
}

func NewCachedKeys(inner APIKeyStore, rdb redis.Cmdable, ttl, jitter time.Duration) *CachedKeys {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if jitter <= 0 {
		jitter = 5 * time.Minute
	}
	return &CachedKeys{
		inner:    inner,
		rdb:      rdb,
		ttl:      ttl,
		jitter:   jitter,
		nullTTL:  30 * time.Second,
		lockWait: 20 * time.Millisecond,
	}
}

func (c *CachedKeys) LookupKey(ctx context.Context, keyHash string) (auth.Record, error) {
	rkey := "gw:apikey:" + keyHash
	if rec, ok, err := c.fromRedis(ctx, rkey); err == nil && ok {
		if rec.UserID == "" && rec.Status == "" {
			return auth.Record{}, ErrNotFound
		}
		return rec, nil
	}

	v, err, _ := c.group.Do(keyHash, func() (any, error) {
		if rec, ok, err := c.fromRedis(ctx, rkey); err == nil && ok {
			return rec, nil
		}

		lockKey := rkey + ":lock"
		gotLock, lockErr := c.rdb.SetNX(ctx, lockKey, "1", 5*time.Second).Result()
		if lockErr == nil && !gotLock {
			time.Sleep(c.lockWait)
			if rec, ok, err := c.fromRedis(ctx, rkey); err == nil && ok {
				return rec, nil
			}
		}

		rec, err := c.inner.LookupKey(ctx, keyHash)
		if err != nil {
			if errors.Is(err, ErrNotFound) || errors.Is(err, auth.ErrInvalid) {
				_ = c.rdb.Set(ctx, rkey, nullSentinel, c.nullTTL).Err()
				_ = c.rdb.Del(ctx, lockKey).Err()
				return auth.Record{}, ErrNotFound
			}
			_ = c.rdb.Del(ctx, lockKey).Err()
			return auth.Record{}, err
		}
		body, _ := json.Marshal(rec)
		_ = c.rdb.Set(ctx, rkey, body, c.jitteredTTL()).Err()
		_ = c.rdb.Del(ctx, lockKey).Err()
		return rec, nil
	})
	if err != nil {
		return auth.Record{}, err
	}
	rec := v.(auth.Record)
	if rec.UserID == "" && rec.Status == "" {
		return auth.Record{}, ErrNotFound
	}
	return rec, nil
}

func (c *CachedKeys) fromRedis(ctx context.Context, rkey string) (auth.Record, bool, error) {
	val, err := c.rdb.Get(ctx, rkey).Result()
	if err == redis.Nil {
		return auth.Record{}, false, nil
	}
	if err != nil {
		return auth.Record{}, false, err
	}
	if val == nullSentinel {
		return auth.Record{}, true, nil
	}
	var rec auth.Record
	if err := json.Unmarshal([]byte(val), &rec); err != nil {
		return auth.Record{}, false, err
	}
	return rec, true, nil
}

func (c *CachedKeys) jitteredTTL() time.Duration {
	if c.jitter <= 0 {
		return c.ttl
	}
	return c.ttl + time.Duration(rand.Int63n(int64(c.jitter)+1))
}
