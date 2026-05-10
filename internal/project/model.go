package project

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
)

type Difficulty string

const (
	DifficultyEasy      Difficulty = "easy"
	DifficultyNormal    Difficulty = "normal"
	DifficultyHard      Difficulty = "hard"
	DifficultyLegendary Difficulty = "legendary"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

type Project struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Status      Status     `json:"status"`
	Difficulty  Difficulty `json:"difficulty,omitempty"`
	RewardXP    int        `json:"reward_xp"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type Member struct {
	ProjectID  uuid.UUID `json:"project_id"`
	UserID     string    `json:"user_id"`
	Role       Role      `json:"role"`
	AssignedAt time.Time `json:"assigned_at"`
}

type CreateReq struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Difficulty  Difficulty `json:"difficulty,omitempty"`
	RewardXP    int        `json:"reward_xp,omitempty"`
}

type UpdateReq struct {
	Name        *string     `json:"name,omitempty"`
	Description *string     `json:"description,omitempty"`
	Status      *Status     `json:"status,omitempty"`
	Difficulty  *Difficulty `json:"difficulty,omitempty"`
	RewardXP    *int        `json:"reward_xp,omitempty"`
}

type SetRoleReq struct {
	Role Role `json:"role"`
}
