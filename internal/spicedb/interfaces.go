package spicedb

import (
	"context"

	"github.com/google/uuid"
)

// PermissionChecker checks fine-grained access.
type PermissionChecker interface {
	CanViewTicket(ctx context.Context, ticketID uuid.UUID, userID string, visibility VisibilityMode) (bool, error)
	CanManageTicket(ctx context.Context, ticketID uuid.UUID, userID string) (bool, error)
	CanViewProject(ctx context.Context, projectID uuid.UUID, userID string) (bool, error)
}

// RelationshipWriter writes and deletes SpiceDB relationships.
type RelationshipWriter interface {
	WriteTicket(ctx context.Context, ticketID, projectID uuid.UUID, requesterID, assigneeID string) error
	WriteWorkspaceMember(ctx context.Context, workspaceID uuid.UUID, userID, role string) error
	DeleteWorkspaceMember(ctx context.Context, workspaceID uuid.UUID, userID string) error
	WriteProjectMember(ctx context.Context, projectID uuid.UUID, userID, role string) error
	DeleteProjectMember(ctx context.Context, projectID uuid.UUID, userID string) error
	WriteProjectWorkspace(ctx context.Context, projectID, workspaceID uuid.UUID) error
	AddTicketViewer(ctx context.Context, ticketID uuid.UUID, userID string) error
}

// Ensure concrete types satisfy interfaces.
var _ PermissionChecker = (*Checker)(nil)
var _ PermissionChecker = (*NoopChecker)(nil)
var _ RelationshipWriter = (*Writer)(nil)
var _ RelationshipWriter = (*NoopWriter)(nil)
