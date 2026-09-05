package ratelimit

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed sliding_window.lua
var slidingWindowLua string

var slidingWindowScript = redis.NewScript(slidingWindowLua)

// Redis shares one sorted set across every gateway replica. The Lua script
// makes evict → count → decide → insert a single Redis command.
type Redis struct {
	rdb    redis.Cmdable
	prefix string
	id     string
	limit  int
	window time.Duration
	now    func() time.Time
	seq    atomic.Uint64
}

func NewRedis(rdb redis.Cmdable, limit int, window time.Duration) *Redis {
	return &Redis{
		rdb:    rdb,
		prefix: "gw:rl:",
		id:     newInstanceID(),
		limit:  limit,
		window: window,
		now:    time.Now,
	}
}

func (r *Redis) Allow(ctx context.Context, key string) (Decision, error) {
	now := r.now().UnixMilli()
	member := strconv.FormatInt(now, 10) + "-" + r.id + "-" + strconv.FormatUint(r.seq.Add(1), 10)
	values, err := slidingWindowScript.Run(ctx, r.rdb, []string{r.prefix + key},
		strconv.FormatInt(now, 10),
		strconv.FormatInt(windowMillis(r.window), 10),
		strconv.Itoa(r.limit),
		member,
	).Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("sliding window: %w", err)
	}
	allowed, count, err := parsePair(values)
	if err != nil {
		return Decision{}, err
	}
	return Decision{Allowed: allowed, Count: count, Limit: int64(r.limit)}, nil
}

func newInstanceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}
