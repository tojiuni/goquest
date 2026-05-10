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

	// Subtask
	ListSubtasks(ctx context.Context, parentID uuid.UUID) ([]*Ticket, error)
	AllSubtasksTerminal(ctx context.Context, parentID uuid.UUID) (bool, error)

	// Tags
	SetTicketTags(ctx context.Context, ticketID uuid.UUID, tagIDs []uuid.UUID) error

	// Attempts
	CreateAttempt(ctx context.Context, a *Attempt) error
	ListAttempts(ctx context.Context, ticketID uuid.UUID) ([]*Attempt, error)
	CountAttempts(ctx context.Context, ticketID uuid.UUID, userID string) (int, error)

	// SLA
	ListOverdue(ctx context.Context) ([]*Ticket, error)
}
