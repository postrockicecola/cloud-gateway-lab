package quota

import (
	"context"
	"fmt"
	"strconv"
)

type Reservation struct {
	Allowed   bool
	Remaining int64
}

type Accountant interface {
	Reserve(ctx context.Context, account string, cost int64) (Reservation, error)
}

func parsePair(values []any) (allowed bool, remaining int64, err error) {
	if len(values) < 2 {
		return false, 0, fmt.Errorf("script returned %d values", len(values))
	}
	flag, err := toInt64(values[0])
	if err != nil {
		return false, 0, err
	}
	remaining, err = toInt64(values[1])
	if err != nil {
		return false, 0, err
	}
	return flag == 1, remaining, nil
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
