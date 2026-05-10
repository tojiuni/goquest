package comment

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type AuthorKind string

const (
	AuthorKindStaff    AuthorKind = "staff"
	AuthorKindAgent    AuthorKind = "agent"
	AuthorKindCustomer AuthorKind = "customer"
	AuthorKindSystem   AuthorKind = "system"
)

type Comment struct {
	ID            uuid.UUID  `json:"id"`
	TicketID      uuid.UUID  `json:"ticket_id"`
	AuthorID      string     `json:"author_id"`
	AuthorKind    AuthorKind `json:"author_kind"`
	Body          string     `json:"body"`
	IsInternal    bool       `json:"is_internal"`
	IsDraft       bool       `json:"is_draft"`
	GardenerScore *float32   `json:"gardener_score,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type CreateReq struct {
	Body          string   `json:"body"`
	IsInternal    bool     `json:"is_internal,omitempty"`
	IsDraft       bool     `json:"is_draft,omitempty"`        // agent AI suggestions
	GardenerScore *float32 `json:"gardener_score,omitempty"` // 0.0–1.0
}

type UpdateReq struct {
	Body string `json:"body"`
}

// (ErrForbidden / ErrNotFound are defined in service.go and store.go)
var _ = errors.New
