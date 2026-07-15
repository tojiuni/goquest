package spicedb

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrForbidden is returned when a permission check denies access.
var ErrForbidden = errors.New("forbidden")

// PermissionChecker checks fine-grained access.
type PermissionChecker interface {
	CanViewTicket(ctx context.Context, ticketID uuid.UUID, userID string, visibility VisibilityMode) (bool, error)
	CanManageTicket(ctx context.Context, ticketID uuid.UUID, userID string) (bool, error)
	CanViewProject(ctx context.Context, projectID uuid.UUID, userID string) (bool, error)
	// Check is the generic single-resource primitive: does userID hold `permission`
	// on objType:objID? (user subject is encoded centrally.)
	Check(ctx context.Context, objType, objID, permission, userID string) (bool, error)
	// VisibleResourceIDs returns the objType ids on which userID holds `permission`,
	// via LookupResources. unrestricted=true signals "no restriction" (NoopChecker /
	// shadow) so callers skip filtering. When unrestricted=true, ids is undefined
	// (may be nil) and must not be used.
	VisibleResourceIDs(ctx context.Context, objType, permission, userID string) (ids []string, unrestricted bool, err error)
}

// RelationshipWriter writes and deletes SpiceDB relationships.
type RelationshipWriter interface {
	WriteTicket(ctx context.Context, ticketID, projectID uuid.UUID, requesterID, assigneeID string) error
	WriteWorkspaceMember(ctx context.Context, workspaceID uuid.UUID, userID, role string) error
	DeleteWorkspaceMember(ctx context.Context, workspaceID uuid.UUID, userID string) error
	WriteProjectMember(ctx context.Context, projectID uuid.UUID, userID, role string) error
	DeleteProjectMember(ctx context.Context, projectID uuid.UUID, userID string) error
	WriteProjectWorkspace(ctx context.Context, projectID, workspaceID uuid.UUID) error
	WriteWorkspaceOrg(ctx context.Context, workspaceID uuid.UUID, orgID string) error
	DeleteWorkspaceOrg(ctx context.Context, workspaceID uuid.UUID) error
	AddTicketViewer(ctx context.Context, ticketID uuid.UUID, userID string) error
	ReplaceTicketAssignee(ctx context.Context, ticketID uuid.UUID, assigneeID string) error
	ReplaceTicketProject(ctx context.Context, ticketID, projectID uuid.UUID) error
}

// Ensure concrete types satisfy interfaces.
var _ PermissionChecker = (*Checker)(nil)
var _ PermissionChecker = (*NoopChecker)(nil)
var _ RelationshipWriter = (*Writer)(nil)
var _ RelationshipWriter = (*NoopWriter)(nil)
