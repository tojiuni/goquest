package device

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	// Upsert inserts or refreshes a device token. Returns the persisted row.
	Upsert(ctx context.Context, userID string, req RegisterReq) (*Token, error)
	// Delete removes a token by id. The caller must verify ownership.
	Delete(ctx context.Context, id uuid.UUID) error
	// ListByUser returns all tokens registered for a user (useful for testing).
	ListByUser(ctx context.Context, userID string) ([]*Token, error)
}
