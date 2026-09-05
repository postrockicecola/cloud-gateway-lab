package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

type Decision struct {
	Allowed bool
	Count   int64
	Limit   int64
}

type Limiter interface {
	Allow(ctx context.Context, key string) (Decision, error)
}

func parsePair(values []any) (allowed bool, count int64, err error) {
	if len(values) < 2 {
		return false, 0, fmt.Errorf("script returned %d values", len(values))
	}
	flag, err := toInt64(values[0])
	if err != nil {
		return false, 0, err
	}
	count, err = toInt64(values[1])
	if err != nil {
		return false, 0, err
	}
	return flag == 1, count, nil
}

func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case string:
		return strconv.ParseInt(n, 10, 64)
	default:
		return 0, fmt.Errorf("unexpected numeric type %T", v)
	}
}

func windowMillis(window time.Duration) int64 {
	ms := window.Milliseconds()
	if ms < 1 {
		return 1
	}
	return ms
}
