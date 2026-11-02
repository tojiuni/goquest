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

// TicketSummary is the slice of a ticket an inbox row needs.
type TicketSummary struct {
	ID         uuid.UUID `json:"id"`
	TitleName  string    `json:"title_name,omitempty"`
	Title      string    `json:"title"`
	Visibility string    `json:"visibility"` // "project" | "restricted"
}

// Listed is an approval with its ticket, as returned by the workspace inbox.
type Listed struct {
	*Approval
	Ticket TicketSummary `json:"ticket"`
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
	// ListByWorkspace returns approvals on tickets whose project belongs to the
	// workspace, newest first, with a ticket summary. It does NOT apply
	// per-ticket visibility: the caller must filter by what the user may view.
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, f ListFilter) ([]*Listed, error)
	// Transition moves id from -> to and appends an Event. d is non-nil only
	// for decisions (approved/rejected/revision_requested). It returns
	// ErrInvalidTransition if the graph forbids it and ErrStale if the stored
	// status is no longer `from`.
	Transition(ctx context.Context, id uuid.UUID, from, to Status, actor, note string, d *Decision) (*Approval, error)
	// SupersedeOpenMerge moves every pending/approved merge approval on the
	// same ticket, repo and pr whose head_sha differs from newHeadSHA to
	// superseded, in the caller's transaction scope, and returns their ids.
	// SupersedeStaleMerge does the same across every ticket for a PR, used when
	// a webhook reports a new head commit. It returns the superseded ids.
	SupersedeStaleMerge(ctx context.Context, repo string, pr int, headSHA, actor string) ([]uuid.UUID, error)
	SupersedeOpenMerge(ctx context.Context, ticketID uuid.UUID, repo string, pr int, newHeadSHA, actor string) ([]uuid.UUID, error)
}
