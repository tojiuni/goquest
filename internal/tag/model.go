package tag

import (
	"time"

	"github.com/google/uuid"
)

type Tag struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateReq struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type UpdateReq struct {
	Name  *string `json:"name,omitempty"`
	Color *string `json:"color,omitempty"`
}
