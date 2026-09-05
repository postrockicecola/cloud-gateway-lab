package store

import (
	"context"

	"cloud-gateway-lab/internal/auth"
	"cloud-gateway-lab/internal/endpoint"
)

var ErrNotFound = auth.ErrInvalid

type APIKeyStore interface {
	LookupKey(ctx context.Context, keyHash string) (auth.Record, error)
}

type EndpointStore interface {
	ListEndpoints(ctx context.Context) ([]endpoint.Endpoint, error)
}

type Store interface {
	APIKeyStore
	EndpointStore
}
