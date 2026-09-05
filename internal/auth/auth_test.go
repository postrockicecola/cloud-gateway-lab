package auth

import (
	"context"
	"errors"
	"testing"
)

func TestHashAndLookup(t *testing.T) {
	mem, err := ParseKeyList("sk-alice:alice,sk-bob:bob")
	if err != nil {
		t.Fatal(err)
	}
	a := New(mem)
	user, err := a.Lookup(context.Background(), "Bearer sk-alice")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "alice" {
		t.Fatalf("user = %+v", user)
	}
	_, err = a.Lookup(context.Background(), "Bearer sk-unknown")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	_, err = a.Lookup(context.Background(), "")
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestDisabledKey(t *testing.T) {
	mem := NewMemory()
	mem.Put("sk-dead", Record{UserID: "dead", Status: "disabled"})
	a := New(mem)
	_, err := a.Lookup(context.Background(), "Bearer sk-dead")
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestPassthrough(t *testing.T) {
	a := NewPassthrough()
	user, err := a.Lookup(context.Background(), "Bearer dev-user")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != "dev-user" {
		t.Fatalf("user = %+v", user)
	}
}

func TestHashKeyStable(t *testing.T) {
	if HashKey("sk-alice") != HashKey("sk-alice") {
		t.Fatal("hash not stable")
	}
	if HashKey("sk-alice") == "sk-alice" {
		t.Fatal("hash stored plaintext")
	}
}
