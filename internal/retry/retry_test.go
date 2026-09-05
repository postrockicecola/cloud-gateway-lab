package retry

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"cloud-gateway-lab/internal/provider"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want Action
	}{
		{&provider.Error{StatusCode: 400, Message: "bad"}, Fail},
		{&provider.Error{StatusCode: 401, Message: "auth"}, Fail},
		{&provider.Error{StatusCode: 403, Message: "forbid"}, Fail},
		{&provider.Error{StatusCode: 429, Message: "slow"}, RetryOther},
		{&provider.Error{StatusCode: 503, Message: "down"}, Retry},
		{&provider.Error{Message: "connection refused"}, Retry},
		{context.DeadlineExceeded, Retry},
		{context.Canceled, Fail},
		{&net.OpError{Op: "dial", Err: errors.New("refused")}, Retry},
	}
	for _, tc := range cases {
		if got := Classify(tc.err); got != tc.want {
			t.Fatalf("Classify(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestBackoff(t *testing.T) {
	cfg := Config{BaseDelay: 100 * time.Millisecond}
	if Backoff(cfg, 0) != 100*time.Millisecond {
		t.Fatal(Backoff(cfg, 0))
	}
	if Backoff(cfg, 1) != 200*time.Millisecond {
		t.Fatal(Backoff(cfg, 1))
	}
	if Backoff(cfg, 20) != 800*time.Millisecond {
		t.Fatal(Backoff(cfg, 20))
	}
}
