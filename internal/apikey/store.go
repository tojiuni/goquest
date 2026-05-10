package apikey

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, k *APIKey, keyHash string) error
	List(ctx context.Context, projectID uuid.UUID) ([]*APIKey, error)
	Delete(ctx context.Context, id uuid.UUID) error

	// FindByHash looks up an API key by its SHA-256 hash.
	// Returns ErrNotFound if not present or expired.
	FindByHash(ctx context.Context, keyHash string) (*APIKey, error)

	// TouchLastUsed updates last_used_at to now.
	TouchLastUsed(ctx context.Context, id uuid.UUID) error
}
