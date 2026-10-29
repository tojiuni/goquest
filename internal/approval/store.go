package approval

import (
	"context"

	"github.com/google/uuid"
)

// ListFilter narrows List. Zero values mean "no filter".
type ListFilter struct {
	TicketID    *uuid.UUID
	Status      Status
	Kind        Kind
	Repo        string
	RequestedBy string
	Limit       int
	Offset      int
}

// Store persists approvals. Records are never deleted; Transition is the only
// mutation after Create and is a compare-and-set on the current status.
type Store interface {
	// Create inserts a pending approval. For kind=merge it first supersedes the
	// ticket's open merge approvals for the same repo+pr with a different
	// head_sha, in the same transaction, and links the new row via supersedes_id.
	// It returns ErrDuplicatePending if an identical request is already pending.
	Create(ctx context.Context, req NewReq) (*Approval, error)
	// Get returns the approval with its transition events.
	Get(ctx context.Context, id uuid.UUID) (*Approval, error)
	List(ctx context.Context, f ListFilter) ([]*Approval, error)
	// Transition moves id from -> to and appends an Event. d is non-nil only
	// for decisions (approved/rejected/revision_requested). It returns
	// ErrInvalidTransition if the graph forbids it and ErrStale if the stored
	// status is no longer `from`.
	Transition(ctx context.Context, id uuid.UUID, from, to Status, actor, note string, d *Decision) (*Approval, error)
	// SupersedeOpenMerge moves every pending/approved merge approval on the
	// same ticket, repo and pr whose head_sha differs from newHeadSHA to
	// superseded, in the caller's transaction scope, and returns their ids.
	SupersedeOpenMerge(ctx context.Context, ticketID uuid.UUID, repo string, pr int, newHeadSHA, actor string) ([]uuid.UUID, error)
}
