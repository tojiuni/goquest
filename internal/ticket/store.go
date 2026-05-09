package ticket

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, t *Ticket) error
	Get(ctx context.Context, id uuid.UUID) (*Ticket, error)
	Update(ctx context.Context, id uuid.UUID, req UpdateTicketReq) (*Ticket, error)
	List(ctx context.Context, f ListTicketsFilter) ([]*Ticket, int, error)
}
