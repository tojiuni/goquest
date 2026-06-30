package tag

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, t *Tag) error
	Get(ctx context.Context, id uuid.UUID) (*Tag, error)
	Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Tag, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*Tag, error)
	GetByName(ctx context.Context, workspaceID uuid.UUID, name string) (*Tag, error)
}
