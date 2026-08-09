package ticket

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, t *Ticket) error
	Get(ctx context.Context, id uuid.UUID) (*Ticket, error)
	GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*Ticket, error)
	// GetByTitleNameAnyProject is GetByProjectAndTitleName without a project
	// filter — see the pgStore implementation's doc comment for why this
	// exists (VCS auto-link's cross-project fallback) and its
	// ErrAmbiguousTitleName contract.
	GetByTitleNameAnyProject(ctx context.Context, titleName string) (*Ticket, error)
	Update(ctx context.Context, id uuid.UUID, req UpdateTicketReq) (*Ticket, error)
	// ReopenTerminal moves a completed/cancelled ticket back to in_progress,
	// intentionally bypassing the validateTransition state machine (terminals
	// are closed by design — see statemachine_quality_test.go). Callers must
	// verify the ticket is actually terminal before calling; this method does
	// not re-check status.
	ReopenTerminal(ctx context.Context, id uuid.UUID) (*Ticket, error)
	List(ctx context.Context, f ListTicketsFilter) ([]*Ticket, int, error)
	// Aggregate returns COUNT(*) grouped by one whitelisted column, honoring the
	// same filters + visibility predicate as List. Unknown groupBy → ErrInvalidGroupBy.
	Aggregate(ctx context.Context, groupBy string, f ListTicketsFilter) ([]AggBucket, error)

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

	// Relations (typed directed edges between tickets: blocks/depends_on/duplicates)
	AddRelation(ctx context.Context, fromID, toID uuid.UUID, relationType, createdBy string) (TicketRelation, bool, error)
	// RemoveRelation deletes relationID only if it belongs to ticketID (either
	// side). found=false means the id doesn't exist at all OR belongs to a
	// different ticket pair — the store doesn't distinguish the two cases so
	// the ownership invariant can't be bypassed by any future caller.
	RemoveRelation(ctx context.Context, ticketID, relationID uuid.UUID) (found bool, err error)
	ListRelationsForTicket(ctx context.Context, ticketID uuid.UUID) ([]TicketRelation, error)
	// ListRelationsWithSummaryForTicket is like ListRelationsForTicket but joins
	// in a summary (title_name/title/status/visibility) of whichever ticket is
	// NOT ticketID, so GET .../relations can render without a second
	// round-trip per row. The handler uses OtherTicketVisibility to filter out
	// relations the caller can't view the other side of.
	ListRelationsWithSummaryForTicket(ctx context.Context, ticketID uuid.UUID) ([]RelationWithSummary, error)
	ListAllRelations(ctx context.Context, limit, offset int) ([]TicketRelation, int, error)
}
