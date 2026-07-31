package mention

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, m *Mention) error
	ListByActor(ctx context.Context, actorID string, limit, offset int) ([]*Mention, error)
	ListViewsByActor(ctx context.Context, actorID string, limit, offset int) ([]*View, error)
	CountUnseenByActor(ctx context.Context, actorID string) (int, error)
	MarkSeen(ctx context.Context, id uuid.UUID, actorID string) error
}

var ErrNotFound = errors.New("mention: not found")
