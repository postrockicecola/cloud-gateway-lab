package limiter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrRateLimitExceeded   = errors.New("RATE_LIMIT_EXCEEDED")
	ErrInsufficientBalance = errors.New("INSUFFICIENT_BALANCE")
)

// Limiter atomically rate-limits and pre-deducts token quota in Redis.
type Limiter struct {
	rdb            redis.Cmdable
	script         *redis.Script
	limit          int64
	window         time.Duration
	defaultBalance int64
	now            func() time.Time
	seq            atomic.Uint64
}

type Config struct {
	Limit          int64
	Window         time.Duration
	DefaultBalance int64
	LuaScript      string
}

func New(rdb redis.Cmdable, cfg Config) (*Limiter, error) {
	if rdb == nil {
		return nil, errors.New("redis client is required")
	}
	if cfg.Limit < 1 {
		return nil, errors.New("limit must be positive")
	}
	if cfg.Window <= 0 {
		return nil, errors.New("window must be positive")
	}
	script := cfg.LuaScript
	if script == "" {
		loaded, err := loadLuaScript()
		if err != nil {
			return nil, err
		}
		script = loaded
	}
	return &Limiter{
		rdb:            rdb,
		script:         redis.NewScript(script),
		limit:          cfg.Limit,
		window:         cfg.Window,
		defaultBalance: cfg.DefaultBalance,
		now:            time.Now,
	}, nil
}

// PreDeduct checks the sliding window and freezes tokens on the user balance.
// A false result with a sentinel error means a business rejection; any other
// error is a Redis / script failure.
func (l *Limiter) PreDeduct(ctx context.Context, userID string, tokens int64) (bool, error) {
	if userID == "" {
		return false, errors.New("userID is required")
	}
	if tokens < 1 {
		return false, errors.New("tokens must be positive")
	}

	if l.defaultBalance > 0 {
		if err := l.rdb.SetNX(ctx, balanceKey(userID), l.defaultBalance, 0).Err(); err != nil {
			return false, fmt.Errorf("seed balance: %w", err)
		}
	}

	now := l.now().UnixMicro()
	// Keep scores unique when two requests land in the same microsecond.
	now += int64(l.seq.Add(1) % 1000)

	values, err := l.script.Run(ctx, l.rdb,
		[]string{rateLimitKey(userID), balanceKey(userID)},
		strconv.FormatInt(now, 10),
		strconv.FormatInt(windowMicros(l.window), 10),
		strconv.FormatInt(l.limit, 10),
		strconv.FormatInt(tokens, 10),
	).Slice()
	if err != nil {
		return false, fmt.Errorf("prededuct: %w", err)
	}
	if len(values) < 1 {
		return false, errors.New("prededuct: empty script result")
	}

	status, ok := values[0].(string)
	if !ok {
		return false, fmt.Errorf("prededuct: unexpected status type %T", values[0])
	}
	switch status {
	case "OK":
		return true, nil
	case "RATE_LIMIT_EXCEEDED":
		return false, ErrRateLimitExceeded
	case "INSUFFICIENT_BALANCE":
		return false, ErrInsufficientBalance
	default:
		return false, fmt.Errorf("prededuct: unknown status %q", status)
	}
}

// SettleQuota refunds leftover pre-deducted tokens or charges the shortfall.
// diff = preDeducted - actual; INCRBY when positive, DECRBY when negative.
func (l *Limiter) SettleQuota(ctx context.Context, userID string, preDeductedTokens, actualTokens int64) error {
	if userID == "" {
		return errors.New("userID is required")
	}
	if actualTokens < 0 {
		actualTokens = 0
	}
	diff := preDeductedTokens - actualTokens
	if diff == 0 {
		return nil
	}

	key := balanceKey(userID)
	var err error
	if diff > 0 {
		err = l.rdb.IncrBy(ctx, key, diff).Err()
	} else {
		err = l.rdb.DecrBy(ctx, key, -diff).Err()
	}
	if err != nil {
		return fmt.Errorf("settle quota: %w", err)
	}
	return nil
}

// Credit adds tokens to a user balance. Used by tests and local seeding.
func (l *Limiter) Credit(ctx context.Context, userID string, tokens int64) error {
	if userID == "" {
		return errors.New("userID is required")
	}
	return l.rdb.IncrBy(ctx, balanceKey(userID), tokens).Err()
}

func rateLimitKey(userID string) string {
	return "llm:rl:" + userID
}

func balanceKey(userID string) string {
	return "llm:bal:" + userID
}

func windowMicros(window time.Duration) int64 {
	us := window.Microseconds()
	if us < 1 {
		return 1
	}
	return us
}

func loadLuaScript() (string, error) {
	candidates := []string{"lua/rate_limit_prededuct.lua"}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "..", "..", "lua", "rate_limit_prededuct.lua"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "lua", "rate_limit_prededuct.lua"))
	}
	var last error
	for _, path := range candidates {
		body, err := os.ReadFile(path)
		if err == nil {
			return string(body), nil
		}
		last = err
	}
	return "", fmt.Errorf("load lua script: %w", last)
}
