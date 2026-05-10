package template

import (
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

// Template is a reusable ticket shape stored per-project.
type Template struct {
	ID             uuid.UUID             `json:"id"`
	ProjectID      uuid.UUID             `json:"project_id"`
	Name           string                `json:"name"`
	Title          string                `json:"title"`
	Description    string                `json:"description,omitempty"`
	Type           ticket.TicketType           `json:"type,omitempty"`
	Priority       ticket.Priority       `json:"priority,omitempty"`
	CompletionMode ticket.CompletionMode `json:"completion_mode"`
	CompletionSpec ticket.CompletionSpec `json:"completion_spec,omitempty"`
	RewardXP       int                   `json:"reward_xp,omitempty"`
	CreatedBy      string                `json:"created_by"`
	CreatedAt      time.Time             `json:"created_at"`
}

type CreateReq struct {
	Name           string                `json:"name"`
	Title          string                `json:"title"`
	Description    string                `json:"description,omitempty"`
	Type           ticket.TicketType           `json:"type,omitempty"`
	Priority       ticket.Priority       `json:"priority,omitempty"`
	CompletionMode ticket.CompletionMode `json:"completion_mode,omitempty"`
	CompletionSpec ticket.CompletionSpec `json:"completion_spec,omitempty"`
	RewardXP       int                   `json:"reward_xp,omitempty"`
}
