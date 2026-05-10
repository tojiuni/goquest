package workspace

import (
	"time"

	"github.com/google/uuid"
)

type Type string

const (
	TypePersonal Type = "personal"
	TypeShared   Type = "shared"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

type Workspace struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	Type        Type      `json:"type"`
	OwnerID     string    `json:"owner_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Member struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Role        Role      `json:"role"`
	XP          int       `json:"xp"`
	Level       int       `json:"level"`
	Title       string    `json:"title"`
	JoinedAt    time.Time `json:"joined_at"`
}

type Invitation struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	Email       string     `json:"email,omitempty"`
	Token       string     `json:"token"`
	Role        Role       `json:"role"`
	InvitedBy   string     `json:"invited_by"`
	ExpiresAt   time.Time  `json:"expires_at"`
	UsedAt      *time.Time `json:"used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type CreateReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        Type   `json:"type"`
}

type UpdateReq struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type InviteReq struct {
	Email string `json:"email,omitempty"`
	Role  Role   `json:"role"`
}

type SetRoleReq struct {
	Role Role `json:"role"`
}
