package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrMissing  = errors.New("missing api key")
	ErrInvalid  = errors.New("invalid api key")
	ErrDisabled = errors.New("api key disabled")
)

type User struct {
	ID     string
	Name   string
	Status string
}

type Record struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type KeyStore interface {
	LookupKey(ctx context.Context, keyHash string) (Record, error)
}

type Authenticator struct {
	store       KeyStore
	passthrough bool
}

func New(store KeyStore) *Authenticator {
	return &Authenticator{store: store}
}

// NewPassthrough treats any non-empty Bearer token as a user id.
// Used only when no API keys are configured (local lab).
func NewPassthrough() *Authenticator {
	return &Authenticator{passthrough: true}
}

func (a *Authenticator) Lookup(ctx context.Context, authorization string) (User, error) {
	raw := StripBearer(authorization)
	if raw == "" {
		return User{}, ErrMissing
	}
	if a.passthrough {
		return User{ID: raw, Name: raw, Status: "active"}, nil
	}
	if a.store == nil {
		return User{}, ErrInvalid
	}
	rec, err := a.store.LookupKey(ctx, HashKey(raw))
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			return User{}, ErrInvalid
		}
		return User{}, err
	}
	if rec.Status != "" && rec.Status != "active" {
		return User{}, ErrDisabled
	}
	id := rec.UserID
	if id == "" {
		return User{}, ErrInvalid
	}
	return User{ID: id, Name: rec.Name, Status: rec.Status}, nil
}

func StripBearer(authorization string) string {
	value := strings.TrimSpace(authorization)
	if len(value) >= 7 && strings.EqualFold(value[:7], "bearer ") {
		return strings.TrimSpace(value[7:])
	}
	return value
}

func HashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
