package workspace

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, ws *Workspace) error
	Get(ctx context.Context, id uuid.UUID) (*Workspace, error)
	GetBySlug(ctx context.Context, slug string) (*Workspace, error)
	GetByName(ctx context.Context, name string) (*Workspace, error)
	Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Workspace, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListByUser(ctx context.Context, userID string) ([]*Workspace, error)

	AddMember(ctx context.Context, m *Member) error
	GetMember(ctx context.Context, workspaceID uuid.UUID, userID string) (*Member, error)
	ListMembers(ctx context.Context, workspaceID uuid.UUID) ([]*Member, error)
	SetMemberRole(ctx context.Context, workspaceID uuid.UUID, userID string, role Role) error
	RemoveMember(ctx context.Context, workspaceID uuid.UUID, userID string) error
	UpdateMemberXP(ctx context.Context, workspaceID uuid.UUID, userID string, xp, level int, title string) error

	CreateInvitation(ctx context.Context, inv *Invitation) error
	GetInvitationByToken(ctx context.Context, token string) (*Invitation, error)
	ListInvitations(ctx context.Context, workspaceID uuid.UUID) ([]*Invitation, error)
	MarkInvitationUsed(ctx context.Context, id uuid.UUID) error
	DeleteInvitation(ctx context.Context, id uuid.UUID) error
}
