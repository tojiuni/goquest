package comment

import (
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
	ID         uuid.UUID  `json:"id"`
	TicketID   uuid.UUID  `json:"ticket_id"`
	AuthorID   string     `json:"author_id"`
	AuthorKind AuthorKind `json:"author_kind"`
	Body       string     `json:"body"`
	IsInternal bool       `json:"is_internal"`
	CreatedAt  time.Time  `json:"created_at"`
}

type CreateReq struct {
	Body       string `json:"body"`
	IsInternal bool   `json:"is_internal,omitempty"`
}

type UpdateReq struct {
	Body string `json:"body"`
}
