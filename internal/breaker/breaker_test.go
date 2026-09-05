package breaker

import (
	"testing"
	"time"
)

func TestClosedOpenHalfOpen(t *testing.T) {
	b := New(Config{FailureThreshold: 2, Cooldown: 20 * time.Millisecond})
	if !b.Allow() || b.State() != Closed {
		t.Fatal("expected closed")
	}
	b.Failure()
	if b.State() != Closed {
		t.Fatal("single failure should stay closed")
	}
	b.Failure()
	if b.State() != Open || b.Allow() {
		t.Fatalf("state = %s", b.State())
	}

	time.Sleep(25 * time.Millisecond)
	if !b.Eligible() {
		t.Fatal("expected half-open eligible")
	}
	if !b.Allow() {
		t.Fatal("probe should be allowed")
	}
	if b.Allow() {
		t.Fatal("only one half-open probe")
	}
	b.Success()
	if b.State() != Closed {
		t.Fatalf("state = %s", b.State())
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	b := New(Config{FailureThreshold: 1, Cooldown: 10 * time.Millisecond})
	b.Failure()
	time.Sleep(15 * time.Millisecond)
	if !b.Allow() {
		t.Fatal("probe")
	}
	b.Failure()
	if b.State() != Open {
		t.Fatalf("state = %s", b.State())
	}
}
