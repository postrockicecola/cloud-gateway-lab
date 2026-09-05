package retry

import (
	"context"
	"errors"
	"net"
	"time"

	"cloud-gateway-lab/internal/provider"
)

type Action int

const (
	Fail Action = iota
	Retry
	RetryOther
)

type Config struct {
	MaxAttempts int
	BaseDelay   time.Duration
}

func (c Config) withDefaults() Config {
	if c.MaxAttempts < 1 {
		c.MaxAttempts = 3
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = 100 * time.Millisecond
	}
	return c
}

func Classify(err error) Action {
	if err == nil {
		return Fail
	}
	if errors.Is(err, context.Canceled) {
		return Fail
	}

	var pe *provider.Error
	if errors.As(err, &pe) {
		switch pe.StatusCode {
		case 400, 401, 403, 404:
			return Fail
		case 429:
			return RetryOther
		}
		if pe.Retryable() {
			return Retry
		}
		return Fail
	}

	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) || isNet(err) {
		return Retry
	}
	return Fail
}

func Backoff(cfg Config, attempt int) time.Duration {
	cfg = cfg.withDefaults()
	if attempt < 0 {
		attempt = 0
	}
	d := cfg.BaseDelay << attempt
	if d > 800*time.Millisecond {
		d = 800 * time.Millisecond
	}
	return d
}

func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func isNet(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op)
}

func RetryAfter(err error) time.Duration {
	var pe *provider.Error
	if errors.As(err, &pe) {
		return pe.RetryAfter
	}
	return 0
}
