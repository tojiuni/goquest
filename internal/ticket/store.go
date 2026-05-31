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

	// Refs (id-only). All methods are transaction-internal where needed; callers
	// do not assume cross-call atomicity except where explicitly noted.
	AddRefs(ctx context.Context, ticketID uuid.UUID, gopediaL1IDs []string, createdBy string) ([]TicketRef, error)
	ListRefs(ctx context.Context, ticketID uuid.UUID) ([]TicketRef, error)
	RemoveRef(ctx context.Context, ticketID uuid.UUID, gopediaL1ID string) error
	ReorderRefs(ctx context.Context, ticketID uuid.UUID, orderedL1IDs []string) error
	CountRefs(ctx context.Context, ticketID uuid.UUID) (int, error)
	ListTicketIDsByRef(ctx context.Context, gopediaL1ID string) ([]uuid.UUID, error)
	ListRefIDsForTickets(ctx context.Context, ticketIDs []uuid.UUID) (map[uuid.UUID][]string, error)
}
