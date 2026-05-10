package comment

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, c *Comment) error
	Get(ctx context.Context, id uuid.UUID) (*Comment, error)
	ListByTicket(ctx context.Context, ticketID uuid.UUID, includeInternal bool) ([]*Comment, error)
	Update(ctx context.Context, id uuid.UUID, body string) (*Comment, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Publish(ctx context.Context, id uuid.UUID) error
}

var ErrNotFound = errors.New("comment: not found")
