package project

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, p *Project) error
	Get(ctx context.Context, id uuid.UUID) (*Project, error)
	Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Project, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*Project, error)

	AddMember(ctx context.Context, m *Member) error
	GetMember(ctx context.Context, projectID uuid.UUID, userID string) (*Member, error)
	ListMembers(ctx context.Context, projectID uuid.UUID) ([]*Member, error)
	SetMemberRole(ctx context.Context, projectID uuid.UUID, userID string, role Role) error
	RemoveMember(ctx context.Context, projectID uuid.UUID, userID string) error
}
