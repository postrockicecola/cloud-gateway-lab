package quota

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

//go:embed reserve.lua
var reserveLua string

var reserveScript = redis.NewScript(reserveLua)

// Redis freezes cost on a shared balance so every gateway replica sees
// the same remaining quota.
type Redis struct {
	rdb            redis.Cmdable
	prefix         string
	defaultBalance int64
}

func NewRedis(rdb redis.Cmdable, defaultBalance int64) *Redis {
	return &Redis{
		rdb:            rdb,
		prefix:         "gw:quota:",
		defaultBalance: defaultBalance,
	}
}

func (r *Redis) Reserve(ctx context.Context, account string, cost int64) (Reservation, error) {
	if cost < 1 {
		return Reservation{}, fmt.Errorf("cost must be positive")
	}
	values, err := reserveScript.Run(ctx, r.rdb, []string{r.prefix + account},
		strconv.FormatInt(cost, 10),
		strconv.FormatInt(r.defaultBalance, 10),
	).Slice()
	if err != nil {
		return Reservation{}, fmt.Errorf("reserve: %w", err)
	}
	allowed, remaining, err := parsePair(values)
	if err != nil {
		return Reservation{}, err
	}
	return Reservation{Allowed: allowed, Remaining: remaining}, nil
}
