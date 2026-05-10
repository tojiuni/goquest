package macro

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Macro struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	ProjectID   *uuid.UUID `json:"project_id,omitempty"`
	Name        string     `json:"name"`
	Body        string     `json:"body"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type CreateReq struct {
	ProjectID *uuid.UUID `json:"project_id,omitempty"`
	Name      string     `json:"name"`
	Body      string     `json:"body"`
}

type UpdateReq struct {
	Name *string `json:"name,omitempty"`
	Body *string `json:"body,omitempty"`
}

var ErrNotFound = errors.New("macro: not found")
