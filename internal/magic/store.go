package magic

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, t *Token, hash string) error
	FindByHash(ctx context.Context, hash string) (*Token, error)
	MarkUsed(ctx context.Context, id uuid.UUID) error
}
